package main

import (
	"bufio"
	"encoding/binary"
	"image"
	"math"
	"os"
	"sync"
	"time"
)

// recorder writes what a server sends after ServerInit, with when it came, for replaying it through the app's own
// decoder (apple/PersonaTests/RFBReplayTests.swift). The file is a header line "VNCREC1 <width> <height>\n", then
// records of an 8-byte nanosecond offset from the start, a 4-byte length and that many bytes (big-endian).
type recorder struct {
	mu    sync.Mutex
	f     *os.File
	w     *bufio.Writer
	start time.Time
}

func newRecorder(path string) (*recorder, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &recorder{f: f, w: bufio.NewWriterSize(f, 1<<20)}, nil
}

func (r *recorder) init(w, h int, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.start = time.Now()
	_, _ = r.w.WriteString("VNCREC1 " + itoa(w) + " " + itoa(h) + "\n")
}

func (r *recorder) write(b []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.start.IsZero() {
		return
	}
	var h [12]byte
	binary.BigEndian.PutUint64(h[:], uint64(time.Since(r.start)))
	binary.BigEndian.PutUint32(h[8:], uint32(len(b)))
	_, _ = r.w.Write(h[:])
	_, _ = r.w.Write(b)
}

func (r *recorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.w.Flush(); err != nil {
		return err
	}
	return r.f.Close()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// quality compares a decoded framebuffer with a lossless one of the same moment, over r.
type quality struct {
	SSIM float64 // mean SSIM of luma, 8×8 windows
	PSNR float64 // over R, G and B, in dB (+Inf when identical)
}

func luma(img *image.RGBA, r image.Rectangle) []float64 {
	w, h := r.Dx(), r.Dy()
	out := make([]float64, w*h)
	for y := 0; y < h; y++ {
		o := img.PixOffset(r.Min.X, r.Min.Y+y)
		for x := 0; x < w; x++ {
			p := img.Pix[o : o+3 : o+3]
			out[y*w+x] = 0.299*float64(p[0]) + 0.587*float64(p[1]) + 0.114*float64(p[2])
			o += 4
		}
	}
	return out
}

// compare is SSIM (Wang et al. 2004, 8×8 windows at a stride of 4, C1 = (0.01·255)², C2 = (0.03·255)²) on luma and
// PSNR on RGB.
func compare(a, b *image.RGBA, r image.Rectangle) quality {
	r = r.Intersect(a.Rect).Intersect(b.Rect)
	if r.Empty() {
		return quality{}
	}
	var se float64
	for y := r.Min.Y; y < r.Max.Y; y++ {
		oa, ob := a.PixOffset(r.Min.X, y), b.PixOffset(r.Min.X, y)
		for x := 0; x < r.Dx(); x++ {
			for k := 0; k < 3; k++ {
				d := float64(a.Pix[oa+k]) - float64(b.Pix[ob+k])
				se += d * d
			}
			oa += 4
			ob += 4
		}
	}
	mse := se / float64(r.Dx()*r.Dy()*3)
	psnr := math.Inf(1)
	if mse > 0 {
		psnr = 10 * math.Log10(255*255/mse)
	}
	la, lb := luma(a, r), luma(b, r)
	w, h := r.Dx(), r.Dy()
	const win, stride = 8, 4
	c1, c2 := math.Pow(0.01*255, 2), math.Pow(0.03*255, 2)
	var sum float64
	var n int
	for y := 0; y+win <= h; y += stride {
		for x := 0; x+win <= w; x += stride {
			var ma, mb float64
			for j := 0; j < win; j++ {
				for i := 0; i < win; i++ {
					ma += la[(y+j)*w+x+i]
					mb += lb[(y+j)*w+x+i]
				}
			}
			ma /= win * win
			mb /= win * win
			var va, vb, cov float64
			for j := 0; j < win; j++ {
				for i := 0; i < win; i++ {
					da, db := la[(y+j)*w+x+i]-ma, lb[(y+j)*w+x+i]-mb
					va += da * da
					vb += db * db
					cov += da * db
				}
			}
			va /= win*win - 1
			vb /= win*win - 1
			cov /= win*win - 1
			sum += ((2*ma*mb + c1) * (2*cov + c2)) / ((ma*ma + mb*mb + c1) * (va + vb + c2))
			n++
		}
	}
	s := 1.0
	if n > 0 {
		s = sum / float64(n)
	}
	return quality{SSIM: s, PSNR: psnr}
}
