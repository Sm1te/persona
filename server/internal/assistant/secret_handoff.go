package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/extension"
	"github.com/nwverse/persona/server/internal/extmgr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
	"github.com/nwverse/persona/server/internal/transcript"
)

// The hand-off of secret values to one Agent run (docs/extensions/secret-providers.md "The hand-off to one Agent run",
// #3962): agent_start's secret_env names environment variables and the references their values come from.
//
//   - Before anything is asked (handoffPrepare): the names and references are checked, the hub must say it holds every
//     answer to the card (Hello.SecretHandoffGate), the computer must be one this manager connects whose node announced
//     secret_env, the launcher must be Claude Code's (a Claude-type managed launcher or plain claude), and each
//     reference's provider must be installed and on. Any of these failing is the tool's error: no card, no start.
//   - The card (handoffApprove): a permission card under the reserved tool name proto.SecretHandoffTool, naming each
//     item as its provider describes it, the variable, the Agent and the computer, never a reference or a value. Only
//     an approval the hub relayed with the device it vouched for counts (resolveApproved); a channel, the assistant, a
//     remembered answer or an older hub can only say no. "Always" keeps an Always rule per reference × launcher ×
//     computer (extmgr secret_handoff_rules.go), and a later hand-off they all cover asks nothing.
//   - After the approval (handoffResolve, handoffValues.stage): the values are resolved (purpose agent_run, approved by
//     the device or "rule"), sealed to a one-time key of the node and staged there over the manager's own connection
//     (secret.stage), and the start carries the ticket alone. They are never in the model's context, the tool's
//     answer, a log or an error, and the manager's copies are zeroed once the start is sent.

// handoffSecrets is what a hand-off asks of the provider extensions (extmgr.Manager).
type handoffSecrets interface {
	SecretProviderOf(reference string) (extension.SecretProvider, bool)
	DescribeSecret(ctx context.Context, reference string) (extmgr.SecretDescription, error)
	ResolveSecret(ctx context.Context, reference string, purpose wire.SecretPurpose, approvedBy string) (string, error)
	HandoffRuleCovers(ctx context.Context, reference, launcher, machine string) bool
	AddHandoffRules(ctx context.Context, rules []extmgr.HandoffRule) error
}

var _ handoffSecrets = (*extmgr.Manager)(nil)

// nodeAnnouncer is the devices' word on what a computer's node announced (devices.Devices).
type nodeAnnouncer interface {
	NodeAnnounced(id, capability string) bool
}

// errHandoffElsewhere: an approval of a hand-off card that did not come from a device the hub vouched for.
var errHandoffElsewhere = i18n.Err("hub-secret_handoff.approve_elsewhere")

// handoffCardWait is how long a hand-off card waits for its answer.
var handoffCardWait = 10 * time.Minute

// The statuses of a variable in agent_start's answer.
const (
	handoffGranted  = "granted"
	handoffDeclined = "declined"
	handoffExpired  = "expired"
	handoffFailed   = "failed"
)

// handoffVar is one variable to hand over.
type handoffVar struct {
	Name, Reference string
	Provider        extension.SecretProvider
	Title           string // the item as its provider describes it; "" when it could not say
}

// secretHandoff is one Agent start's hand-off, checked and ready to ask.
type secretHandoff struct {
	machine, machineName string
	launcher             string
	vars                 []handoffVar
}

// handoffResult is one variable's outcome, for the model: never a value or a reference.
type handoffResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Code   string `json:"code,omitempty"`
}

// handoffAnswer is agent_start's answer when the hand-off stopped the start.
func handoffAnswer(results []handoffResult, why string) string {
	b, _ := json.Marshal(results)
	return why + " No agent was started. secret_env: " + string(b)
}

func (s *Service) handoffSecrets() handoffSecrets {
	if h, ok := s.refs.(handoffSecrets); ok {
		return h
	}
	return nil
}

