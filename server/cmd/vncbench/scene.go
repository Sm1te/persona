package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

type runFlags struct {
	scenes  *string
	config  *string
	mode    *string
	ref     *bool
	repeat  *int
	jsonOut *string
	record  *string
	browser *string
	area    *string
}

func (r *runFlags) register(fs *flag.FlagSet) {
	r.scenes = fs.String("scenes", "all", "comma-separated: idle,type,scroll,drag,video (all: every one)")
	r.config = fs.String("config", "app", "encodings: app (the app's today), or a list such as tight,zrle,copyrect,raw,desktop,ext,cursor,last,q6,c1,cu,16bpp")
	r.mode = fs.String("mode", "stopwait", "how updates are asked for: stopwait, early, gated, pipeline, continuous")
	r.ref = fs.Bool("ref", true, "a second, lossless connection (the app's) for quality, and its bytes for comparison")
	r.repeat = fs.Int("repeat", 1, "runs of each scene")
	r.jsonOut = fs.String("json", "", "append one JSON line per scene run to this file")
	r.record = fs.String("record", "", "record the test connection's bytes into this directory, one file per scene (VNCREC1)")
	r.browser = fs.String("browser", "Safari", "the browser that shows the bench page")
	r.area = fs.String("area", "", "page: the test connection asks for the bench page's part of the screen only (a view zoomed in on it)")
}

// appEncodings is what the app asks for today (RFBClient.encodings): ZRLE, CopyRect, Raw, DesktopSize,
// ExtendedDesktopSize, Cursor, LastRect.
var appEncodings = []int32{encZRLE, encCopyRect, encRaw, encDesktop, encExtDesktop, encCursor, encLastRect}

func parseConfig(s string) ([]int32, pixelFormat, error) {
	if s == "app" {
		return appEncodings, pf32, nil
	}
	pf := pf32
	var out []int32
	for _, p := range strings.Split(s, ",") {
		switch p = strings.TrimSpace(p); {
		case p == "tight":
			out = append(out, encTight)
		case p == "zrle":
			out = append(out, encZRLE)
		case p == "zlib":
			out = append(out, encZlib)
		case p == "copyrect":
			out = append(out, encCopyRect)
		case p == "raw":
			out = append(out, encRaw)
		case p == "desktop":
			out = append(out, encDesktop)
		case p == "ext":
			out = append(out, encExtDesktop)
		case p == "cursor":
			out = append(out, encCursor)
		case p == "last":
			out = append(out, encLastRect)
		case p == "cu":
			out = append(out, encContinuous, encFence)
		case p == "16bpp":
			pf = pf16
		case len(p) == 2 && p[0] == 'q' && p[1] >= '0' && p[1] <= '9':
			out = append(out, encQuality0+int32(p[1]-'0'))
		case len(p) == 2 && p[0] == 'c' && p[1] >= '0' && p[1] <= '9':
			out = append(out, encCompress0+int32(p[1]-'0'))
		default:
			return nil, pf, fmt.Errorf("unknown config item %q", p)
		}
	}
	return out, pf, nil
}

// bench is the page the scenes run in, served here and shown in a browser on the Mac: it switches scenes as /state
// says and reports the inputs it saw (/event), and its markers let the screen be found in the framebuffer.
type bench struct {
	url     string
	browser string
	polled  time.Time // a page last asked for the scene

	mu     sync.Mutex
	scene  string
	freeze bool
	events []pageEvent
	hello  *pageEvent
	photos []string
}

type pageEvent struct {
	Kind string  `json:"k"`
	T    float64 `json:"t"` // when the page got it, ms since the epoch
	P    float64 `json:"p"` // the next animation frame after it
	W    int     `json:"w"`
	H    int     `json:"h"`
	DPR  float64 `json:"dpr"`
	FPS  float64 `json:"fps"`
}

