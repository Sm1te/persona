package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// session is a connected client whose reader runs on its own goroutine; updates are counted and announced.
type session struct {
	*client
	mu      sync.Mutex
	cond    *sync.Cond
	updates []updateInfo
	err     error
	done    bool
	// requests: how this session asks for updates (stopwait: one after each update is drawn; early: one as each
	// update's header comes; pipeline: keeps two asked; continuous: ContinuousUpdates; none: the caller asks).
	requests string
	// area is the part of the screen incremental requests ask for (empty: all of it), as a zoomed-in view would.
	area atomic.Value
}

// gatedLimit is the largest update asked past early in the gated way: well inside a server's socket buffer, so the
// request never reaches it while it is still writing (macOS's Screen Sharing then goes quiet for up to two seconds).
const gatedLimit = 64 << 10

// asked is the part incremental requests are for.
func (s *session) asked() image.Rectangle {
	if r, ok := s.area.Load().(image.Rectangle); ok && !r.Empty() {
		return r
	}
	return s.Whole()
}

func start(c *client, requests string) *session {
	s := &session{client: c, requests: requests}
	s.cond = sync.NewCond(&s.mu)
	if requests == "early" {
		c.OnUpdateStart = func() { _ = c.Request(true, s.asked()) }
	}
	// gated: early when the update is one small rectangle (its bytes known before they are read), else once it is in.
	var gatedAsked bool
	if requests == "gated" {
		c.OnUpdateStart = func() { gatedAsked = false }
		c.OnFirstSize = func(count, size int) {
			if count == 1 && size <= gatedLimit {
				gatedAsked = true
				_ = c.Request(true, s.asked())
			}
		}
	}
	c.OnUpdate = func(u updateInfo) {
		s.mu.Lock()
		s.updates = append(s.updates, u)
		s.cond.Broadcast()
		s.mu.Unlock()
		switch s.requests {
		case "stopwait", "pipeline":
			_ = c.Request(true, s.asked())
		case "gated":
			if !gatedAsked {
				_ = c.Request(true, s.asked())
			}
		}
	}
	go func() {
		err := c.Run()
		s.mu.Lock()
		s.err, s.done = err, true
		s.cond.Broadcast()
		s.mu.Unlock()
	}()
	return s
}

// first asks for the whole screen and, for pipeline, a second incremental request behind it.
func (s *session) first() error {
	if err := s.Request(false, s.Whole()); err != nil {
		return err
	}
	if s.requests == "pipeline" {
		return s.Request(true, s.Whole())
	}
	return nil
}

// waitUpdates waits until n updates have come in all, or d passes; it reports how many came.
func (s *session) waitUpdates(n int, d time.Duration) int {
	deadline := time.Now().Add(d)
	t := time.AfterFunc(d, func() { s.mu.Lock(); s.cond.Broadcast(); s.mu.Unlock() })
	defer t.Stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.updates) < n && !s.done && time.Now().Before(deadline) {
		s.cond.Wait()
	}
	return len(s.updates)
}

func (s *session) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.updates)
}

func (s *session) since(i int) []updateInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.updates[min(i, len(s.updates)):])
}

func (s *session) failure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if errors.Is(s.err, io.EOF) {
		return nil
	}
	return s.err
}

// settle waits until no update has come for quiet (asking again as its mode does), at most d.
func (s *session) settle(quiet, d time.Duration) bool {
	deadline := time.Now().Add(d)
	last, lastAt := s.count(), time.Now()
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		if n := s.count(); n != last {
			last, lastAt = n, time.Now()
		} else if time.Since(lastAt) >= quiet {
			return true
		}
	}
	return false
}

type probeResult struct {
	Name   string
	Result string
}