// handoffPrepare checks a start's secret_env before anything is asked: env (variable → reference) to launcher on
// machine, as the start's params have them once its model and computer are settled.
func (s *Service) handoffPrepare(ctx context.Context, machine, machineName string, params map[string]any, env map[string]string) (*secretHandoff, error) {
	if len(env) == 0 || len(env) > proto.SecretEnvMost {
		return nil, fmt.Errorf("secret_env takes 1 to %d variables", proto.SecretEnvMost)
	}
	h := &secretHandoff{machine: machine, machineName: machineName}
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if why := compat.SecretEnvNameProblem(name); why != "" {
			return nil, fmt.Errorf("secret_env %q: %s", name, why)
		}
		ref := strings.TrimSpace(env[name])
		if _, ok := extension.ReferenceScheme(ref); !ok {
			return nil, fmt.Errorf("secret_env %s: the value must be a reference such as op://vault/item/field, never a secret itself", name)
		}
		h.vars = append(h.vars, handoffVar{Name: name, Reference: ref})
	}
	if !s.hello(ctx).SecretHandoffGate {
		return nil, errors.New("the hub is older than secret hand-offs (it does not check who approves them): update the hub first")
	}
	nodes, _ := s.devs.(nodeAnnouncer)
	if s.devs == nil || !s.devs.Has(machine) || nodes == nil {
		return nil, fmt.Errorf("%s is not connected by this manager: secrets can be handed only to an Agent on one of its own computers", machineName)
	}
	if !nodes.NodeAnnounced(machine, nodeproto.SecretEnv) {
		return nil, fmt.Errorf("%s cannot take secrets now: its node is offline, older than secret hand-offs, or not resident "+
			"(Persona Link or personad node --attach); on Windows not yet", machineName)
	}
	if rt, _ := params["runtime"].(string); rt != "" {
		return nil, errors.New("secrets can be handed only to Claude Code started by Persona, not to an agent runtime")
	}
	launcher, _ := params["launcher"].(string)
	launcher = s.startLauncher(ctx, launcher)
	if l, managed := s.managedNamed(ctx, launcher); managed && l.AgentType() != "claude" || !managed && launcher != "claude" {
		return nil, fmt.Errorf("secrets can be handed only to Claude Code with a provider Persona manages or plain claude, not %q", launcher)
	}
	h.launcher = launcher
	hs := s.handoffSecrets()
	if hs == nil {
		return nil, errors.New("this manager has no secret providers")
	}
	for i := range h.vars {
		v := &h.vars[i]
		prov, ok := hs.SecretProviderOf(v.Reference)
		if !ok {
			scheme, _ := extension.ReferenceScheme(v.Reference)
			return nil, fmt.Errorf("secret_env %s: no secret provider for %s:// is installed and on", v.Name, scheme)
		}
		v.Provider = prov
		if d, err := hs.DescribeSecret(ctx, v.Reference); err == nil {
			// Words only: a provider that names nothing gets the reference itself as its title, which the card never shows.
			v.Title = clip(oneLine(strings.TrimSpace(strings.Join(handoffWords(d.Title, d.Detail), " › "))), 120)
			if strings.Contains(v.Title, "://") || strings.Contains(v.Title, v.Reference) {
				v.Title = ""
			}
		}
	}
	return h, nil
}

