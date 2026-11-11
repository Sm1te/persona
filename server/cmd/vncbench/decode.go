package main

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"io"
)

// feeder holds one zlib stream's compressed bytes as they come, rectangle by rectangle. It is an io.ByteReader, so
// the inflater reads it a byte at a time and never ahead of what it needs.
type feeder struct {
	buf []byte
	at  int
}

var errStarved = errors.New("zlib stream starved: the rectangle's data ran out")

func (f *feeder) Read(p []byte) (int, error) {
	if f.at >= len(f.buf) {
		return 0, errStarved
	}
	n := copy(p, f.buf[f.at:])
	f.at += n
	return n, nil
}

func (f *feeder) ReadByte() (byte, error) {
	if f.at >= len(f.buf) {
		return 0, errStarved
	}
	b := f.buf[f.at]
	f.at++
	return b, nil
}

func (f *feeder) add(b []byte) {
	if f.at == len(f.buf) {
		f.buf, f.at = f.buf[:0], 0
	} else if f.at > 1<<20 {
		f.buf = append(f.buf[:0], f.buf[f.at:]...)
		f.at = 0
	}
	f.buf = append(f.buf, b...)
}

// zstream is one zlib stream that lasts the connection (ZRLE's, Tight's four, Zlib's).
type zstream struct {
	f feeder
	r io.Reader
}

func (z *zstream) reset() { *z = zstream{} }

func (z *zstream) feed(b []byte) error {
	z.f.add(b)
	if z.r == nil {
		r, err := zlib.NewReader(&z.f)
		if err != nil {
			return fmt.Errorf("zlib header: %w", err)
		}
		z.r = r
	}
	return nil
}

func (z *zstream) read(p []byte) error {
	_, err := io.ReadFull(z.r, p)
	return err
}

func (c *client) compressed(z *zstream, n int) error {
	b := make([]byte, n)
	if _, err := io.ReadFull(c.r, b); err != nil {
		return err
	}
	return z.feed(b)
}

// zrleRect is ZRLE (16): 64×64 tiles of CPIXELs out of the connection's zlib stream.
func (c *client) zrleRect(x, y, w, h int) error {
	n, err := c.u32()
	if err != nil {
		return err
	}
	c.firstSize(int(n))
	if err := c.compressed(&c.zrle, int(n)); err != nil {
		return err
	}
	z := &c.zrle
	cp := c.pf.cpixel()
	px := func(b []byte) (uint8, uint8, uint8) { return c.pf.rgb(c.pf.value(b, cp)) }
	var one [1]byte
	byteAt := func() (byte, error) {
		err := z.read(one[:])
		return one[0], err
	}
	run := func() (int, error) {
		n := 1
		for {
			b, err := byteAt()
			if err != nil {
				return 0, err
			}
			n += int(b)
			if b != 255 {
				return n, nil
			}
		}
	}
	buf := make([]byte, 64*64*cp)
	var pal [128][3]uint8
	for ty := y; ty < y+h; ty += 64 {
		th := min(64, y+h-ty)
		for tx := x; tx < x+w; tx += 64 {
			tw := min(64, x+w-tx)
			sub, err := byteAt()
			if err != nil {
				return err
			}
			switch {
			case sub == 0:
				b := buf[:tw*th*cp]
				if err := z.read(b); err != nil {
					return err
				}
				for j := 0; j < th; j++ {
					o := c.fb.PixOffset(tx, ty+j)
					for i := 0; i < tw; i++ {
						r, g, bb := px(b[(j*tw+i)*cp:])
						c.fb.Pix[o], c.fb.Pix[o+1], c.fb.Pix[o+2], c.fb.Pix[o+3] = r, g, bb, 255
						o += 4
					}
				}
			case sub == 1:
				if err := z.read(buf[:cp]); err != nil {
					return err
				}
				r, g, b := px(buf)
				fill(c.fb, tx, ty, tw, th, r, g, b)
			case sub <= 16:
				np := int(sub)
				if err := z.read(buf[:np*cp]); err != nil {
					return err
				}
				for i := 0; i < np; i++ {
					pal[i][0], pal[i][1], pal[i][2] = px(buf[i*cp:])
				}
				bits := 4
				if np == 2 {
					bits = 1
				} else if np <= 4 {
					bits = 2
				}
				rowBytes := (tw*bits + 7) / 8
				b := buf[:rowBytes*th]
				if err := z.read(b); err != nil {
					return err
				}
				mask := byte(1<<bits - 1)
				for j := 0; j < th; j++ {
					o := c.fb.PixOffset(tx, ty+j)
					for i := 0; i < tw; i++ {
						bit := i * bits
						idx := int(b[j*rowBytes+bit/8]>>(8-bits-bit%8)) & int(mask)
						if idx >= np {
							return fmt.Errorf("zrle palette index")
						}
						c.fb.Pix[o], c.fb.Pix[o+1], c.fb.Pix[o+2], c.fb.Pix[o+3] = pal[idx][0], pal[idx][1], pal[idx][2], 255
						o += 4
					}
				}
			case sub == 128:
				for i := 0; i < tw*th; {
					if err := z.read(buf[:cp]); err != nil {
						return err
					}
					r, g, b := px(buf)
					n, err := run()
					if err != nil {
						return err
					}
					for k := i; k < min(i+n, tw*th); k++ {
						o := c.fb.PixOffset(tx+k%tw, ty+k/tw)
						c.fb.Pix[o], c.fb.Pix[o+1], c.fb.Pix[o+2], c.fb.Pix[o+3] = r, g, b, 255
					}
					i += n
				}
			case sub >= 130:
				np := int(sub) - 128
				if err := z.read(buf[:np*cp]); err != nil {
					return err
				}
				for i := 0; i < np; i++ {
					pal[i][0], pal[i][1], pal[i][2] = px(buf[i*cp:])
				}
				for i := 0; i < tw*th; {
					v, err := byteAt()
					if err != nil {
						return err
					}
					n := 1
					if v&128 != 0 {
						if n, err = run(); err != nil {
							return err
						}
					}
					idx := int(v & 127)
					if idx >= np {
						return fmt.Errorf("zrle palette index")
					}
					for k := i; k < min(i+n, tw*th); k++ {
						o := c.fb.PixOffset(tx+k%tw, ty+k/tw)
						c.fb.Pix[o], c.fb.Pix[o+1], c.fb.Pix[o+2], c.fb.Pix[o+3] = pal[idx][0], pal[idx][1], pal[idx][2], 255
					}
					i += n
				}
			default:
				return fmt.Errorf("zrle subencoding %d", sub)
			}
		}
	}
	return nil
}