func newBench() (*bench, error) {
	b := &bench{scene: "idle"}
	b.photos = photos()
	// A fixed port, so a page left open by an earlier run comes back to this one by itself (it asks /state every
	// 100 ms) instead of a new tab each run.
	l, err := net.Listen("tcp", "127.0.0.1:38780")
	if err != nil {
		if l, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
			return nil, err
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(benchPage))
	})
	mux.HandleFunc("/state", func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		b.polled = time.Now()
		s := map[string]any{"scene": b.scene, "freeze": b.freeze, "photos": len(b.photos)}
		b.mu.Unlock()
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(s)
	})
	mux.HandleFunc("/photo/", func(w http.ResponseWriter, r *http.Request) {
		var i int
		_, _ = fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/photo/"), "%d", &i)
		if i < 0 || i >= len(b.photos) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "max-age=3600")
		http.ServeFile(w, r, b.photos[i])
	})
	mux.HandleFunc("/event", func(w http.ResponseWriter, r *http.Request) {
		var e pageEvent
		if json.NewDecoder(r.Body).Decode(&e) == nil {
			b.mu.Lock()
			if e.Kind == "hello" {
				b.hello = &e
			} else {
				b.events = append(b.events, e)
			}
			b.mu.Unlock()
		}
		w.WriteHeader(http.StatusNoContent)
	})
	go func() { _ = http.Serve(l, mux) }()
	b.url = "http://" + l.Addr().String() + "/"
	return b, nil
}

// photos are a few of macOS's own desktop pictures as JPEG, for the page's photographic content (none: the page
// draws its own).
func photos() []string {
	dir := filepath.Join(os.TempDir(), "vncbench-photos")
	_ = os.MkdirAll(dir, 0o755)
	var out []string
	for i, src := range []string{
		"/System/Library/Desktop Pictures/Sonoma.heic",
		"/System/Library/Desktop Pictures/.wallpapers/Sonoma Horizon/Sonoma Horizon.heic",
		"/System/Library/Desktop Pictures/iMac Blue.heic",
		"/System/Library/Desktop Pictures/Radial Sky Blue.heic",
	} {
		dst := filepath.Join(dir, fmt.Sprintf("%d.jpg", i))
		if _, err := os.Stat(dst); err != nil {
			if _, err := os.Stat(src); err != nil {
				continue
			}
			if exec.Command("sips", "-s", "format", "jpeg", "-s", "formatOptions", "90", "-Z", "1600", src, "--out", dst).Run() != nil {
				continue
			}
		}
		out = append(out, dst)
	}
	return out
}

// show brings the page up: one already open finds this run within a moment; else the browser opens it.
func (b *bench) show() error {
	time.Sleep(1500 * time.Millisecond)
	b.mu.Lock()
	open := time.Since(b.polled) < time.Second
	b.mu.Unlock()
	if open {
		return exec.Command("open", "-a", b.browser).Run()
	}
	if err := exec.Command("open", "-a", b.browser, b.url).Run(); err != nil {
		return fmt.Errorf("open the bench page: %w", err)
	}
	return nil
}

func (b *bench) set(scene string, freeze bool) {
	b.mu.Lock()
	b.scene, b.freeze = scene, freeze
	b.mu.Unlock()
}

func (b *bench) eventsSince(t time.Time) []pageEvent {
	ms := float64(t.UnixNano()) / 1e6
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []pageEvent
	for _, e := range b.events {
		if e.T >= ms {
			out = append(out, e)
		}
	}
	return out
}

// layout is where the page is on the Mac's screen, in framebuffer pixels.
type layout struct {
	View  image.Rectangle // the page's viewport
	Scale float64         // framebuffer pixels a CSS pixel
}

func (l layout) at(x, y float64) image.Point {
	return image.Pt(l.View.Min.X+int(x*l.Scale), l.View.Min.Y+int(y*l.Scale))
}

// marker is the 24×24 square that changes colour on every input the page gets (16 px in, at the top).
func (l layout) marker() image.Point { return l.at(28, 12) }
func (l layout) focus() image.Point  { return l.at(56, 12) }

// stage is the area the scenes draw in.
func (l layout) stage() image.Rectangle {
	return image.Rectangle{l.at(0, 28), l.View.Max}
}

var palette = [8][3]uint8{{230, 30, 30}, {30, 200, 30}, {30, 30, 230}, {230, 200, 30}, {20, 20, 20}, {240, 240, 240}, {240, 120, 20}, {150, 30, 200}}

func colourAt(fb *image.RGBA, p image.Point) [3]uint8 {
	if !p.In(fb.Rect) {
		return [3]uint8{}
	}
	o := fb.PixOffset(p.X, p.Y)
	return [3]uint8{fb.Pix[o], fb.Pix[o+1], fb.Pix[o+2]}
}

