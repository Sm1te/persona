package node

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/nodeclient"
	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
)

// The hand-off to one Agent run (docs/extensions/secret-providers.md "The hand-off to one Agent run", #3962): after
// the user approved the card, the manager that connects this computer asks this node for a one-time key
// (secret.stage {check}), seals the values to it and stages them here (secret.stage {key_id, sealed}), for a one-time
// ticket. Whatever relays the manager's connection (the hub, for a Persona Link or a mesh computer) sees no value. The
// Agent's start carries only the ticket (session.start's secret_ticket); its start command runs `personad launch
// --ticket <ticket> …`, which fetches them from node.sock with this machine's launch key and execs the Agent with them
// in its environment. They are never on a command line, typed into a pane, or written to disk by Persona.
//
// A key and a ticket each work once and for at most proto.SecretStageKeep; the first start that names a ticket takes
// it, and an engine's resume after a restart, a later turn or another Agent starts without the values. The process
// (every connection's Node in it: attach makes one per manager connection) keeps the values it was handed, the last
// secretScrubMost, to hide them in everything it sends: a best-effort guard, since the Agent may still write a value
// into its own files on this computer.

// secretScrubMost is how many handed-over values the process keeps hiding; the least recently handed over go first.
// secretStageMost bounds the keys and the tickets waiting at once.
const (
	secretScrubMost = 64
	secretStageMost = 32
)

type stagedSecrets struct {
	env      []proto.SecretEnvVar
	expires  time.Time
	reserved bool // a start named it: no other start may (secretStage.reserve)
}

type stageKey struct {
	priv    *ecdh.PrivateKey
	expires time.Time
}

// secretStage is the process's stage keys, staged hand-offs and the values it hides.
type secretStage struct {
	mu     sync.Mutex
	keys   map[string]stageKey      // by key id
	staged map[string]stagedSecrets // by ticket
	scrub  *secretScrubber
}

func newSecretStage() *secretStage {
	return &secretStage{keys: map[string]stageKey{}, staged: map[string]stagedSecrets{}, scrub: &secretScrubber{}}
}

var (
	processStageOnce sync.Once
	processStage     *secretStage
)

// handoffs is the process's hand-offs, shared by every connection's Node: a value staged under one connection is
// still hidden, and its ticket still good, after the manager reconnects. A test may give its Node its own.
func (n *Node) handoffs() *secretStage {
	if n.secrets != nil {
		return n.secrets
	}
	processStageOnce.Do(func() { processStage = newSecretStage() })
	return processStage
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// secretStageCall answers secret.stage: to a check, a one-time key; to a hand-off sealed to one, a one-time ticket.
func (n *Node) secretStageCall(params json.RawMessage) (any, error) {
	var p proto.SecretStageParams
	err := json.Unmarshal(params, &p)
	clear(params)
	if err != nil {
		return nil, err
	}
	if n.keeper == nil { // capabilities: only a resident node keeps hiding them after a reconnect
		return nil, errors.New("secret.stage: only a resident node (Persona Link, personad node --attach) takes hand-offs")
	}
	if runtime.GOOS == "windows" {
		// personad launch stays alive as claude's parent there (osproc.Exec), holding the values for the run.
		return nil, errors.New("secret.stage: secrets are not handed to Agents on Windows yet")
	}
	s := n.handoffs()
	now := time.Now()
	if p.Check {
		s.mu.Lock()
		s.dropExpired(now)
		busy := len(s.keys) >= secretStageMost
		s.mu.Unlock()
		if busy {
			return nil, errors.New("secret.stage: too many hand-offs at once")
		}
		priv, err := ecdh.P256().GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		id, err := randomHex(16)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.dropExpired(now)
		s.keys[id] = stageKey{priv: priv, expires: now.Add(proto.SecretStageKeep)}
		s.mu.Unlock()
		return proto.SecretStageResult{Key: base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes()), KeyID: id}, nil
	}
	s.mu.Lock()
	k, ok := s.keys[p.KeyID]
	delete(s.keys, p.KeyID) // one use, opened or not
	s.mu.Unlock()
	if !ok || now.After(k.expires) {
		return nil, errors.New("secret.stage: the key is unknown, used or expired: ask for a new one")
	}
	env, err := proto.OpenSecretEnv(k.priv, p.KeyID, p.Sealed)
	clear(p.Sealed)
	if err != nil {
		return nil, errors.New("secret.stage: " + err.Error())
	}
	if err := checkSecretEnv(env); err != nil {
		clearEnv(env)
		return nil, err
	}
	ticket, err := randomHex(32)
	if err != nil {
		clearEnv(env)
		return nil, err
	}
	expires := now.Add(proto.SecretStageKeep)
	s.mu.Lock()
	s.dropExpired(now)
	if len(s.staged) >= secretStageMost {
		s.mu.Unlock()
		clearEnv(env)
		return nil, errors.New("secret.stage: too many hand-offs at once")
	}
	s.staged[ticket] = stagedSecrets{env: env, expires: expires}
	s.mu.Unlock()
	for _, v := range env { // staged: hidden from now on, the start's own output included
		s.scrub.add(v.Value)
	}
	time.AfterFunc(proto.SecretStageKeep, func() { s.take(ticket, false) }) // not used in time: gone
	return proto.SecretStageResult{Ticket: ticket, ExpiresAt: expires.UTC()}, nil
}

