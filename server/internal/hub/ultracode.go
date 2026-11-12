package hub

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// sessionModes keeps model and effort changes in their existing RPC (the node applies the model first), then
// changes the independent session-only ultracode switch. Sending it to session.modes would let older nodes
// silently ignore it and claim success. Dismissing a resume-loss notice uses the same new RPC without changing
// the switch: {"ultracode_dismiss_lost":true} becomes {"dismiss_lost":true}.
func (h *Hub) sessionModes(w http.ResponseWriter, r *http.Request, m *Machine) {
	p := map[string]any{}
	if r.ContentLength != 0 {
		if err := decodeJSONBody(w, r, &p, 32<<20); err != nil && !errors.Is(err, io.EOF) {
			writeBodyErr(w, err, err)
			return
		}
	}
	if p == nil {
		writeErr(w, http.StatusBadRequest, errors.New("request body must be a JSON object"))
		return
	}
	if pr := principalOf(r.Context()); pr.Role == roleExtension { // extension_modes.go
		if why := h.extensionModeRefused(pr, m.cfg.ID, r.PathValue("sid"), p); why != "" {
			mode, _ := p["permission_mode"].(string)
			h.refuseExtensionMode(w, r, pr, m.cfg.ID, r.PathValue("sid"), mode, why)
			return
		}
	}
	// Keep speed separate from session.modes, which older nodes ignore unknown fields in.
	if tier, exists := p["service_tier"]; exists {
		if value, ok := tier.(string); !ok || value == "" || len(p) != 1 {
			writeErr(w, http.StatusBadRequest, errors.New("service_tier must be a nonempty string in a separate request"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		var out json.RawMessage
		if err := m.call(ctx, "session.speed", map[string]any{"session_id": r.PathValue("sid"), "service_tier": tier}, &out); err != nil {
			var re *rpc.Error
			if errors.As(err, &re) && strings.HasPrefix(re.Message, "unknown method") {
				writeCode(w, http.StatusConflict, proto.CodeNodeOutdated, i18n.T(h.requestLang(r), "rpc-errors.node_outdated"), nil)
				return
			}
			callErr(w, err)
			return
		}
		writeJSON(w, out)
		return
	}
	ultra := map[string]any{"session_id": r.PathValue("sid")}
	for _, field := range []struct{ http, rpc string }{{"ultracode", "ultracode"}, {"ultracode_dismiss_lost", "dismiss_lost"}} {
		if v, exists := p[field.http]; exists {
			if _, ok := v.(bool); !ok {
				writeErr(w, http.StatusBadRequest, errors.New(field.http+" must be a boolean"))
				return
			}
			ultra[field.rpc] = v
			delete(p, field.http)
		}
	}
	// A switch to another launcher (#5035), carried out by the user's next message (node/launcher_switch.go); from is
	// the launcher the client saw the session on. An older node would drop launcher and switch the model on the
	// launcher the Agent runs on.
	for _, field := range []string{"launcher", "from"} {
		if v, exists := p[field]; exists {
			if _, ok := v.(string); !ok {
				writeErr(w, http.StatusBadRequest, errors.New(field+" must be a string"))
				return
			}
		}
	}
	if launcher, _ := p["launcher"].(string); strings.TrimSpace(launcher) != "" && !m.switchesLaunchers() {
		writeCode(w, http.StatusConflict, proto.CodeNodeOutdated, i18n.T(h.requestLang(r), "hub-machine.node_no_launcher_switch"), nil)
		return
	}
	p["session_id"] = r.PathValue("sid")
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	var out json.RawMessage
	if len(p) > 1 || len(ultra) == 1 {
		if err := m.call(ctx, "session.modes", p, &out); err != nil {
			callErr(w, err)
			return
		}
		if model, _ := p["model"].(string); strings.TrimSpace(model) != "" { // switched (the node read it back), or picked with a launcher
			sid := r.PathValue("sid")
			s, _ := m.session(sid)
			h.modelUsed(h.sessionOwner(m.cfg.ID, sid), m.cfg.ID, modesLauncher(p, s), model)
		}
	}
	if len(ultra) > 1 {
		if err := m.call(ctx, "session.ultracode", ultra, &out); err != nil {
			var re *rpc.Error
			if errors.As(err, &re) && strings.HasPrefix(re.Message, "unknown method") {
				writeCode(w, http.StatusConflict, proto.CodeNodeOutdated, i18n.T(h.requestLang(r), "hub-ultracode.node_outdated"), nil)
				return
			}
			callErr(w, err)
			return
		}
	}
	if len(out) == 0 {
		out = json.RawMessage(`{}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(out)
}

// modesLauncher is the launcher a model in a POST …/modes body p runs on: the launcher it names (a switch, #5035),
// else the session's own.
func modesLauncher(p map[string]any, s proto.Session) string {
	if l, _ := p["launcher"].(string); strings.TrimSpace(l) != "" {
		return strings.TrimSpace(l)
	}
	return cmp.Or(s.Launcher, "claude")
}

// switchesLaunchers: the machine's node takes launcher in session.modes (capability launcher_switch), as its manager
// reported it. Not before the manager reported the node at all: hasCapability takes a node that says nothing for a
// test's, and an older node met right after a hub restart would drop the launcher and switch the model in place.
func (m *Machine) switchesLaunchers() bool {
	m.mu.Lock()
	reported := m.info.Protocol > 0 || m.info.Version != ""
	m.mu.Unlock()
	return reported && m.hasCapability(nodeproto.LauncherSwitch)
}
