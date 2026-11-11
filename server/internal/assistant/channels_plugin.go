package assistant

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/nwverse/persona/server/internal/compat"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The channels plugins contribute (the channel extension point, docs/extensions/channels.md "Contributing a channel"): the program
// talks to its chat app through the hub — the owner's messages come as channels.receive, what is sent goes as
// channels.send — and everything else is here: commands, where a message goes, the turn (channels_runs.go), what is
// asked answered in text, pushes and the sync mirror.

// channelSend sends text to channel id's owner: in pieces its messages hold, each once by its ref.
func (s *Service) channelSend(ctx context.Context, id, ref, text, kind, replyTo string) error {
	v, ok, err := s.pluginChannelNow(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w %s", errNoChannel, id)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	pieces := channel.Split(text, v.Capabilities.MaxText)
	for i, piece := range pieces {
		p := channel.SendParams{Ref: ref, Text: piece, Kind: kind, ReplyTo: replyTo}
		if len(pieces) > 1 {
			p.Ref = fmt.Sprintf("%s#%d", ref, i)
		}
		err := s.appCall(ctx, AppChannels, ChannelsSend, map[string]any{"channel": id, "ref": p.Ref, "text": p.Text, "kind": p.Kind,
			"reply_to": p.ReplyTo}, nil)
		var re *rpc.Error
		if errors.As(err, &re) && re.Code == CodeCannotSend {
			return errCannotSend
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// channelTell sends a line to channel id's owner, logging a failure (never the text).
func (s *Service) channelTell(ctx context.Context, id, replyTo, text, kind string) error {
	err := s.channelSend(ctx, id, randomHex(8), text, kind, replyTo)
	if err != nil {
		s.logf("channels: %s: send: %v", id, err)
	}
	return err
}

// extensionSink is a plugin's channel as the pipeline's sink: text, and "typing" when it has it.
type extensionSink struct {
	s  *Service
	id string
}

// answer sends the answer in the pieces the channel takes, each by its ref: tried again (an error) unless there is
// nowhere to send it (cannot_send, the channel gone).
func (e extensionSink) answer(ctx context.Context, r *channel.Run, answer, note string, _ time.Duration, _, quiet bool, _ channel.End) error {
	if quiet {
		return nil
	}
	text := strings.TrimSpace(strings.TrimSpace(answer) + "\n\n" + note)
	err := e.s.channelSend(ctx, e.id, r.ID+":"+fmt.Sprint(r.Base), text, channel.OutAnswer, r.ReplyTo)
	if errors.Is(err, errCannotSend) || errors.Is(err, errNoChannel) {
		e.s.logf("channels: %s: an answer could not be sent: %v", e.id, err)
		return nil
	}
	return err
}

func (e extensionSink) typing(ctx context.Context, on bool) {
	if v, ok := e.s.pluginChannel(ctx, e.id); ok && v.Capabilities.Typing {
		_ = e.s.appCall(ctx, AppChannels, ChannelsTyping, map[string]any{"channel": e.id, "on": on}, nil)
	}
}

// --- a message from the owner ---

// channelReceive takes one of the owner's messages: a command, the answer to what was asked, or a message for the
// conversation the channel talks to.
// action is the owner's action the hub handed over with it: what it answers, it answers with that.
func (s *Service) channelReceive(id string, m channel.Message, action string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	text := strings.TrimSpace(m.Text)
	if strings.HasPrefix(text, "/") && len(m.Images) == 0 && len(m.Files) == 0 {
		handled, rest := s.channelCommand(ctx, id, m, text)
		if handled {
			return
		}
		// What a command did not take ("/new help me…") goes on without its name: answering a pending ask must not
		// read "/new" as part of the answer. An unknown command returns the text unchanged, so non-command paths
		// are identical.
		text = strings.TrimSpace(rest)
		m.Text = text
	}
	if s.channelAnswerAsk(ctx, id, m, text, action) {
		return
	}
	if err := s.channelForward(ctx, id, m); err != nil {
		s.logf("channels: %s: send: %v", id, err)
		_ = s.channelTell(ctx, id, m.ID, i18n.T(s.botLang(ctx), "assistant-channels_plugin.not_sent", i18n.Args{"error": clip(oneLine(err.Error()), 200)}), channel.OutNote)
	}
}

// channelCommand runs one of the commands every channel has; false (and the text to send on) for one it does not
// know: it may be the conversation's own (/compact).
func (s *Service) channelCommand(ctx context.Context, id string, m channel.Message, text string) (bool, string) {
	name, rest, _ := strings.Cut(text, " ")
	rest = strings.TrimSpace(rest)
	l := s.botLang(ctx)
	reply := func(t string) { _ = s.channelTell(ctx, id, m.ID, t, channel.OutNote) }
	setTarget := func(t channel.Target) {
		c := &s.chans
		c.mu.Lock()
		s.loadChannelsLocked(ctx)
		s.channelPrefLocked(id).Target = &t
		s.saveChannelsLocked()
		c.mu.Unlock()
	}
	switch strings.ToLower(name) {
	case "/help", "/帮助":
		reply(i18n.T(l, "assistant-channels_plugin.help"))
	case "/main", "/主对话":
		setTarget(channel.Target{Kind: channel.TargetMain})
		reply(i18n.T(l, "assistant-channels_plugin.now_to_main"))
	case "/new", "/新对话":
		setTarget(channel.Target{Kind: channel.TargetNew})
		if rest == "" {
			reply(i18n.T(l, "assistant-channels_plugin.next_is_new"))
			return true, ""
		}
		return false, rest // "/new help me…": the rest starts it
	case "/stop", "/停":
		rt, err := s.channelTarget(ctx, id)
		switch {
		case err != nil:
			reply(i18n.T(l, "assistant-channels_plugin.not_stopped", i18n.Args{"error": err.Error()}))
		case rt.isNew:
			reply(i18n.T(l, "assistant-channels_plugin.nothing_running"))
		default:
			if err := s.call(ctx, rt.machine, "session.interrupt", map[string]any{"session_id": rt.session.SessionID}, nil); err != nil {
				reply(i18n.T(l, "assistant-channels_plugin.not_stopped", i18n.Args{"error": clip(oneLine(err.Error()), 200)}))
				return true, ""
			}
			c := &s.chans
			c.mu.Lock()
			c.st.Runs = slices.DeleteFunc(c.st.Runs, func(r *channel.Run) bool { return r.Channel == id && r.SessionID == rt.session.SessionID })
			s.saveChannelStateLocked()
			c.mu.Unlock()
			s.channelsChanged()
			extensionSink{s: s, id: id}.typing(ctx, false)
			reply(i18n.T(l, "assistant-channels_plugin.stopped"))
		}
	case "/status", "/状态":
		reply(s.channelStatusText(ctx, id))
	default:
		return false, text
	}
	return true, ""
}

func (s *Service) channelStatusText(ctx context.Context, id string) string {
	l := s.botLang(ctx)
	where := i18n.T(l, "assistant-channels_plugin.main_chat")
	if rt, err := s.channelTarget(ctx, id); err == nil {
		switch {
		case rt.isNew && !rt.main:
			where = i18n.T(l, "assistant-channels_plugin.next_new_chat")
		case !rt.main:
			where = rt.label
		}
	}
	w := s.channelWaiting(id)
	out := i18n.T(l, "assistant-channels_plugin.status_to", i18n.Args{"target": where})
	if w.Answers > 0 {
		out += i18n.T(l, "assistant-channels_plugin.status_answers", i18n.Args{"count": w.Answers})
	}
	if w.Asks > 0 {
		out += i18n.T(l, "assistant-channels_plugin.status_asks", i18n.Args{"count": w.Asks})
	}
	return out
}

// channelTarget is where channel id's messages go now.
func (s *Service) channelTarget(ctx context.Context, id string) (resolvedTarget, error) {
	c := &s.chans
	c.mu.Lock()
	s.loadChannelsLocked(ctx)
	t := channel.Target{Kind: channel.TargetMain}
	if p := s.channelPrefLocked(id); p.Target != nil {
		t = *p.Target
	}
	c.mu.Unlock()
	switch t.Kind {
	case channel.TargetNew:
		return s.resolveBotTarget(ctx, botTarget{Kind: targetPersona})
	case channel.TargetConversation:
		d, ok := s.device(ctx, t.MachineID)
		if !ok {
			return resolvedTarget{}, errors.New(i18n.T(s.botLang(ctx), "assistant-channels_plugin.machine_gone"))
		}
		sess, ok := s.session(ctx, d.ID, t.SessionID)
		if !ok {
			return resolvedTarget{}, errors.New(i18n.T(s.botLang(ctx), "assistant-channels_plugin.chat_gone"))
		}
		sess.SessionID, sess.MachineID = t.SessionID, d.ID
		main := s.settings(ctx).Main
		return resolvedTarget{machine: d.ID, machineName: d.Name, session: sess, label: conversationName(sess, main), main: t.SessionID == main}, nil
	}
	return s.resolveBotTarget(ctx, botTarget{Kind: targetMain})
}

// channelForward sends the owner's message to the conversation the channel talks to (starting it when there is none
// yet) and follows its turn.
func (s *Service) channelForward(ctx context.Context, id string, m channel.Message) error {
	c := &s.chans
	c.mu.Lock()
	generation := c.generation[id]
	c.mu.Unlock()
	v, ok := s.pluginChannel(ctx, id)
	if !ok {
		return fmt.Errorf("no channel %s", id)
	}
	caps := v.Capabilities.Normalized()
	rt, err := s.channelTarget(ctx, id)
	if err != nil {
		return err
	}
	in := prompts.ChannelMessage{Persona: true, Name: v.Name, Owner: s.owner(ctx), Plain: caps.Format == channel.FormatPlain,
		Asks: caps.Asks != channel.AsksNone, Text: strings.TrimSpace(m.Text), Quote: m.Quote, Forward: m.Forward,
		Unsupported: slices.Clone(m.Unsupported)}
	var images []proto.Image
	for _, img := range m.Images {
		if len(images) < channel.ImagesMost && len(img.Data) <= channel.ImageMost {
			mime := img.MIME
			if !strings.HasPrefix(mime, "image/") {
				mime = http.DetectContentType(img.Data)
			}
			images = append(images, proto.Image{Mime: mime, Data: base64.StdEncoding.EncodeToString(img.Data)})
			continue
		}
		in.Unsupported = append(in.Unsupported, "更多的图片")
	}
	for _, f := range m.Files {
		if !caps.FilesIn || len(f.Data) > channel.FileMost || rt.machine == "" {
			in.Unsupported = append(in.Unsupported, "文件「"+firstNonEmpty(f.Name, "未命名")+"」")
			continue
		}
		path, err := s.uploadBytes(ctx, rt.machine, firstNonEmpty(f.Name, "file"), f.Data)
		if err != nil {
			return fmt.Errorf("%s%w", i18n.T(s.botLang(ctx), "assistant-channels_plugin.save_file", i18n.Args{"machine": rt.machineName}), err)
		}
		in.Files = append(in.Files, path)
	}
	in.Unsupported = slices.Compact(in.Unsupported)
	if m.Empty() && len(images) == 0 {
		return nil
	}
	text := prompts.RenderChannelMessage(in)
	run := &channel.Run{ID: id + "_" + m.ID, Channel: id, ReplyTo: m.ID, TargetKind: channel.TargetConversation, Label: rt.label,
		Started: time.Now().UTC(), Probe: channel.Probe(in.Text, append(slices.Clone(in.Files), in.Forward, in.Quote)...)}
	if rt.isNew {
		sess, err := s.Start(ctx, StartParams{Text: text, Images: images, Main: rt.main, Origin: OriginChannel, OriginRef: id})
		if err != nil {
			return fmt.Errorf("%s%w", i18n.T(s.botLang(ctx), "assistant-channels_plugin.not_started"), err)
		}
		if !rt.main { // /new: the chat goes on in it
			c := &s.chans
			c.mu.Lock()
			s.loadChannelsLocked(ctx)
			s.channelPrefLocked(id).Target = &channel.Target{Kind: channel.TargetConversation, MachineID: sess.MachineID, SessionID: sess.SessionID}
			s.saveChannelsLocked()
			c.mu.Unlock()
		}
		run.MachineID, run.SessionID, run.Probe = sess.MachineID, sess.SessionID, ""
	} else {
		sid := rt.session.SessionID
		run.MachineID, run.SessionID = rt.machine, sid
		run.Base = s.lastSeq(ctx, rt.machine, sid)
		cid := channel.ClientMsgID(id, m.ID)
		run.MsgIDs = []string{cid}
		p := map[string]any{"session_id": sid, "text": text, "client_msg_id": cid}
		if len(images) > 0 {
			p["images"] = images
		}
		s.UserSpoke(ctx, SessionRef{MachineID: rt.machine, SessionID: sid}) // unarchived, and its title the user's to keep
		params, err := s.SendParams(ctx, SendParams{MachineID: rt.machine, SessionID: sid, Params: p})
		if err != nil {
			return err
		}
		if err := s.sendOnce(ctx, rt.machine, params); err != nil {
			return err
		}
	}
	s.startChannelRunForBinding(run, &generation)
	return nil
}

// sendOnce sends a message to a conversation under the hub's once-only guard (session.send_once): a message id the
// chat app delivers again is not sent twice.
func (s *Service) sendOnce(ctx context.Context, machine string, params map[string]any) error {
	return s.call(ctx, machine, DeviceSessionSendOnce, params, nil)
}

// lastSeq is the conversation's last line now (0 when it has none yet).
func (s *Service) lastSeq(ctx context.Context, machine, sid string) int {
	var res struct {
		ToSeq int `json:"to_seq"`
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := s.call(cctx, machine, DeviceSessionItems, map[string]any{"session_id": sid, "limit": 1, "lazy_images": true}, &res); err != nil {
		return 0
	}
	return res.ToSeq
}

// uploadBytes saves a file on a machine (its temp directory, like the app's uploads) and returns the path.
func (s *Service) uploadBytes(ctx context.Context, machine, name string, data []byte) (string, error) {
	return s.uploadBytesInto(ctx, machine, name, data, "")
}

// uploadBytesInto is uploadBytes into the node's dest ("": its temp directory; "terminal": uploads/terminal/<date>/,
// swept after 7 days, node/upload_sweep.go).
func (s *Service) uploadBytesInto(ctx context.Context, machine, name string, data []byte, dest string) (string, error) {
	var begin struct {
		UploadID string `json:"upload_id"`
	}
	params := map[string]any{"name": name, "size": len(data)}
	if dest != "" {
		params["dest"] = dest
	}
	if err := s.call(ctx, machine, "file.upload.begin", params, &begin); err != nil {
		return "", err
	}
	for off := 0; off < len(data); off += proto.UploadChunkBytes {
		end := min(off+proto.UploadChunkBytes, len(data))
		if err := s.call(ctx, machine, "file.upload.chunk", map[string]any{"upload_id": begin.UploadID, "offset": off, "data": data[off:end]}, nil); err != nil {
			_ = s.call(context.WithoutCancel(ctx), machine, "file.upload.abort", map[string]any{"upload_id": begin.UploadID}, nil)
			return "", err
		}
	}
	var out proto.UploadedFile
	if err := s.call(ctx, machine, "file.upload.end", map[string]any{"upload_id": begin.UploadID}, &out); err != nil {
		return "", err
	}
	return out.Path, nil
}

// --- what is asked, in text (asks: text) ---

// textChannels are the plugin channels that are connected, with their settings: what asks, pushes and the mirror go
// to.
func (s *Service) textChannels(ctx context.Context) []channel.View {
	list, err := s.channelsNow(ctx)
	if err != nil {
		return nil
	}
	var out []channel.View
	for _, v := range list {
		if v.Source == channel.SourcePlugin && v.Kind == channel.KindIM && v.State == channel.StateConnected {
			out = append(out, s.filledChannel(ctx, v))
		}
	}
	return out
}

func featureOn(v channel.View, id string) bool {
	for _, f := range v.Features {
		if f.ID == id {
			return f.On
		}
	}
	return false
}

// channelDrives: channel id drives this conversation — its target, or a run of it is on it.
func (s *Service) channelDrives(ctx context.Context, id, machine, session string) bool {
	for _, r := range s.channelRuns(id) {
		if r.MachineID == machine && r.SessionID == session {
			return true
		}
		// The hub's own approvals (mail_send) do not name the conversation: one on the assistant's machine while a
		// run goes on is most likely that run's.
		if session == "" && r.MachineID == machine {
			return true
		}
	}
	if session == "" {
		return false
	}
	rt, err := s.channelTarget(ctx, id)
	return err == nil && !rt.isNew && rt.machine == machine && rt.session.SessionID == session
}

// channelInteraction: a new card (interaction.announced): sent as text to the plugin channels that drive its
// conversation (or take every agent's).
func (s *Service) channelInteraction(ix proto.Interaction) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var to []string
	for _, v := range s.textChannels(ctx) {
		if v.Capabilities.Asks != channel.AsksText || !featureOn(v, channel.FeatureAsks) {
			continue
		}
		if ix.Private {
			continue // a Just me turn's card: never in a channel (meeting_justme.go)
		}
		// "All questions" does not take a health conversation's: only one the channel drives itself (health.go).
		if !s.channelDrives(ctx, v.ID, ix.MachineID, ix.SessionID) && (!featureOn(v, channel.FeatureAllAsks) || s.ixInHealth(ctx, ix)) {
			continue
		}
		to = append(to, v.ID)
	}
	eachChannel(context.Background(), to, func(ctx context.Context, id string) { s.askInChannel(ctx, id, ix) })
}

// channelSendTimeout is how long one channel has for what is sent to it, the hub's retries included
// (extmgr/channels_extension.go).
const channelSendTimeout = 30 * time.Second

// eachChannel calls send for every channel in ids at once, each with its own channelSendTimeout from parent, and
// returns when all are done: a channel program that hangs uses up only its own time, never the next channel's.
func eachChannel(parent context.Context, ids []string, send func(ctx context.Context, id string)) {
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(parent, channelSendTimeout)
			defer cancel()
			send(ctx, id)
		})
	}
	wg.Wait()
}

func (s *Service) askInChannel(ctx context.Context, id string, ix proto.Interaction) {
	c := &s.chans
	c.mu.Lock()
	s.loadChannelStateLocked(ctx)
	known := slices.ContainsFunc(c.st.Asks, func(a *channel.Ask) bool { return a.Channel == id && a.ID == ix.ID })
	generation := c.generation[id]
	now := c.clock()
	c.mu.Unlock()
	if known {
		return
	}
	a := channel.NewAsk(id, ix, now)
	text, answerable := channel.AskText(s.botLang(ctx), ix, a)
	reply := ""
	for _, r := range s.channelRuns(id) {
		if r.MachineID == ix.MachineID && r.SessionID == ix.SessionID {
			reply = r.ReplyTo
		}
	}
	if err := s.channelSend(ctx, id, "ask:"+ix.ID, text, channel.OutAsk, reply); err != nil {
		return // not asked there (nowhere to send to yet): the app has it
	}
	if !answerable {
		return
	}
	c.mu.Lock()
	if _, ended := c.ended[ix.ID]; !ended && c.generation[id] == generation && !slices.ContainsFunc(c.st.Asks, func(x *channel.Ask) bool { return x.Channel == id && x.ID == a.ID }) {
		c.st.Asks = append(c.st.Asks, a)
		s.saveChannelStateLocked()
	}
	c.mu.Unlock()
}

// channelInteractionDone: a card is over (answered anywhere, withdrawn, expired). What was sent as text cannot be
// taken back: it only stops waiting, and a reply is then an ordinary message.
func (s *Service) channelInteractionDone(ix proto.Interaction) { s.channelInteractionEnded(ix, false) }

// channelInteractionEnded is channelInteractionDone; inChat: the card was taken back because the user wrote in the
// conversation, so their next words are no late answer to it.
func (s *Service) channelInteractionEnded(ix proto.Interaction, inChat bool) {
	c := &s.chans
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.stLoaded {
		return
	}
	n := len(c.st.Asks)
	c.st.Asks = slices.DeleteFunc(c.st.Asks, func(a *channel.Ask) bool {
		if a.ID != ix.ID {
			return false
		}
		if !inChat {
			c.noteEndedLocked(a)
		}
		return true
	})
	if len(c.st.Asks) != n {
		s.saveChannelStateLocked()
	}
}

// endedAskFor is how long a reply to a text ask that ended elsewhere is still read as a late answer to it.
const endedAskFor = 10 * time.Minute

type endedAsk struct {
	ask *channel.Ask
	at  time.Time
}

// noteEndedLocked remembers a as its channel's latest ask that ended without being answered there.
func (c *channelsBook) noteEndedLocked(a *channel.Ask) {
	if c.lastEnded == nil {
		c.lastEnded = map[string]endedAsk{}
	}
	c.lastEnded[a.Channel] = endedAsk{ask: a, at: c.clock()}
}

// channelAnsweredElsewhere: nothing waits in channel id, but text reads as a late reply to its latest ask that ended
// elsewhere (#5093): the owner is told so, and the words do not go into the conversation as a new message. Any other
// message forgets that ask: what comes after it is ordinary.
func (s *Service) channelAnsweredElsewhere(ctx context.Context, id string, m channel.Message, text string) bool {
	c := &s.chans
	c.mu.Lock()
	e, ok := c.lastEnded[id]
	delete(c.lastEnded, id)
	fresh := ok && c.clock().Sub(e.at) < endedAskFor
	c.mu.Unlock()
	if !fresh || len(m.Images) > 0 || len(m.Files) > 0 || !channel.ReadsAsAnswer(e.ask, text) {
		return false
	}
	_ = s.channelTell(ctx, id, m.ID, i18n.T(s.botLang(ctx), "assistant-channels_plugin.answered_elsewhere"), channel.OutNote)
	return true
}

// channelAnswerAsk takes the owner's message as the answer to the latest thing asked in the channel; false when
// nothing waits (or what waited was answered elsewhere): the message is then an ordinary one. A question asked
// without waiting (channel.Ask.Later) holds no turn and may be up for hours, so it takes only a reply that reads as
// its answer (an option's number or name); other words answer what was asked before it, else are an ordinary message.
func (s *Service) channelAnswerAsk(ctx context.Context, id string, m channel.Message, text, action string) bool {
	if text == "" {
		return false
	}
	c := &s.chans
	c.mu.Lock()
	s.loadChannelStateLocked(ctx)
	waits := slices.ContainsFunc(c.st.Asks, func(a *channel.Ask) bool { return a.Channel == id })
	c.mu.Unlock()
	if !waits {
		return s.channelAnsweredElsewhere(ctx, id, m, text)
	}
	pending := s.pendingIDs(ctx) // before s.chans.mu: a call to the hub is never made under it
	l := s.botLang(ctx)
	if pending == nil {
		return false // the hub could not be asked: an ordinary message
	}
	c.mu.Lock()
	n := len(c.st.Asks)
	c.st.Asks = slices.DeleteFunc(c.st.Asks, func(a *channel.Ask) bool {
		if pending[a.ID] {
			return false
		}
		c.noteEndedLocked(a) // its interaction.ended was missed
		return true
	})
	var a *channel.Ask
	for _, x := range c.st.Asks {
		if x.Channel == id && (!x.Later || channel.ReadsAsAnswer(x, text)) {
			a = x // the latest one asked that this reply may answer
		}
	}
	if len(c.st.Asks) != n {
		s.saveChannelStateLocked()
	}
	if a == nil {
		c.mu.Unlock()
		return s.channelAnsweredElsewhere(ctx, id, m, text)
	}
	delete(c.lastEnded, id) // this reply answers one still waiting: an ask that ended before it is past
	// A question ask can sit at its final step while it is otherwise still pending: after a crash between ReadAnswer
	// and its resolve, or while the final resolve is still in flight. ReadAnswer only says "answer it in the app"
	// there, so step its last question back first, letting this reply finish it.
	if a.Kind == proto.IxQuestion && a.Step == len(a.Questions) {
		rewindLastAnswer(a)
	}
	r, done, again := channel.ReadAnswer(l, a, text)
	if done && r.Answers != nil {
		// ReadAnswer returns the ask's own Answers map by reference. The resolve runs without the channel lock and
		// marshals this map for the hub call, while another reply could meanwhile heal or rewind the same ask; detach
		// the resolution's copy here under the lock so the two never touch the same map concurrently.
		r.Answers = maps.Clone(r.Answers)
	}
	s.saveChannelStateLocked()
	c.mu.Unlock()
	reply := func(t string) { _ = s.channelTell(ctx, id, m.ID, t, channel.OutNote) }
	switch {
	case again != "":
		reply(again)
		return true
	case !done:
		reply(channel.QuestionText(l, a, a.Step)) // a question answered, the next one asked
		return true
	}
	ok, err := s.resolveInteraction(ctx, id, a.ID, r, action)
	c.mu.Lock()
	if err == nil {
		c.st.Asks = slices.DeleteFunc(c.st.Asks, func(x *channel.Ask) bool { return x.ID == a.ID })
		if e, ok := c.lastEnded[id]; ok && e.ask.ID == a.ID {
			delete(c.lastEnded, id) // answered here: interaction.ended came before the resolve returned
		}
	} else {
		// The answer never reached the hub: keep the ask so the owner can send it again, instead of losing a
		// multi-question ask's answers so far. ReadAnswer took its last question's answer and moved Step on;
		// step back so the re-sent reply answers that same question again.
		rewindLastAnswer(a)
	}
	left := 0
	for _, x := range c.st.Asks {
		// On a failed resolve this ask is still in the list and is the one the note is about: it is not "more".
		if x.Channel == id && x.ID != a.ID {
			left++
		}
	}
	s.saveChannelStateLocked()
	c.mu.Unlock()
	switch {
	case err != nil:
		reply(i18n.T(l, "assistant-channels_plugin.answer_not_sent", i18n.Args{"error": clip(oneLine(err.Error()), 200)}))
	case !ok:
		reply(i18n.T(l, "assistant-channels_plugin.answered_elsewhere"))
	default:
		reply(channel.Answered(l, r))
	}
	if left > 0 {
		reply(i18n.T(l, "assistant-channels_plugin.more_waiting", i18n.Args{"count": left}))
	}
	return true
}

// resolveInteraction answers a card as the user did in channel ch (interactions.resolve), with the owner's action the
// hub handed over with that message or press there (the hub refuses one without it, or one of another channel):
// false when it is no longer pending.
func (s *Service) resolveInteraction(ctx context.Context, ch, id string, r proto.Resolution, action string) (bool, error) {
	var res struct {
		OK bool `json:"ok"`
	}
	err := s.appCall(ctx, AppInteractions, InteractionsResolve, map[string]any{"interaction_id": id, "resolution": r,
		"action": action, "channel": ch}, &res)
	return res.OK, err
}

// rewindLastAnswer undoes the answer ReadAnswer just took on the last question of a question ask, so a reply re-sent
// after a failed resolve answers that question again (and the answers given before it are kept). Other ask kinds
// record no per-question progress.
func rewindLastAnswer(a *channel.Ask) {
	if a.Kind != proto.IxQuestion || a.Step == 0 || a.Step > len(a.Questions) {
		return
	}
	delete(a.Answers, a.Questions[a.Step-1].Question)
	a.Step--
}

// --- pushes and the sync mirror ---

// channelPush sends what notify_user pushed to the phone to the plugin channels with pushes on too.
func (s *Service) channelPush(machine, session, title, body string) {
	_, _ = s.channelPushTo(context.Background(), machine, session, title, body, "", nil)
}

// channelPushTo is channelPush, each channel's send with its own channelSendTimeout from parent, for a push that may
// be sent again when ref is set: it goes to every channel with ref and the channel's id (ref:id, which that channel
// takes once: a program of several channels tells their copies apart), and to none that took is true of. It returns
// each channel's result, by id; the error is the list's when it could not be read while the hub is away (nothing was
// sent then).
func (s *Service) channelPushTo(parent context.Context, machine, session, title, body, ref string, took func(id string) bool) (map[string]error, error) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	if _, err := s.channelsNow(ctx); err != nil && !answered(err) {
		return nil, err
	}
	var to []string
	for _, v := range s.textChannels(ctx) {
		if v.Capabilities.Push && featureOn(v, channel.FeaturePushes) && (took == nil || !took(v.ID)) {
			to = append(to, v.ID)
		}
	}
	text := strings.TrimSpace("🔔 " + title + "\n\n" + body)
	var mu sync.Mutex
	out := map[string]error{}
	eachChannel(parent, to, func(ctx context.Context, id string) {
		var err error
		if ref == "" {
			err = s.channelTell(ctx, id, "", text, channel.OutPush)
		} else if err = s.channelSend(ctx, id, ref+":"+id, text, channel.OutPush, ""); err != nil {
			s.logf("channels: %s: send: %v", id, err)
		}
		if err == nil {
			s.markPushed(id, machine, session)
		}
		mu.Lock()
		out[id] = err
		mu.Unlock()
	})
	return out, nil
}