func probe(ctx context.Context, t target, cred credentials) error {
	fmt.Printf("VNC probe of %s\n\n", t)
	c, err := connect(ctx, t, cred, pf32, []int32{encRaw}, nil)
	if err != nil {
		return err
	}
	pf := c.ServerPF
	fmt.Printf("server: %q, %d×%d, %q\n", c.Version, c.Width, c.Height, c.Name)
	fmt.Printf("its pixel format: %d bpp, depth %d, big-endian %v, true colour %v, max %d/%d/%d, shift %d/%d/%d\n\n",
		pf.BPP, pf.Depth, pf.BigEndian, pf.TrueColour, pf.RMax, pf.GMax, pf.BMax, pf.RShift, pf.GShift, pf.BShift)
	_ = c.Close()

	// Image encodings, one connection each: SetEncodings [E, Raw] then a full update. The encodings of its rectangles
	// say whether the server took E.
	fmt.Println("## Image encodings (asked alone, Raw behind it; a whole-screen update)")
	fmt.Println()
	fmt.Println("| encoding | answered with | bytes | ms |")
	fmt.Println("| --- | --- | ---: | ---: |")
	for _, e := range []int32{encRaw, 2, 4, 5, encZlib, encTight, 8, 15, encZRLE, 17, 20, 21, 50, -260} {
		r, err := oneUpdate(ctx, t, cred, pf32, []int32{e, encRaw})
		if err != nil && r.unknown != 0 {
			fmt.Printf("| %d %s | %s (no decoder here) | | |\n", e, encName(e), encName(r.unknown))
			continue
		}
		if err != nil {
			fmt.Printf("| %d %s | error: %v | | |\n", e, encName(e), err)
			continue
		}
		fmt.Printf("| %d %s | %s | %d | %.0f |\n", e, encName(e), r.used, r.bytes, r.ms)
	}
	fmt.Println()

	fmt.Println("## Preference order")
	fmt.Println()
	for _, encs := range [][]int32{{encRaw, encZRLE}, {encZRLE, encTight}, {encTight, encZRLE}} {
		r, err := oneUpdate(ctx, t, cred, pf32, encs)
		names := []string{}
		for _, e := range encs {
			names = append(names, encName(e))
		}
		if err != nil {
			fmt.Printf("- asked %s: error %v\n", strings.Join(names, ", "), err)
			continue
		}
		fmt.Printf("- asked %s: answered with %s\n", strings.Join(names, ", "), r.used)
	}
	fmt.Println()

	// Encodings above 255 the server might have of its own (Apple's): ask all of them before Raw; whatever it
	// answers with is one, then ask again without it.
	fmt.Println("## Private encodings (1000–1199, 0x574D5600 VMware, ask all before Raw, peel off what is used)")
	fmt.Println()
	var priv []int32
	for e := int32(1000); e < 1200; e++ {
		priv = append(priv, e)
	}
	priv = append(priv, 0x574D5664, 0x574D5665, 0x574D5666, 0x574D5667, 0x574D5668)
	found := []string{}
	for range 8 {
		r, err := oneUpdate(ctx, t, cred, pf32, append(slices.Clone(priv), encRaw))
		if err != nil && r.unknown == 0 {
			fmt.Printf("- error: %v\n", err)
			break
		}
		if r.unknown == 0 {
			break
		}
		found = append(found, fmt.Sprintf("%d (0x%X)", r.unknown, r.unknown))
		priv = slices.DeleteFunc(priv, func(e int32) bool { return e == r.unknown })
	}
	if len(found) == 0 {
		fmt.Println("- none: it answers with Raw")
	} else {
		fmt.Println("- used: " + strings.Join(found, ", "))
	}
	fmt.Println()

	fmt.Println("## Pseudo-encodings")
	fmt.Println()
	if err := probePseudo(ctx, t, cred); err != nil {
		fmt.Printf("- error: %v\n", err)
	}
	fmt.Println()

	fmt.Println("## Pixel formats (ZRLE, whole screen, quality against 32-bit)")
	fmt.Println()
	if err := probeFormats(ctx, t, cred); err != nil {
		fmt.Printf("- error: %v\n", err)
	}
	fmt.Println()

	fmt.Println("## Tight quality and compression levels (whole screen, against lossless ZRLE)")
	fmt.Println()
	if err := probeTight(ctx, t, cred); err != nil {
		fmt.Printf("- error: %v\n", err)
	}
	return nil
}

type oneResult struct {
	used    string
	bytes   int64
	ms      float64
	unknown int32
	shot    *image.RGBA
	kinds   []int64
}

