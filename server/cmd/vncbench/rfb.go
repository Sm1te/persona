package main

import (
	"bufio"
	"crypto/aes"
	"crypto/md5"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// RFB encodings and pseudo-encodings this tool knows by name.
const (
	encRaw        int32 = 0
	encCopyRect   int32 = 1
	encZlib       int32 = 6
	encTight      int32 = 7
	encZRLE       int32 = 16
	encDesktop    int32 = -223
	encLastRect   int32 = -224
	encCursor     int32 = -239
	encXCursor    int32 = -240
	encPointerPos int32 = -232
	encExtDesktop int32 = -308
	encFence      int32 = -312
	encContinuous int32 = -313
	// Tight's quality (-32 + 0…9) and compression level (-256 + 0…9).
	encQuality0  int32 = -32
	encCompress0 int32 = -256
)

// Server messages.
const (
	msgUpdate     = 0
	msgColourMap  = 1
	msgBell       = 2
	msgCutText    = 3
	msgEndOfCU    = 150
	msgServerFenc = 248
)

func encName(e int32) string {
	switch {
	case e == encRaw:
		return "Raw"
	case e == encCopyRect:
		return "CopyRect"
	case e == 2:
		return "RRE"
	case e == 4:
		return "CoRRE"
	case e == 5:
		return "Hextile"
	case e == encZlib:
		return "Zlib"
	case e == encTight:
		return "Tight"
	case e == 8:
		return "ZlibHex"
	case e == encZRLE:
		return "ZRLE"
	case e == 17:
		return "ZYWRLE"
	case e == 21:
		return "JPEG"
	case e == 50:
		return "OpenH264"
	case e == encDesktop:
		return "DesktopSize"
	case e == encLastRect:
		return "LastRect"
	case e == encCursor:
		return "Cursor"
	case e == encXCursor:
		return "XCursor"
	case e == encPointerPos:
		return "PointerPos"
	case e == encExtDesktop:
		return "ExtendedDesktopSize"
	case e == encFence:
		return "Fence"
	case e == encContinuous:
		return "ContinuousUpdates"
	case e >= -32 && e <= -23:
		return fmt.Sprintf("Quality%d", e+32)
	case e >= -256 && e <= -247:
		return fmt.Sprintf("Compress%d", e+256)
	}
	return fmt.Sprintf("enc%d", e)
}

// pixelFormat is RFB's PIXEL_FORMAT.
type pixelFormat struct {
	BPP, Depth             uint8
	BigEndian, TrueColour  bool
	RMax, GMax, BMax       uint16
	RShift, GShift, BShift uint8
}

var (
	// pf32 is what the app asks for: 32 bits, depth 24, little-endian, red at 16 (BGRA in memory).
	pf32 = pixelFormat{32, 24, false, true, 255, 255, 255, 16, 8, 0}
	// pf16 is RGB565, little-endian.
	pf16 = pixelFormat{16, 16, false, true, 31, 63, 31, 11, 5, 0}
	// pf8 is BGR233.
	pf8 = pixelFormat{8, 8, false, true, 7, 7, 3, 0, 3, 6}
)

func (p pixelFormat) wire() []byte {
	b := make([]byte, 16)
	b[0], b[1] = p.BPP, p.Depth
	if p.BigEndian {
		b[2] = 1
	}
	if p.TrueColour {
		b[3] = 1
	}
	binary.BigEndian.PutUint16(b[4:], p.RMax)
	binary.BigEndian.PutUint16(b[6:], p.GMax)
	binary.BigEndian.PutUint16(b[8:], p.BMax)
	b[10], b[11], b[12] = p.RShift, p.GShift, p.BShift
	return b
}

func (p pixelFormat) bytesPerPixel() int { return int(p.BPP) / 8 }

// cpixel is ZRLE's CPIXEL size: three bytes for a 32-bit format whose colours fit in its low three bytes.
func (p pixelFormat) cpixel() int {
	if p.BPP == 32 && p.Depth <= 24 && p.TrueColour && p.RMax <= 255 && p.GMax <= 255 && p.BMax <= 255 &&
		p.RShift <= 16 && p.GShift <= 16 && p.BShift <= 16 {
		return 3
	}
	return p.bytesPerPixel()
}

// tpixel is Tight's TPIXEL size: three bytes (R, G, B) for 32 bits, depth 24, 8 bits each.
func (p pixelFormat) tpixel() bool {
	return p.BPP == 32 && p.Depth == 24 && p.RMax == 255 && p.GMax == 255 && p.BMax == 255
}

// value reads one pixel of n bytes in this format's byte order (n may be smaller than BPP/8: CPIXEL).
func (p pixelFormat) value(b []byte, n int) uint32 {
	var v uint32
	if p.BigEndian {
		for i := 0; i < n; i++ {
			v = v<<8 | uint32(b[i])
		}
		if n < p.bytesPerPixel() { // CPIXEL of a big-endian format: the low bytes
			return v
		}
		return v
	}
	for i := n - 1; i >= 0; i-- {
		v = v<<8 | uint32(b[i])
	}
	return v
}

func scale(v uint32, max uint16) uint8 {
	if max == 255 {
		return uint8(v)
	}
	if max == 0 {
		return 0
	}
	return uint8((v*255 + uint32(max)/2) / uint32(max))
}

func (p pixelFormat) rgb(v uint32) (uint8, uint8, uint8) {
	return scale(v>>p.RShift&uint32(p.RMax), p.RMax), scale(v>>p.GShift&uint32(p.GMax), p.GMax), scale(v>>p.BShift&uint32(p.BMax), p.BMax)
}

// updateInfo is one FramebufferUpdate as it came.
type updateInfo struct {
	Start, End time.Time // its first byte, its last
	Bytes      int64
	Rects      int
	Encodings  map[int32]int // rectangles by encoding
	Dirty      image.Rectangle
	Area       int // pixels painted
}

// client is an RFB 3.8 client that decodes into an RGBA framebuffer.
type client struct {
	conn net.Conn
	in   *countingReader
	r    *bufio.Reader
	wmu  sync.Mutex
	out  atomic.Int64

	Version  string
	Width    int
	Height   int
	Name     string
	ServerPF pixelFormat
	pf       pixelFormat

	fbMu sync.Mutex
	fb   *image.RGBA

	zrle  zstream
	tight [4]zstream
	zlib  zstream
	// tightKinds counts Tight rectangles by kind (tightFill…), when set.
	tightKinds []int64
	// The rectangle being read and how many the update has.
	rectIndex, rectCount int

	// What the server said it supports by answering for it.
	ContinuousOK atomic.Bool
	FenceOK      atomic.Bool
	CursorSeen   atomic.Bool
	ExtSeen      atomic.Bool

	// OnUpdate sees each update once it is drawn, with the framebuffer lock held.
	OnUpdate func(u updateInfo)
	// OnUpdateStart sees each update's header, before its rectangles are read.
	OnUpdateStart func()
	// OnRequest sees each FramebufferUpdateRequest as it goes out (trace).
	OnRequest func(incremental bool, r image.Rectangle)
	// OnFirstSize sees the first rectangle of an update of count rectangles once its size in bytes is known, before
	// its data is read (the gated way of asking).
	OnFirstSize func(count int, size int)
	// OnRect sees each rectangle's header and its byte count once read (the probe stops at unknown ones).
	OnRect func(x, y, w, h int, enc int32, bytes int64)
	// Unknown is the first encoding this client could not decode, if any (the connection is then unusable).
	Unknown atomic.Int32
}

type countingReader struct {
	r   io.Reader
	n   atomic.Int64
	rec *recorder
}

func (c *countingReader) Read(b []byte) (int, error) {
	n, err := c.r.Read(b)
	c.n.Add(int64(n))
	if n > 0 && c.rec != nil {
		c.rec.write(b[:n])
	}
	return n, err
}

// credentials are an account and password for Apple Remote Desktop's sign-in (or VeNCrypt Plain).
type credentials struct{ Account, Password string }

var errRefused = errors.New("sign-in refused")

// dial signs in over conn and reads ServerInit. rec, when set, records every byte the server sends after ServerInit.
func dial(conn net.Conn, cred credentials, rec *recorder) (*client, error) {
	c := &client{conn: conn, in: &countingReader{r: conn}}
	c.r = bufio.NewReaderSize(c.in, 256<<10)
	v := make([]byte, 12)
	if _, err := io.ReadFull(c.r, v); err != nil {
		return nil, fmt.Errorf("greeting: %w", err)
	}
	c.Version = string(v[:11])
	if _, err := conn.Write([]byte("RFB 003.008\n")); err != nil {
		return nil, err
	}
	n, err := c.r.ReadByte()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, fmt.Errorf("server refused: %s", c.reason())
	}
	types := make([]byte, n)
	if _, err := io.ReadFull(c.r, types); err != nil {
		return nil, err
	}
	pick := byte(0)
	for _, want := range []byte{30, 19, 1} {
		for _, t := range types {
			if t == want && pick == 0 {
				pick = t
			}
		}
	}
	if pick == 0 {
		return nil, fmt.Errorf("no supported security type in %v", types)
	}
	if _, err := conn.Write([]byte{pick}); err != nil {
		return nil, err
	}
	switch pick {
	case 30:
		if err := c.ard(cred); err != nil {
			return nil, err
		}
	case 19:
		if err := c.veNCrypt(cred); err != nil {
			return nil, err
		}
	}
	res, err := c.u32()
	if err != nil {
		return nil, err
	}
	if res != 0 {
		return nil, fmt.Errorf("%w: %s", errRefused, c.reason())
	}
	if _, err := conn.Write([]byte{1}); err != nil { // ClientInit: shared
		return nil, err
	}
	w, _ := c.u16()
	h, _ := c.u16()
	pfb := make([]byte, 16)
	if _, err := io.ReadFull(c.r, pfb); err != nil {
		return nil, err
	}
	c.ServerPF = pixelFormat{pfb[0], pfb[1], pfb[2] != 0, pfb[3] != 0, binary.BigEndian.Uint16(pfb[4:]),
		binary.BigEndian.Uint16(pfb[6:]), binary.BigEndian.Uint16(pfb[8:]), pfb[10], pfb[11], pfb[12]}
	nl, err := c.u32()
	if err != nil {
		return nil, err
	}
	name := make([]byte, min(nl, 1<<16))
	if _, err := io.ReadFull(c.r, name); err != nil {
		return nil, err
	}
	c.Width, c.Height, c.Name = int(w), int(h), string(name)
	c.fb = image.NewRGBA(image.Rect(0, 0, c.Width, c.Height))
	c.pf = c.ServerPF
	c.in.rec = rec
	if rec != nil {
		rec.init(c.Width, c.Height, c.Name)
		// What bufio already read past ServerInit was not recorded: record it now.
		if b, _ := c.r.Peek(c.r.Buffered()); len(b) > 0 {
			rec.write(b)
		}
	}
	return c, nil
}

