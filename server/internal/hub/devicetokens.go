package hub

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/link"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/screen"
)

// One credential per device (docs/protocol/protocol.md "Authentication"). The pairing token in hub.json still opens everything (the CLI,
// apps from before this); an app trades it for a token of its own right after pairing and stops keeping it, so a lost
// phone is revoked alone instead of re-pairing every device. A watch gets an answer-only token: it can do what the
// watch app does (answerReads, answerWrites) and nothing else.

const (
	roleFull   = "full"
	roleAnswer = "answer"
	// roleLink is a persona-link credential (link.go): it opens its machine's link and nothing else.
	roleLink = "link"
	// roleLinkPair is a pairing code made for one machine's persona-link (`personad hub pair --machine`): all it can
	// do is become that credential.
	roleLinkPair = "link_pair"
)

// principal is who a request comes from. Only the master token has an empty ID.
type principal struct {
	ID          string
	Name        string
	Role        string
	Kind        string // persisted enrollment kind; diagnostic fallback only
	PairHash    string
	PairExpires time.Time
	Machine     string // roleLink, roleLinkPair: the machine in hub.json it is bound to; rolePeer: the peer's hub_id
	// roleExtension: the extension the token was made for and the scopes it opens (extension_scopes.go), and the
	// manager whose extension it is (the one that knew the token): a request that names no manager is its.
	Extension string
	Scopes    []string
	Manager   string
	// extHash, an extension token's hash: what it relays is kept under it, with Manager (relayID), which that manager
	// names when it drops the token (extwire.HubTokenRevoked). extAt: when the hub asked the managers whose it is.
	extHash string
	extAt   time.Time
	// extOpens, extScope: an extension's token opens the request's route (the manager said), else the scope it lacks.
	extOpens bool
	extScope string
}

type principalKey struct{}

func principalOf(ctx context.Context) principal {
	if p, ok := ctx.Value(principalKey{}).(principal); ok {
		return p
	}
	return principal{Role: roleFull} // handlers called directly (tests)
}

// devTokens caches token lookups so an app's burst of requests is one query.
type devTokens struct {
	mu         sync.Mutex
	cache      map[string]cachedToken // token hash -> lookup
	touched    map[string]time.Time   // device id -> last_seen written
	revokedIDs map[string]time.Time   // device id -> revocation; fences in-flight lookups and upgrades
	failures   authLimiter
	vpnLocal   func(netip.Addr) bool // nil uses the OS interface list
	// onLink lists the IPv6 prefixes on this machine's interfaces (ingress.go); nil reads the OS interface list.
	onLink     func() []netip.Prefix
	linkCache  []netip.Prefix
	linkCached time.Time

	// extFailures are extensions' tokens' failures from the internet, apart: they gate only extensions' tokens
	// (http.go auth).
	extFailures authLimiter
}

type cachedToken struct {
	p    *principal // nil: no such token
	at   time.Time
	used time.Time
}

const tokenCacheFor = 30 * time.Second

const tokenRevokedFor = 5 * time.Minute

// codeTokenStoreUnavailable (503): the token store did not answer, so there is no verdict on the token: not counted,
// and not a 401, which apps read as their credential being revoked.
const codeTokenStoreUnavailable = "token_store_unavailable"

// isRevoked requires t.mu. The database remains the authority after this window.
func (t *devTokens) isRevoked(id string) bool {
	at, ok := t.revokedIDs[id]
	return id != "" && ok && time.Since(at) < tokenRevokedFor
}

func (h *Hub) isDeviceRevoked(id string) bool {
	t := &h.devtok
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.isRevoked(id)
}

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// authenticate maps a bearer token to its principal; ok false for an unknown or revoked one, and for one the token
// store could not look up.
func (h *Hub) authenticate(ctx context.Context, tok string) (principal, bool) {
	p, ok, _ := h.lookupToken(ctx, tok)
	return p, ok
}