func checkSecretEnv(env []proto.SecretEnvVar) error {
	if len(env) == 0 || len(env) > proto.SecretEnvMost {
		return errors.New("secret.stage: 1 to 8 variables")
	}
	seen := map[string]bool{}
	for i, v := range env {
		label := "variable " + strconv.Itoa(i+1) // never the name: a value swapped into its place could read as one
		if why := compat.SecretEnvNameProblem(v.Name); why != "" {
			return errors.New("secret.stage: " + label + ": " + why)
		}
		if seen[v.Name] {
			return errors.New("secret.stage: " + label + " twice")
		}
		seen[v.Name] = true
		if len(v.Value) < proto.SecretEnvValueLeast || len(v.Value) > proto.SecretEnvValueMost || bytes.IndexByte(v.Value, 0) >= 0 {
			return errors.New("secret.stage: " + label + ": a value of 16 bytes to 16 KB without NUL")
		}
	}
	return nil
}

func clearEnv(env []proto.SecretEnvVar) {
	for _, v := range env {
		clear(v.Value)
	}
}

func (s *secretStage) dropExpired(now time.Time) {
	for t, st := range s.staged {
		if now.After(st.expires) {
			clearEnv(st.env)
			delete(s.staged, t)
		}
	}
	for id, k := range s.keys {
		if now.After(k.expires) {
			delete(s.keys, id)
		}
	}
}

// take removes ticket's values and returns them (use: the caller zeroes them after use); nil when it is unknown,
// used, expired or no start's yet (a ticket fetched before its start named it was not fetched by that start). Without
// use, or with nothing returned, they are zeroed here.
func (s *secretStage) take(ticket string, use bool) []proto.SecretEnvVar {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.staged[ticket]
	delete(s.staged, ticket)
	if !ok {
		return nil
	}
	if !use || !st.reserved || time.Now().After(st.expires) { // only for the start that named it
		clearEnv(st.env)
		return nil
	}
	return st.env
}

// has: ticket is staged, good now and no start's yet (session.start checks before it starts; the start reserves it).
func (s *secretStage) has(ticket string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.staged[ticket]
	return ok && !st.reserved && time.Now().Before(st.expires)
}

// reserve gives ticket to the start that names it first: false when it is unknown, expired or another start has it.
func (s *secretStage) reserve(ticket string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.staged[ticket]
	if !ok || st.reserved || time.Now().After(st.expires) {
		return false
	}
	st.reserved = true
	s.staged[ticket] = st
	return true
}