// handoffApprove has the hand-off approved: by Always rules that cover every variable, else on its card. approvedBy is
// "rule" or "device:<id>"; "" when it was not approved, with the tool's answer saying why.
func (s *Service) handoffApprove(ctx context.Context, convMachine, self string, h *secretHandoff, agent, cwd, prompt string) (approvedBy, answer string, err error) {
	hs := s.handoffSecrets()
	covered := s.handoffUserTurn(ctx, convMachine, self) // rules only for what the user asked themselves
	for _, v := range h.vars {
		if !covered {
			break
		}
		if !hs.HandoffRuleCovers(ctx, v.Reference, h.launcher, h.machine) {
			covered = false
			break
		}
	}
	if covered {
		return "rule", "", nil
	}
	l := s.lang(ctx)
	input := proto.SecretHandoffInput{}
	var items, names []string
	for _, v := range h.vars {
		input.Variables = append(input.Variables, proto.SecretHandoffVariable{Name: v.Name, Provider: v.Provider.Extension,
			ProviderName: v.Provider.Name, Title: v.Title})
		item := v.Provider.Name
		if v.Title != "" {
			item += " › " + v.Title
		}
		items = append(items, i18n.T(l, "assistant-secret_handoff.item", i18n.Args{"item": item, "name": v.Name}))
		names = append(names, v.Name)
	}
	// Where it runs and what it is asked, for the card itself: never in the summary, which is also the push's text.
	input.Agent, input.MachineName, input.Cwd, input.Prompt = agent, h.machineName, cwd, clip(oneLine(prompt), 200)
	raw, _ := json.Marshal(input)
	now := time.Now().UTC()
	expires := now.Add(handoffCardWait)
	ix := proto.Interaction{ID: "ix_" + randomHex(6), MachineID: convMachine, SessionID: self, SessionTitle: "Persona",
		Kind: proto.IxPermission, ToolName: proto.SecretHandoffTool, ToolInput: raw, ToolTitle: i18n.T(l, "assistant-secret_handoff.title"),
		Summary:     handoffSummary(l, strings.Join(items, "; "), agent, h.machineName),
		AlwaysLabel: i18n.T(l, "assistant-secret_handoff.always", i18n.Args{"launcher": h.launcher, "machine": h.machineName}),
		CreatedAt:   now, ExpiresAt: expires}
	r, err := s.ask(ctx, ix)
	claim, vouched := s.takeHandoffClaim(ix.ID)
	if err != nil {
		return "", "", err
	}
	results := func(status string) []handoffResult {
		out := make([]handoffResult, 0, len(names))
		for _, n := range names {
			out = append(out, handoffResult{Name: n, Status: status})
		}
		return out
	}
	// The approval that claimed the card is the answer (whatever else reached the question): an explicit allow from a
	// device the hub vouched for. Anything else declines, a no with "always" or other fields beside it included.
	switch {
	case vouched:
	case r.Dismiss && !time.Now().Before(expires.Add(-time.Second)):
		return "", handoffAnswer(results(handoffExpired), "The hand-off card expired unanswered."), nil
	case proto.SecretHandoffApproves(r): // not through the hub's gate: refused, as the hub would
		return "", handoffAnswer(results(handoffDeclined), "The hand-off was not approved on iPhone or the web."), nil
	default:
		return "", handoffAnswer(results(handoffDeclined), "The user declined handing over the secrets."), nil
	}
	approvedBy = "device:" + claim.approver.DeviceID
	if claim.r.Always {
		rules := make([]extmgr.HandoffRule, 0, len(h.vars))
		for _, v := range h.vars {
			rules = append(rules, extmgr.HandoffRule{Reference: v.Reference, Launcher: h.launcher, Machine: h.machine,
				Provider: v.Provider.Extension, By: approvedBy, At: now})
		}
		if err := hs.AddHandoffRules(context.WithoutCancel(ctx), rules); err != nil {
			s.logf("assistant: hand-off card %s: its Always was not kept: %v", ix.ID, err)
		}
	}
	return approvedBy, "", nil
}

// handoffSummary is the card's one line: which items go to which Agent on which computer; an Agent without a name yet
// (a conversation not titled yet) is "an Agent", as the cards say.
func handoffSummary(l i18n.Lang, items, agent, machine string) string {
	if strings.TrimSpace(agent) == "" {
		return i18n.T(l, "assistant-secret_handoff.summary_unnamed", i18n.Args{"items": items, "machine": machine})
	}
	return i18n.T(l, "assistant-secret_handoff.summary", i18n.Args{"items": items, "agent": agent, "machine": machine})
}