// lookupToken is authenticate with the reason a token is not accepted: err is set when the token store did not answer,
// which says nothing of the token, so the caller must not treat it as revoked (#3217).
func (h *Hub) lookupToken(ctx context.Context, tok string) (principal, bool, error) {
	if tok == "" {
		return principal{}, false, nil
	}
	if subtle.ConstantTimeCompare([]byte(tok), []byte(h.cfg.Token)) == 1 {
		return principal{Role: roleFull}, true, nil
	}
	if strings.HasPrefix(tok, extTokenPrefix) { // the manager made it and says whose it is (extensions_link.go)
		p, ok, _, _, _ := h.authorizeExtension(ctx, tok, nil)
		return p, ok, nil
	}
	if h.store == nil {
		return principal{}, false, nil
	}
	if strings.HasPrefix(tok, "jx_") { // a code pairing another hub (peer_pair.go)
		p, ok := h.peerPairPrincipal(ctx, tok)
		return p, ok, nil
	}
	hash := hashToken(tok)
	if strings.HasPrefix(tok, "jp_") {
		pt, err := h.store.PairTokenByHash(ctx, hash)
		if err != nil {
			return principal{}, false, err
		}
		if pt == nil {
			return principal{}, false, nil
		}
		// Revoking the paired device expires this row, ending its redemption grace too.
		until := pairExpires(pt)
		if !time.Now().Before(until) {
			return principal{}, false, nil
		}
		role := roleFull
		if pt.MachineID != "" {
			role = roleLinkPair
		}
		return principal{ID: "pair_" + hash, Role: role, PairHash: hash, PairExpires: until, Machine: pt.MachineID}, true, nil
	}
	t := &h.devtok
	t.mu.Lock()
	c, hit := t.cache[hash]
	if hit {
		c.used = time.Now()
		t.cache[hash] = c
	}
	t.mu.Unlock()
	if !hit || time.Since(c.at) > tokenCacheFor {
		dt, err := h.store.DeviceTokenByHash(ctx, hash)
		if err != nil {
			return principal{}, false, err
		}
		c = cachedToken{at: time.Now(), used: time.Now()}
		if dt != nil {
			c.p = &principal{ID: dt.ID, Name: dt.Name, Role: dt.Role, Kind: dt.Kind, Machine: dt.MachineID}
		}
		t.mu.Lock()
		if c.p != nil && t.isRevoked(c.p.ID) {
			t.mu.Unlock()
			return principal{}, false, nil
		}
		if t.cache == nil {
			t.cache, t.touched = map[string]cachedToken{}, map[string]time.Time{}
		}
		if len(t.cache) >= h.cfg.Security.cacheEntries() {
			oldestKey := ""
			var oldest time.Time
			for key, entry := range t.cache {
				if time.Since(entry.at) > tokenCacheFor {
					delete(t.cache, key)
				} else if oldestKey == "" || entry.used.Before(oldest) {
					oldestKey, oldest = key, entry.used
				}
			}
			if len(t.cache) >= h.cfg.Security.cacheEntries() {
				delete(t.cache, oldestKey)
			}
		}
		t.cache[hash] = c
		t.mu.Unlock()
	}
	if c.p == nil {
		return principal{}, false, nil
	}
	t.mu.Lock()
	// A cache hit may have been copied just before revoked cleared it.
	if t.isRevoked(c.p.ID) {
		t.mu.Unlock()
		return principal{}, false, nil
	}
	if t.touched == nil {
		t.touched = map[string]time.Time{}
	}
	if len(t.touched) >= h.cfg.Security.cacheEntries() {
		for id, at := range t.touched {
			if time.Since(at) >= 5*time.Minute {
				delete(t.touched, id)
			}
		}
		if len(t.touched) >= h.cfg.Security.cacheEntries() {
			for id := range t.touched {
				delete(t.touched, id)
				break
			}
		}
	}
	touch := time.Since(t.touched[c.p.ID]) > 5*time.Minute
	if touch {
		t.touched[c.p.ID] = time.Now()
	}
	t.mu.Unlock()
	if touch {
		go func(id string) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = h.store.TouchDeviceToken(ctx, id)
		}(c.p.ID)
	}
	return *c.p, true, nil
}

