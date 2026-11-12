package hub

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/webasset"
)

// webDist is the web client built into a published personad (scripts/release/publish.sh copies web/dist here before building);
// in other builds only the placeholder is there. Its hashed assets and its third-party-licenses.txt are kept gzipped
// (webasset.Precompress, run by the build after the copy): name.gz instead of name.
//
//go:embed all:webdist
var webDist embed.FS

// webApp serves the web client (web/, docs/web/web.md) from <data dir>/web on the hub's own port, so it shares the
// API's origin: no CORS, and the WebSocket and images authenticate the same way. Without that directory the copy built
// into personad serves (published builds), and without either every non-API path is a 404, as before. Paths without a
// file fall back to index.html (client-side routes).
//
// The built-in copy's precompressed files (docs/web/web.md "Precompressed assets") go out as they are stored to a
// client that takes gzip, and decompressed to any other request, with the headers and bytes a plain file would
// have. A first-screen asset the web build also wrote as name.br (webasset.BrotliSuffix) goes out as that copy to a
// client that takes br (serveBrotli). The data dir's files are plain and gzipResponses compresses them as it sends
// them, as before; its .br copies are not sent.
//
// Hashed assets are kept for a year. Everything else at the root (index.html, sw.js, the manifest, the icons) keeps its
// name across builds, so it is no-cache: checked again on every load, and a 304 while it has not changed. The data
// dir's files revalidate by their Last-Modified; the built-in copy's have no modification time, so they carry an ETag
// of their content (rootTags).
func webApp(dir string) http.Handler {
	embedded, _ := fs.Sub(webDist, "webdist")
	return webFrom(dir, embedded)
}