// handoffUserTurn reports whether the user asked for this start themselves, the only case an Always rule may spare the
// card (the decision on #3962): conversation self on machine is one the user opened (its origin, noted when it was
// started, is "user", and no extension's token opened it), and every message of the turn calling the tool is one a
// person's own app sent, as the hub vouched (assistantwire.SendParams.Human, kept by its client_msg_id in humanSent),
// with no other author on it. A message's client_msg_id alone proves nothing: channels, the inbox, extensions and
// Persona's own sends carry one too. Whatever it cannot tell (an origin the backfill guessed or none, the transcript
// unreadable or cut, a message the hub did not vouch for, a manager restarted since) is no: the card.
//
// It trusts what is not a message in that turn (the tools' results, earlier turns, the context): a page of mail the
// model read on the user's request can still steer it.
func (s *Service) handoffUserTurn(ctx context.Context, machine, self string) bool {
	if !s.userOpened(ctx, self) {
		return false
	}
	var res struct {
		Items []proto.Item `json:"items"`
	}
	if err := s.call(ctx, machine, DeviceSessionItems, map[string]any{"session_id": self, "turns": 1, "lazy_images": true}, &res); err != nil {
		return false
	}
	// A page the node cut (its item cap, its parse budget) may have left the turn's first messages out: only a window that
	// opens with the turn's prompt is read.
	if len(res.Items) == 0 || len(res.Items) >= handoffTurnMost {
		return false
	}
	if first := res.Items[0]; first.Role != "user" || first.Kind != proto.KindText && first.Kind != proto.KindImage || first.Queued {
		return false
	}
	for _, it := range res.Items {
		if it.Role != "user" || it.Kind == proto.KindToolResult || it.ToolUseID != "" { // a tool's result (its pictures too)
			continue
		}
		if it.Source != nil || it.Injected != nil || it.From != "" || it.Task != nil || it.Wake {
			return false
		}
		words := it.Text
		if it.Kind == proto.KindImage { // a picture of the user's message: tied to it by the id alone
			words = ""
		}
		if !s.humanSent.has(self, it.ClientMsgID, words, it.Kind == proto.KindImage, handoffLine(it.ID)) {
			return false
		}
	}
	return true
}

// handoffTurnMost is the most items the node gives for one turn (node.go's page cap): a turn that long is not read.
const handoffTurnMost = 2000

// humanSends are the messages a person's own app sent into each conversation, by client_msg_id, as the hub vouched
// (SendParams.Human, and a conversation's first message, StartParams.Human), with the start of their words: the node
// ties an id to the first line that begins like the text sent, so a line is the user's only when it holds those words
// too. An id counts once: for the first transcript line it was read on, never another line carrying it; and a send of
// it the hub did not vouch for, or a second one with other words, voids it. Kept a day, at most humanSendsMost per
// conversation, in memory only (a restart forgets them, and the card is shown).
type humanSends struct {
	mu sync.Mutex
	m  map[string]map[string]humanSend // session → client_msg_id → the send
}

type humanSend struct {
	words string // the text sent, its words joined by single spaces, at most humanWordsMost bytes
	at    time.Time
	line  string // the transcript line it counted for (handoffLine), once one was read
	void  bool   // sent again by someone else, or with other words: it counts for nothing
}

// withoutContext is a vouched message's words, or its line's, compared without any context block: the ones the
// transcript drops from every user line, and the workspace mentions, which the app wrote one way and the manager sent
// filled in (a vouched message holds no other block, workspace_mention.go fillWorkspaces).
func withoutContext(text string) string {
	return transcript.ViewErrorsRe.ReplaceAllString(contextBlockRe.ReplaceAllString(text, ""), "")
}