// extensionMirror sends a turn that ended without being asked for in a channel: the main conversation's to those
// with sync on, a scheduled task's own conversation's to those with pushes on.
func (s *Service) extensionMirror(ctx context.Context, machine, session string) {
	parent := ctx // what each channel's send gets its own time from (eachChannel)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var to []channel.View
	for _, v := range s.textChannels(ctx) {
		if v.Capabilities.Push && (featureOn(v, channel.FeatureSync) || featureOn(v, channel.FeaturePushes)) {
			to = append(to, v)
		}
	}
	if len(to) == 0 {
		return
	}
	main := s.isMainOn(ctx, machine, session)
	task := s.taskOfConversation(ctx, machine, session)
	if !main && task == "" {
		return
	}
	if sess, ok := s.session(ctx, machine, session); !ok || sess.WokeByTask && !main || spaceOf(sess) == SpaceHealth {
		return // a health task's runs stay off the channels (health.go)
	}
	asked, answer, ok := s.channelLastTurn(ctx, machine, session)
	if !ok {
		return
	}
	var ids []string
	for _, v := range to {
		switch {
		case featureOn(v, channel.FeaturePushes) && s.pushedLately(v.ID, machine, session):
			continue // its notify_user copy already reached this channel
		case strings.Contains(asked, `title="`+v.Name+`机器人"`):
			continue // its own message: answered by the run
		case main && featureOn(v, channel.FeatureSync):
		case task != "" && featureOn(v, channel.FeaturePushes):
		default:
			continue
		}
		ids = append(ids, v.ID)
	}
	if len(ids) == 0 {
		return
	}
	l := s.botLang(ctx)
	head := i18n.T(l, "assistant-channels_plugin.main_chat")
	if !main {
		head = i18n.T(l, "assistant-channels_plugin.scheduled_task", i18n.Args{"task": task})
	} else if asked != "" {
		head += i18n.T(l, "assistant-channels_plugin.asked_elsewhere", i18n.Args{"asked": clip(oneLine(stripContext(asked)), 80)})
	}
	eachChannel(parent, ids, func(ctx context.Context, id string) {
		_ = s.channelTell(ctx, id, "", head+"\n\n"+answer, channel.OutPush)
	})
}

