package node

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/shellcmd"
)

// The Cindy engine (docs/integrations/engines.md). Cindy runs coding agents (Claude Code, Codex, Pi) in its desktop
// app and loopback host service (`cindy-host`), storing sessions and messages in its profile database and Claude
// transcripts on disk. While the machine's engine is Cindy, the node runs an in-process runtime bridge (runtimes.go)
// that talks to Cindy's loopback host API (`host-endpoint.json` / `CINDY_HOST_API`) or reads its profile database
// (`cindy-<owner>.db`) when only the database is on disk.

// cindyRuntimeID is the runtime id the Cindy bridge registers with (and its sessions' space kind and host).
const cindyRuntimeID = proto.EngineCindy

var (
	// cindyBridgePoll is how often the bridge refreshes Cindy's sessions and pending interactions.
	cindyBridgePoll = 1500 * time.Millisecond
	// cindyBridgeRestart is the wait before the bridge reconnects after its loop ends.
	cindyBridgeRestart = 2 * time.Second
	// cindyAskRetry is how long a card the node could not take (no hub connected yet) waits before retrying.
	cindyAskRetry = 5 * time.Second
)

// cindyBridgeState holds the in-process Cindy bridge lifecycle on RuntimeHost.
type cindyBridgeState struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	status string
}

// setCindyBridge starts or stops the Cindy runtime bridge on rh.
func (rh *RuntimeHost) setCindyBridge(on bool) {
	b := &rh.cindy
	b.mu.Lock()
	defer b.mu.Unlock()
	if on == (b.cancel != nil) {
		return
	}
	if !on {
		b.cancel()
		b.cancel = nil
		done := b.done
		b.mu.Unlock()
		<-done
		b.mu.Lock()
		rh.logger.Printf("cindy bridge: stopped (the engine is not Cindy any more)")
		return
	}
	base := rh.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	b.cancel, b.done, b.status = cancel, make(chan struct{}), ""
	go rh.cindyBridgeLoop(ctx, b.done)
}

func (rh *RuntimeHost) cindyBridgeStatus() string {
	rh.cindy.mu.Lock()
	defer rh.cindy.mu.Unlock()
	if rh.cindy.cancel == nil {
		return ""
	}
	return rh.cindy.status
}

func (rh *RuntimeHost) setCindyStatus(s string) {
	rh.cindy.mu.Lock()
	rh.cindy.status = s
	rh.cindy.mu.Unlock()
}

func (rh *RuntimeHost) cindyBridgeLoop(ctx context.Context, done chan struct{}) {
	defer close(done)
	for ctx.Err() == nil {
		ep, ok := discoverCindyEndpoint(ctx, cindyDataDirs())
		if ok && (ep.BaseURL != "" || ep.DBPath != "") {
			err := rh.runCindyBridge(ctx, ep)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				rh.setCindyStatus(err.Error())
				rh.logger.Printf("cindy bridge: %v (again in %s)", err, cindyBridgeRestart)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(cindyBridgeRestart):
		}
	}
}

