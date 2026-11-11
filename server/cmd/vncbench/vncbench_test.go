package main

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"net"
	"testing"
	"time"
)

// testClient is a client over one end of a pipe, already past ServerInit at w×h in pf32, and the other end.
func testClient(t *testing.T, w, h int) (*client, net.Conn) {
	t.Helper()
	a, b := net.Pipe()
	c := &client{conn: a, in: &countingReader{r: a}, pf: pf32, Width: w, Height: h}
	c.r = bufio.NewReader(c.in)
	c.fb = image.NewRGBA(image.Rect(0, 0, w, h))
	c.tightKinds = make([]int64, tightKinds)
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	return c, b
}

func rectHeader(x, y, w, h int, enc int32) []byte {
	b := []byte{}
	for _, v := range []int{x, y, w, h} {
		b = binary.BigEndian.AppendUint16(b, uint16(v))
	}
	return binary.BigEndian.AppendUint32(b, uint32(enc))
}

// syncFlushed is data through one zlib stream w, flushed as a server ends each rectangle.
func syncFlushed(t *testing.T, z *zlib.Writer, out *bytes.Buffer, data []byte) []byte {
	t.Helper()
	out.Reset()
	if _, err := z.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := z.Flush(); err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), out.Bytes()...)
}

func compact(n int) []byte {
	b := []byte{byte(n & 0x7f)}
	if n > 0x7f {
		b[0] |= 0x80
		b = append(b, byte(n>>7&0x7f))
		if n > 0x3fff {
			b[1] |= 0x80
			b = append(b, byte(n>>14))
		}
	}
	return b
}

func at(fb *image.RGBA, x, y int) [3]uint8 { return colourAt(fb, image.Pt(x, y)) }