func (c *client) reason() string {
	n, err := c.u32()
	if err != nil {
		return err.Error()
	}
	b := make([]byte, min(n, 4096))
	_, _ = io.ReadFull(c.r, b)
	return string(b)
}

func (c *client) u16() (uint16, error) {
	var b [2]byte
	_, err := io.ReadFull(c.r, b[:])
	return binary.BigEndian.Uint16(b[:]), err
}

func (c *client) u32() (uint32, error) {
	var b [4]byte
	_, err := io.ReadFull(c.r, b[:])
	return binary.BigEndian.Uint32(b[:]), err
}

// ard is Apple Remote Desktop's sign-in (security type 30): Diffie-Hellman, then the account and password sealed
// with AES-128-ECB under the shared secret's MD5.
func (c *client) ard(cred credentials) error {
	gen := make([]byte, 2)
	if _, err := io.ReadFull(c.r, gen); err != nil {
		return err
	}
	ln, err := c.u16()
	if err != nil {
		return err
	}
	prime, theirs := make([]byte, ln), make([]byte, ln)
	if _, err := io.ReadFull(c.r, prime); err != nil {
		return err
	}
	if _, err := io.ReadFull(c.r, theirs); err != nil {
		return err
	}
	p := new(big.Int).SetBytes(prime)
	x, err := rand.Int(rand.Reader, p)
	if err != nil {
		return err
	}
	pub := new(big.Int).Exp(new(big.Int).SetBytes(gen), x, p).FillBytes(make([]byte, ln))
	shared := new(big.Int).Exp(new(big.Int).SetBytes(theirs), x, p).FillBytes(make([]byte, ln))
	key := md5.Sum(shared)
	block := make([]byte, 128)
	_, _ = rand.Read(block)
	user, pass := []byte(cred.Account), []byte(cred.Password)
	if len(user) > 63 {
		user = user[:63]
	}
	if len(pass) > 63 {
		pass = pass[:63]
	}
	copy(block, user)
	block[len(user)] = 0
	copy(block[64:], pass)
	block[64+len(pass)] = 0
	ciph, err := aes.NewCipher(key[:])
	if err != nil {
		return err
	}
	sealed := make([]byte, 128)
	for i := 0; i < 128; i += 16 {
		ciph.Encrypt(sealed[i:i+16], block[i:i+16])
	}
	_, err = c.conn.Write(append(sealed, pub...))
	return err
}

