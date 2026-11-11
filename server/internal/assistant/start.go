package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// Starting the assistant's conversations (docs/assistant/assistant.md "Process", "New main chat"): its directory on the machine is
// prepared first (the CLAUDE.md written from what the hub tells it, the connectors' and plugins' skills), the model
// configuration applied, then the node's assistant.send. The main conversation is one of them; putting it away for a
// new one keeps what it left for the next one's first message.

// errNoAssistantMachine: the hub has no machine for the assistant. Coded, so the app is told why in plain words with
// what to do (apierr.Call: 409 no_machine, docs/assistant/onboarding.md 5.2); its message stays the words logs and runs record.
// The main conversation's start and the memory (memory) both refuse with it.
var errNoAssistantMachine error = &rpc.Error{Code: proto.CodeNoMachine, Message: "assistant machine is not configured",
	Data: json.RawMessage(`{"delivered":"none"}`)}

// missingAssistantMachine distinguishes a computer awaiting confirmation from having no computer to use.
func (s *Service) missingAssistantMachine(ctx context.Context) error {
	if d, ok := s.pendingAssistantMachine(ctx); ok {
		return apierr.OnMachine(&rpc.Error{Code: proto.CodeMachinePending, Message: errMachinePending.Error(),
			Data: json.RawMessage(`{"delivered":"none"}`)}, d.Name)
	}
	return errNoAssistantMachine
}

// mainTitle is the main conversation's title on the lists the apps get: it has no title of its own
// (conversation_rename refuses it, the apps show the persona there), so neither its first message (the node's title for
// a transcript without one) nor a model's name nor a rename shows up as one (#1313).
const mainTitle = "Persona"

var errRenameMain = i18n.Err("assistant-start.rename_main")

var errWorkAssistant = i18n.Err("assistant-start.work_machine")

// claudeMD is the assistant directory's CLAUDE.md (prompts/assistant/claude_md.md) for its machine here. Its machine
// list is written from where the assistant actually runs: the ssh_host names are the hub machine's ssh aliases, which
// work only for an assistant on the hub's own machine, and a link (or tailcat) machine has no ssh at all; from anywhere
// else there is no command to give, only the Agents tools.
func (s *Service) claudeMD(ctx context.Context, here string, larkCLI bool) string {
	hello := s.hello(ctx)
	d := prompts.AssistantClaudeMD{Owner: strings.TrimSpace(hello.Owner), Notes: s.notes(ctx), ExtraDirs: s.kbExtraDirs(ctx),
		KnowledgeDir: s.kb.dir, LarkCLI: larkCLI, PersonaView: hello.PersonaView, Language: answerLanguage(hello.Locale)}
	devices := s.devices(ctx)
	if i := slices.IndexFunc(devices, func(d Device) bool { return d.ID == here }); i >= 0 {
		d.OnHub = devices[i].Kind == "local"
		if cwd := s.assistantCwd(devices[i]); cwd != "" {
			d.StateDir = path.Dir(cwd)
		}
	}
	for _, m := range devices {
		local := m.ID == here
		d.Machines = append(d.Machines, prompts.AssistantMachine{Name: m.Name, ID: m.ID, Local: local, SSHHost: m.SSHHost,
			AgentsOnly: !local && !(d.OnHub && m.Kind == "ssh")})
	}
	return prompts.RenderAssistantClaudeMD(d)
}

// prepare tells machine's node what it needs before a message to the assistant (node assistant.prepare) and answers
// the assistant's directory there: its CLAUDE.md with the plugins' parts, and the connectors signed in (rate limited
// counts: still signed in), whose built-in skills it puts in the directory
// (assistantskills.Requires) or takes out, and whose command lines it puts on the assistant's PATH (personad tool --cli <id>).
// A plugin skill the node says it does not have goes with its files next time.
func (s *Service) prepare(ctx context.Context, machine string) (string, error) {
	return s.prepareSpace(ctx, machine, "")
}

// prepareSpace is prepare for one of the assistant's spaces: "health" is the health space's directory with its own
// CLAUDE.md (docs/assistant/health.md); the connectors and plugin skills go there as to the assistant's own.
func (s *Service) prepareSpace(ctx context.Context, machine, space string) (string, error) {
	cwd, _, err := s.prepareSpaceIf(ctx, machine, space, false)
	return cwd, err
}