func near(a, b [3]uint8, tol int) bool {
	for i := range 3 {
		d := int(a[i]) - int(b[i])
		if d < -tol || d > tol {
			return false
		}
	}
	return true
}

// paletteIndex is the marker colour nearest c (-1: none of them).
func paletteIndex(c [3]uint8) int {
	best, bi := 1<<30, -1
	for i, p := range palette {
		d := 0
		for k := range 3 {
			x := int(c[k]) - int(p[k])
			d += x * x
		}
		if d < best {
			best, bi = d, i
		}
	}
	if best > 3*60*60 {
		return -1
	}
	return bi
}

// locate finds the page's corner markers (magenta at the top left, cyan at the bottom right) in fb.
func locate(fb *image.RGBA, dpr float64) (layout, bool) {
	magenta, cyan := [3]uint8{255, 0, 255}, [3]uint8{0, 255, 255}
	block := func(x, y int, c [3]uint8) bool {
		for j := 0; j < 8; j++ {
			for i := 0; i < 8; i++ {
				if !near(colourAt(fb, image.Pt(x+i, y+j)), c, 50) {
					return false
				}
			}
		}
		return true
	}
	var tl, br image.Point
	found := false
	for y := 0; y < fb.Rect.Dy()-8 && !found; y++ {
		for x := 0; x < fb.Rect.Dx()-8; x++ {
			if block(x, y, magenta) {
				tl, found = image.Pt(x, y), true
				break
			}
		}
	}
	if !found {
		return layout{}, false
	}
	found = false
	for y := fb.Rect.Dy() - 1; y >= 8 && !found; y-- {
		for x := fb.Rect.Dx() - 1; x >= 8; x-- {
			if block(x-7, y-7, cyan) && x > tl.X && y > tl.Y && block(x-15, y-15, cyan) {
				br, found = image.Pt(x+1, y+1), true
				break
			}
		}
	}
	if !found {
		return layout{}, false
	}
	if dpr <= 0 {
		dpr = 1
	}
	// The cyan marker ends 24 CSS pixels in from the viewport's corner.
	inset := int(24 * dpr)
	return layout{View: image.Rectangle{tl, br.Add(image.Pt(inset, inset))}, Scale: dpr}, true
}

// targetAt says whether the page's target checker is on the screen at p (its centre): magenta top left and bottom
// right, cyan top right and bottom left (page.go's conic gradient).
func targetAt(fb *image.RGBA, p image.Point) bool {
	magenta, cyan := [3]uint8{255, 0, 255}, [3]uint8{0, 255, 255}
	return near(colourAt(fb, p.Add(image.Pt(-6, -6))), magenta, 60) && near(colourAt(fb, p.Add(image.Pt(6, -6))), cyan, 60) &&
		near(colourAt(fb, p.Add(image.Pt(-6, 6))), cyan, 60) && near(colourAt(fb, p.Add(image.Pt(6, 6))), magenta, 60)
}

// aim waits until the target is visible at p, bringing the browser forward if something covers it.
func aim(b *bench, c *probeConn, p image.Point) error {
	for try := 0; try < 12; try++ {
		var ok bool
		c.WithFramebuffer(func(fb *image.RGBA) { ok = targetAt(fb, p) })
		if ok {
			return nil
		}
		if try%4 == 3 {
			_ = exec.Command("open", "-a", b.browser).Run()
		}
		time.Sleep(400 * time.Millisecond)
	}
	return errors.New("another window covers the bench page where the input would go: nothing sent")
}

// sceneResult is one scene's numbers on the test connection (and the lossless one's bytes alongside).
type sceneResult struct {
	Scene       string    `json:"scene"`
	Config      string    `json:"config"`
	Mode        string    `json:"mode"`
	Target      string    `json:"target"`
	Seconds     float64   `json:"seconds"`
	BytesIn     int64     `json:"bytes_in"`
	BytesOut    int64     `json:"bytes_out"`
	Mbps        float64   `json:"mbps"`
	Updates     int       `json:"updates"`
	FPS         float64   `json:"fps"`
	StageFPS    float64   `json:"stage_fps"`
	Latencies   []float64 `json:"latency_ms"`
	InjectMS    []float64 `json:"inject_ms"`
	ScreenMS    []float64 `json:"screen_ms"`
	SSIM        float64   `json:"ssim"`
	PSNR        float64   `json:"psnr"`
	SSIMs       []float64 `json:"ssims"`
	RefBytesIn  int64     `json:"ref_bytes_in"`
	RefMbps     float64   `json:"ref_mbps"`
	RefUpdates  int       `json:"ref_updates"`
	Encodings   string    `json:"encodings"`
	PageFPS     float64   `json:"page_fps"`
	DecodeMSAvg float64   `json:"decode_ms_avg"`
}