func (c *client) veNCrypt(cred credentials) error {
	v := make([]byte, 2)
	if _, err := io.ReadFull(c.r, v); err != nil {
		return err
	}
	if _, err := c.conn.Write([]byte{0, 2}); err != nil {
		return err
	}
	if ok, err := c.r.ReadByte(); err != nil || ok != 0 {
		return fmt.Errorf("VeNCrypt 0.2 refused")
	}
	n, _ := c.r.ReadByte()
	plain := false
	for range n {
		t, err := c.u32()
		if err != nil {
			return err
		}
		plain = plain || t == 256
	}
	if !plain {
		return fmt.Errorf("VeNCrypt offers no Plain")
	}
	b := binary.BigEndian.AppendUint32(nil, 256)
	b = binary.BigEndian.AppendUint32(b, uint32(len(cred.Account)))
	b = binary.BigEndian.AppendUint32(b, uint32(len(cred.Password)))
	b = append(b, cred.Account...)
	b = append(b, cred.Password...)
	_, err := c.conn.Write(b)
	return err
}

// BytesIn is every byte read from the server so far (handshake included).
func (c *client) BytesIn() int64 { return c.in.n.Load() }

// BytesOut is every byte written after the handshake.
func (c *client) BytesOut() int64 { return c.out.Load() }