// prepareSpaceIf is prepareSpace; onlyChanged skips the node when it would get what this hub last gave it there, the
// same CLAUDE.md and plugin skills (refreshInstructions), and it answers whether it was written. An answer that brings
// a skill's files always goes: plugins.assistant hands them out once, so skipping would leave the old files on the
// machine until the CLAUDE.md changed.
func (s *Service) prepareSpaceIf(ctx context.Context, machine, space string, onlyChanged bool) (string, bool, error) {
	if s.isWorkMachine(machine) {
		return "", false, errWorkAssistant
	}
	var conns struct {
		Connectors []string          `json:"connectors"`
		CLIs       map[string]string `json:"clis"`
		LarkCLI    bool              `json:"lark_cli"`
	}
	if err := s.appCall(ctx, AppConnectors, ConnectorsSignedIn, map[string]any{}, &conns); err != nil {
		return "", false, err
	}
	var plugins struct {
		Instructions string          `json:"instructions"`
		Skills       json.RawMessage `json:"skills"`
	}
	if err := s.appCall(ctx, AppPlugins, PluginsAssistant, map[string]any{"machine_id": machine}, &plugins); err != nil {
		return "", false, err
	}
	if len(plugins.Skills) == 0 {
		plugins.Skills = json.RawMessage("null")
	}
	dir, md := s.assistantRel(), s.claudeMD(ctx, machine, conns.LarkCLI)+plugins.Instructions
	if space == SpaceHealth {
		dir, md = s.healthRel(), s.healthClaudeMD(ctx, machine)
	}
	key := machine + "\x00" + dir
	digests, files := skillDigests(plugins.Skills)
	given := preparedState{md: md, skills: digests}
	if onlyChanged && !files {
		s.preparedMu.Lock()
		same := s.prepared[key] == given
		s.preparedMu.Unlock()
		if same {
			return "", false, nil
		}
	}
	params := map[string]any{"dir": dir, "claude_md": md,
		"connectors": conns.Connectors, "clis": conns.CLIs, "plugin_skills": plugins.Skills}
	var prep struct {
		Cwd  string   `json:"cwd"`
		Need []string `json:"plugin_skills_need"`
	}
	if err := s.call(ctx, machine, DeviceAssistantPrepare, params, &prep); err != nil {
		return "", false, err
	}
	s.preparedMu.Lock()
	if s.prepared == nil {
		s.prepared = map[string]preparedState{}
	}
	if len(prep.Need) > 0 {
		// The node lacks some skills: their files come with the next plugins.assistant (plugins.skills_need below), and
		// the next refresh prepares this directory again, so its copies are made from them.
		delete(s.prepared, key)
	} else {
		s.prepared[key] = given
	}
	s.preparedMu.Unlock()
	if len(prep.Need) > 0 {
		if err := s.appCall(ctx, AppPlugins, PluginsSkillsNeed, map[string]any{"machine_id": machine, "names": prep.Need}, nil); err != nil {
			s.logf("assistant: plugin skills %v: %v", prep.Need, err)
		}
	}
	return prep.Cwd, true, nil
}

// preparedState is what prepareSpaceIf gave a node for a directory: the CLAUDE.md and the plugin skills (skillDigests).
type preparedState struct{ md, skills string }

// skillDigests are the plugin skills of a plugins.assistant answer as prepareSpaceIf compares them, each one's plugin,
// name and digest; files says whether any comes with its files.
func skillDigests(raw json.RawMessage) (digests string, files bool) {
	var skills []struct {
		Name   string                     `json:"name"`
		Plugin string                     `json:"plugin"`
		Digest string                     `json:"digest"`
		Files  map[string]json.RawMessage `json:"files"`
	}
	_ = json.Unmarshal(raw, &skills) // null from a manager without plugins: none
	var b strings.Builder
	for _, sk := range skills {
		fmt.Fprintf(&b, "%s/%s %s\n", sk.Plugin, sk.Name, sk.Digest)
		files = files || len(sk.Files) > 0
	}
	return b.String(), files
}

// instructionsEvery is how often refreshInstructions looks at the CLAUDE.md the assistant's machine should have.
const instructionsEvery = time.Minute