// answerReads and answerWrites are all an answer-only device may do: what the watch app does and has done, no more. A
// GET is not harmless for being a GET (one reads any file of a computer, one runs a launcher), so none gets through for
// its method. Something new the watch needs goes here.
var (
	answerReads = lazyre.New(`^/api/v1/(machines|sessions|sessions/archived|interactions|inbox|workspace-board|workspaces|sessions/[^/]+/[^/]+/items|` +
		`extensions/-/(widgets|shortcuts)|personas/nwverse/pet/[A-Za-z0-9_-][A-Za-z0-9._-]*\.(json|png))$`)
	// The watch draws extensions' widgets on its faces and runs their shortcuts (docs/extensions/extensions.md "System-level placements").
	// It also fetches the built-in Milk Frog's art for its row and face, and only that: pet/ of the nwverse package
	// (docs/apple/persona.md "The Milk Frog on the watch"), not another package or an uploaded look.
	answerWrites = lazyre.New(`^/api/v1/(interactions/[^/]+/(resolve|choose)|sessions/[^/]+/[^/]+/(messages|seen|interrupt)|` +
		`inbox/[^/]+/(done|reply|draft|suggestion)|extensions/[^/]+/-/(widget|shortcut)/[^/]+)$`)
	answerPuts = lazyre.New(`^/api/v1/devices/[^/]+/widgets$`)
	// answerKey is a key the watch can press on a screen card it could not read: a menu's number, Esc, Return.
	answerKey = lazyre.New(`^([1-9]|esc|enter)$`)
)

func allowed(p principal, method, path string) bool {
	switch p.Role {
	case roleFull:
		return true
	case roleLink:
		return method == http.MethodGet && (path == link.Path || path == link.StreamPath)
	case roleLinkPair:
		return method == http.MethodPost && path == "/api/v1/auth/tokens"
	case rolePeer:
		return method == http.MethodGet && path == peerPath
	case rolePeerPair:
		return method == http.MethodPost && path == peerPairPath
	case roleAnswer:
		if method == http.MethodGet || method == http.MethodHead {
			return answerReads.MatchString(path)
		}
		return method == http.MethodPost && answerWrites.MatchString(path) || method == http.MethodPut && answerPuts.MatchString(path)
	}
	return false
}

// answerMayResolve is what an answer-only device may say to a card: pick an answer, say yes or no and why. Typing into a
// terminal, pressing more than a menu's key, filling in a card's fields and making a tool's approval stand (always) are
// for a full device. A screen card's own "don't ask again" is one of its options, and the watch shows it.
func answerMayResolve(r proto.Resolution) bool {
	if r.Text != "" || len(r.PreKeys) > 0 || r.Always || r.Message != "" || len(r.Values) > 0 || r.Action != "" || len(r.Content) > 0 {
		return false
	}
	return len(r.Keys) == 0 || len(r.Keys) == 1 && answerKey.MatchString(r.Keys[0])
}

// picksFreeText: the answer picks a screen card's free-text field ("Type something", screen.KindTypeSomething), alone
// or among a multi-select's options. That field takes the text the answer brings, and an answer-only device brings
// none: the node would open the field and press enter on nothing (#1185). Checked before the card is claimed, so a
// refused answer leaves it pending.
func (h *Hub) picksFreeText(id string, r proto.Resolution) bool {
	ix, ok := h.pendingInteraction(id) // as the manager told it (fleet_link.go)
	if !ok || ix.Dialog == nil {
		return false
	}
	d := ix.Dialog
	for _, k := range append([]string{r.Option}, r.Options...) {
		if o := d.Option(k); k != "" && o != nil && o.Kind == screen.KindTypeSomething {
			return true
		}
	}
	return false
}