// One update of ZRLE (solid and raw tiles over one stream, two rectangles) and Tight (fill, a two-colour palette,
// copy, gradient and JPEG, the streams kept across rectangles) decodes to what was encoded.
func TestDecodersDrawWhatWasEncoded(t *testing.T) {
	c, srv := testClient(t, 80, 70)
	var zb, tb bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&zb, 6)
	tw, _ := zlib.NewWriterLevel(&tb, 6)
	var msg []byte
	msg = append(msg, 0, 0, 0, 7) // FramebufferUpdate, 7 rectangles

	// ZRLE: a 70×10 rectangle: a solid tile (64 wide) and a raw 6×10 tile; then a second rectangle on the same stream.
	tiles := []byte{1, 0x30, 0x20, 0x10} // solid, CPIXEL B G R
	tiles = append(tiles, 0)             // raw
	for i := 0; i < 6*10; i++ {
		tiles = append(tiles, byte(i), 0x80, 0xff)
	}
	z1 := syncFlushed(t, zw, &zb, tiles)
	msg = append(msg, rectHeader(0, 0, 70, 10, encZRLE)...)
	msg = binary.BigEndian.AppendUint32(msg, uint32(len(z1)))
	msg = append(msg, z1...)
	z2 := syncFlushed(t, zw, &zb, []byte{1, 9, 8, 7})
	msg = append(msg, rectHeader(70, 0, 10, 10, encZRLE)...)
	msg = binary.BigEndian.AppendUint32(msg, uint32(len(z2)))
	msg = append(msg, z2...)

	// Tight fill: TPIXEL is R G B.
	msg = append(msg, rectHeader(0, 10, 20, 20, encTight)...)
	msg = append(msg, 0x80, 200, 100, 50)
	// Tight palette of two colours, 1 bit a pixel, rows padded to bytes: 16×16 is 32 bytes, compressed on stream 1.
	pal := make([]byte, 0, 32)
	for y := 0; y < 16; y++ {
		if y%2 == 0 {
			pal = append(pal, 0xff, 0x00)
		} else {
			pal = append(pal, 0x00, 0xff)
		}
	}
	tpal := syncFlushed(t, tw, &tb, pal)
	msg = append(msg, rectHeader(20, 10, 16, 16, encTight)...)
	msg = append(msg, 0x40|0x10, 1, 1, 0, 0, 0, 255, 255, 255) // stream 1, explicit filter: palette of 2
	msg = append(msg, compact(len(tpal))...)
	msg = append(msg, tpal...)
	// Tight copy filter under 12 bytes: sent as it is (3×1×3).
	msg = append(msg, rectHeader(40, 10, 3, 1, encTight)...)
	msg = append(msg, 0x00, 1, 2, 3, 4, 5, 6, 7, 8, 9)
	// Tight gradient filter: a horizontal ramp, each pixel 4 more than the one left of it.
	grad := []byte{}
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			if y == 0 && x == 0 {
				grad = append(grad, 10, 10, 10)
			} else if y == 0 {
				grad = append(grad, 4, 4, 4)
			} else {
				grad = append(grad, 0, 0, 0) // the prediction is right
			}
		}
	}
	// A fresh stream 2 (the reset bit set) needs its own zlib header.
	var gb bytes.Buffer
	gw, _ := zlib.NewWriterLevel(&gb, 6)
	tg := syncFlushed(t, gw, &gb, grad)
	msg = append(msg, rectHeader(50, 10, 8, 4, encTight)...)
	msg = append(msg, 0x40|0x20|0x04, 2) // stream 2, reset stream 2, explicit filter: gradient
	msg = append(msg, compact(len(tg))...)
	msg = append(msg, tg...)
	// Tight JPEG of a flat colour.
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for i := range img.Pix {
		img.Pix[i] = []byte{40, 160, 220, 255}[i%4]
	}
	var jb bytes.Buffer
	_ = jpeg.Encode(&jb, img, &jpeg.Options{Quality: 95})
	msg = append(msg, rectHeader(60, 40, 16, 16, encTight)...)
	msg = append(msg, 0x90)
	msg = append(msg, compact(jb.Len())...)
	msg = append(msg, jb.Bytes()...)

	done := make(chan updateInfo, 1)
	c.OnUpdate = func(u updateInfo) { done <- u }
	go func() { _, _ = srv.Write(msg) }()
	runErr := make(chan error, 1)
	go func() { runErr <- c.Run() }()
	var u updateInfo
	select {
	case u = <-done:
	case <-time.After(5 * time.Second):
		select {
		case err := <-runErr:
			t.Fatalf("no update decoded: %v", err)
		default:
			t.Fatal("no update decoded")
		}
	}
	if u.Rects != 7 || u.Bytes != int64(len(msg)) {
		t.Errorf("rects %d bytes %d, want 7 and %d", u.Rects, u.Bytes, len(msg))
	}
	for _, tc := range []struct {
		x, y int
		want [3]uint8
		what string
	}{
		{5, 5, [3]uint8{0x10, 0x20, 0x30}, "ZRLE solid (B G R on the wire)"},
		{64, 0, [3]uint8{0xff, 0x80, 0}, "ZRLE raw, first pixel"},
		{69, 9, [3]uint8{0xff, 0x80, 59}, "ZRLE raw, last pixel"},
		{75, 5, [3]uint8{7, 8, 9}, "ZRLE on the same stream, second rectangle"},
		{3, 20, [3]uint8{200, 100, 50}, "Tight fill"},
		{20, 10, [3]uint8{255, 255, 255}, "Tight palette, bit set"},
		{28, 10, [3]uint8{0, 0, 0}, "Tight palette, bit clear"},
		{28, 11, [3]uint8{255, 255, 255}, "Tight palette, next row"},
		{42, 10, [3]uint8{7, 8, 9}, "Tight copy, uncompressed"},
		{50, 10, [3]uint8{10, 10, 10}, "Tight gradient, first"},
		{57, 13, [3]uint8{38, 38, 38}, "Tight gradient, last"},
	} {
		if got := at(c.fb, tc.x, tc.y); got != tc.want {
			t.Errorf("%s at %d,%d: %v, want %v", tc.what, tc.x, tc.y, got, tc.want)
		}
	}
	if got := at(c.fb, 68, 48); !near(got, [3]uint8{40, 160, 220}, 6) {
		t.Errorf("Tight JPEG: %v", got)
	}
	if k := c.tightKinds; k[tightFill] != 1 || k[tightPalette] != 1 || k[tightCopy] != 1 || k[tightGradient] != 1 || k[tightJPEG] != 1 {
		t.Errorf("tight kinds %v", k)
	}
}

func TestCompareIsOneForTheSamePictureAndFallsWithNoise(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			a.Set(x, y, color.RGBA{uint8(x * 4), uint8(y * 4), uint8((x + y) * 2), 255})
		}
	}
	if q := compare(a, a, a.Rect); q.SSIM != 1 || !math.IsInf(q.PSNR, 1) {
		t.Fatalf("same picture: %+v", q)
	}
	b := image.NewRGBA(a.Rect)
	copy(b.Pix, a.Pix)
	for i := 0; i < len(b.Pix); i += 4 {
		if (i/4)%3 == 0 {
			b.Pix[i] ^= 0x10
		}
	}
	q := compare(a, b, a.Rect)
	if q.SSIM >= 0.99 || q.SSIM < 0.5 || q.PSNR < 20 || q.PSNR > 40 {
		t.Fatalf("noisy picture: %+v", q)
	}
}

// The shaping adds its delay each way and holds the way down to its rate.
func TestShapedDelaysAndLimits(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	s := shape(a, 30*time.Millisecond, 1e6) // 1 MB/s down
	go func() {
		buf := make([]byte, 16)
		_, _ = b.Read(buf)
		_, _ = b.Write(make([]byte, 200<<10))
	}()
	t0 := time.Now()
	_, _ = s.Write([]byte("go"))
	got := 0
	buf := make([]byte, 64<<10)
	for got < 200<<10 {
		n, err := s.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		got += n
	}
	// 30 ms up, 200 KB at 1 MB/s, 30 ms down: about 0.26 s.
	if d := time.Since(t0); d < 230*time.Millisecond || d > 600*time.Millisecond {
		t.Fatalf("took %s", d)
	}
}