func (c *client) zlibRect(x, y, w, h int) error {
	n, err := c.u32()
	if err != nil {
		return err
	}
	if err := c.compressed(&c.zlib, int(n)); err != nil {
		return err
	}
	bpp := c.pf.bytesPerPixel()
	b := make([]byte, w*h*bpp)
	if err := c.zlib.read(b); err != nil {
		return err
	}
	for j := 0; j < h; j++ {
		o := c.fb.PixOffset(x, y+j)
		for i := 0; i < w; i++ {
			r, g, bb := c.pf.rgb(c.pf.value(b[(j*w+i)*bpp:], bpp))
			c.fb.Pix[o], c.fb.Pix[o+1], c.fb.Pix[o+2], c.fb.Pix[o+3] = r, g, bb, 255
			o += 4
		}
	}
	return nil
}

// compactLength is Tight's 1-3 byte length.
func (c *client) compactLength() (int, error) {
	n := 0
	for i := 0; i < 3; i++ {
		b, err := c.r.ReadByte()
		if err != nil {
			return 0, err
		}
		if i == 2 {
			return n | int(b)<<14, nil
		}
		n |= int(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			return n, nil
		}
	}
	return n, nil
}

// TightStats counts Tight's subencodings by kind (fill, jpeg, copy, palette, gradient) and their bytes.
type tightKind int

const (
	tightFill tightKind = iota
	tightJPEG
	tightCopy
	tightPalette
	tightGradient
	tightKinds
)

var tightNames = [...]string{"fill", "jpeg", "copy", "palette", "gradient"}