func (h *Hub) tokenRoutes(api *http.ServeMux) {
	// Trade the caller's credential for a device token. kind "watch" gets an answer-only one; kind "link" a
	// persona-link credential for the link machine named in "machine" (link.go). A browser signing in again names its
	// earlier credential in "replaces", and that one is revoked once the new one is made, when it is a web one: a
	// browser keeps one device instead of a row of them with the same name (docs/protocol/protocol.md "Authentication").
	api.HandleFunc("POST /api/v1/auth/tokens", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name     string `json:"name"`
			Kind     string `json:"kind"`
			Machine  string `json:"machine"`
			Replaces string `json:"replaces"`
			// SystemName: what a computer's system calls it (link: persona-link pair), its name when it has none yet.
			SystemName string `json:"system_name"`
		}
		if err := decodeJSONBody(w, r, &req, 4<<10); err != nil {
			writeBodyErr(w, err, err)
			return
		}
		p := principalOf(r.Context())
		if p.Role != roleFull && p.Role != roleLinkPair {
			writeErr(w, http.StatusForbidden, errors.New("this device may not add devices"))
			return
		}
		if h.store == nil {
			writeErr(w, http.StatusServiceUnavailable, errors.New("no database"))
			return
		}
		kind, role, machine := req.Kind, roleFull, ""
		if p.Role == roleLinkPair { // made for one machine's persona-link: that credential, and no other
			if req.Machine != "" && req.Machine != p.Machine {
				writeErr(w, http.StatusForbidden, fmt.Errorf("this pairing code is for machine %q", p.Machine))
				return
			}
			kind, req.Machine = "link", p.Machine
		}
		switch kind {
		case "link":
			machine, role = req.Machine, roleLink
			if mc := h.knownEntry(machine); mc == nil || mc.Kind != kindLink { // hub.json's, or one its manager keeps (devices_relay.go)
				writeErr(w, http.StatusBadRequest, fmt.Errorf("no machine %q of kind link", machine))
				return
			}
		case "watch":
			role = roleAnswer
		case "phone", "web":
		default:
			kind = "other"
		}
		name := strings.TrimSpace(req.Name)
		if name == "" || len([]rune(name)) > 80 {
			name = map[string]string{"phone": "iPhone", "watch": "Apple Watch"}[kind]
			if kind == "link" {
				name = h.knownEntry(machine).Name + " 的 Persona Link"
			}
			if name == "" {
				name = "设备"
			}
		}
		tok := "jd_" + randomHex(24)
		dt := db.DeviceToken{ID: "dev_" + randomHex(6), Name: name, Kind: kind, Role: role, MachineID: machine}
		var err error
		if p.PairHash != "" {
			var ok bool
			ok, err = h.store.RedeemPairToken(r.Context(), p.PairHash, dt, hashToken(tok))
			if err == nil && !ok {
				failures := 0
				if h.sourceOf(r) == sourcePublic {
					failures = h.devtok.failures.fail(authSourceKey(r), time.Now(), h.cfg.Security)
				}
				h.securityError(w, r, http.StatusUnauthorized, "", failures)
				return
			}
		} else {
			err = h.store.CreateDeviceToken(r.Context(), dt, hashToken(tok))
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		h.logf("device token %s for %q (%s, %s)", dt.ID, name, kind, role)
		replaced := ""
		if old := strings.TrimSpace(req.Replaces); kind == "web" && old != "" && old != dt.ID {
			// Only a web credential: an id a browser kept cannot take a phone, a watch or a computer's link off.
			if ok, err := h.revokeWebToken(r.Context(), old); err != nil {
				h.logf("device token %s: replacing %s: %v", dt.ID, old, err)
			} else if ok {
				replaced = old
				h.revoked(old)
				h.sendRevocations(newlyReachable(nil, h.meshView()))
				h.logf("device token %s replaces %s", dt.ID, old)
			}
		}
		if kind == "link" {
			h.machineAudit(r.Context(), machine, "redeem", dt.ID, nil)
			h.nameAfterSystem(r.Context(), machine, req.SystemName)
		}
		if kind == "link" && h.machinePending(machine) {
			h.notifyComputerJoining(machine) // the one to confirm (machines_add.go)
		} else {
			h.notifyPairing(name, dt.ID) // also when it replaced one: a credential was still made
		}
		resp := map[string]any{"id": dt.ID, "token": tok, "role": role, "name": name, "machine": machine}
		if replaced != "" {
			resp["replaced"] = replaced
		}
		writeJSON(w, resp)
	})
	api.HandleFunc("GET /api/v1/auth/tokens", func(w http.ResponseWriter, r *http.Request) {
		list := []db.DeviceToken{}
		if h.store != nil {
			var err error
			if list, err = h.store.DeviceTokens(r.Context()); err != nil {
				writeErr(w, http.StatusInternalServerError, err)
				return
			}
		}
		// push: per token, the way pushes reach the device it registered (Settings › Devices, #5998)
		writeJSON(w, map[string]any{"tokens": list, "current": principalOf(r.Context()).ID, "push": h.push.routesByCredential()})
	})
	api.HandleFunc("DELETE /api/v1/auth/tokens/{id}", func(w http.ResponseWriter, r *http.Request) {
		if h.store == nil {
			writeErr(w, http.StatusServiceUnavailable, errors.New("no database"))
			return
		}
		id := r.PathValue("id")
		ok, err := h.store.RevokeDeviceToken(r.Context(), id)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if !ok {
			writeErr(w, http.StatusNotFound, errors.New("no such device"))
			return
		}
		h.revoked(id)
		h.sendRevocations(newlyReachable(nil, h.meshView()))
		writeJSON(w, map[string]any{"ok": true})
	})
}