// errSecretTicket is a start's ticket that is not staged here (unknown, used, expired, or another start's).
var errSecretTicket = errors.New("the values approved for this start are no longer here (the card was answered more than two minutes ago, or they were used): approve it again")

// secretEnvFor answers node.sock's secret.env: ticket's variables for a request that carries the machine's launch
// key, the ticket used up. A request without the key gets the same error whatever it names, and leaves the ticket.
func (n *Node) secretEnvFor(req sockRequest) map[string]any {
	key, err := ensureLaunchKey(nodeclient.LaunchKeyPath())
	if err != nil {
		return map[string]any{"error": "the node cannot read its launch key: " + err.Error()}
	}
	if subtle.ConstantTimeCompare([]byte(req.LaunchKey), []byte(key)) != 1 {
		n.logf("secret.env refused: the request did not carry this machine's launch key")
		return map[string]any{"error": "refused: the values go only to a personad launch that can read this machine's launch key"}
	}
	env := n.handoffs().take(req.Ticket, true)
	if env == nil {
		return map[string]any{"error": errSecretTicket.Error()}
	}
	out := make([]string, 0, len(env))
	for _, v := range env {
		out = append(out, v.Name+"="+string(v.Value))
	}
	clearEnv(env)
	n.logf("secret.env: %d variable(s) handed to a start", len(out))
	return map[string]any{"env": out}
}

// secretEnvRequest is what `personad launch --ticket` asks the node for.
func secretEnvRequest(ticket string) sockRequest {
	key, _ := readLaunchKey(nodeclient.LaunchKeyPath())
	return sockRequest{Kind: "secret.env", Ticket: ticket, LaunchKey: key}
}

// fetchSecretEnv is ticket's variables (NAME=value) from the local node, for the process about to exec the Agent.
// The names are checked again here: the process that execs is the last to see them.
func fetchSecretEnv(ctx context.Context, ticket string) ([]string, error) {
	line, err := nodeclient.Dial(ctx, secretEnvRequest(ticket))
	if err != nil {
		return nil, err
	}
	var resp struct {
		Env   []string `json:"env"`
		Error string   `json:"error"`
	}
	err = json.Unmarshal(line, &resp)
	clear(line)
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	for _, kv := range resp.Env {
		name, _, _ := strings.Cut(kv, "=")
		if why := compat.SecretEnvNameProblem(name); why != "" {
			return nil, errors.New("a variable the node gave: " + why) // never the name (checkSecretEnv)
		}
	}
	return resp.Env, nil
}

// ---- hiding them ----

// secretScrubber hides handed-over values in what the node sends: inside JSON string values as JSON writes them, in
// raw bytes as they are, and their base64 anywhere. Each value comes in several forms (scrubForms); every form is
// found in one pass over what is sent (scrubIndex), whatever the number of values.
type secretScrubber struct {
	mu      sync.RWMutex
	values  [][]byte    // least recently handed over first, at most secretScrubMost
	lineIdx *scrubIndex // forms inside JSON strings, and base64
	rawIdx  *scrubIndex // forms in raw bytes, and base64
}

