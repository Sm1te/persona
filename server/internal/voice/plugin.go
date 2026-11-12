package voice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/identity"
)

// Plugin serves the voice plugin's HTTP API on the hub's socket.
type Plugin struct {
	cfg    Config
	cat    *catalog
	hub    *hubClient
	link   *hubLink // the conversations' one WebSocket to the hub (hublink.go)
	logger *log.Logger
	active atomic.Int32 // open conversations: a redeploy waits for 0 (GET /health)
	// toolless are the providers (by id) whose service refused the function tools of an auto provider's session start
	// (Provider.Tools unset, #1420): their sessions go without them from then on, for as long as the plugin runs.
	toolless sync.Map
	calls    atomic.Int64 // the calls taken since it started: a call's id in the log (calllog.go)
	speaks   atomic.Int64 // the texts read aloud since it started: a read-aloud's id in the log (speak.go)
}

func New(cfg Config, hubURL, hubToken string, logger *log.Logger) *Plugin {
	c := cfg
	pl := &Plugin{cfg: c, cat: &catalog{cfg: &c, now: time.Now}, hub: newHubClient(hubURL, hubToken), logger: logger}
	pl.link = newHubLink(pl.hub, pl.logf)
	pl.cat.logf = pl.logf
	return pl
}

func (pl *Plugin) logf(format string, args ...any) { pl.logger.Printf(format, args...) }

// refusesTools: provider id's service refused the function tools (toolless).
func (pl *Plugin) refusesTools(id string) bool {
	_, ok := pl.toolless.Load(id)
	return ok
}

// forgoTools remembers that provider id's service refuses the function tools; false when that was known already.
func (pl *Plugin) forgoTools(id string) bool {
	_, known := pl.toolless.LoadOrStore(id, struct{}{})
	return !known
}

// Handler is the plugin's API (docs/extensions/extensions.md "Voice"): /health, /info, /models, /voices, /speak, /ws.
func (pl *Plugin) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeHTTPJSON(w, http.StatusOK, map[string]any{"ok": true, "conversations": pl.active.Load()})
	})
	mux.HandleFunc("GET /info", pl.serveInfo)
	mux.HandleFunc("POST /speak", pl.serveSpeak)
	mux.HandleFunc("GET /models", func(w http.ResponseWriter, r *http.Request) {
		models, def, err := pl.cat.models(r.Context(), pl.logf)
		if err != nil {
			writeHTTPJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		writeHTTPJSON(w, http.StatusOK, map[string]any{"models": models, "default": def})
	})
	mux.HandleFunc("GET /voices", func(w http.ResponseWriter, r *http.Request) {
		model := r.URL.Query().Get("model")
		if model != "" {
			if _, _, ok := splitModel(model); !ok {
				writeHTTPJSON(w, http.StatusBadRequest, map[string]any{"error": "bad model id"})
				return
			}
		}
		v, err := pl.cat.voices(r.Context(), model)
		if err != nil {
			status, out := http.StatusBadGateway, map[string]any{"error": err.Error()}
			switch {
			case errors.Is(err, errNoProvider):
				status = http.StatusNotFound
			case errors.Is(err, errNoUpstream):
				status, out["code"] = http.StatusServiceUnavailable, codeNoUpstream
			}
			writeHTTPJSON(w, status, out)
			return
		}
		writeHTTPJSON(w, http.StatusOK, v)
	})
	mux.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		// Only the hub reaches this socket (0600), and it has checked the app.
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		c.SetReadLimit(1 << 20)
		// Who is calling, as the hub found it (docs/extensions/extensions.md "The caller on forwarded requests"): the role decides the tools, the
		// language the words the model is told.
		role, lang := r.Header.Get(extwire.HeaderRole), i18n.Resolve(r.Header.Get(extwire.HeaderLang))
		pl.converse(context.WithoutCancel(r.Context()), c, role, lang)
	})
	return mux
}

func writeHTTPJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Run is `personad voice`: serve on PERSONA_EXTENSION_SOCKET (or its old names) until ctx ends.
func Run(ctx context.Context) error {
	logger := log.New(os.Stderr, "", 0)
	socket := extensionEnv("SOCKET")
	if socket == "" {
		return errors.New("PERSONA_EXTENSION_SOCKET is not set: this runs under the Persona hub as its built-in voice extension (docs/extensions/extensions.md \"Built-in voice extension\")")
	}
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	hubURL := identity.Getenv("PERSONA_HUB_URL")
	if hubURL == "" {
		hubURL = "http://127.0.0.1:18660"
	}
	pl := New(cfg, hubURL, identity.Getenv("PERSONA_HUB_TOKEN"), logger)
	_ = os.Remove(socket)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		return err
	}
	for _, p := range cfg.Providers {
		via := ""
		if p.SSH != "" {
			via = " via " + p.SSH
		}
		logger.Printf("provider %s: %s dialect%s", p.ID, p.Dialect, via)
	}
	if cfg.Setup != "" {
		// It runs all the same: the hub shows it needs setting up, a conversation is refused with no_upstream.
		logger.Printf("no realtime voice service yet (missing: %s): set its address and key in the extension's settings", cfg.Setup)
	}
	for _, w := range cfg.Warnings {
		logger.Printf("config: %s", w)
	}
	logger.Printf("listening on %s", socket)
	srv := &http.Server{Handler: pl.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}