// oneUpdate connects, asks for encs and one whole-screen update, and reports what it came in.
func oneUpdate(ctx context.Context, t target, cred credentials, pf pixelFormat, encs []int32) (oneResult, error) {
	c, err := connect(ctx, t, cred, pf, encs, nil)
	if err != nil {
		return oneResult{}, err
	}
	defer c.Close()
	c.tightKinds = make([]int64, tightKinds)
	used := map[int32]int{}
	var mu sync.Mutex
	c.OnRect = func(x, y, w, h int, enc int32, bytes int64) {
		mu.Lock()
		used[enc]++
		mu.Unlock()
	}
	s := start(c, "none")
	sent := time.Now()
	if err := s.Request(false, s.Whole()); err != nil {
		return oneResult{}, err
	}
	s.waitUpdates(1, 10*time.Second)
	r := oneResult{unknown: c.Unknown.Load(), kinds: c.tightKinds}
	mu.Lock()
	keys := []int32{}
	for e := range used {
		keys = append(keys, e)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	parts := []string{}
	for _, e := range keys {
		parts = append(parts, fmt.Sprintf("%s×%d", encName(e), used[e]))
	}
	mu.Unlock()
	r.used = strings.Join(parts, " ")
	ups := s.since(0)
	if len(ups) == 0 {
		if r.unknown != 0 {
			return r, fmt.Errorf("unknown encoding %d", r.unknown)
		}
		if err := s.failure(); err != nil {
			return r, err
		}
		return r, errors.New("no update in 10 s")
	}
	r.bytes = ups[0].Bytes
	r.ms = float64(ups[0].End.Sub(sent).Microseconds()) / 1000
	r.shot = c.Snapshot()
	return r, nil
}

func probePseudo(ctx context.Context, t target, cred credentials) error {
	encs := []int32{encZRLE, encCopyRect, encRaw, encDesktop, encExtDesktop, encCursor, encXCursor, encPointerPos, encLastRect,
		encFence, encContinuous}
	c, err := connect(ctx, t, cred, pf32, encs, nil)
	if err != nil {
		return err
	}
	defer c.Close()
	used := map[int32]int{}
	var mu sync.Mutex
	c.OnRect = func(x, y, w, h int, enc int32, bytes int64) {
		mu.Lock()
		used[enc]++
		mu.Unlock()
	}
	s := start(c, "none")
	if err := s.Request(false, s.Whole()); err != nil {
		return err
	}
	s.waitUpdates(1, 10*time.Second)
	time.Sleep(time.Second)
	mu.Lock()
	fmt.Printf("- ExtendedDesktopSize (-308) rectangle in the first update: %v\n", used[encExtDesktop] > 0)
	fmt.Printf("- Cursor (-239) / XCursor (-240) shape sent: %v / %v\n", used[encCursor] > 0, used[encXCursor] > 0)
	fmt.Printf("- PointerPos (-232) sent: %v\n", used[encPointerPos] > 0)
	mu.Unlock()
	fmt.Printf("- Fence (-312): server Fence message: %v\n", c.FenceOK.Load())
	fmt.Printf("- ContinuousUpdates (-313): EndOfContinuousUpdates message: %v\n", c.ContinuousOK.Load())
	if !c.ContinuousOK.Load() {
		return nil
	}
	n := s.count()
	if err := c.EnableContinuous(true, c.Whole()); err != nil {
		return err
	}
	_ = c.Pointer(c.Width/2, c.Height/2, 0)
	time.Sleep(200 * time.Millisecond)
	_ = c.Pointer(c.Width/2+40, c.Height/2+40, 0)
	time.Sleep(time.Second)
	fmt.Printf("- with ContinuousUpdates on: %d updates in 1.2 s without a request\n", s.count()-n)
	return nil
}

func probeFormats(ctx context.Context, t target, cred credentials) error {
	ref, err := oneUpdate(ctx, t, cred, pf32, []int32{encZRLE})
	if err != nil {
		return err
	}
	fmt.Println("| format | bytes | vs 32-bit | SSIM | PSNR dB |")
	fmt.Println("| --- | ---: | ---: | ---: | ---: |")
	fmt.Printf("| 32 bpp (the app's) | %d | 100%% | 1 | ∞ |\n", ref.bytes)
	for _, f := range []struct {
		name string
		pf   pixelFormat
	}{{"16 bpp RGB565", pf16}, {"8 bpp BGR233", pf8}} {
		r, err := oneUpdate(ctx, t, cred, f.pf, []int32{encZRLE})
		if err != nil {
			fmt.Printf("| %s | error: %v | | | |\n", f.name, err)
			continue
		}
		q := compare(r.shot, ref.shot, ref.shot.Rect)
		fmt.Printf("| %s (%s) | %d | %.0f%% | %.4f | %.1f |\n", f.name, r.used, r.bytes, 100*float64(r.bytes)/float64(ref.bytes), q.SSIM, q.PSNR)
	}
	return nil
}

func probeTight(ctx context.Context, t target, cred credentials) error {
	ref, err := oneUpdate(ctx, t, cred, pf32, []int32{encZRLE})
	if err != nil {
		return err
	}
	fmt.Printf("lossless ZRLE: %d bytes\n\n", ref.bytes)
	fmt.Println("| asked | answered with | bytes | vs ZRLE | SSIM | PSNR dB | kinds (fill/jpeg/copy/palette/gradient) |")
	fmt.Println("| --- | --- | ---: | ---: | ---: | ---: | --- |")
	type ask struct {
		name string
		encs []int32
	}
	asks := []ask{{"Tight, no quality", []int32{encTight}}}
	for _, l := range []int32{1, 6, 9} {
		asks = append(asks, ask{fmt.Sprintf("Tight, compress %d, no quality", l), []int32{encTight, encCompress0 + l}})
	}
	for _, q := range []int32{0, 2, 4, 5, 6, 7, 8, 9} {
		asks = append(asks, ask{fmt.Sprintf("Tight, quality %d", q), []int32{encTight, encQuality0 + q, encCompress0 + 6}})
	}
	for _, a := range asks {
		r, err := oneUpdate(ctx, t, cred, pf32, a.encs)
		if err != nil {
			fmt.Printf("| %s | error: %v | | | | | |\n", a.name, err)
			continue
		}
		q := compare(r.shot, ref.shot, ref.shot.Rect)
		fmt.Printf("| %s | %s | %d | %.0f%% | %.4f | %.1f | %v |\n", a.name, r.used, r.bytes, 100*float64(r.bytes)/float64(ref.bytes), q.SSIM, q.PSNR, r.kinds)
	}
	return nil
}

// shot saves the screen as the app would get it (ZRLE, 32-bit) to a PNG, scaled down by scale.
func shot(ctx context.Context, t target, cred credentials, path string, scale int) error {
	r, err := oneUpdate(ctx, t, cred, pf32, []int32{encZRLE})
	if err != nil {
		return err
	}
	img := r.shot
	if scale > 1 {
		b := img.Rect
		small := image.NewRGBA(image.Rect(0, 0, b.Dx()/scale, b.Dy()/scale))
		for y := 0; y < small.Rect.Dy(); y++ {
			for x := 0; x < small.Rect.Dx(); x++ {
				small.Set(x, y, img.At(x*scale, y*scale))
			}
		}
		img = small
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// probePrivate asks for encodings lo…hi in chunks of size before Raw, each on a connection of its own, and says what
// each chunk was answered with.
func probePrivate(ctx context.Context, t target, cred credentials, lo, hi, size int32) {
	for a := lo; a <= hi; a += size {
		var encs []int32
		for e := a; e < a+size && e <= hi; e++ {
			encs = append(encs, e)
		}
		r, err := oneUpdate(ctx, t, cred, pf32, append(encs, encRaw))
		switch {
		case r.unknown != 0:
			fmt.Printf("- %d–%d: answered with %d (0x%X), not decodable here\n", a, encs[len(encs)-1], r.unknown, r.unknown)
		case err != nil:
			fmt.Printf("- %d–%d: %v\n", a, encs[len(encs)-1], err)
		default:
			fmt.Printf("- %d–%d: %s\n", a, encs[len(encs)-1], r.used)
		}
	}
}

// dump asks for enc alone (then Raw) and one whole-screen update, and prints the update's header, the first
// rectangle's header and the bytes after it, with how many came in all within wait: for an encoding this tool cannot
// read (Apple's own), to see what it is.
func dump(ctx context.Context, t target, cred credentials, enc int32, n int, wait time.Duration, out string) error {
	encs := []int32{enc, encRaw}
	c, err := connect(ctx, t, cred, pf32, encs, nil)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Request(false, c.Whole()); err != nil {
		return err
	}
	_ = c.conn.SetReadDeadline(time.Now().Add(wait))
	var all []byte
	buf := make([]byte, 64<<10)
	for {
		k, err := c.r.Read(buf)
		all = append(all, buf[:k]...)
		if err != nil {
			break
		}
	}
	fmt.Printf("encoding %d: %d bytes in %s\n", enc, len(all), wait)
	if out != "" {
		if err := os.WriteFile(out, all, 0o600); err != nil {
			return err
		}
	}
	if len(all) >= 16 {
		fmt.Printf("update: type %d, %d rectangles\n", all[0], binary.BigEndian.Uint16(all[2:]))
		h := all[4:16]
		fmt.Printf("rect: %d,%d %d×%d encoding %d\n", binary.BigEndian.Uint16(h[0:]), binary.BigEndian.Uint16(h[2:]),
			binary.BigEndian.Uint16(h[4:]), binary.BigEndian.Uint16(h[6:]), int32(binary.BigEndian.Uint32(h[8:])))
		body := all[16:]
		fmt.Printf("%s", hex.Dump(body[:min(n, len(body))]))
	}
	return nil
}