func (s *secretScrubber) add(value []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := slices.IndexFunc(s.values, func(v []byte) bool { return bytes.Equal(v, value) }); i >= 0 {
		// Handed over again: the newest now, the last to go (its forms are the same).
		v := s.values[i] // read before Delete shifts the slice
		s.values = append(slices.Delete(s.values, i, i+1), v)
		return
	}
	s.values = append(s.values, bytes.Clone(value))
	if len(s.values) > secretScrubMost {
		clear(s.values[0])
		s.values = s.values[1:]
	}
	var inJSON, raw, b64 []scrubForm
	for _, v := range s.values {
		for _, w := range lineEndings(v) { // a multi-line value (a PEM key) as a terminal shows it, CRLF
			raw = appendForm(raw, scrubForm{b: w})
			for _, html := range []bool{false, true} { // the node's own lines leave < > & as they are; other encoders escape them
				var j bytes.Buffer
				enc := json.NewEncoder(&j)
				enc.SetEscapeHTML(html)
				if enc.Encode(string(w)) == nil {
					quoted := bytes.TrimSuffix(j.Bytes(), []byte("\n"))
					inJSON = appendForm(inJSON, scrubForm{b: quoted[1 : len(quoted)-1]})
				}
			}
		}
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
			b64 = appendForm(b64, scrubForm{b: []byte(enc.EncodeToString(v)), b64: true})
		}
	}
	s.lineIdx = newScrubIndex(append(slices.Clone(inJSON), b64...))
	s.rawIdx = newScrubIndex(append(raw, b64...))
}

// lineEndings is v, and for a value of several lines the same with LF, with CRLF, and as a terminal shows v itself.
func lineEndings(v []byte) [][]byte {
	if !bytes.ContainsAny(v, "\r\n") {
		return [][]byte{v}
	}
	lf := bytes.ReplaceAll(v, []byte("\r\n"), []byte("\n"))
	crlf := bytes.ReplaceAll(lf, []byte("\n"), []byte("\r\n"))
	// A terminal turns each LF into CRLF: a value that had CRLF itself shows with CR CR LF.
	return [][]byte{v, lf, crlf, bytes.ReplaceAll(v, []byte("\n"), []byte("\r\n"))}
}

func appendForm(list []scrubForm, f scrubForm) []scrubForm {
	if len(f.b) == 0 || slices.ContainsFunc(list, func(x scrubForm) bool { return bytes.Equal(x.b, f.b) }) {
		return list
	}
	f.b = bytes.Clone(f.b)
	return append(list, f)
}

// active: there is something to hide.
func (s *secretScrubber) active() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.values) > 0
}

// redactedB64 is what a value's base64 becomes: as long, and base64 itself, so a payload around it still decodes.
func redactedB64(f []byte) []byte {
	out := bytes.Repeat([]byte("A"), len(f))
	for i, c := range f {
		if c == '=' {
			out[i] = '='
		}
	}
	return out
}

// scrubForm is one form of a value; b64 forms are replaced with base64 of the same length, others with proto.Redacted.
type scrubForm struct {
	b   []byte
	b64 bool
}

// scrubIndex finds every form in one pass: a rolling hash over windows of the shortest form's length (at most 16),
// a bit filter of the forms' first windows, then the forms whose first window it is.
type scrubIndex struct {
	window int
	pow    uint64 // scrubPrime^(window-1)
	filter []uint64
	first  map[uint64][]int // the hash of a form's first window → the forms
	forms  []scrubForm
}

const scrubPrime = 1099511628211

func newScrubIndex(forms []scrubForm) *scrubIndex {
	if len(forms) == 0 {
		return nil
	}
	x := &scrubIndex{window: 16, forms: forms, filter: make([]uint64, 1<<16/64), first: map[uint64][]int{}}
	for _, f := range forms {
		x.window = min(x.window, len(f.b))
	}
	x.pow = 1
	for range x.window - 1 {
		x.pow *= scrubPrime
	}
	for i, f := range forms {
		h := hashWindow(f.b[:x.window])
		x.filter[(h&0xffff)>>6] |= 1 << (h & 63)
		x.first[h] = append(x.first[h], i)
	}
	return x
}

func hashWindow(b []byte) uint64 {
	var h uint64
	for _, c := range b {
		h = h*scrubPrime + uint64(c)
	}
	return h
}

// scrubSpan is a place a form was found, [at, end), or several overlapping ones joined.
type scrubSpan struct {
	at, end int
	b64     bool
}