func webFrom(dir string, embedded fs.FS) http.Handler {
	tags := &rootTags{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// The data dir wins (scripts/deploy/deploy.sh web swaps it in whole), checked on every request like before.
		var site fs.FS
		builtIn := false // site is the copy built into personad, whose assets may be stored gzipped
		if st, err := os.Stat(filepath.Join(dir, "index.html")); err == nil && !st.IsDir() {
			site = os.DirFS(dir)
		} else if embedded != nil {
			if st, err := fs.Stat(embedded, "index.html"); err == nil && !st.IsDir() {
				site, builtIn = embedded, true
			}
		}
		if site == nil {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		file := name
		asset, hidden := strings.HasPrefix(name, "assets/"), strings.HasPrefix(path.Base(name), ".")
		kept := asset || name == webasset.Licenses // a file the build may keep gzipped (webasset.Precompressible)
		// In the built-in copy an asset's .gz is how another asset is stored, never a file of its own (Vite makes none);
		// so is the licenses' .gz, which falls back to index.html like any other path without a file. An asset's .br is
		// the build's brotli copy of another asset in either copy, never a file of its own either.
		storedForm := builtIn && (asset && strings.HasSuffix(name, webasset.Suffix) || name == webasset.Licenses+webasset.Suffix) ||
			asset && strings.HasSuffix(name, webasset.BrotliSuffix)
		stored := false // file is kept as file + webasset.Suffix
		if name == "" || hidden || storedForm || !isFile(site, name) {
			switch {
			case builtIn && kept && !hidden && !storedForm && isFile(site, name+webasset.Suffix):
				stored = true
			case asset: // a missing hashed asset is a real 404, not the app
				http.NotFound(w, r)
				return
			default:
				file = "index.html"
			}
		}
		root := rootFile(file)
		switch {
		// the shell, the service worker, the manifest and the icons (docs/web/web.md "Install as an app") are checked again on
		// every load
		case root:
			w.Header().Set("Cache-Control", "no-cache")
		case strings.HasPrefix(name, "assets/"):
			// Vite names them by content hash
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		if path.Ext(file) == ".webmanifest" { // not in Go's built-in table, and nosniff would leave it untyped
			w.Header().Set("Content-Type", "application/manifest+json")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if htmlRuntimeWorker(name) {
			w.Header().Set("Content-Security-Policy", htmlRuntimeWorkerCSP)
		}
		if file == "index.html" {
			serveIndex(w, r, site, builtIn, tags)
			return
		}
		if builtIn && asset && brotliWanted(r) && serveBrotli(w, r, site, name) {
			return
		}
		if stored {
			serveStored(w, r, site, file)
			return
		}
		f, err := site.Open(file)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		rs, ok := f.(io.ReadSeeker)
		if err != nil || !ok {
			http.NotFound(w, r)
			return
		}
		if builtIn && root {
			if tag, ok := tags.of(site, file); ok {
				setETag(w, r, tag, st.Size(), file)
			}
		}
		http.ServeContent(w, r, path.Base(file), st.ModTime(), rs)
	})
}

// rootFile: a file beside index.html that keeps its name across builds (the shell, sw.js, the manifest, the icons,
// compatibility-bootstrap.js). Not the licenses, which the build may keep gzipped (webasset.Precompressible) and which
// are served as before.
func rootFile(file string) bool {
	return !strings.Contains(file, "/") && file != webasset.Licenses
}

// rootTags is the ETag of each root file of the built-in copy, worked out on its first request and kept by name: the
// embedded files never change while the hub runs (webFrom keeps one per copy).
type rootTags struct{ m sync.Map }

func (t *rootTags) of(site fs.FS, name string) (string, bool) {
	if v, ok := t.m.Load(name); ok {
		return v.(string), true
	}
	b, err := fs.ReadFile(site, name)
	if err != nil {
		return "", false
	}
	tag := contentTag(b)
	t.m.Store(name, tag)
	return tag, true
}

// contentTag is a strong ETag of b: the first 128 bits of its SHA-256.
func contentTag(b []byte) string {
	sum := sha256.Sum256(b)
	return `"` + base64.RawURLEncoding.EncodeToString(sum[:16]) + `"`
}

// setETag sets the response's ETag to tag, weak when gzipResponses will compress the answer (gzipWriter weakens a 200's
// tag itself, and a 304 must carry the one the 200 would have). A file whose type cannot be told by name gets none:
// gzipWriter would sniff its bytes to decide.
func setETag(w http.ResponseWriter, r *http.Request, tag string, size int64, name string) {
	ct := w.Header().Get("Content-Type")
	if ct == "" {
		ct = mime.TypeByExtension(path.Ext(name))
	}
	if ct == "" {
		return
	}
	if gzipWanted(r) && size >= gzipMinSize && webasset.CompressibleType(ct) {
		tag = "W/" + tag
	}
	w.Header().Set("ETag", tag)
}

// isFile: name is in site and not a directory.
func isFile(site fs.FS, name string) bool {
	st, err := fs.Stat(site, name)
	return err == nil && !st.IsDir()
}

// serveStored serves the file name that site keeps as name + webasset.Suffix. A request gzipResponses would
// compress (gzipWanted) gets the stored bytes, marked as gzip, which gzipResponses then passes on as they are. Any
// other (no gzip, HEAD, a range) gets them decompressed as they are read (gunzipSeeker), the same answer as for the
// plain file.
func serveStored(w http.ResponseWriter, r *http.Request, site fs.FS, name string) {
	f, err := site.Open(name + webasset.Suffix)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	rs, ok := f.(io.ReadSeeker)
	if err != nil || !ok {
		http.NotFound(w, r)
		return
	}
	// The type goes by the plain name, as http.ServeContent would; one only sniffing could tell is sent decompressed.
	if ct := mime.TypeByExtension(path.Ext(name)); ct != "" && gzipWanted(r) {
		h := w.Header()
		h.Set("Content-Type", ct)
		h.Set("Content-Encoding", "gzip")
		h.Add("Vary", "Accept-Encoding")
		http.ServeContent(w, r, path.Base(name), st.ModTime(), rs)
		return
	}
	plain, err := newGunzipSeeker(rs)
	if err != nil { // a broken build; not to be kept for a year by a cache
		w.Header().Del("Cache-Control")
		http.Error(w, "broken asset", http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, r, path.Base(name), st.ModTime(), plain)
}

// serveBrotli sends the brotli copy the web build wrote of the asset name (name + webasset.BrotliSuffix), as it is
// stored, to a request brotliWanted took; gzipResponses passes an answer that has its Content-Encoding on as it is.
// It reports false, having written nothing, when there is no copy or its type cannot be told by name: the request
// then gets the gzip or plain file, as before.
func serveBrotli(w http.ResponseWriter, r *http.Request, site fs.FS, name string) bool {
	ct := mime.TypeByExtension(path.Ext(name))
	if ct == "" {
		return false
	}
	f, err := site.Open(name + webasset.BrotliSuffix)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	rs, ok := f.(io.ReadSeeker)
	if err != nil || !ok || st.IsDir() {
		return false
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("Content-Encoding", "br")
	h.Add("Vary", "Accept-Encoding")
	http.ServeContent(w, r, path.Base(name), st.ModTime(), rs)
	return true
}

// gunzipSeeker reads a gzip file's content as http.ServeContent reads a file: its size comes from the gzip trailer
// (ISIZE, which gzip.Reader checks at the end), so a HEAD decompresses nothing, a GET as it is sent, and a range only
// up to its end. A seek back starts decompressing again from the top.
type gunzipSeeker struct {
	src       io.ReadSeeker // the gzip bytes
	zr        *gzip.Reader
	size      int64
	pos, want int64 // where zr is, where the next Read starts
}

func newGunzipSeeker(src io.ReadSeeker) (*gunzipSeeker, error) {
	var trailer [4]byte
	if _, err := src.Seek(-int64(len(trailer)), io.SeekEnd); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(src, trailer[:]); err != nil {
		return nil, err
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(src) // reads and checks the header
	if err != nil {
		return nil, err
	}
	size := int64(trailer[0]) | int64(trailer[1])<<8 | int64(trailer[2])<<16 | int64(trailer[3])<<24
	return &gunzipSeeker{src: src, zr: zr, size: size}, nil
}

func (g *gunzipSeeker) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekCurrent:
		offset += g.want
	case io.SeekEnd:
		offset += g.size
	}
	if offset < 0 {
		return 0, errors.New("gunzipSeeker: negative position")
	}
	g.want = offset
	return offset, nil
}

func (g *gunzipSeeker) Read(p []byte) (int, error) {
	if g.want < g.pos { // back: from the top again
		if _, err := g.src.Seek(0, io.SeekStart); err != nil {
			return 0, err
		}
		if err := g.zr.Reset(g.src); err != nil {
			return 0, err
		}
		g.pos = 0
	}
	if g.want > g.pos {
		n, err := io.CopyN(io.Discard, g.zr, g.want-g.pos)
		g.pos += n
		if err != nil {
			return 0, err
		}
	}
	n, err := g.zr.Read(p)
	g.pos += int64(n)
	g.want = g.pos
	return n, err
}

// serveIndex serves index.html with its <base> at the sub-path a reverse proxy serves the hub under (docs/web/web.md "Subpaths"):
// the page's files are relative to it and lib/base.ts reads it for the API, the WebSocket and the page's routes. The
// same file answers differently per X-Forwarded-Prefix, so a cache must key on it (Vary). The built-in copy's ETag is
// that of the bytes sent: at the root the file's (kept in tags), under a sub-path worked out each time.
// htmlRuntimeWorkerCSP is the policy of the worker that runs an assistant html card's code on the web (#4215,
// web/src/lib/html-runtime/, docs/assistant/assistant-views.md "Free-form HTML rendering"): it may load and connect to
// nothing. The worker is one file with QuickJS built in and gets the WebAssembly compiled from the page, so it needs
// nothing but to instantiate it ('wasm-unsafe-eval'). One of the layers between a card and the network: the card's
// code has no network API in QuickJS, and the worker removes its own once QuickJS is up.
const htmlRuntimeWorkerCSP = "default-src 'none'; script-src 'wasm-unsafe-eval'"

// htmlRuntimeWorker: the web build's html card worker, assets/html-runtime.worker-<hash>.js (Vite names it so).
func htmlRuntimeWorker(name string) bool {
	return strings.HasPrefix(name, "assets/html-runtime.worker-") && strings.HasSuffix(name, ".js") && !strings.Contains(strings.TrimPrefix(name, "assets/"), "/")
}

func serveIndex(w http.ResponseWriter, r *http.Request, site fs.FS, builtIn bool, tags *rootTags) {
	b, err := fs.ReadFile(site, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var mod time.Time
	if st, err := fs.Stat(site, "index.html"); err == nil {
		mod = st.ModTime()
	}
	w.Header().Add("Vary", "X-Forwarded-Prefix")
	prefix := forwardedPrefix(r)
	body := withBase(b, prefix)
	if builtIn {
		tag, ok := contentTag(body), true
		if prefix == "" {
			tag, ok = tags.of(site, "index.html")
		}
		if ok {
			setETag(w, r, tag, int64(len(body)), "index.html")
		}
	}
	http.ServeContent(w, r, "index.html", mod, bytes.NewReader(body))
}

// maxPrefix bounds X-Forwarded-Prefix: a proxy's sub-path is a few short segments.
const maxPrefix = 256

// forwardedPrefix is the sub-path a reverse proxy serves the hub under, from its X-Forwarded-Prefix ("/persona",
// trailing slashes dropped), or "" at the root. Only plain path segments count: no dot segments, nothing that could
// leave the attribute (quotes, angle brackets, spaces, control characters, percent escapes); anything else is ignored.
func forwardedPrefix(r *http.Request) string {
	v := r.Header.Get("X-Forwarded-Prefix")
	if v == "" || len(v) > maxPrefix || v[0] != '/' {
		return ""
	}
	p := strings.TrimRight(v, "/")
	if p == "" {
		return ""
	}
	for _, seg := range strings.Split(p[1:], "/") {
		if seg == "" || seg == "." || seg == ".." {
			return ""
		}
		for i := 0; i < len(seg); i++ {
			c := seg[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~') {
				return ""
			}
		}
	}
	return p
}

// baseTag is index.html's <base> as web/index.html writes it (and Vite keeps it).
var baseTag = []byte(`<base href="/" />`)

// withBase points index.html's <base> at prefix + "/"; at the root (prefix "") the page is as it was built.
func withBase(index []byte, prefix string) []byte {
	if prefix == "" {
		return index
	}
	return bytes.Replace(index, baseTag, []byte(`<base href="`+prefix+`/" />`), 1)
}