// instructionsLoop keeps the CLAUDE.md of the assistant and of the health space on the assistant's machine the ones
// this hub renders: right after it starts (a new version with new prompts) and whenever what they say changes (notes,
// machines, plugins). They used to be written only when a conversation started, so the main conversation, which goes
// on for days, kept the prompts of the hub that started it (#3235: #3068's ring cards never reached it). The node
// resumes a conversation in a fresh process on its next idle message once its directory's CLAUDE.md changed
// (node assistantProc.instructionsStale): the next message reads the new one.
func (s *Service) instructionsLoop(ctx context.Context) {
	t := time.NewTicker(instructionsEvery)
	defer t.Stop()
	for {
		s.refreshInstructions(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// refreshInstructions writes the assistant's and the health space's CLAUDE.md on the assistant's machine when they
// differ from the ones this hub last wrote there (none yet after a start: both are written), and their plugin skills
// when those changed (prepareSpaceIf).
func (s *Service) refreshInstructions(ctx context.Context) {
	if s.hello(ctx).Assistant.Standby {
		return // a mesh member: the center's assistant is the one in use (docs/mesh.md)
	}
	machine := s.settings(ctx).MachineID
	if machine == "" || s.isWorkMachine(machine) {
		return
	}
	d, ok := s.device(ctx, machine)
	if !ok || !d.Online || d.Home == "" {
		return
	}
	for _, space := range []string{"", SpaceHealth} {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, wrote, err := s.prepareSpaceIf(cctx, machine, space, true)
		cancel()
		switch {
		case err != nil:
			s.logf("assistant: refresh instructions (space %q): %v", space, err)
		case wrote:
			s.logf("assistant: wrote the CLAUDE.md of space %q on %s", space, machine)
		}
	}
}

// startOn starts a new conversation on machine: the directory prepared, the model configuration of p.Purpose ("" is
// the main chat's) applied, then the node's assistant.send (headless: no herdr tab, streamed tokens, any launcher per message).
func (s *Service) startOn(ctx context.Context, machine string, p StartParams, main bool) (proto.Session, error) {
	if p.Space != "" && p.Space != SpaceHealth {
		return proto.Session{}, badRequest(fmt.Errorf("space %q: health or none", p.Space))
	}
	if p.PermissionMode == "" && p.GuestTools == nil {
		p.PermissionMode = s.settings(ctx).DefaultPermissionMode
		if p.PermissionMode == "" {
			p.PermissionMode = proto.ModeBypass
		}
	}
	var cwd string
	var err error
	if p.GuestTools != nil {
		cwd, err = s.guestPrepare(ctx, machine) // its own directory: none of the assistant's instructions, memory or skills
	} else {
		cwd, err = s.prepareSpace(ctx, machine, p.Space)
	}
	if err != nil {
		return proto.Session{}, err
	}
	params := map[string]any{"cwd": cwd, "launcher": p.Launcher, "text": p.Text, "images": p.Images, "permission_mode": p.PermissionMode,
		"effort": p.Effort, "is_main": main}
	if p.ConfigDir != "" {
		params["config_dir"] = p.ConfigDir
	}
	if p.GuestTools != nil {
		// The node starts it with no built-in tools and only these of Persona's (node/assistant_guest.go).
		params["guest"] = map[string]any{"tools": p.GuestTools}
	}
	if p.NewSessionID != "" {
		params["new_session_id"] = p.NewSessionID // node/assistant_guest.go: under this id, once
	}
	if p.ClientMsgID != "" {
		params["client_msg_id"] = p.ClientMsgID // its first message's (node/node.go assistant.send)
	}
	purpose := p.Purpose
	if purpose == "" {
		purpose = "assistant"
	}
	if p.Launcher != "" {
		fit, refusal := s.mainChatFit(ctx, machine, p.Launcher)
		switch fit {
		case mainChatRefused:
			return proto.Session{}, refusal
		case mainChatGone:
			delete(params, "launcher")
		}
	}
	if err := s.applyModel(ctx, purpose, params); err != nil {
		return proto.Session{}, err
	}
	if params["launch"] != "headless" {
		return proto.Session{}, badRequest(fmt.Errorf("purpose %q requires headless launch for assistant.send", purpose))
	}
	if _, err := s.finishMainChatModel(ctx, machine, purpose, params, false); err != nil {
		return proto.Session{}, err
	}
	used, _ := params["launcher"].(string)
	var res struct {
		SessionID string `json:"session_id"`
	}
	err = s.call(ctx, machine, DeviceAssistantSend, params, &res)
	s.startRefused(ctx, machine, used, err) // Claude Code or the launcher missing: the readiness's claude step
	if err != nil {
		if d, ok := s.device(ctx, machine); ok {
			err = apierr.OnMachine(err, d.Name) // claude_missing on a sample user's MacBook (docs/assistant/onboarding.md 4.6)
		}
		return proto.Session{}, err
	}
	sess := proto.Session{SessionID: res.SessionID, Cwd: cwd, Status: proto.StatusWorking, Launcher: used, PermissionMode: p.PermissionMode,
		Effort: p.Effort, StartedByPersona: true, UpdatedAt: time.Now().UTC()}
	sess.MachineID, sess.Assistant, sess.State, sess.Space = machine, true, proto.StateWorking, p.Space
	return sess, nil
}

// start starts a conversation: on p.MachineID ("" the assistant's machine), or the main conversation.
func (s *Service) start(ctx context.Context, p StartParams) (proto.Session, error) {
	if p.Main && p.Space != "" {
		return proto.Session{}, badRequest(errors.New("the health space has no main conversation"))
	}
	if p.GuestTools != nil && (p.Main || p.Space != "") {
		return proto.Session{}, badRequest(errors.New("a guest conversation is neither the main one nor in a space"))
	}
	if p.Main {
		return s.startMain(ctx, p)
	}
	return s.startConversationOn(ctx, p, false)
}

func (s *Service) startConversationOn(ctx context.Context, p StartParams, main bool) (sess proto.Session, err error) {
	machine := p.MachineID
	if machine == "" {
		machine = s.settings(ctx).MachineID
		if machine == "" {
			return proto.Session{}, s.missingAssistantMachine(ctx)
		}
	}
	if s.isWorkMachine(machine) {
		return proto.Session{}, errWorkAssistant
	}
	// MCP discovery can arrive before assistant.send returns its session ID. Keep the
	// manager's authorization until the resulting conversation's origin is recorded.
	finishStart := s.beginAssistantStart(machine)
	defer finishStart()
	guestID := ""
	if p.GuestTools != nil {
		if p.NewSessionID != "" {
			guestID = p.NewSessionID
		} else {
			guestID = newGuestSessionID()
			p.NewSessionID = guestID
		}
		// The Agent SDK bridge discovers its tools before assistant.send returns. Register the guest's restriction
		// first, as roomCreate does, so that first discovery is the declared list rather than an empty one.
		if err := s.guestSet(ctx, machine, guestID, p.GuestTools); err != nil {
			return proto.Session{}, err
		}
		defer func() {
			if sess.SessionID != guestID {
				if err := s.guestDrop(context.WithoutCancel(ctx), guestID); err != nil {
					s.logf("assistant: forget an unstarted guest conversation %s: %v", short(guestID), err)
				}
			}
		}()
	}
	sess, err = s.startOn(ctx, machine, p, main)
	if errors.Is(err, ErrNoDevice) && p.MachineID == "" {
		return proto.Session{}, s.missingAssistantMachine(ctx)
	}
	if err != nil {
		return sess, err
	}
	if guestID != "" && sess.SessionID != guestID {
		return sess, errors.New("the node did not start the guest under its registered session id")
	}
	sess.Origin, sess.OriginRef, sess.OpenedBy = firstNonEmpty(p.Origin, OriginUser), p.OriginRef, p.OpenedBy
	s.originSet(ctx, sess.MachineID, sess.SessionID, sess.Origin, sess.OriginRef, sess.OpenedBy)
	return sess, nil
}

// startConversation starts a new conversation of the assistant's own (a scheduled task's, conversation_new's).
func (s *Service) startConversation(ctx context.Context, p startParams) (proto.Session, error) {
	return s.start(ctx, StartParams{Text: prompts.MessageSourceText(p.Text, p.Source), PermissionMode: p.PermissionMode, Purpose: p.Purpose, Space: p.Space,
		Origin: p.Origin, OriginRef: p.OriginRef})
}

// Start is assistant.start: a conversation one of the hub's features starts (the inbox's AI twin), or the channels'
// (feishu_message.go, channels_plugin.go). It is named after its first turn like the app's, unless it is untitled.
func (s *Service) Start(ctx context.Context, p StartParams) (proto.Session, error) {
	if p.Origin == "" || p.Origin == OriginUser {
		p.Text = prompts.ProtectUserMessage(p.Text)
	}
	sess, err := s.start(ctx, p)
	if err != nil {
		return proto.Session{}, err
	}
	if !p.Untitled {
		s.titleNew(sess.MachineID, sess.SessionID, time.Now())
	}
	return sess, nil
}

// ---- the main conversation ----

// mainHandoffKey is the setting with what the old main conversation left, until the next one takes it.
const mainHandoffKey = "assistant.main_handoff"

// maxFormerMains is how many earlier main conversations the assistant's machine keeps listing however quiet.
const maxFormerMains = 10

// mainHandoff is what a replaced main conversation left for the next one (node assistant.handoff).
type mainHandoff struct {
	From      string    `json:"from"`
	MachineID string    `json:"machine_id"`
	Until     time.Time `json:"until"`
	// For: the new main conversation it waits in, started without a message (Android starts it empty, then sends).
	For     string `json:"for,omitempty"`
	Summary string `json:"summary,omitempty"`
	Recent  []struct {
		Role string `json:"role"`
		Text string `json:"text"`
	} `json:"recent,omitempty"`
}

// mainBook holds the main conversation still while it changes, and the handoff with no database.
type mainBook struct {
	mu        sync.Mutex
	handoff   mainHandoff
	execution mainExecutionBook
}

func (s *Service) loadHandoff(ctx context.Context) mainHandoff {
	if st := s.store(); st != nil {
		var h mainHandoff
		if ok, err := st.Setting(ctx, mainHandoffKey, &h); err != nil || !ok {
			return mainHandoff{}
		}
		return h
	}
	return s.main.handoff
}

func (s *Service) saveHandoff(ctx context.Context, h mainHandoff) error {
	if st := s.store(); st != nil {
		return st.PutSetting(ctx, mainHandoffKey, h)
	}
	s.main.handoff = h
	return nil
}

// startMain starts a new main conversation with the user's first message: what the old one left goes before it
// (prompts/assistant/main_handoff), once. Started without a message, it waits for the first one sent to it.
func (s *Service) startMain(ctx context.Context, p StartParams) (proto.Session, error) {
	s.main.mu.Lock()
	defer s.main.mu.Unlock()
	handoff := s.loadHandoff(ctx)
	empty := strings.TrimSpace(p.Text) == "" && len(p.Images) == 0
	if handoff.From != "" && !empty {
		p.Text = s.renderHandoff(ctx, handoff, p.Text)
	}
	p.MachineID, p.Purpose, p.Main = "", "", false
	p.Origin, p.OriginRef, p.OpenedBy = OriginUser, "", "" // the main conversation is the user's, whatever channel opened it
	sess, err := s.startConversationOn(ctx, p, true)
	if err != nil {
		return proto.Session{}, err
	}
	if _, err := s.updateSettings(ctx, func(v *Settings) error {
		*v = s.effective(ctx, *v)
		v.Main, v.MainMachineID = sess.SessionID, sess.MachineID
		return nil
	}); err != nil {
		s.logf("assistant: save main: %v", err)
	}
	if handoff.From != "" {
		next := mainHandoff{} // used
		if empty {
			next, next.For = handoff, sess.SessionID
		}
		if err := s.saveHandoff(ctx, next); err != nil {
			s.logf("assistant: what the old main conversation left: %v", err)
		}
	}
	s.settingsChanged(ctx)
	return sess, nil
}

// newMain puts the main conversation away: what it left is kept for the next one (carry), it is no longer the main
// one, renamed and archived. The next message to the main conversation starts a new one (startMain). It answers the
// old conversation's id ("" when there was none) and whether anything of it is carried over.
func (s *Service) newMain(ctx context.Context, carry bool, expect string) (string, bool, error) {
	s.main.mu.Lock()
	defer s.main.mu.Unlock()
	cfg := s.settings(ctx)
	old := cfg.Main
	if expect != "" && expect != old {
		return "", false, &rpc.Error{Code: "main_changed", Message: i18n.T(s.lang(ctx), "assistant-start.main_changed")}
	}
	if old == "" {
		return "", false, nil
	}
	machine, c, found := s.findSession(ctx, old, cfg.mainRouteMachine())
	if found && (c.Status == proto.StatusWorking || (c.Compaction != nil && c.Compaction.State == proto.CompactionRunning)) {
		return "", false, &rpc.Error{Code: "busy", Message: i18n.T(s.lang(ctx), "assistant-start.main_busy")}
	}
	if !found {
		machine = cfg.mainRouteMachine()
	}
	now := time.Now().UTC()
	handoff := mainHandoff{From: old, MachineID: machine, Until: now}
	carried := false
	if carry && found {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := s.call(cctx, machine, DeviceAssistantHandoff, map[string]any{"session_id": old}, &handoff)
		cancel()
		handoff.From, handoff.MachineID, handoff.Until = old, machine, now
		if err != nil {
			s.logf("assistant: what the main conversation left: %v", err)
		}
		carried = handoff.Summary != "" || len(handoff.Recent) > 0
	}
	if err := s.saveHandoff(ctx, handoff); err != nil {
		return "", false, err
	}
	if err := s.putAwayMain(ctx, machine, old, now); err != nil {
		return "", false, err
	}
	s.logf("assistant: main conversation %s put away (carried over: %v)", short(old), carried)
	return old, carried, nil
}

// clearMain clears the main conversation's context in place (the apps' "Clear context" and /clear, as Cindy's /clear
// does): it is put away as newMain puts it, with nothing carried over, then its process is closed whether it works or
// not (a turn stops, and nothing it ran in the background starts another turn there) and a question it waits on is
// taken back. What an earlier newMain left for the next conversation is dropped too, so the next message to the main
// conversation starts a new one with a fresh context (startMain): no --resume, no handoff. Nothing is deleted, and the
// work it handed out (tasks, their Agents) goes on. It answers the old conversation's id ("" when there was none).
func (s *Service) clearMain(ctx context.Context, expect string) (string, error) {
	// past the checks it goes through as one change, even when the app stops waiting for the answer
	ctx = context.WithoutCancel(ctx)
	old, machine, c, found, err := s.clearMainLocked(ctx, expect)
	if err != nil || old == "" {
		return old, err
	}
	// after the lock: the node may be slow, and every message to the main conversation waits on that lock
	if found {
		s.closeMain(ctx, machine, c)
	}
	s.logf("assistant: main conversation %s cleared", short(old))
	return old, nil
}

// clearMainLocked is clearMain's change under s.main.mu: the check, the handoff dropped and the conversation put away.
func (s *Service) clearMainLocked(ctx context.Context, expect string) (old, machine string, c proto.Session, found bool, err error) {
	s.main.mu.Lock()
	defer s.main.mu.Unlock()
	cfg := s.settings(ctx)
	old = cfg.Main
	if expect != "" && expect != old {
		return "", "", c, false, &rpc.Error{Code: "main_changed", Message: i18n.T(s.lang(ctx), "assistant-start.main_changed")}
	}
	if err := s.saveHandoff(ctx, mainHandoff{}); err != nil {
		return "", "", c, false, err
	}
	if old == "" {
		return "", "", c, false, nil
	}
	machine, c, found = s.findSession(ctx, old, cfg.mainRouteMachine())
	if !found {
		machine = cfg.mainRouteMachine()
	}
	if err := s.putAwayMain(ctx, machine, old, time.Now().UTC()); err != nil {
		return "", "", c, false, err
	}
	return old, machine, c, found, nil
}

// closeMain ends the main conversation's process once clearMain has put it away: its turn stops as with the Stop
// button, and a question it waits on is taken back. Only Persona's own headless process is closed: one the user resumed
// in a terminal (a pane) is theirs, and the node's session.close would close that terminal. Unlike the Stop button it
// does not stop a Feishu request the conversation reports on (UserStopped): that would interrupt the Agents working on
// the tasks it handed out, which a clear leaves running. A failure is only logged: an offline machine runs no turn.
func (s *Service) closeMain(ctx context.Context, machine string, c proto.Session) {
	s.withdrawAskUser(c.SessionID)
	if c.PaneID != "" {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := s.call(cctx, machine, "session.close", map[string]any{"session_id": c.SessionID}, nil); err != nil {
		s.logf("assistant: close the main conversation %s: %v", short(c.SessionID), err)
	}
}

// putAwayMain makes old no longer the main conversation: first among the former ones (its machine keeps listing it),
// renamed "旧主对话 · 至 M月D日" by the user's hand (autonaming leaves it) and archived; sending to it again brings it back
// (unarchiveOnSend). The caller holds s.main.mu.
func (s *Service) putAwayMain(ctx context.Context, machine, old string, now time.Time) error {
	if _, err := s.updateSettings(ctx, func(v *Settings) error {
		*v = s.effective(ctx, *v)
		v.Main, v.MainMachineID = "", ""
		v.Former = slices.DeleteFunc(v.Former, func(f FormerMain) bool { return f.SessionID == old })
		v.Former = append([]FormerMain{{MachineID: machine, SessionID: old, Until: now}}, v.Former...)
		if len(v.Former) > maxFormerMains {
			v.Former = v.Former[:maxFormerMains]
		}
		return nil
	}); err != nil {
		return err
	}
	local := now.In(s.location(ctx))
	s.titleSet(machine, old, fmt.Sprintf("旧主对话 · 至 %d月%d日", local.Month(), local.Day()), titleByUser)
	if _, err := s.setArchived(ctx, machine, old, true, titleByUser); err != nil {
		s.logf("assistant: archive the old main conversation %s: %v", short(old), err)
	}
	s.settingsChanged(ctx)
	return nil
}

// findSession is the session id on the machine preferred, else on any machine (false when none lists it).
func (s *Service) findSession(ctx context.Context, id, preferred string) (string, proto.Session, bool) {
	machines := []string{preferred}
	for _, d := range s.devices(ctx) {
		if d.ID != preferred {
			machines = append(machines, d.ID)
		}
	}
	for _, m := range machines {
		if c, ok := s.session(ctx, m, id); ok {
			return m, c, true
		}
	}
	return "", proto.Session{}, false
}

// handoffFor is the text of a message to conversation sid: with what the old main conversation left before it when
// sid is the new main one that was started empty and this is the first message to it.
func (s *Service) handoffFor(ctx context.Context, sid, text string) string {
	if sid == "" {
		return text
	}
	s.main.mu.Lock()
	defer s.main.mu.Unlock()
	handoff := s.loadHandoff(ctx)
	if handoff.From == "" || handoff.For != sid {
		return text
	}
	if err := s.saveHandoff(ctx, mainHandoff{}); err != nil {
		s.logf("assistant: clear what the old main conversation left: %v", err)
		return text
	}
	return s.renderHandoff(ctx, handoff, text)
}

// renderHandoff puts what the old main conversation left before text (prompts/assistant/main_handoff).
func (s *Service) renderHandoff(ctx context.Context, handoff mainHandoff, text string) string {
	hp := prompts.MainHandoff{FromID: handoff.From, Summary: handoff.Summary, Text: text}
	local := handoff.Until.In(s.location(ctx))
	hp.Until = fmt.Sprintf("%d月%d日 %02d:%02d", local.Month(), local.Day(), local.Hour(), local.Minute())
	for _, t := range handoff.Recent {
		hp.Recent = append(hp.Recent, prompts.HandoffTurn{Role: t.Role, Text: t.Text})
	}
	return prompts.RenderMainHandoff(hp)
}

// ---- the routes ----

// requestOrigin is where a conversation POST /api/v1/assistant/conversations opens comes from: an extension's
// (a Feishu /agent when it runs on lark.agent's model), else the user's in an app.
func requestOrigin(r *http.Request, purpose string) (origin, ref string) {
	if r.Header.Get(RoleHeader) != roleExtension {
		return OriginUser, ""
	}
	ext := r.Header.Get(ExtensionHeader)
	if purpose == purposeLarkAgent {
		return OriginLarkAgent, ext
	}
	return OriginExtension, ext
}

// requestOpener is the extension whose token asks POST /api/v1/assistant/conversations to open one (the hub's
// headers: a client's own are dropped on the way), "" for anyone else.
func requestOpener(r *http.Request) string {
	if r.Header.Get(RoleHeader) != roleExtension {
		return ""
	}
	return r.Header.Get(ExtensionHeader)
}

func (s *Service) startRoutes() {
	s.handle("POST /api/v1/assistant/conversations", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Prompt         string        `json:"prompt"`
			Images         []proto.Image `json:"images"`
			Launcher       string        `json:"launcher"`
			PermissionMode string        `json:"permission_mode"`
			Effort         string        `json:"effort"`
			Main           bool          `json:"main"` // becomes the main conversation
			// Purpose is the model purpose it runs under ("assistant" when empty; a plugin's /agent asks for its own);
			// MachineID one of the hub's machines instead of the assistant's (docs/protocol/protocol.md "REST (App → hub)").
			Purpose   string `json:"purpose"`
			MachineID string `json:"machine_id"`
			Space     string `json:"space"` // "health": a conversation of the health space (docs/assistant/health.md)
			// ReportTasks: the tasks it opens are told back to it, as the main conversation's quiet ones are (#3192,
			// reporters.go); a plugin's /agent hands their results on.
			ReportTasks bool `json:"report_tasks"`
			// Guest: a guest conversation (guest.go): only these connector tools, nothing of the assistant's.
			Guest *GuestSpec `json:"guest"`
		}
		if err := decodeLargeBody(w, r, &in, 32<<20); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 110*time.Second)
		defer cancel()
		if in.Purpose != "" {
			if _, err := s.resolveModel(ctx, in.Purpose); err != nil {
				apierr.Write(w, http.StatusBadRequest, fmt.Errorf("purpose %q: %w", in.Purpose, err))
				return
			}
		}
		if in.Space != "" && in.Space != SpaceHealth {
			apierr.Write(w, http.StatusBadRequest, fmt.Errorf("space %q: health or none", in.Space))
			return
		}
		if in.Space != "" && in.Main {
			apierr.Write(w, http.StatusBadRequest, errors.New("the health space has no main conversation"))
			return
		}
		if in.MachineID != "" {
			if _, ok := s.device(ctx, in.MachineID); !ok {
				apierr.Write(w, http.StatusBadRequest, fmt.Errorf("no machine %q", in.MachineID))
				return
			}
		}
		if in.Guest != nil && (in.Main || in.Space != "") {
			apierr.Write(w, http.StatusBadRequest, errors.New("a guest conversation is neither the main one nor in a space"))
			return
		}
		if err := s.checkGuest(ctx, in.Guest); err != nil {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		var guestTools []string
		if in.Guest != nil {
			guestTools, in.ReportTasks = in.Guest.Tools, false // it hands nothing out
		}
		origin, ref := requestOrigin(r, in.Purpose)
		sentence := in.Prompt // as the owner wrote it, for the route (route.go)
		if in.Guest == nil {
			in.Prompt = s.withWorkspaces(ctx, in.Prompt) // as SendParams does for the next messages
		}
		// The main conversation is the assistant's, on its machine.
		ctx, note := withLauncherNote(ctx)
		sess, err := s.Start(ctx, StartParams{Text: in.Prompt, Images: in.Images, Launcher: in.Launcher, PermissionMode: in.PermissionMode,
			Effort: in.Effort, Purpose: in.Purpose, MachineID: in.MachineID, Main: in.Main, Space: in.Space, Origin: origin, OriginRef: ref,
			OpenedBy: requestOpener(r), GuestTools: guestTools})
		if err != nil {
			apierr.Call(w, err)
			return
		}
		if in.ReportTasks && !in.Main {
			s.markReporter(ctx, sess.MachineID, sess.SessionID)
		}
		// The owner's New chat: is it a direction? In the background; nothing waits for it (route.go).
		if origin == OriginUser && !in.Main && in.Guest == nil && in.Space == "" && in.Purpose == "" && !in.ReportTasks {
			go s.routeNewChat(sess.MachineID, sess.SessionID, sentence)
		}
		if *note != "" {
			apierr.WriteJSON(w, map[string]any{"session": sess, "launcher_note": *note})
			return
		}
		apierr.WriteJSON(w, map[string]any{"session": sess})
	})
	// What this hub's assistant can do that an extension asks before relying on it: an older hub answers 404 and
	// would ignore what it does not know (a guest conversation started as an ordinary one).
	s.handle("GET /api/v1/assistant/capabilities", func(w http.ResponseWriter, r *http.Request) {
		apierr.WriteJSON(w, map[string]any{"guest_conversations": true})
	})
	s.handle("POST /api/v1/assistant/conversations/{id}/compact", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			MachineID string `json:"machine_id"` // "" is the assistant's machine
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		if in.MachineID == "" {
			in.MachineID = s.settings(r.Context()).MachineID
		}
		if _, ok := s.device(r.Context(), in.MachineID); !ok {
			apierr.Write(w, http.StatusBadRequest, fmt.Errorf("no machine %q", in.MachineID))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute) // a process may have to start first
		defer cancel()
		if err := s.call(ctx, in.MachineID, DeviceAssistantCompact, map[string]any{"session_id": r.PathValue("id")}, nil); err != nil {
			apierr.Call(w, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"ok": true})
	})
	s.handle("POST /api/v1/assistant/main/new", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Carry  string `json:"carry"`  // summary (default) | none
			Expect string `json:"expect"` // the main conversation the app shows: refused when it is another by now
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		if in.Carry != "" && in.Carry != "summary" && in.Carry != "none" {
			apierr.Write(w, http.StatusBadRequest, fmt.Errorf("carry %q: summary or none", in.Carry))
			return
		}
		old, carried, err := s.newMain(r.Context(), in.Carry != "none", in.Expect)
		if err != nil {
			apierr.Call(w, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"ok": true, "archived": old, "carried": carried})
	})
	s.handle("POST /api/v1/assistant/main/clear", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Expect string `json:"expect"` // the main conversation the app shows: refused when it is another by now
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
			apierr.Write(w, http.StatusBadRequest, err)
			return
		}
		old, err := s.clearMain(r.Context(), in.Expect)
		if err != nil {
			apierr.Call(w, err)
			return
		}
		apierr.WriteJSON(w, map[string]any{"ok": true, "archived": old})
	})
}