type markerSample struct {
	at  time.Time
	idx int
}

// probeConn is a connection the scenes measure: its updates, and the marker's colour after each that drew it.
type probeConn struct {
	*session
	mu      sync.Mutex
	lay     layout
	samples []markerSample
}

func open(ctx context.Context, t target, cred credentials, pf pixelFormat, encs []int32, mode string, rec *recorder) (*probeConn, error) {
	c, err := connect(ctx, t, cred, pf, encs, rec)
	if err != nil {
		return nil, err
	}
	p := &probeConn{}
	s := start(c, mode)
	p.session = s
	inner := c.OnUpdate
	c.OnUpdate = func(u updateInfo) {
		p.mu.Lock()
		m := p.lay.marker()
		if !p.lay.View.Empty() && m.In(u.Dirty) {
			p.samples = append(p.samples, markerSample{u.End, paletteIndex(colourAt(c.fb, m))})
		}
		p.mu.Unlock()
		inner(u)
	}
	if mode == "continuous" {
		// ContinuousUpdates needs the server's EndOfContinuousUpdates first: asked with the first request.
		if err := s.first(); err != nil {
			return nil, err
		}
		s.waitUpdates(1, 10*time.Second)
		time.Sleep(200 * time.Millisecond)
		if !c.ContinuousOK.Load() {
			_ = c.Close()
			return nil, errors.New("the server does not do ContinuousUpdates")
		}
		if err := c.EnableContinuous(true, c.Whole()); err != nil {
			return nil, err
		}
		return p, nil
	}
	if err := s.first(); err != nil {
		return nil, err
	}
	if s.waitUpdates(1, 10*time.Second) < 1 {
		return nil, fmt.Errorf("no first update: %v", s.failure())
	}
	return p, nil
}

func (p *probeConn) setLayout(l layout) {
	p.mu.Lock()
	p.lay = l
	p.mu.Unlock()
}

// markerNow is the marker's colour index in the framebuffer now.
func (p *probeConn) markerNow() int {
	var idx int
	p.WithFramebuffer(func(fb *image.RGBA) { idx = paletteIndex(colourAt(fb, p.lay.marker())) })
	return idx
}