// handoffLine is the transcript line an item belongs to: its id before the last "#", as the node ties a send to a line
// (node echo.go echoLineKey); "" for an item without an id.
func handoffLine(id string) string {
	if i := strings.LastIndexByte(id, '#'); i >= 0 {
		return id[:i]
	}
	return id
}

// humanWordsMost bounds the words kept of a send: enough that another's line cannot hold them by chance.
const humanWordsMost = 256

// humanWords are text's words joined by single spaces, cut to at most most bytes on a rune boundary.
func humanWords(text string, most int) string {
	w := strings.Join(strings.Fields(text), " ")
	if len(w) <= most {
		return w
	}
	cut := most
	for cut > 0 && !utf8.RuneStart(w[cut]) {
		cut--
	}
	return w[:cut]
}

const (
	humanSendsMost = 64
	humanSendsKeep = 24 * time.Hour
)

func (h *humanSends) note(session, id, text string) {
	if session == "" || id == "" {
		return
	}
	words := humanWords(withoutContext(text), humanWordsMost)
	h.mu.Lock()
	defer h.mu.Unlock()
	ids := h.sessionLocked(session)
	if sent, ok := ids[id]; ok { // the same id again: the same message (the node sends it once), else nothing
		if sent.words != words {
			sent.void = true
			ids[id] = sent
		}
		return
	}
	makeRoomLocked(ids, true)
	ids[id] = humanSend{words: words, at: time.Now()}
}

// voidSend: a send into session carried id, and it is not one that counts (the hub did not vouch for it, or it carries
// someone else's words). Were id one a person's app sent, it no longer counts (another author's line may carry it
// now); in a conversation with vouched sends, one not known yet is kept void while there is room, so a later vouched
// send of it does not count either. A conversation with none keeps nothing, and void ids never push out vouched ones.
func (h *humanSends) voidSend(session, id string) {
	if session == "" || id == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.m[session]) == 0 {
		return
	}
	ids := h.sessionLocked(session)
	sent, ok := ids[id]
	if !ok {
		if len(ids) == 0 || !makeRoomLocked(ids, false) {
			return
		}
		sent.at = time.Now()
	}
	sent.void = true
	ids[id] = sent
}

// sessionLocked is session's sends, the stale ones dropped; a conversation not known yet first drops the
// conversations all of whose sends are stale. h.mu held.
func (h *humanSends) sessionLocked(session string) map[string]humanSend {
	now := time.Now()
	if h.m == nil {
		h.m = map[string]map[string]humanSend{}
	}
	ids := h.m[session]
	if ids == nil {
		for k, other := range h.m {
			fresh := false
			for _, sent := range other {
				if now.Sub(sent.at) <= humanSendsKeep {
					fresh = true
					break
				}
			}
			if !fresh {
				delete(h.m, k)
			}
		}
		ids = map[string]humanSend{}
		h.m[session] = ids
	}
	for k, sent := range ids {
		if now.Sub(sent.at) > humanSendsKeep {
			delete(ids, k)
		}
	}
	return ids
}

// makeRoomLocked makes room for one more in ids, which holds at most humanSendsMost: the oldest void id goes first,
// then, when vouched ones may (vouched), the oldest of all; false when there is no room. h.mu held.
func makeRoomLocked(ids map[string]humanSend, vouched bool) bool {
	if len(ids) < humanSendsMost {
		return true
	}
	oldest, oldestAt := "", time.Time{}
	for _, voidOnly := range []bool{true, false} {
		if !voidOnly && !vouched {
			break
		}
		for k, sent := range ids {
			if voidOnly && !sent.void {
				continue
			}
			if oldest == "" || sent.at.Before(oldestAt) {
				oldest, oldestAt = k, sent.at
			}
		}
		if oldest != "" {
			delete(ids, oldest)
			return true
		}
	}
	return false
}