// find is every place in b a form starts.
func (x *scrubIndex) find(b []byte) []scrubSpan {
	if x == nil || len(b) < x.window {
		return nil
	}
	var hits []scrubSpan
	h := hashWindow(b[:x.window])
	for i := 0; ; i++ {
		if x.filter[(h&0xffff)>>6]&(1<<(h&63)) != 0 {
			for _, fi := range x.first[h] {
				if f := x.forms[fi]; bytes.HasPrefix(b[i:], f.b) {
					hits = append(hits, scrubSpan{at: i, end: i + len(f.b), b64: f.b64})
				}
			}
		}
		if i+x.window >= len(b) {
			return hits
		}
		h = (h-uint64(b[i])*x.pow)*scrubPrime + uint64(b[i+x.window])
	}
}

// replaceSpans is b with the hits that ok accepts replaced, overlapping ones as one (base64 of the same length only
// when each of them is a base64 form).
func replaceSpans(b []byte, hits []scrubSpan, ok func(scrubSpan) bool) []byte {
	slices.SortFunc(hits, func(x, y scrubSpan) int {
		if x.at != y.at {
			return x.at - y.at
		}
		return y.end - x.end
	})
	var spans []scrubSpan
	for _, h := range hits {
		if ok != nil && !ok(h) {
			continue
		}
		if n := len(spans); n > 0 && h.at < spans[n-1].end {
			spans[n-1].end = max(spans[n-1].end, h.end)
			spans[n-1].b64 = spans[n-1].b64 && h.b64
			continue
		}
		spans = append(spans, h)
	}
	if len(spans) == 0 {
		return b
	}
	var out bytes.Buffer
	out.Grow(len(b))
	last := 0
	for _, sp := range spans {
		out.Write(b[last:sp.at])
		if sp.b64 {
			out.Write(redactedB64(b[sp.at:sp.end]))
		} else {
			out.WriteString(proto.Redacted)
		}
		last = sp.end
	}
	out.Write(b[last:])
	return out.Bytes()
}

// bytes is b, raw bytes the node sends (a terminal's output), with every value and its base64 hidden.
func (s *secretScrubber) bytes(b []byte) []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return replaceSpans(b, s.rawIdx.find(b), nil)
}

// line is one JSON line with the values hidden inside its string values, never in object keys: a match must start
// where a character or escape starts and end inside the same string, so the line stays valid JSON. A value's base64
// becomes as many base64 characters, so a base64 payload stays one (its bytes there garbled).
func (s *secretScrubber) line(b []byte) []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	hits := s.lineIdx.find(b)
	if len(hits) == 0 {
		return b
	}
	str, unit := stringValues(b)
	return replaceSpans(b, hits, func(h scrubSpan) bool {
		return str[h.at] != 0 && str[h.at] == str[h.end-1] && unit[h.at]
	})
}

// stringValues maps line b: str[i] is the number (from 1) of the JSON string value byte i is inside (0 outside one,
// on its quotes, in an object key or in an unclosed string), and unit[i] says a character or an escape starts there.
func stringValues(b []byte) (str []int32, unit []bool) {
	str, unit = make([]int32, len(b)), make([]bool, len(b))
	var n int32
	for i := 0; i < len(b); {
		if b[i] != '"' {
			i++
			continue
		}
		end := i + 1 // the closing quote
		for end < len(b) && b[end] != '"' {
			if b[end] == '\\' {
				end++
			}
			end++
		}
		if end >= len(b) {
			break // not closed: left as it is
		}
		if !isKey(b, end+1) {
			n++
			for j := i + 1; j < end; {
				str[j], unit[j] = n, true
				step := 1
				switch {
				case b[j] == '\\' && j+1 < end && b[j+1] == 'u': // \uXXXX is one unit
					step = 6
				case b[j] == '\\':
					step = 2
				}
				for k := j + 1; k < j+step && k < end; k++ {
					str[k] = n
				}
				j += step
			}
		}
		i = end + 1
	}
	return str, unit
}