// ---- what the hub asks about a message or a rename ----

// badRequest is err as the refusal of a request that is wrong as it is (the hub answers it 400).
// badRequest is err as a bus error, said in l when it is an i18n.Message (no l: the default language).
func badRequest(err error, l ...i18n.Lang) error {
	msg := err.Error()
	if len(l) > 0 {
		msg = i18n.Localize(err, l[0])
	}
	return &rpc.Error{Code: CodeBadRequest, Message: msg}
}

// appLang is the language an app's request came in (a bus call's lang), else the user's.
func (s *Service) appLang(ctx context.Context, lang string) i18n.Lang {
	return i18n.Lang(lang).Or(s.lang(ctx))
}

// SendParams is assistant.send_params: a message for conversation p.SessionID on p.MachineID, with what it runs on
// filled in (sendModel) and, the first one to a new main conversation started empty, what the old one left before
// its text. A conversation the machine does not list (yet) that is not the main one keeps what it runs on.
func (s *Service) SendParams(ctx context.Context, p SendParams) (map[string]any, error) {
	return s.messageParams(ctx, p, true)
}

// messageParams is SendParams for a message that is the owner's own (owner), or one the manager writes in their
// place (a channel's "answer again"), which preempts nothing.
func (s *Service) messageParams(ctx context.Context, p SendParams, owner bool) (map[string]any, error) {
	if p.Params == nil {
		p.Params = map[string]any{}
	}
	delete(p.Params, "source") // app input cannot claim an automated author
	delete(p.Params, "message_source")
	if id, _ := p.Params["client_msg_id"].(string); id != "" {
		s.availOwnerSent(p.MachineID, p.SessionID, id, time.Now()) // a replay opens no reply wait (availability.go)
	}
	text, said := p.Params["text"].(string)
	if said {
		p.Params["text"] = prompts.ProtectUserMessage(text)
	} else {
		_, said = p.Params["images"] // the node asking what its own message runs on carries neither (NodeAnswer)
	}
	sent, _ := p.Params["text"].(string)
	params, err := s.sendParams(ctx, p)
	if err == nil && p.Human { // the hub's word: a person's own app sent it (secret_handoff.go handoffUserTurn)
		id, _ := p.Params["client_msg_id"].(string)
		s.humanSent.note(p.SessionID, id, sent)
	}
	if err == nil && said && owner {
		s.ownerInterjects(p.SessionID, params)
	}
	return params, err
}