// has: id is a send the hub vouched for into session, text (the line the node tied to it) holds its words, and line is
// the transcript line it counts for: the first one it was read on (a picture of it, picture, is tied by the id alone,
// on the same line).
func (h *humanSends) has(session, id, text string, picture bool, line string) bool {
	if session == "" || id == "" || line == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	sent, ok := h.m[session][id]
	if !ok || sent.void || time.Since(sent.at) > humanSendsKeep {
		return false
	}
	if !picture && !strings.Contains(humanWords(withoutContext(text), 1<<20), sent.words) {
		return false
	}
	switch sent.line {
	case "":
		sent.line = line
		h.m[session][id] = sent
	case line:
	default:
		return false // the id on a second line: it counted for its first already
	}
	return true
}

// userOpened: conversation id is one the user opened, as noted when it was started (never a backfill's guess), and no
// extension's token opened it.
func (s *Service) userOpened(ctx context.Context, id string) bool {
	b := &s.origins
	b.mu.Lock()
	defer b.mu.Unlock()
	if !s.originsLocked(ctx) {
		return false
	}
	o := b.m[id]
	return o != nil && o.Origin == OriginUser && o.By == originByStart && o.Ext == ""
}

// handoffClaim is the approval that claimed a hand-off card, with the device the hub vouched for: the answer the
// card's tool goes by, whatever else reaches its question.
type handoffClaim struct {
	approver assistantwire.HandoffApprover
	r        proto.Resolution
	at       time.Time
}

// claimHandoffLocked records, under s.fleet.mu as hand-off card id is claimed with r, whether r is an approval from a
// device the hub vouched for. Claims left behind by a tool that stopped waiting go after an hour.
func (s *Service) claimHandoffLocked(id string, r proto.Resolution, approver *assistantwire.HandoffApprover) {
	if approver == nil || approver.DeviceID == "" || !proto.SecretHandoffApproves(r) || r.AnsweredBy != "" || r.Remembered {
		delete(s.fleet.handoffBy, id)
		return
	}
	now := time.Now()
	if s.fleet.handoffBy == nil {
		s.fleet.handoffBy = map[string]handoffClaim{}
	}
	for k, c := range s.fleet.handoffBy {
		if now.Sub(c.at) > time.Hour {
			delete(s.fleet.handoffBy, k)
		}
	}
	s.fleet.handoffBy[id] = handoffClaim{approver: *approver, r: r, at: now}
}

// takeHandoffClaim is the approval that claimed hand-off card id, and forgets it.
func (s *Service) takeHandoffClaim(id string) (handoffClaim, bool) {
	s.fleet.mu.Lock()
	defer s.fleet.mu.Unlock()
	c, ok := s.fleet.handoffBy[id]
	delete(s.fleet.handoffBy, id)
	return c, ok
}

// handoffValues are a hand-off's resolved values, in memory until the start is sent: stage seals them for one start,
// wipe zeroes them.
type handoffValues struct {
	s       *Service
	machine string
	env     []proto.SecretEnvVar
}

// handoffResolve resolves h's values, approved by approvedBy. A variable that fails stops the start: the answer says
// which, with its provider's code.
func (s *Service) handoffResolve(ctx context.Context, h *secretHandoff, approvedBy string) (*handoffValues, string) {
	hs := s.handoffSecrets()
	out := &handoffValues{s: s, machine: h.machine}
	results := make([]handoffResult, 0, len(h.vars))
	failed := false
	for _, v := range h.vars {
		if p, ok := hs.SecretProviderOf(v.Reference); !ok || p.Extension != v.Provider.Extension {
			// The provider the card named was switched off or another claims the scheme now: not the one approved.
			failed = true
			results = append(results, handoffResult{Name: v.Name, Status: handoffFailed, Code: "provider_changed"})
			continue
		}
		val, err := hs.ResolveSecret(ctx, v.Reference, wire.SecretPurpose{Kind: "agent_run", ID: h.launcher, Machine: h.machine}, approvedBy)
		code := ""
		switch {
		case err != nil:
			code = "failed"
			var se *extmgr.SecretError
			if errors.As(err, &se) {
				code = se.Code
			}
		case len(val) < proto.SecretEnvValueLeast:
			code = "too_short" // the node hides only values it can tell apart from text
		case len(val) > proto.SecretEnvValueMost:
			code = "too_long"
		case !utf8.ValidString(val) || strings.ContainsRune(val, 0):
			code = "not_text"
		}
		if code != "" {
			failed = true
			results = append(results, handoffResult{Name: v.Name, Status: handoffFailed, Code: code})
			continue
		}
		out.env = append(out.env, proto.SecretEnvVar{Name: v.Name, Value: []byte(val)})
		results = append(results, handoffResult{Name: v.Name, Status: handoffGranted})
	}
	if failed {
		out.wipe()
		return nil, handoffAnswer(results, "A secret could not be read from its provider after the approval.")
	}
	return out, ""
}