// changedAfter is when the marker first showed another colour than from after t (zero: not within d).
func (p *probeConn) changedAfter(t time.Time, from int, d time.Duration) time.Time {
	deadline := time.Now().Add(d)
	for {
		p.mu.Lock()
		for _, s := range p.samples {
			if s.at.After(t) && s.idx != from && s.idx >= 0 {
				p.mu.Unlock()
				return s.at
			}
		}
		p.mu.Unlock()
		if time.Now().After(deadline) {
			return time.Time{}
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func runScenes(ctx context.Context, t target, cred credentials, f runFlags) error {
	encs, pf, err := parseConfig(*f.config)
	if err != nil {
		return err
	}
	mode := *f.mode
	if mode == "continuous" && !slices.Contains(encs, encContinuous) {
		encs = append(encs, encContinuous, encFence)
	}
	scenes := strings.Split(*f.scenes, ",")
	if *f.scenes == "all" {
		scenes = []string{"idle", "type", "scroll", "drag", "video"}
	}
	b, err := newBench()
	if err != nil {
		return err
	}
	b.set("idle", false)
	b.browser = *f.browser
	if err := b.show(); err != nil {
		return err
	}
	// The lossless reference connects straight to the server (not through the relay or the shaping): it is a
	// picture of the moment, not a measurement of the path.
	var ref *probeConn
	if *f.ref {
		rt := target{Addr: t.Addr}
		if t.Hub != "" {
			rt = target{Hub: t.Hub, Machine: t.Machine, Token: t.Token}
		}
		if ref, err = open(ctx, rt, cred, pf32, appEncodings, "stopwait", nil); err != nil {
			return fmt.Errorf("reference connection: %w", err)
		}
		defer ref.Close()
	}
	var out *os.File
	if *f.jsonOut != "" {
		if out, err = os.OpenFile(*f.jsonOut, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err != nil {
			return err
		}
		defer out.Close()
	}
	fmt.Printf("| scene | config | mode | target | Mbit/s | lossless Mbit/s | fps | stage fps | latency ms p50/p90 (n) | inject / screen ms | SSIM | PSNR |\n")
	fmt.Printf("| --- | --- | --- | --- | ---: | ---: | ---: | ---: | --- | --- | ---: | ---: |\n")
	for range *f.repeat {
		for _, sc := range scenes {
			var rec *recorder
			if *f.record != "" {
				_ = os.MkdirAll(*f.record, 0o755)
				name := fmt.Sprintf("%s-%s-%s-%d.vncrec", sc, strings.ReplaceAll(*f.config, ",", "+"), mode, time.Now().Unix())
				if rec, err = newRecorder(filepath.Join(*f.record, name)); err != nil {
					return err
				}
			}
			var r sceneResult
			for attempt := 0; attempt < 3; attempt++ {
				var test *probeConn
				test, err = open(ctx, t, cred, pf, encs, mode, rec)
				if err != nil {
					return fmt.Errorf("test connection: %w", err)
				}
				if *f.area == "page" {
					if lay, lerr := ensure(b, test, sc); lerr == nil {
						test.area.Store(lay.View)
						_ = test.Request(true, lay.View)
					}
				}
				r, err = runScene(b, sc, test, ref)
				_ = test.Close()
				if err == nil {
					break
				}
				fmt.Fprintf(os.Stderr, "vncbench: %s (try %d): %v\n", sc, attempt+1, err)
				_ = exec.Command("open", "-a", b.browser).Run()
				time.Sleep(time.Second)
			}
			if rec != nil {
				_ = rec.Close()
			}
			if err != nil {
				fmt.Printf("| %s | %s | %s | %s | error: %v |\n", sc, *f.config, mode, t, err)
				continue
			}
			r.Config, r.Mode, r.Target = *f.config, mode, t.String()
			if *f.area != "" {
				r.Config += " area=" + *f.area
			}
			printResult(r)
			if out != nil {
				line, _ := json.Marshal(r)
				_, _ = out.Write(append(line, '\n'))
			}
		}
	}
	return nil
}

func pct(v []float64, p float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	s := slices.Clone(v)
	sort.Float64s(s)
	i := int(math.Ceil(p*float64(len(s)))) - 1
	return s[max(0, min(i, len(s)-1))]
}

func printResult(r sceneResult) {
	lat := "–"
	if len(r.Latencies) > 0 {
		lat = fmt.Sprintf("%.0f / %.0f (%d)", pct(r.Latencies, 0.5), pct(r.Latencies, 0.9), len(r.Latencies))
	}
	split := "–"
	if len(r.InjectMS) > 0 {
		split = fmt.Sprintf("%.0f / %.0f", pct(r.InjectMS, 0.5), pct(r.ScreenMS, 0.5))
	}
	refMbps := "–"
	if r.RefBytesIn > 0 || r.RefUpdates > 0 {
		refMbps = fmt.Sprintf("%.2f", r.RefMbps)
	}
	q := "–"
	if r.SSIM > 0 {
		q = fmt.Sprintf("%.4f", r.SSIM)
	}
	psnr := "–"
	if r.PSNR > 0 {
		psnr = fmt.Sprintf("%.1f", r.PSNR)
	}
	fmt.Printf("| %s | %s | %s | %s | %.2f | %s | %.1f | %.1f | %s | %s | %s | %s |\n", r.Scene, r.Config, r.Mode, r.Target,
		r.Mbps, refMbps, r.FPS, r.StageFPS, lat, split, q, psnr)
}

// ensure shows the page's scene and finds it on the screen through c, bringing the browser to the front when
// another window covers it (other work on the same Mac opens windows of its own).
func ensure(b *bench, c *probeConn, scene string) (layout, error) {
	b.set(scene, false)
	deadline := time.Now().Add(20 * time.Second)
	for try := 0; time.Now().Before(deadline); try++ {
		if try > 0 && try%4 == 0 {
			_ = exec.Command("open", "-a", b.browser).Run()
		}
		time.Sleep(300 * time.Millisecond)
		b.mu.Lock()
		dpr := 1.0
		if b.hello != nil {
			dpr = b.hello.DPR
		}
		b.mu.Unlock()
		var l layout
		var ok bool
		c.WithFramebuffer(func(fb *image.RGBA) { l, ok = locate(fb, dpr) })
		if ok {
			return l, nil
		}
	}
	return layout{}, errors.New("the bench page is not on the screen (is the Mac unlocked, the browser in front?)")
}

func runScene(b *bench, scene string, test, ref *probeConn) (sceneResult, error) {
	lay, err := ensure(b, test, scene)
	if err != nil {
		return sceneResult{}, err
	}
	test.setLayout(lay)
	if ref != nil {
		ref.setLayout(lay)
	}
	time.Sleep(800 * time.Millisecond) // the scene drawn
	test.settle(300*time.Millisecond, 5*time.Second)
	if ref != nil {
		ref.settle(300*time.Millisecond, 5*time.Second)
	}
	r := sceneResult{Scene: scene}
	n0, in0, out0 := test.count(), test.BytesIn(), test.BytesOut()
	var rn0 int
	var rin0 int64
	if ref != nil {
		rn0, rin0 = ref.count(), ref.BytesIn()
	}
	t0 := time.Now()
	var inputs []time.Time
	var froms []int
	press := func(at time.Time) { inputs = append(inputs, at); froms = append(froms, test.markerNow()) }
	stage := lay.stage()
	var ssims []float64
	sample := func() {
		if ref == nil {
			return
		}
		test.settle(400*time.Millisecond, 4*time.Second)
		ref.settle(400*time.Millisecond, 4*time.Second)
		q := compare(test.Snapshot(), ref.Snapshot(), lay.View)
		ssims = append(ssims, q.SSIM)
		if q.SSIM < 0.5 {
			fmt.Fprintf(os.Stderr, "  low SSIM %.3f in %s\n", q.SSIM, scene)
		}
		r.PSNR = q.PSNR
	}
	switch scene {
	case "idle":
		time.Sleep(8 * time.Second)
	case "type":
		tg := lay.at(60, 28+60)
		var focused bool
		for click := 0; click < 3 && !focused; click++ {
			if err := aim(b, test, tg); err != nil {
				return r, err
			}
			_ = test.Pointer(tg.X, tg.Y, 0)
			_ = test.Pointer(tg.X, tg.Y, 1)
			_ = test.Pointer(tg.X, tg.Y, 0)
			time.Sleep(500 * time.Millisecond)
			test.settle(300*time.Millisecond, 3*time.Second)
			test.WithFramebuffer(func(fb *image.RGBA) { focused = near(colourAt(fb, lay.focus()), [3]uint8{30, 200, 30}, 60) })
		}
		if !focused {
			return r, errors.New("the bench page does not have the keyboard: not typing into another app")
		}
		t0 = time.Now()
		for _, ch := range "the quick brown fox jumps over the lazy dog 0123456789" {
			if !test.lay.View.Empty() {
				var still bool
				test.WithFramebuffer(func(fb *image.RGBA) { still = near(colourAt(fb, lay.focus()), [3]uint8{30, 200, 30}, 60) })
				if !still {
					return r, errors.New("the bench page lost the keyboard: stopped typing")
				}
			}
			press(time.Now())
			_ = test.Key(uint32(ch), true)
			time.Sleep(25 * time.Millisecond)
			_ = test.Key(uint32(ch), false)
			time.Sleep(110 * time.Millisecond)
		}
	case "scroll":
		tg := lay.at(60, 28+60)
		if err := aim(b, test, tg); err != nil {
			return r, err
		}
		_ = test.Pointer(tg.X, tg.Y, 0)
		time.Sleep(300 * time.Millisecond)
		t0 = time.Now()
		for burst := range 6 {
			wheel := byte(16) // down
			if burst%2 == 1 {
				wheel = 8
			}
			test.settle(250*time.Millisecond, 2*time.Second)
			if err := aim(b, test, tg); err != nil {
				return r, err
			}
			press(time.Now())
			for range 15 {
				_ = test.Pointer(tg.X, tg.Y, wheel)
				_ = test.Pointer(tg.X, tg.Y, 0)
				time.Sleep(30 * time.Millisecond)
			}
		}
	case "drag":
		// The panel's title bar, on its target (page.go: the panel starts at 60, 60 in the stage; the target 12, 2 in
		// its bar, 24 wide).
		grab := lay.at(60+12+12, 28+60+2+12)
		_ = test.Pointer(grab.X, grab.Y, 0)
		time.Sleep(200 * time.Millisecond)
		t0 = time.Now()
		for range 3 {
			test.settle(250*time.Millisecond, 2*time.Second)
			if err := aim(b, test, grab); err != nil {
				return r, err
			}
			_ = test.Pointer(grab.X, grab.Y, 1)
			time.Sleep(50 * time.Millisecond)
			p := grab
			for i := range 60 {
				if i == 0 {
					press(time.Now())
				}
				p = image.Pt(grab.X+(i+1)*8, grab.Y+(i+1)*3)
				_ = test.Pointer(p.X, p.Y, 1)
				time.Sleep(16 * time.Millisecond)
			}
			for i := range 60 {
				q := image.Pt(p.X-(i+1)*8, p.Y-(i+1)*3)
				_ = test.Pointer(q.X, q.Y, 1)
				time.Sleep(16 * time.Millisecond)
			}
			_ = test.Pointer(grab.X, grab.Y, 0)
		}
	case "video":
		t0 = time.Now()
		time.Sleep(10 * time.Second)
	default:
		return r, fmt.Errorf("unknown scene %q", scene)
	}
	end := time.Now()
	inEnd, outEnd := test.BytesIn(), test.BytesOut()
	var refEnd int64
	var refN int
	if ref != nil {
		refEnd, refN = ref.BytesIn(), ref.count()
	}
	// Latencies: each input to the first update after it that shows the marker changed.
	for i, at := range inputs {
		if ch := test.changedAfter(at, froms[i], 3*time.Second); !ch.IsZero() {
			r.Latencies = append(r.Latencies, float64(ch.Sub(at).Microseconds())/1000)
			// The page's own clock of the same input: when it got it.
			for _, e := range b.eventsSince(at) {
				got := time.UnixMicro(int64(e.T * 1000))
				if got.Before(ch) {
					r.InjectMS = append(r.InjectMS, float64(got.Sub(at).Microseconds())/1000)
					r.ScreenMS = append(r.ScreenMS, float64(ch.Sub(got).Microseconds())/1000)
				}
				break
			}
		}
	}
	ups := test.since(n0)
	var dec time.Duration
	stageN := 0
	for _, u := range ups {
		if u.End.After(end) {
			break
		}
		dec += u.End.Sub(u.Start)
		if u.Dirty.Overlaps(stage) {
			stageN++
		}
	}
	secs := end.Sub(t0).Seconds()
	if scene == "video" {
		// Frozen three times: the picture as it settles mid-motion.
		for range 3 {
			b.set("video", true)
			sample()
			b.set("video", false)
			time.Sleep(1500 * time.Millisecond)
		}
	} else {
		sample()
	}
	in, out := inEnd-in0, outEnd-out0
	r.Seconds = secs
	r.BytesIn, r.BytesOut = in, out
	r.Mbps = float64(in) * 8 / 1e6 / secs
	r.Updates = len(ups)
	r.FPS = float64(len(ups)) / secs
	r.StageFPS = float64(stageN) / secs
	if len(ups) > 0 {
		r.DecodeMSAvg = float64(dec.Microseconds()) / 1000 / float64(len(ups))
	}
	if ref != nil {
		r.RefBytesIn = refEnd - rin0
		r.RefMbps = float64(r.RefBytesIn) * 8 / 1e6 / secs
		r.RefUpdates = refN - rn0
	}
	if len(ssims) > 0 {
		r.SSIMs = ssims
		var s float64
		for _, v := range ssims {
			s += v
		}
		r.SSIM = s / float64(len(ssims))
	}
	encs := map[int32]int{}
	for _, u := range ups {
		for e, n := range u.Encodings {
			encs[e] += n
		}
	}
	keys := []int32{}
	for e := range encs {
		keys = append(keys, e)
	}
	slices.Sort(keys)
	parts := []string{}
	for _, e := range keys {
		parts = append(parts, fmt.Sprintf("%s×%d", encName(e), encs[e]))
	}
	r.Encodings = strings.Join(parts, " ")
	return r, nil
}

// recordOnly records what the server sends for d with the given encodings and request mode, with no scene: the
// replay benchmark's input (apple/PersonaTests/RFBReplayTests.swift).
func recordOnly(ctx context.Context, t target, cred credentials, config, mode, path string, d time.Duration) error {
	encs, pf, err := parseConfig(config)
	if err != nil {
		return err
	}
	if mode == "continuous" && !slices.Contains(encs, encContinuous) {
		encs = append(encs, encContinuous, encFence)
	}
	rec, err := newRecorder(path)
	if err != nil {
		return err
	}
	defer rec.Close()
	p, err := open(ctx, t, cred, pf, encs, mode, rec)
	if err != nil {
		return err
	}
	defer p.Close()
	time.Sleep(d)
	fmt.Printf("recorded %s: %d updates, %.1f MB in %s\n", path, p.count(), float64(p.BytesIn())/1e6, d)
	return nil
}

// trace runs one scene on a connection asking as mode does and prints, in order, each request going out and each
// update's start, end, size and painted area, in milliseconds from the start: how the server answers each way of
// asking.
func trace(ctx context.Context, t target, cred credentials, scene, mode string, d time.Duration, areaSpec string) error {
	b, err := newBench()
	if err != nil {
		return err
	}
	b.browser = "Safari"
	b.set(scene, false)
	if err := b.show(); err != nil {
		return err
	}
	c, err := connect(ctx, t, cred, pf32, appEncodings, nil)
	if err != nil {
		return err
	}
	var mu sync.Mutex
	var lines []string
	t0 := time.Now()
	stamp := func() float64 { return float64(time.Since(t0).Microseconds()) / 1000 }
	c.OnRequest = func(inc bool, r image.Rectangle) {
		mu.Lock()
		lines = append(lines, fmt.Sprintf("%9.1f request incremental=%v %v", stamp(), inc, r))
		mu.Unlock()
	}
	p := &probeConn{}
	p.session = start(c, mode)
	defer p.Close()
	inner, innerStart := c.OnUpdate, c.OnUpdateStart
	c.OnUpdateStart = func() {
		mu.Lock()
		lines = append(lines, fmt.Sprintf("%9.1f update starts", stamp()))
		mu.Unlock()
		if innerStart != nil {
			innerStart()
		}
	}
	c.OnUpdate = func(u updateInfo) {
		mu.Lock()
		lines = append(lines, fmt.Sprintf("%9.1f update ends: %d bytes, %d rects, %d px painted in %v, took %.1f ms", stamp(), u.Bytes, u.Rects,
			u.Area, u.Dirty, float64(u.End.Sub(u.Start).Microseconds())/1000))
		mu.Unlock()
		inner(u)
	}
	if err := p.first(); err != nil {
		return err
	}
	if areaSpec != "" {
		// Once the first (whole) update is in, only this part from then on.
		p.waitUpdates(1, 10*time.Second)
		var area image.Rectangle
		switch areaSpec {
		case "page", "stage":
			lay, err := ensure(b, p, scene)
			if err != nil {
				return err
			}
			area = lay.View
			if areaSpec == "stage" {
				area = lay.stage()
			}
		default:
			var x, y, w, h int
			if _, err := fmt.Sscanf(areaSpec, "%d,%d,%d,%d", &x, &y, &w, &h); err != nil {
				return fmt.Errorf("-trace-area %q: want x,y,w,h, page or stage", areaSpec)
			}
			area = image.Rect(x, y, x+w, y+h)
		}
		mu.Lock()
		lines = append(lines, fmt.Sprintf("%9.1f from now on asking for %v only", stamp(), area))
		mu.Unlock()
		p.area.Store(area)
		_ = c.Request(true, area)
	}
	time.Sleep(d)
	mu.Lock()
	defer mu.Unlock()
	for _, l := range lines {
		fmt.Println(l)
	}
	fmt.Printf("%d updates in %s\n", p.count(), d)
	return nil
}