// ownerInterjects: params are the owner's own message to conversation id, ready to send. On a round they did not
// start it is read at once, whatever interject_background says (owner_preempt.go).
func (s *Service) ownerInterjects(id string, params map[string]any) {
	if s.ownerPreempts(id) {
		params["interject_background"] = true
	}
	s.ownerSent(id)
}

// sendParams also serves manager-generated notices, which must not be marked as
// user-authored merely because they share the app's model-selection path.
func (s *Service) sendParams(ctx context.Context, p SendParams) (map[string]any, error) {
	params := p.Params
	if params == nil {
		params = map[string]any{}
	}
	params["is_main"] = false
	if err := s.roomRefusesSend(ctx, p.MachineID, p.SessionID, p.Lang); err != nil {
		return nil, err // an ended room: 409 meeting_ended (meeting_room.go)
	}
	if s.isGuest(ctx, p.MachineID, p.SessionID) {
		// A guest conversation keeps what it started on: no launcher, model, runtime or mode picked for a message
		// (the node refuses a change too, node/assistant_guest.go).
		for _, k := range []string{"launcher", "model", "effort", "model_fingerprint", "runtime", "permission_mode"} {
			delete(params, k)
		}
		return params, nil
	}
	cfg := s.settings(ctx)
	main := cfg.Main != "" && cfg.Main == p.SessionID && cfg.mainRouteMachine() == p.MachineID
	if !main {
		if _, known := s.session(ctx, p.MachineID, p.SessionID); !known {
			return params, nil
		}
	}
	if err := s.sendModel(ctx, p.MachineID, p.SessionID, params); err != nil {
		var other *launcherOtherKind
		if errors.As(err, &other) {
			return nil, other.rpcError(s.appLang(ctx, p.Lang)) // 409 launcher_other_kind, with the kinds for the app
		}
		return nil, badRequest(err, s.appLang(ctx, p.Lang)) // a typed model that is no id is the app's user's to read
	}
	if text, ok := params["text"].(string); ok {
		text = s.withWorkspaces(ctx, text) // an @-mentioned workspace: where it is and what worked there (workspace_mention.go)
		if main {
			text = s.handoffFor(ctx, p.SessionID, text)
		}
		params["text"] = text
	}
	return params, nil
}