// isKey: what follows a string's closing quote (at i) makes it an object key.
func isKey(b []byte, i int) bool {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\r' || b[i] == '\n') {
		i++
	}
	return i < len(b) && b[i] == ':'
}

// scrubWriter is the node's connection with the values hidden in every line it writes (Serve). rpc.Peer writes
// one whole line a call.
type scrubWriter struct {
	w io.Writer
	s *secretScrubber
}

func (w scrubWriter) Write(p []byte) (int, error) {
	if !w.s.active() {
		return w.w.Write(p)
	}
	if _, err := w.w.Write(w.s.line(p)); err != nil {
		return 0, err
	}
	return len(p), nil
}

// scrubBytes is b, raw bytes the node sends as base64 (the full herdr client's), with the handed-over values hidden.
// A value split between two pieces is not found.
func (n *Node) scrubBytes(b []byte) []byte {
	s := n.handoffs().scrub
	if !s.active() {
		return b
	}
	return s.bytes(bytes.Clone(b))
}

// scrubB64 is a base64 payload the node sends (a terminal frame's data) with the values hidden in the bytes it holds:
// decoded, hidden, encoded again. One that does not decode goes as it is (the connection's own hiding still sees it).
func (n *Node) scrubB64(data string) string {
	s := n.handoffs().scrub
	if !s.active() || data == "" {
		return data
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return data
	}
	clean := s.bytes(raw)
	if bytes.Equal(clean, raw) {
		return data
	}
	return base64.StdEncoding.EncodeToString(clean)
}

// ---- the start ----

const (
	secretEnvAgentCode = "secret_env_agent"
	secretEnvGoneCode  = proto.CodeSecretEnvGone
	secretEnvAgentText = "secrets can be handed only to a new Claude Code start in a terminal, with a provider added in Persona or plain claude"
)

// secretStartProblem is why a start cannot take ticket: only a new Claude Code start whose command personad can exec
// (a Claude provider added in Persona, or plain claude; not a function from the shell's rc file), with the ticket
// staged; the start that passes takes the ticket, so no other start may name it.
func (n *Node) secretStartProblem(kind, launcher, resume, ticket string) error {
	if kind != "claude" || resume != "" {
		return refusal(secretEnvAgentCode, secretEnvAgentText, nil)
	}
	if l, managed := n.managedLauncher(launcher); managed && l.AgentType() != "claude" || !managed && launcher != "claude" {
		return refusal(secretEnvAgentCode, secretEnvAgentText, nil)
	}
	if !n.handoffs().reserve(ticket) {
		return refusal(secretEnvGoneCode, errSecretTicket.Error(), nil)
	}
	return nil
}

// withSecretTicket is line, a Claude command line from claudeCommandLine, started through `personad launch --ticket`:
// the launcher's own word replaced, the rest as it was.
func (n *Node) withSecretTicket(line, launcher, ticket string, q func(string) string) string {
	rest := strings.TrimPrefix(line, n.launcherCommandQ(launcher, q))
	wrap := q(n.bin) + " launch --ticket " + q(ticket) + " "
	if _, managed := n.managedLauncher(launcher); managed {
		return wrap + launcher + rest
	}
	word := "claude" // plain claude, with the start directory it may carry in front (launcherCommandQ)
	if d := n.claudeStartDir.Load(); d != nil && *d != "" {
		return "CLAUDE_CONFIG_DIR=" + shellDirQ(*d, q) + " " + wrap + "-- " + word + rest
	}
	return wrap + "-- " + word + rest
}

// capabilities is what this node announces: secret_env only from a resident node (attach, Persona Link), whose
// process outlives a connection, so the values it hides stay hidden when its manager reconnects. A node that serves
// one connection (--stdio) would forget them while its Agents still run.
func (n *Node) capabilities() []string {
	if n.keeper != nil {
		return nodeproto.Announced
	}
	return slices.DeleteFunc(slices.Clone(nodeproto.Announced), func(c string) bool { return c == nodeproto.SecretEnv })
}