func (rh *RuntimeHost) runCindyBridge(ctx context.Context, ep *cindyEndpoint) error {
	// If an external runtime connection already owns "cindy", yield until it leaves.
	if rh.reg != nil {
		rh.reg.mu.Lock()
		existing := rh.reg.byRuntime(cindyRuntimeID)
		rh.reg.mu.Unlock()
		if existing != nil {
			return nil
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	hostEnd, bridgeEnd := net.Pipe()
	go func() {
		rh.serveConn(ctx, hostEnd, "")
		cancel()
	}()
	ver := ep.Version
	if ver == "" {
		ver = cindyInstalledVersion(cindyBin(), cindyDataDirs())
	}
	if ver == "" {
		ver = rh.version
	}
	b := newCindyBridge(ep, bridgeEnd, rh.logger.Printf)
	rh.setCindyStatus("")
	return b.run(ctx, ver)
}

// cindyBridge bridges a Cindy host API or SQLite profile database to the node's runtime protocol.
type cindyBridge struct {
	ep   *cindyEndpoint
	conn io.ReadWriteCloser
	peer *rpc.Peer
	logf func(string, ...any)
	http *http.Client

	mu       sync.Mutex
	cindyOf  map[string]string     // Persona session_id -> Cindy session id
	rowByID  map[string]cindyDBRow // Cindy session id -> latest row
	asks     map[string]*cindyAsk  // prompt key -> active phone interaction
	pushed   string                // last marshalled sessions list
	launches string                // last marshalled launchers list
	watched  map[string]bool       // Persona session_ids being watched
}

type cindyAsk struct {
	cindyID string
	reqID   string
	cancel  context.CancelFunc
	retryAt time.Time
}

type cindyDBRow struct {
	ID              string          `json:"id"`
	Title           string          `json:"title"`
	WorkingDir      string          `json:"workingDir"`
	ProjectDir      string          `json:"projectDir"`
	WorkspaceKind   string          `json:"workspaceKind"`
	WorktreePath    string          `json:"worktreePath"`
	Model           string          `json:"model"`
	Effort          string          `json:"effort"`
	PermissionMode  string          `json:"permissionMode"`
	PlanModeEnabled bool            `json:"planModeEnabled"`
	Status          string          `json:"status"`
	SDKSessionID    string          `json:"sdkSessionId"`
	AgentKind       string          `json:"agentKind"`
	ProviderID      string          `json:"providerId"`
	LastActivityAt  int64           `json:"lastActivityAt"`
	UserSendAt      int64           `json:"userSendAt"`
	UpdatedAt       json.RawMessage `json:"updatedAt"`
	ListPreview     string          `json:"listPreview"`
	ListPreviewRole string          `json:"listPreviewRole"`
}

func newCindyBridge(ep *cindyEndpoint, conn io.ReadWriteCloser, logf func(string, ...any)) *cindyBridge {
	return &cindyBridge{
		ep:      ep,
		conn:    conn,
		logf:    logf,
		http:    &http.Client{Timeout: 10 * time.Second},
		cindyOf: map[string]string{},
		rowByID: map[string]cindyDBRow{},
		asks:    map[string]*cindyAsk{},
		watched: map[string]bool{},
	}
}

func (b *cindyBridge) writable() bool {
	return b.ep != nil && b.ep.BaseURL != "" && b.ep.Token != ""
}

func (b *cindyBridge) run(ctx context.Context, version string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer b.conn.Close()
	b.peer = rpc.New("c", b.conn)
	b.peer.OnRequest = b.request
	served := make(chan error, 1)
	go func() { served <- b.peer.Serve(ctx, b.conn) }()

	w := b.writable()
	launchers := b.fetchLaunchers(ctx)
	hello := map[string]any{
		"runtime":   cindyRuntimeID,
		"name":      proto.EngineNames[proto.EngineCindy],
		"agent":     "claude",
		"version":   version,
		"instance":  newUUID(),
		"caps":      map[string]any{"send": w, "interrupt": w, "start": w, "resume": w, "close": w, "modes": w},
		"launchers": launchers,
	}
	if err := b.peer.Call(ctx, "runtime.hello", hello, nil); err != nil {
		return fmt.Errorf("hello: %w", err)
	}
	if raw, err := json.Marshal(launchers); err == nil {
		b.launches = string(raw)
	}

	wake := make(chan struct{}, 1)
	if w {
		go b.watchSSE(ctx, wake)
	}
	t := time.NewTicker(cindyBridgePoll)
	defer t.Stop()
	for {
		if err := b.refresh(ctx); err != nil {
			return err
		}
		select {
		case err := <-served:
			if err == nil {
				err = errors.New("the runtime host closed the connection")
			}
			return err
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
		case <-t.C:
		}
	}
}

func (b *cindyBridge) watchSSE(ctx context.Context, wake chan<- struct{}) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, b.ep.BaseURL+"/events", nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+b.ep.Token)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 2<<20)
	for sc.Scan() {
		line := sc.Text()
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		var ev struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionId"`
			Event     struct {
				Type string `json:"type"`
				Data struct {
					Text    string `json:"text"`
					IsFinal bool   `json:"isFinal"`
				} `json:"data"`
			} `json:"event"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil {
			continue
		}
		if ev.Type == "agent_event" && ev.Event.Type == "text" && !ev.Event.Data.IsFinal && ev.Event.Data.Text != "" {
			if sid := b.personaIDOf(ev.SessionID); sid != "" && b.isWatched(sid) {
				_ = b.peer.Notify("session.delta", map[string]any{"session_id": sid, "text": ev.Event.Data.Text})
			}
			continue
		}
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

func (b *cindyBridge) personaIDOf(cindyID string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	for pid, cid := range b.cindyOf {
		if cid == cindyID {
			return pid
		}
	}
	return ""
}

func (b *cindyBridge) isWatched(sid string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.watched[sid]
}

func (b *cindyBridge) postJSON(ctx context.Context, path string, in any, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.ep.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if b.ep.Token != "" {
		req.Header.Set("Authorization", "Bearer "+b.ep.Token)
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var errBody struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &errBody)
		msg := firstOf(errBody.Message, errBody.Error, strings.TrimSpace(string(raw)))
		return fmt.Errorf("cindy %s (%d): %s", path, resp.StatusCode, msg)
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (b *cindyBridge) fetchLaunchers(ctx context.Context) []map[string]any {
	if !b.writable() {
		return nil
	}
	var catalog struct {
		Providers []struct {
			ID        string   `json:"id"`
			Name      string   `json:"name"`
			Connected bool     `json:"connected"`
			Agents    []string `json:"agents"`
			Models    map[string][]struct {
				ID            string   `json:"id"`
				Name          string   `json:"name"`
				Efforts       []string `json:"efforts"`
				DefaultEffort string   `json:"defaultEffort"`
			} `json:"models"`
		} `json:"providers"`
	}
	if err := b.postJSON(ctx, "/api/providers", map[string]any{}, &catalog); err != nil {
		return nil
	}
	var out []map[string]any
	for _, p := range catalog.Providers {
		if !p.Connected {
			continue
		}
		for _, ag := range []struct{ cindyKey, personaAgent, suffix string }{
			{"claude-code", "claude", "claude"},
			{"codex", "codex", "codex"},
			{"pi", "pi", "pi"},
		} {
			ms := p.Models[ag.cindyKey]
			if len(ms) == 0 && !slices.Contains(p.Agents, ag.cindyKey) {
				continue
			}
			name := "cindy-" + ag.suffix
			if p.ID != "" && p.ID != "xd" && shellcmd.NameRe.MatchString("cindy-"+p.ID+"-"+ag.suffix) {
				name = "cindy-" + p.ID + "-" + ag.suffix
			}
			var models []map[string]any
			defModel := ""
			for i, m := range ms {
				if m.ID == "" {
					continue
				}
				if i == 0 {
					defModel = firstOf(m.Name, m.ID)
				}
				entry := map[string]any{"id": m.ID, "name": firstOf(m.Name, m.ID), "default": i == 0}
				if len(m.Efforts) > 0 {
					entry["efforts"] = m.Efforts
				}
				if m.DefaultEffort != "" {
					entry["default_effort"] = m.DefaultEffort
				}
				models = append(models, entry)
			}
			l := map[string]any{"name": name, "agent": ag.personaAgent}
			if defModel != "" {
				l["model"] = defModel
			}
			if len(models) > 0 {
				l["models"] = models
			}
			out = append(out, l)
		}
	}
	return out
}

func (b *cindyBridge) refresh(ctx context.Context) error {
	rows, running, aliases, err := b.loadSessions(ctx)
	if err != nil {
		return err
	}
	if b.writable() {
		if ls := b.fetchLaunchers(ctx); ls != nil {
			if raw, err := json.Marshal(ls); err == nil && string(raw) != b.launches {
				if b.peer.Notify("runtime.launchers", map[string]any{"launchers": ls}) == nil {
					b.launches = string(raw)
				}
			}
		}
	}
	cindyOf := map[string]string{}
	rowByID := map[string]cindyDBRow{}
	waiting := map[string]string{}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		if r.ID == "" || r.Status == "archived" || r.Status == "deleted" {
			continue
		}
		var pending []cindyPendingInteraction
		if b.writable() {
			_ = b.postJSON(ctx, "/api/interactions/pending", map[string]any{"sessionId": r.ID}, &pending)
		}
		mapped, sid := mapCindySessionRow(r, running[r.ID], len(pending) > 0, aliases)
		if sid == "" {
			continue
		}
		out = append(out, mapped)
		cindyOf[sid] = r.ID
		rowByID[r.ID] = r
		if len(pending) > 0 {
			waiting[r.ID] = sid
			b.syncPrompts(ctx, r.ID, sid, pending)
		}
	}
	b.mu.Lock()
	b.cindyOf = cindyOf
	b.rowByID = rowByID
	b.mu.Unlock()
	if raw, err := json.Marshal(out); err == nil && string(raw) != b.pushed {
		if b.peer.Notify("runtime.sessions", map[string]any{"sessions": out}) == nil {
			b.pushed = string(raw)
		}
	}
	b.prunePrompts(ctx, waiting)
	return nil
}

func (b *cindyBridge) loadSessions(ctx context.Context) ([]cindyDBRow, map[string]bool, map[string]string, error) {
	running := map[string]bool{}
	aliases := map[string]string{}
	if b.writable() {
		var rows []cindyDBRow
		if err := b.postJSON(ctx, "/api/sessions/list", map[string]any{"status": "active", "limit": 300}, &rows); err != nil {
			return nil, nil, nil, err
		}
		var rt []struct {
			SessionID     string `json:"sessionId"`
			IsTurnRunning bool   `json:"isTurnRunning"`
		}
		if b.postJSON(ctx, "/api/runtime/sessions", map[string]any{}, &rt) == nil {
			for _, s := range rt {
				if s.IsTurnRunning {
					running[s.SessionID] = true
				}
			}
		}
		var pa []struct {
			ProjectKey string `json:"projectKey"`
			Alias      string `json:"alias"`
		}
		if b.postJSON(ctx, "/api/project-aliases/list", map[string]any{}, &pa) == nil {
			for _, a := range pa {
				if a.ProjectKey != "" && a.Alias != "" {
					aliases[a.ProjectKey] = a.Alias
				}
			}
		}
		return rows, running, aliases, nil
	}
	if b.ep != nil && b.ep.DBPath != "" {
		rows, err := readCindySQLiteSessions(ctx, b.ep.DBPath)
		return rows, running, aliases, err
	}
	return nil, nil, nil, errors.New("no Cindy endpoint or database")
}