// sendModel puts into p (a message for one of the assistant's conversations, sid on machine) which launcher and model
// run it: one picked in the chat's menu (sent with the message) wins; else the main conversation and a scheduled
// task's own conversation (the morning briefing's, #1544) follow Model configuration › Main chat, and the others keep what they run. Those two follow
// the setting on every message, not what they ran last: once a run had moved one to another launcher, every later
// message stayed there (#1518). A launcher or model that is gone falls back to the assistant's. A picked one is checked
// and fingerprinted like a configured one. The phone's messages, the node's own and the follow-ups the assistant
// schedules for itself (runTask) all go through here.
//
// A model picked for this message may be one the launcher's list does not have (typed in the chip, #1644): it has to
// be a model id, and it is kept. Only a model carried over from what the conversation ran is held to the list (and the
// ids typed for that launcher before), so a model the gateway dropped does not fail every later turn.
func (s *Service) sendModel(ctx context.Context, machine, sid string, p map[string]any) error {
	cfg := s.settings(ctx)
	p["is_main"] = cfg.Main != "" && cfg.Main == sid && cfg.mainRouteMachine() == machine
	if cfg.InterjectBackground != nil && *cfg.InterjectBackground {
		p["interject_background"] = true
	}
	followsSetting := cfg.Main != "" && cfg.Main == sid && cfg.mainRouteMachine() == machine || s.isTaskConversation(ctx, machine, sid)
	picked := false // a model picked with this message
	if m, ok := p["model"].(string); ok && strings.TrimSpace(m) != "" {
		m = strings.TrimSpace(m)
		if err := checkModelID(m); err != nil {
			return err
		}
		p["model"], picked = m, true
	}
	l, _ := p["launcher"].(string)
	asked := l != "" // picked in the chat's menu for this message
	if asked {
		if err := s.otherKindPick(ctx, machine, sid, l); err != nil {
			return err // another agent kind can't resume this conversation: refused, not swapped on the node
		}
	}
	if l == "" && !followsSetting {
		if c, known := s.session(ctx, machine, sid); known {
			l = c.Launcher // no launcher picked: the one this conversation runs
			if l != "" {
				p["launcher"] = l
			}
			// And the model it runs, so no pick keeps it; one that is no model id (Claude's "<synthetic>") is not.
			if _, asked := p["model"]; !asked && proto.ValidModelID(c.Model) {
				p["model"] = c.Model
			}
		}
	}
	// A launcher or model that is gone (a renamed managed launcher, a model the gateway dropped), or a launcher that
	// can't run the assistant (a Paseo provider, a managed Codex one: launcher_mainchat.go) fails every turn: fall back
	// to the assistant's launcher and its default. One picked for this message that can't run it is refused, saying why.
	if l != "" {
		model, _ := p["model"].(string)
		fit, refusal := s.mainChatFit(ctx, machine, l)
		switch {
		case fit == mainChatRefused && asked:
			return refusal
		case fit != mainChatFits:
			l = s.launcherNow(ctx)
			s.logf("[%s] %s: its launcher can't run it any more, using %s", machine, short(sid), l)
			p["launcher"] = l
			delete(p, "model")
		case model != "" && !picked && !s.launcherHasModel(ctx, machine, l, model):
			s.logf("[%s] %s: model %s is not in %s's list any more, using its default", machine, short(sid), model, l)
			delete(p, "model")
		}
	}
	pickedLauncher := l != ""
	if pickedLauncher {
		if err := s.applyPickedModel(ctx, p); err != nil {
			return err
		}
	} else if err := s.applyModel(ctx, "assistant", p); err != nil {
		return err
	}
	_, err := s.finishMainChatModel(ctx, machine, "assistant", p, pickedLauncher)
	return err
}