func (c *client) send(b []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.out.Add(int64(len(b)))
	_, err := c.conn.Write(b)
	return err
}

// SetPixelFormat asks for pf from now on.
func (c *client) SetPixelFormat(pf pixelFormat) error {
	c.pf = pf
	return c.send(append([]byte{0, 0, 0, 0}, pf.wire()...))
}

func (c *client) SetEncodings(encs []int32) error {
	b := []byte{2, 0, byte(len(encs) >> 8), byte(len(encs))}
	for _, e := range encs {
		b = binary.BigEndian.AppendUint32(b, uint32(e))
	}
	return c.send(b)
}

func (c *client) Request(incremental bool, r image.Rectangle) error {
	if c.OnRequest != nil {
		c.OnRequest(incremental, r)
	}
	inc := byte(0)
	if incremental {
		inc = 1
	}
	b := []byte{3, inc}
	for _, v := range []int{r.Min.X, r.Min.Y, r.Dx(), r.Dy()} {
		b = binary.BigEndian.AppendUint16(b, uint16(v))
	}
	return c.send(b)
}

// EnableContinuous turns ContinuousUpdates on (or off) for r.
func (c *client) EnableContinuous(on bool, r image.Rectangle) error {
	en := byte(0)
	if on {
		en = 1
	}
	b := []byte{150, en}
	for _, v := range []int{r.Min.X, r.Min.Y, r.Dx(), r.Dy()} {
		b = binary.BigEndian.AppendUint16(b, uint16(v))
	}
	return c.send(b)
}

func (c *client) Pointer(x, y int, buttons byte) error {
	return c.send([]byte{5, buttons, byte(x >> 8), byte(x), byte(y >> 8), byte(y)})
}

func (c *client) Key(sym uint32, down bool) error {
	d := byte(0)
	if down {
		d = 1
	}
	return c.send(binary.BigEndian.AppendUint32([]byte{4, d, 0, 0}, sym))
}

func (c *client) Whole() image.Rectangle { return image.Rect(0, 0, c.Width, c.Height) }