// channelLastTurn is the conversation's last turn: what was asked and its answer (false when it has no answer).
func (s *Service) channelLastTurn(ctx context.Context, machine, session string) (string, string, bool) {
	asked, answer, _, ok := s.channelLastTurnOf(ctx, machine, session)
	return asked, answer, ok
}

// channelLastTurnOf is channelLastTurn with what tells this turn from another with the same words: its last item's ID
// (else its time), so two runs that asked and answered alike are still two turns.
func (s *Service) channelLastTurnOf(ctx context.Context, machine, session string) (string, string, string, bool) {
	var res struct {
		Items []proto.Item `json:"items"`
	}
	if err := s.call(ctx, machine, DeviceSessionItems, map[string]any{"session_id": session, "turns": 1, "lazy_images": true}, &res); err != nil {
		return "", "", "", false
	}
	asked := ""
	for _, it := range res.Items {
		if it.Role == "user" && it.Kind == proto.KindText && asked == "" {
			asked = strings.TrimSpace(it.Text)
		}
	}
	var texts []string
	for _, p := range channel.Answer(res.Items) {
		texts = append(texts, p.Text)
	}
	answer := strings.TrimSpace(strings.Join(texts, "\n\n"))
	turn := ""
	if n := len(res.Items); n > 0 {
		if turn = res.Items[n-1].ID; turn == "" {
			turn = res.Items[n-1].TS.UTC().Format(time.RFC3339Nano)
		}
	}
	return asked, answer, turn, answer != ""
}

// isMainOn: session on machine is the main conversation.
func (s *Service) isMainOn(ctx context.Context, machine, session string) bool {
	cfg := s.settings(ctx)
	return cfg.Main != "" && cfg.Main == session && cfg.MachineID == machine
}

// taskOfConversation is the name of the scheduled task whose own conversation this is, or "".
func (s *Service) taskOfConversation(ctx context.Context, machine, session string) string {
	for _, t := range s.taskViews(ctx) {
		if t.RunSessionID == session && t.RunMachineID == machine {
			return t.Label
		}
	}
	return ""
}

// stripContext leaves out the context blocks of a message (what the user wrote is after them); a block left open
// ends it.
func stripContext(s string) string {
	s = contextBlockRe.ReplaceAllString(s, "")
	if loc := contextOpenRe.FindStringIndex(s); loc != nil {
		s = s[:loc[0]]
	}
	return strings.TrimSpace(s)
}

// contextBlockRe and contextOpenRe are a <persona-context> block.
var (
	contextBlockRe = lazyre.New(`(?s)<` + compat.ContextPattern() + `\b[^>]*>.*?</` + compat.ContextPattern() + `>`)
	contextOpenRe  = lazyre.New(`<` + compat.ContextPattern() + `\b`)
)