// isTaskConversation: sid on machine is a scheduled task's own conversation.
func (s *Service) isTaskConversation(ctx context.Context, machine, sid string) bool {
	return slices.ContainsFunc(s.taskViews(ctx), func(t TaskView) bool { return t.RunSessionID == sid && t.RunMachineID == machine })
}

// Rename is assistant.rename: the user renames conversation p.SessionID in the app ({title?, tab_name?}). The main
// conversation has no title to change; a Codex one keeps the assistant's title (its tab is still renamed). The node
// renames; a new title is the user's from now on. An extension's (p.Extension, as the hub's auth found it) is only a
// hint (titleHinted): the hub still names a conversation it started, as it names the others.
func (s *Service) Rename(ctx context.Context, p RenameParams) (json.RawMessage, error) {
	params := p.Params
	if params == nil {
		params = map[string]any{}
	}
	params["session_id"] = p.SessionID
	title, _ := params["title"].(string)
	if strings.TrimSpace(title) != "" && s.isMain(ctx, p.SessionID) {
		return nil, badRequest(errRenameMain, s.lang(ctx)) // the apps show the persona there (#1313)
	}
	if c, found := s.session(ctx, p.MachineID, p.SessionID); found && !c.Assistant {
		return s.renameAgent(ctx, p, c)
	}
	if c, _ := s.session(ctx, p.MachineID, p.SessionID); c.Agent == "codex" {
		delete(params, "title") // only the assistant's title for Codex (its conversation_rename does the same)
	}
	out := json.RawMessage(`{"ok":true}`)
	if params["title"] != nil || params["tab_name"] != nil {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := s.call(cctx, p.MachineID, DeviceSessionRename, params, &out); err != nil {
			return nil, err
		}
	}
	switch {
	case strings.TrimSpace(title) == "":
	case p.Extension != "":
		s.titleHinted(p.MachineID, p.SessionID, title)
	default:
		s.titleSet(p.MachineID, p.SessionID, title, titleByUser)
	}
	return out, nil
}

// answerLanguage is what the CLAUDE.md asks the assistant to answer in, and the language the inbox's summaries and
// the conversations' titles are written in: the user's language by its own name, "" for zh-Hans (the prompts then
// say "use Chinese", as they did before i18n).
func answerLanguage(locale string) string {
	l := i18n.Lang(locale)
	if !i18n.Valid(l) || l == i18n.ZhHans {
		return ""
	}
	return l.Name()
}