// Snapshot is a copy of the framebuffer now.
func (c *client) Snapshot() *image.RGBA {
	c.fbMu.Lock()
	defer c.fbMu.Unlock()
	out := image.NewRGBA(c.fb.Rect)
	copy(out.Pix, c.fb.Pix)
	return out
}

// WithFramebuffer runs f with the framebuffer locked.
func (c *client) WithFramebuffer(f func(fb *image.RGBA)) {
	c.fbMu.Lock()
	defer c.fbMu.Unlock()
	f(c.fb)
}

var errUnknown = errors.New("unknown encoding")

// Run reads and decodes server messages until the connection ends.
func (c *client) Run() error {
	for {
		t, err := c.r.ReadByte()
		if err != nil {
			return err
		}
		switch t {
		case msgUpdate:
			if err := c.update(); err != nil {
				return err
			}
		case msgColourMap:
			var h [5]byte
			if _, err := io.ReadFull(c.r, h[:]); err != nil {
				return err
			}
			n := int(binary.BigEndian.Uint16(h[3:]))
			if _, err := c.r.Discard(n * 6); err != nil {
				return err
			}
		case msgBell:
		case msgCutText:
			if _, err := c.r.Discard(3); err != nil {
				return err
			}
			n, err := c.u32()
			if err != nil {
				return err
			}
			if _, err := c.r.Discard(int(n)); err != nil {
				return err
			}
		case msgEndOfCU:
			c.ContinuousOK.Store(true)
		case msgServerFenc:
			var h [8]byte
			if _, err := io.ReadFull(c.r, h[:]); err != nil {
				return err
			}
			c.FenceOK.Store(true)
			flags := binary.BigEndian.Uint32(h[3:])
			data := make([]byte, h[7])
			if _, err := io.ReadFull(c.r, data); err != nil {
				return err
			}
			if flags&(1<<31) != 0 { // a request: answer with the flags we understand (none but the request bit off)
				b := []byte{248, 0, 0, 0}
				b = binary.BigEndian.AppendUint32(b, flags&^(1<<31)&0x7)
				b = append(b, byte(len(data)))
				b = append(b, data...)
				_ = c.send(b)
			}
		default:
			return fmt.Errorf("unknown server message %d", t)
		}
	}
}

func (c *client) update() error {
	start := time.Now()
	before := c.in.n.Load() - int64(c.r.Buffered()) - 1
	var h [3]byte
	if _, err := io.ReadFull(c.r, h[:]); err != nil {
		return err
	}
	count := int(binary.BigEndian.Uint16(h[1:]))
	if c.OnUpdateStart != nil {
		c.OnUpdateStart()
	}
	u := updateInfo{Start: start, Encodings: map[int32]int{}}
	c.fbMu.Lock()
	defer c.fbMu.Unlock()
	c.rectIndex, c.rectCount = 0, count
	for i := 0; i < count; i++ {
		c.rectIndex = i
		var rh [12]byte
		at := c.in.n.Load() - int64(c.r.Buffered())
		if _, err := io.ReadFull(c.r, rh[:]); err != nil {
			return err
		}
		x, y := int(binary.BigEndian.Uint16(rh[0:])), int(binary.BigEndian.Uint16(rh[2:]))
		w, hh := int(binary.BigEndian.Uint16(rh[4:])), int(binary.BigEndian.Uint16(rh[6:]))
		enc := int32(binary.BigEndian.Uint32(rh[8:]))
		if enc == encLastRect {
			break
		}
		paints, err := c.rect(x, y, w, hh, enc)
		if c.OnRect != nil {
			c.OnRect(x, y, w, hh, enc, c.in.n.Load()-int64(c.r.Buffered())-at)
		}
		if err != nil {
			return err
		}
		u.Rects++
		u.Encodings[enc]++
		if paints {
			r := image.Rect(x, y, x+w, y+hh)
			u.Dirty = u.Dirty.Union(r)
			u.Area += w * hh
		}
	}
	u.End = time.Now()
	u.Bytes = c.in.n.Load() - int64(c.r.Buffered()) - before
	if c.OnUpdate != nil {
		c.OnUpdate(u)
	}
	return nil
}