// stage seals the values to a one-time key of the computer's node and stages them there, over the manager's own
// connection: the ticket a start carries.
func (v *handoffValues) stage(ctx context.Context) (string, error) {
	var key proto.SecretStageResult
	if err := v.s.nodeCall(ctx, v.machine, proto.MethodSecretStage, proto.SecretStageParams{Check: true}, &key); err != nil {
		return "", err
	}
	sealed, err := proto.SealSecretEnv(key.Key, key.KeyID, v.env)
	if err != nil {
		return "", err
	}
	var staged proto.SecretStageResult
	if err := v.s.nodeCall(ctx, v.machine, proto.MethodSecretStage, proto.SecretStageParams{KeyID: key.KeyID, Sealed: sealed}, &staged); err != nil {
		return "", err
	}
	if staged.Ticket == "" {
		return "", errors.New("the node gave no ticket")
	}
	return staged.Ticket, nil
}

// wipe zeroes the values.
func (v *handoffValues) wipe() {
	if v == nil {
		return
	}
	for i := range v.env {
		clear(v.env[i].Value)
	}
	v.env = nil
}

// granted is the answer's line for a start that took the values.
func (h *secretHandoff) granted(approvedBy string) string {
	results := make([]handoffResult, 0, len(h.vars))
	for _, v := range h.vars {
		results = append(results, handoffResult{Name: v.Name, Status: handoffGranted})
	}
	b, _ := json.Marshal(results)
	by := "approved on the user's device"
	if approvedBy == "rule" {
		by = "by the user's Always rule"
	}
	return fmt.Sprintf(" Its environment has the secrets for this run only (%s); never print or log them. secret_env: %s", by, b)
}

// checkSecretEnvArg refuses agent_start arguments whose secret_env is not an object of strings (decoding it loosely
// would start the Agent with fewer variables than asked).
func checkSecretEnvArg(args json.RawMessage) error {
	var a struct {
		SecretEnv json.RawMessage `json:"secret_env"`
	}
	if json.Unmarshal(args, &a) != nil || len(a.SecretEnv) == 0 || string(a.SecretEnv) == "null" {
		return nil
	}
	var env map[string]string
	if err := json.Unmarshal(a.SecretEnv, &env); err != nil {
		return errors.New("secret_env must be an object of variable names to references (strings)")
	}
	return nil
}

// withoutSecretEnv is agent_start's arguments without secret_env, for a card that shows them (the health space's
// confirmation): the references stay off every card.
func withoutSecretEnv(args json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(args, &m) != nil {
		return args
	}
	if _, ok := m["secret_env"]; !ok {
		return args
	}
	delete(m, "secret_env")
	out, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return out
}

// refusedWith: err is a node's or the devices' refusal with code.
func refusedWith(err error, code string) bool {
	var re *rpc.Error
	return errors.As(err, &re) && re.Code == code
}

// handoffWords are the parts that say something.
func handoffWords(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