// revokeWebToken revokes a live credential only when a browser made it (kind "web"), and says whether it did.
func (h *Hub) revokeWebToken(ctx context.Context, id string) (bool, error) {
	list, err := h.store.DeviceTokens(ctx)
	if err != nil {
		return false, err
	}
	i := slices.IndexFunc(list, func(t db.DeviceToken) bool { return t.ID == id })
	if i < 0 || list[i].Kind != "web" || list[i].RevokedAt != nil {
		return false, nil
	}
	return h.store.RevokeDeviceToken(ctx, id)
}

// revoked forgets a device's cached lookups, closes its live connections (what it relays to the manager too) and stops
// its pushes.
func (h *Hub) revoked(id string) {
	t := &h.devtok
	t.mu.Lock()
	now := time.Now()
	if t.revokedIDs == nil {
		t.revokedIDs = map[string]time.Time{}
	}
	// Prune expired records on revocation, never evicting a recent revocation for capacity.
	for deviceID, at := range t.revokedIDs {
		if now.Sub(at) >= tokenRevokedFor {
			delete(t.revokedIDs, deviceID)
		}
	}
	t.revokedIDs[id] = now
	for k, c := range t.cache {
		if c.p != nil && c.p.ID == id {
			delete(t.cache, k)
		}
	}
	// Registration holds h.mu while checking t.mu; release t.mu before taking h.mu.
	t.mu.Unlock()
	h.mu.Lock()
	for deviceID, r := range h.phoneTools {
		if deviceID == id || r.CredentialID == id {
			delete(h.phoneTools, deviceID)
			delete(h.wokeAt, deviceID)
		}
	}
	delete(h.rings.devices, id) // its kept notify events (notifyring.go)
	var gone []*client
	for c := range h.clients {
		if c.device == id {
			c.phone = false
			gone = append(gone, c)
		}
	}
	h.mu.Unlock()
	for _, c := range gone {
		c.conn.CloseNow()
	}
	links := h.closeLinks(id) + h.closePeerLinksOf(id)
	// Its extension calls, port forwards, live screens and previews (extension_calls.go).
	links += h.dropRelayed(id)
	if hub := h.peerForCredential(id); hub != "" { // a peer's credential: that pairing ends
		go func() {
			if _, err := h.unpairPeer(context.Background(), hub, true); err != nil {
				h.logf("mesh: unpair after revoking %s: %v", id, err)
			}
		}()
	}
	h.live.endCredential(id) // finish the end pushes while their devices are still registered
	h.push.signedOut(id)     // nil-safe; forgets the devices this credential registered (push.go)
	h.logf("device token %s revoked (%d connections closed)", id, len(gone)+links)
}