// rect decodes one rectangle; paints is whether it changed the framebuffer's pixels.
func (c *client) rect(x, y, w, h int, enc int32) (paints bool, err error) {
	inside := x >= 0 && y >= 0 && x+w <= c.Width && y+h <= c.Height
	switch enc {
	case encRaw:
		if !inside {
			return false, fmt.Errorf("raw rect outside")
		}
		c.firstSize(w * h * c.pf.bytesPerPixel())
		return true, c.raw(x, y, w, h)
	case encCopyRect:
		c.firstSize(4)
		sx, _ := c.u16()
		sy, err := c.u16()
		if err != nil {
			return false, err
		}
		if inside {
			copyRect(c.fb, x, y, w, h, int(sx), int(sy))
		}
		return true, nil
	case encZRLE:
		if !inside {
			return false, fmt.Errorf("zrle rect outside")
		}
		return true, c.zrleRect(x, y, w, h)
	case encTight:
		if !inside {
			return false, fmt.Errorf("tight rect outside")
		}
		return true, c.tightRect(x, y, w, h)
	case encZlib:
		if !inside {
			return false, fmt.Errorf("zlib rect outside")
		}
		return true, c.zlibRect(x, y, w, h)
	case encDesktop:
		c.resize(w, h)
		return false, nil
	case encExtDesktop:
		c.ExtSeen.Store(true)
		n, err := c.r.ReadByte()
		if err != nil {
			return false, err
		}
		if _, err := c.r.Discard(3 + int(n)*16); err != nil {
			return false, err
		}
		if y == 0 && (w != c.Width || h != c.Height) {
			c.resize(w, h)
		}
		return false, nil
	case encCursor:
		c.CursorSeen.Store(true)
		bpp := c.pf.bytesPerPixel()
		_, err := c.r.Discard(w*h*bpp + (w+7)/8*h)
		return false, err
	case encXCursor:
		c.CursorSeen.Store(true)
		if w*h == 0 {
			return false, nil
		}
		_, err := c.r.Discard(6 + (w+7)/8*h*2)
		return false, err
	case encPointerPos:
		return false, nil
	}
	c.Unknown.CompareAndSwap(0, enc)
	return false, fmt.Errorf("%w %d (%s)", errUnknown, enc, encName(enc))
}

func (c *client) firstSize(n int) {
	if c.rectIndex == 0 && c.OnFirstSize != nil {
		c.OnFirstSize(c.rectCount, n)
	}
}

func (c *client) resize(w, h int) {
	c.Width, c.Height = w, h
	c.fb = image.NewRGBA(image.Rect(0, 0, w, h))
}

func (c *client) raw(x, y, w, h int) error {
	bpp := c.pf.bytesPerPixel()
	row := make([]byte, w*bpp)
	for j := 0; j < h; j++ {
		if _, err := io.ReadFull(c.r, row); err != nil {
			return err
		}
		o := c.fb.PixOffset(x, y+j)
		for i := 0; i < w; i++ {
			r, g, b := c.pf.rgb(c.pf.value(row[i*bpp:], bpp))
			c.fb.Pix[o], c.fb.Pix[o+1], c.fb.Pix[o+2], c.fb.Pix[o+3] = r, g, b, 255
			o += 4
		}
	}
	return nil
}

func copyRect(fb *image.RGBA, x, y, w, h, sx, sy int) {
	if sx < 0 || sy < 0 || sx+w > fb.Rect.Dx() || sy+h > fb.Rect.Dy() {
		return
	}
	if sy < y {
		for j := h - 1; j >= 0; j-- {
			copy(fb.Pix[fb.PixOffset(x, y+j):fb.PixOffset(x+w, y+j)], fb.Pix[fb.PixOffset(sx, sy+j):fb.PixOffset(sx+w, sy+j)])
		}
		return
	}
	for j := 0; j < h; j++ {
		copy(fb.Pix[fb.PixOffset(x, y+j):fb.PixOffset(x+w, y+j)], fb.Pix[fb.PixOffset(sx, sy+j):fb.PixOffset(sx+w, sy+j)])
	}
}

func fill(fb *image.RGBA, x, y, w, h int, r, g, b uint8) {
	for j := 0; j < h; j++ {
		o := fb.PixOffset(x, y+j)
		for i := 0; i < w; i++ {
			fb.Pix[o], fb.Pix[o+1], fb.Pix[o+2], fb.Pix[o+3] = r, g, b, 255
			o += 4
		}
	}
}