// tightRect is Tight (7): fill, JPEG, or basic compression (copy, palette or gradient filter) on one of four zlib
// streams.
func (c *client) tightRect(x, y, w, h int) error {
	ctl, err := c.r.ReadByte()
	if err != nil {
		return err
	}
	for i := 0; i < 4; i++ {
		if ctl&(1<<i) != 0 {
			c.tight[i].reset()
		}
	}
	kind := ctl >> 4
	tp := c.pf.tpixel()
	pl := c.pf.bytesPerPixel()
	if tp {
		pl = 3
	}
	px := func(b []byte) (uint8, uint8, uint8) {
		if tp {
			return b[0], b[1], b[2]
		}
		return c.pf.rgb(c.pf.value(b, pl))
	}
	switch {
	case kind == 8:
		b := make([]byte, pl)
		if _, err := io.ReadFull(c.r, b); err != nil {
			return err
		}
		r, g, bb := px(b)
		fill(c.fb, x, y, w, h, r, g, bb)
		c.tightCount(tightFill)
		return nil
	case kind == 9:
		n, err := c.compactLength()
		if err != nil {
			return err
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(c.r, b); err != nil {
			return err
		}
		img, err := jpeg.Decode(bytes.NewReader(b))
		if err != nil {
			return fmt.Errorf("tight jpeg: %w", err)
		}
		draw.Draw(c.fb, image.Rect(x, y, x+w, y+h), img, img.Bounds().Min, draw.Src)
		c.tightCount(tightJPEG)
		return nil
	case kind > 9:
		return fmt.Errorf("tight compression %d", kind)
	}
	stream := int(kind & 3)
	filter := byte(0)
	if kind&4 != 0 {
		if filter, err = c.r.ReadByte(); err != nil {
			return err
		}
	}
	var pal [256][3]uint8
	np := 0
	size := w * h * pl
	switch filter {
	case 0:
		c.tightCount(tightCopy)
	case 1:
		b, err := c.r.ReadByte()
		if err != nil {
			return err
		}
		np = int(b) + 1
		pb := make([]byte, np*pl)
		if _, err := io.ReadFull(c.r, pb); err != nil {
			return err
		}
		for i := 0; i < np; i++ {
			pal[i][0], pal[i][1], pal[i][2] = px(pb[i*pl:])
		}
		if np == 2 {
			size = (w + 7) / 8 * h
		} else {
			size = w * h
		}
		c.tightCount(tightPalette)
	case 2:
		c.tightCount(tightGradient)
	default:
		return fmt.Errorf("tight filter %d", filter)
	}
	data := make([]byte, size)
	if size < 12 {
		if _, err := io.ReadFull(c.r, data); err != nil {
			return err
		}
	} else {
		n, err := c.compactLength()
		if err != nil {
			return err
		}
		if err := c.compressed(&c.tight[stream], n); err != nil {
			return err
		}
		if err := c.tight[stream].read(data); err != nil {
			return fmt.Errorf("tight stream %d: %w", stream, err)
		}
	}
	switch filter {
	case 0:
		for j := 0; j < h; j++ {
			o := c.fb.PixOffset(x, y+j)
			for i := 0; i < w; i++ {
				r, g, b := px(data[(j*w+i)*pl:])
				c.fb.Pix[o], c.fb.Pix[o+1], c.fb.Pix[o+2], c.fb.Pix[o+3] = r, g, b, 255
				o += 4
			}
		}
	case 1:
		for j := 0; j < h; j++ {
			o := c.fb.PixOffset(x, y+j)
			for i := 0; i < w; i++ {
				var idx int
				if np == 2 {
					idx = int(data[j*((w+7)/8)+i/8]>>(7-i%8)) & 1
				} else {
					idx = int(data[j*w+i])
				}
				if idx >= np {
					return fmt.Errorf("tight palette index")
				}
				c.fb.Pix[o], c.fb.Pix[o+1], c.fb.Pix[o+2], c.fb.Pix[o+3] = pal[idx][0], pal[idx][1], pal[idx][2], 255
				o += 4
			}
		}
	case 2:
		if !tp {
			return fmt.Errorf("tight gradient needs 24-bit pixels here")
		}
		prev := make([]int, (w+1)*3)
		cur := make([]int, (w+1)*3)
		for j := 0; j < h; j++ {
			o := c.fb.PixOffset(x, y+j)
			for i := 0; i < w; i++ {
				var v [3]uint8
				for k := 0; k < 3; k++ {
					left, up, ul := cur[i*3+k], prev[(i+1)*3+k], prev[i*3+k]
					p := min(max(left+up-ul, 0), 255)
					v[k] = uint8(p + int(data[(j*w+i)*3+k]))
					cur[(i+1)*3+k] = int(v[k])
				}
				c.fb.Pix[o], c.fb.Pix[o+1], c.fb.Pix[o+2], c.fb.Pix[o+3] = v[0], v[1], v[2], 255
				o += 4
			}
			prev, cur = cur, prev
			for k := range cur {
				cur[k] = 0
			}
		}
	}
	return nil
}

func (c *client) tightCount(k tightKind) {
	if c.tightKinds != nil {
		c.tightKinds[k]++
	}
}
