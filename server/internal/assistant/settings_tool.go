package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/kb"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// persona_settings: Persona changes its own settings for the user (docs/assistant/assistant.md "Current structure", the
// "Control Persona itself" row). The assistant's own ("About me", its launcher, "notify tasks the main chat starts only
// in the main chat") are its to read and change; the hub's (the inbox's rules, the blocked Claude config directories,
// mail accounts…) are the hub's settings app's, which asks the user itself (app.tool persona_settings). Reads are free;
// every change is a card on the phone that says what changes (before → after) and is applied only when approved.

// ownSetting is one of the assistant's own settings persona_settings offers.
type ownSetting struct {
	Setting
	get func(ctx context.Context) (string, error)
	set func(ctx context.Context, v json.RawMessage) (*ownChange, error)
}

// ownChange is a change waiting for the phone: what the card says and how it is made (none: nothing to change).
type ownChange struct {
	summary  string                   // for the assistant: the change made nothing to do
	cardText func(l i18n.Lang) string // what the card says, in the user's language
	apply    func(ctx context.Context) (string, error)
}

// card is what the approval card says in l.
func (c *ownChange) card(l i18n.Lang) string {
	if c.cardText != nil {
		return c.cardText(l)
	}
	return c.summary
}

func (s *Service) ownSettings() []ownSetting {
	getSet := []string{"get", "set"}
	return []ownSetting{
		{Setting: Setting{Key: standingAuthorizationsKey, Title: "Standing authorizations", Actions: getSet,
			About: "user-confirmed actions for one computer and absolute directory tree; only the main conversation may set them, and every change requires the user’s own permission approval",
			Value: "{revision, authorizations: [{id?, machine_id, directory, actions, conditions, quote, source, reversible}]}; get first; retain existing entries unchanged or remove them; omit id for new grants; granted_at is set by the server"},
			get: s.getStandingAuthorizations, set: s.setStandingAuthorizations},
		{Setting: Setting{Key: "assistant.notes", Title: "关于我", Actions: getSet,
			About: "what Persona knows about the user (设置 › 助手 › 关于我), Markdown appended to the assistant's instructions; new conversations read it",
			Value: "a string (Markdown; the whole text)"},
			get: func(ctx context.Context) (string, error) { return orNone(s.notes(ctx)), nil },
			set: s.setNotes},
		{Setting: Setting{Key: "assistant.main_tasks_quiet", Title: "主对话派的任务只在主对话里通知", Actions: getSet,
			About: "on (the default): tasks the main conversation opens (task_plan, agent_start) send no pushes, stay off the Live Activity and " +
				"task badges, and the main conversation is told when one is done, waits for the user or fails instead; off: they notify like any agent. " +
				"Applies to tasks opened from now on",
			Value: "true or false"},
			get: func(ctx context.Context) (string, error) { return fmt.Sprint(s.mainTasksQuiet(ctx)), nil },
			set: s.setMainTasksQuiet},
		{Setting: Setting{Key: "assistant.start_requested_tasks", Title: "我在对话里说过的，直接开始", Actions: getSet,
			About: "on (the default): task_plan tasks marked user_requested (the user asked for that very task in this turn) start at once " +
				"instead of waiting for 开始 on the plan card; off: only read-only tasks and remembered kinds start without asking",
			Value: "true or false"},
			get: func(ctx context.Context) (string, error) { return fmt.Sprint(s.startRequestedTasks(ctx)), nil },
			set: s.setStartRequestedTasks},
		{Setting: Setting{Key: "assistant.task_workspace_preference", Title: "Task workspace preference", Actions: getSet,
			About: "prefer-existing (default): reuse the task directory if free; prefer-new: isolate writable Git tasks. Read-only defaults to none, busy directories to new; explicit task workspace wins. Paths come from the machine engine",
			Value: "prefer-existing or prefer-new"},
			get: func(ctx context.Context) (string, error) { return s.taskWorkspacePreference(ctx), nil },
			set: s.setTaskWorkspacePreference},
		{Setting: Setting{Key: "assistant.recording_days", Title: "录音在知识库里保留", Actions: getSet,
			About: "how many days the phone's and watch's recording transcripts stay in the knowledge base (kind recordings); older " +
				"transcripts are deleted from it, and the phone deletes its local recordings too (audio and text). " +
				"Meeting minutes follow assistant.minutes_days instead. 14 by default",
			Value: "a whole number of days, 1–365"},
			get: func(ctx context.Context) (string, error) { return fmt.Sprint(s.recordingDays(ctx)), nil },
			set: s.setRecordingDays},
		{Setting: Setting{Key: "assistant.minutes_days", Title: "Keep meeting minutes", Actions: getSet,
			About: "how many days a meeting recording's minutes stay in the knowledge base. Past assistant.recording_days its " +
				"transcript, summary and live log are deleted and only the minutes (and the recording's metadata) stay, still " +
				"searchable and readable with kb_get, until this many days after the meeting; then the rest goes too. Never " +
				"fewer than assistant.recording_days for finite windows. 0 (or forever) keeps minutes until the user deletes them, " +
				"which is the default",
			Value: "0 or forever, or a whole number of days, 1–3650"},
			get: func(ctx context.Context) (string, error) {
				return minutesDaysLabel(s.lang(ctx), s.minutesDays(ctx)), nil
			},
			set: s.setMinutesDays},
		{Setting: Setting{Key: "assistant.kb_topics", Title: "Organize knowledge points by topic", Actions: getSet,
			About: "on (the default): the knowledge base pulls durable knowledge (concepts, decisions, practices, pitfalls, facts, open " +
				"questions) out of the projects' timelines and the chats' summaries and files it by topic (kind topics), a few model calls " +
				"a round on the knowledge base's model; off: no more calls, the topics already written stay",
			Value: "true or false"},
			get: func(ctx context.Context) (string, error) { return fmt.Sprint(s.kbTopics(ctx)), nil },
			set: s.setKBTopics},
		{Setting: Setting{Key: "assistant.main_background_folded", Title: "Fold background rounds", Actions: getSet,
			About: "on (the default): in the apps, a main-conversation round the user did not start (another conversation's " +
				"conversation_send / conversation_new message, a scheduled task or follow-up) is one line that opens on request, and " +
				"a scheduled task's prompt is not shown; rounds that asked or told the user something (ask_user, notify_user, a " +
				"task_review verdict card) stay shown. Off: every round is shown in full. Display only: what runs is unchanged",
			Value: "true or false"},
			get: func(ctx context.Context) (string, error) { return fmt.Sprint(s.mainBackgroundFolded(ctx)), nil },
			set: s.setMainBackgroundFolded},
		{Setting: Setting{Key: "assistant.meeting_attendance_default", Title: "Persona in recorded meetings", Actions: getSet,
			About: "the role a meeting recording on the phone pre-selects (the user can still change it when it starts): off (the " +
				"default) only records and makes minutes afterwards; listener follows the meeting in a session and answers questions " +
				"on the phone's screen; participant also says the answers aloud. Applies to recordings started from now on",
			Value: "off, listener or participant"},
			get: func(ctx context.Context) (string, error) { return s.meetingAttendanceDefault(ctx), nil },
			set: s.setMeetingAttendanceDefault},
		{Setting: Setting{Key: "assistant.done_needs_delivery", Title: "上线并实测才算做完", Actions: getSet,
			About: "off (the default): a task_plan task closes when its work is judged done; on: only once it is live and checked " +
				"(or has nothing to ship): stopped at a PR, merged or off by default it stays open with where it is held, its agent is told " +
				"so with its prompt, and the ones waiting for the user's nod are listed together in the snapshot. Applies from the next judgment / task",
			Value: "true or false"},
			get: func(ctx context.Context) (string, error) { return fmt.Sprint(s.doneNeedsDelivery(ctx)), nil },
			set: s.setDoneNeedsDelivery},
		{Setting: Setting{Key: "assistant.review_sample_percent", Title: "Share of finished tasks to spot-check", Actions: getSet,
			About: "100 (the default): every finished supervised task waits for your task_review. Below 100 the done judge also checks each " +
				"done_when criterion; a task whose criteria are all checkable and met and whose work is live is closed by the judge, and " +
				"only this share of those still comes to you to spot-check (tasks that fail the check wait for task_review as before). " +
				"Applies from the next judgment on",
			Value: "a whole number, 0–100"},
			get: func(ctx context.Context) (string, error) { return fmt.Sprint(s.reviewSamplePercent(ctx)), nil },
			set: s.setReviewSamplePercent},
		{Setting: Setting{Key: "assistant.leaders", Title: "Leader conversations", Actions: getSet,
			About: "empty (the default): only the main conversation supervises the tasks it hands out. A leader is a side conversation " +
				"(one repository or long-running project each) that supervises the tasks it hands out with task_plan itself: their " +
				"supervision messages go to it and it reviews them with task_review; needs_user and failed starts still come to the " +
				"main conversation. Applies to tasks handed out from now on",
			Value: "the whole list of leader conversation ids (conversations_list), [] for none"},
			get: s.getLeaders,
			set: s.setLeaders},
		{Setting: Setting{Key: "assistant.interject_background", Title: "Move running work to the background when I interject", Actions: getSet,
			About: "off (the default): a message the user sends while a Claude conversation is still answering waits until the command " +
				"or subagent running in the foreground ends; on: that work moves to the background (it goes on and reports when done) " +
				"and the message is answered at once. " +
				"Either way, a message the user sends into a round they did not start (supervision, a scheduled task or follow-up, another " +
				"conversation's message, a task's notice) moves that round's work to the background. " +
				"Needs Claude Code 2.1.139 or later on the conversation's computer; applies from the next message",
			Value: "true or false"},
			get: func(ctx context.Context) (string, error) { return fmt.Sprint(s.interjectBackground(ctx)), nil },
			set: s.setInterjectBackground},
		{Setting: Setting{Key: "assistant.agent_tasks", Title: "Every Agent Persona starts is a supervised task", Actions: getSet,
			About: "off (the default): only tasks the main conversation or a leader hands out are supervised; an agent started from an " +
				"app or by agent_start from another conversation is on no task. on: every agent Persona starts gets a quiet to-do (no " +
				"pushes or badges for it) supervised by the conversation that started it when that is the main conversation or a leader, " +
				"else by the main conversation: an app's agent once a turn of it did work (edits, commands), a to-do the user made when " +
				"an agent is linked to it, and report_to_supervisor from an agent on any open to-do reaches its supervisor. Agents " +
				"started by hand in a terminal are left alone. Costs model calls (the done judge, supervision turns). Applies from the " +
				"next start, turn end and report",
			Value: "true or false"},
			get: func(ctx context.Context) (string, error) { return fmt.Sprint(s.agentTasks(ctx)), nil },
			set: s.setAgentTasks},
		{Setting: Setting{Key: "assistant.inbox_signals", Title: "Tell a task's supervisor about new Inbox messages on it", Actions: getSet,
			About: "off (the default): an Inbox message the AI twin's judge links to a task only reaches the twin's handling chat " +
				"(and a message card once it drafts a reply). on: new messages from other people linked to a supervised task also " +
				"reach the task's supervisor (its leader conversation, else the main conversation) as an inbox entry of the next " +
				"supervision message, several collapsed into one per task with their count. Needs the Inbox AI twin, which makes the " +
				"link; Persona's own sends, bot chats and tasks waiting for the user's decision never raise one. Costs supervisor " +
				"turns. Applies from the next new message",
			Value: "true or false"},
			get: func(ctx context.Context) (string, error) { return fmt.Sprint(s.inboxSignalsOn(ctx)), nil },
			set: s.setInboxSignals},
		{Setting: Setting{Key: "assistant.launcher", Title: "Persona 的启动器", Actions: getSet,
			About: "which launcher (model) new Persona conversations start with by default",
			Value: "a launcher name from this setting's get"},
			get: s.getLauncher,
			set: s.setLauncher},
		{Setting: Setting{Key: "model_profiles.tiers", Title: "Agent presets (the model profiles' tiers)", Actions: getSet,
			About: "the Agent presets: each model profile's named tiers, what agent_start / task_plan's tier and the New Agent " +
				"form's presets run on (launcher, model, effort) and when to use each; get lists every profile's, set adds, " +
				"changes or removes one tier, in every profile unless profiles names some (a switch then keeps it)",
			Value: modelTiersValue},
			get: s.getModelTiers,
			set: s.setModelTiers},
	}
}

func (s *Service) toolPersonaSettings(ctx context.Context, call ToolCall, args json.RawMessage) proto.ToolResult {
	var a struct {
		Action string          `json:"action"`
		Key    string          `json:"key"`
		Value  json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return proto.TextResult(fmt.Sprintf("invalid arguments: %v", err), true)
	}
	if a.Key == standingAuthorizationsKey && a.Action == "set" && (call.SessionID == "" || call.SessionID != s.settings(ctx).Main || call.MachineID != s.settings(ctx).MachineID) {
		return proto.TextResult("only the main conversation may change standing authorizations", true)
	}
	own := s.ownSettings()
	if a.Action == "" || a.Action == "list" {
		text, err := s.listSettings(ctx, own)
		if err != nil {
			return proto.TextResult(err.Error(), true)
		}
		return proto.TextResult(text, false)
	}
	for _, o := range own {
		if o.Key == a.Key {
			text, err := s.ownSettingAction(ctx, call, o, a.Action, a.Value)
			if err != nil {
				return proto.TextResult(err.Error(), true)
			}
			return proto.TextResult(text, false)
		}
	}
	return s.appTool(ctx, AppSettings, call, args) // the hub's
}

// listSettings is action list: the assistant's settings and the hub's, by key.
func (s *Service) listSettings(ctx context.Context, own []ownSetting) (string, error) {
	var res struct {
		Settings []Setting `json:"settings"`
	}
	if err := s.appCall(ctx, AppSettings, SettingsList, map[string]any{}, &res); err != nil {
		return "", err
	}
	all := res.Settings
	for _, o := range own {
		all = append(all, o.Setting)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Key < all[j].Key })
	var b strings.Builder
	b.WriteString("Settings you can read and (with the user's approval on the phone) change:\n")
	for _, st := range all {
		fmt.Fprintf(&b, "- %s (%s): %s. Actions: %s.", st.Key, st.Title, st.About, strings.Join(st.Actions, ", "))
		if st.Value != "" {
			fmt.Fprintf(&b, " Value: %s.", st.Value)
		}
		b.WriteByte('\n')
	}
	b.WriteString("Routines and follow-ups have their own tools (schedule_routine, schedule_followup); knowledge base tags are kb_update. " +
		"Not changeable here: tokens, pairing, devices, the machines the hub reaches, permission modes.")
	return b.String(), nil
}

// settingCardWait is how long a change's approval card waits for the user before it expires (and changes nothing).
var settingCardWait = 15 * time.Minute

// ownSettingAction reads one of the assistant's settings, or changes it once the user approves on the phone.
func (s *Service) ownSettingAction(ctx context.Context, call ToolCall, o ownSetting, action string, value json.RawMessage) (string, error) {
	switch action {
	case "get":
		return o.get(ctx)
	case "set":
	default:
		return "", fmt.Errorf("%s does not take %q (it takes %s)", o.Key, action, strings.Join(o.Actions, ", "))
	}
	change, err := o.set(ctx, value)
	if err != nil {
		return "", err
	}
	if change.apply == nil {
		return change.summary, nil // nothing to change
	}
	l := s.lang(ctx)
	title := o.Title
	if k, ok := settingTitles[o.Key]; ok {
		title = i18n.T(l, "assistant-settings_tool."+k)
	}
	shown, _ := json.Marshal(map[string]string{"setting": title, "action": action})
	now := time.Now().UTC()
	r, err := s.ask(ctx, proto.Interaction{ID: "ix_" + randomHex(6), MachineID: call.MachineID, SessionID: call.SessionID,
		Kind: proto.IxPermission, ToolName: "persona_settings", Summary: change.card(l), ToolInput: shown, SessionTitle: "Persona",
		CreatedAt: now, ExpiresAt: now.Add(settingCardWait)})
	if err != nil {
		return "", err
	}
	switch { // only the user's own answer counts: an expired card, or one answered for them, is no decline (#3722)
	case r.UserAllowed():
		return change.apply(ctx)
	case r.UserDenied():
		return "The user declined the change: nothing changed.", nil
	}
	return prompts.RenderSettingUnanswered(), nil
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "（无）"
	}
	return s
}

// settingTitles are the settings' titles for the card the user approves on, in this file's namespace (Title stays
// what the assistant reads).
var settingTitles = map[string]string{standingAuthorizationsKey: "title_standing_authorizations", "assistant.notes": "title_notes", "assistant.main_tasks_quiet": "title_main_tasks_quiet",
	"assistant.task_workspace_preference": "title_task_workspace_preference",
	"assistant.start_requested_tasks":     "title_start_requested_tasks", "assistant.launcher": "title_launcher",
	"assistant.recording_days": "title_recording_days", "assistant.done_needs_delivery": "title_done_needs_delivery",
	"assistant.review_sample_percent": "title_review_sample_percent", "assistant.leaders": "title_leaders",
	"assistant.interject_background": "title_interject_background", "assistant.agent_tasks": "title_agent_tasks", "assistant.inbox_signals": "title_inbox_signals", "model_profiles.tiers": "title_model_tiers",
	"assistant.meeting_attendance_default": "title_meeting_attendance_default", "assistant.minutes_days": "title_minutes_days",
	"assistant.kb_topics": "title_kb_topics", "assistant.main_background_folded": "title_main_background_folded"}

// clipped is a value as the card shows it, in l.
func clipped(l i18n.Lang, s string) string {
	if strings.TrimSpace(s) == "" {
		s = i18n.T(l, "assistant-settings_tool.none")
	}
	return clip(oneLine(s), 120)
}

func (s *Service) setNotes(ctx context.Context, v json.RawMessage) (*ownChange, error) {
	var text string
	if err := json.Unmarshal(v, &text); err != nil {
		return nil, errors.New("value must be a string")
	}
	text = strings.TrimSpace(text)
	before := s.notes(ctx)
	if text == before {
		return &ownChange{summary: "关于我没有变化"}, nil
	}
	if s.store() == nil {
		return nil, errors.New("no database on this hub")
	}
	return &ownChange{cardText: func(l i18n.Lang) string {
		return i18n.T(l, "assistant-settings_tool.change_notes", i18n.Args{"before": clipped(l, before), "after": clipped(l, text)})
	},
		apply: func(ctx context.Context) (string, error) {
			if _, err := s.updateSettings(ctx, func(v *Settings) error { v.Notes = &text; return nil }); err != nil {
				return "", err
			}
			s.settingsChanged(ctx)
			return "关于我 updated; new conversations read it (this one keeps what it started with).", nil
		}}, nil
}

// setBoolSetting changes one of the assistant's on/off settings: v is the new value, current reads it, unchanged is
// the summary when v is what it already is, card the approval card's text (before → after, each "on" or "off" in the
// card's language), field where Settings keeps it, and applied the tool's answer once it is (a format of the value).
func (s *Service) setBoolSetting(ctx context.Context, v json.RawMessage, current func(context.Context) bool, unchanged string,
	card func(l i18n.Lang, before, after string) string, field func(*Settings) **bool, applied string) (*ownChange, error) {
	var on bool
	if err := json.Unmarshal(v, &on); err != nil {
		return nil, errors.New("value must be true or false")
	}
	before := current(ctx)
	if on == before {
		return &ownChange{summary: unchanged}, nil
	}
	if s.store() == nil {
		return nil, errors.New("no database on this hub")
	}
	word := map[bool]string{true: "assistant-settings_tool.on", false: "assistant-settings_tool.off"}
	return &ownChange{cardText: func(l i18n.Lang) string { return card(l, i18n.T(l, word[before]), i18n.T(l, word[on])) },
		apply: func(ctx context.Context) (string, error) {
			if _, err := s.updateSettings(ctx, func(v *Settings) error { *field(v) = &on; return nil }); err != nil {
				return "", err
			}
			s.settingsChanged(ctx)
			return fmt.Sprintf(applied, on), nil
		}}, nil
}

func (s *Service) setMainTasksQuiet(ctx context.Context, v json.RawMessage) (*ownChange, error) {
	return s.setBoolSetting(ctx, v, s.mainTasksQuiet, "「主对话派的任务只在主对话里通知」没有变化", func(l i18n.Lang, before, after string) string {
		return i18n.T(l, "assistant-settings_tool.change_main_tasks_quiet", i18n.Args{"before": before, "after": after})
	}, func(v *Settings) **bool { return &v.MainTasksQuiet },
		"assistant.main_tasks_quiet is %v for tasks opened from now on (existing ones keep theirs).")
}

func (s *Service) setStartRequestedTasks(ctx context.Context, v json.RawMessage) (*ownChange, error) {
	return s.setBoolSetting(ctx, v, s.startRequestedTasks, "「我在对话里说过的，直接开始」没有变化", func(l i18n.Lang, before, after string) string {
		return i18n.T(l, "assistant-settings_tool.change_start_requested_tasks", i18n.Args{"before": before, "after": after})
	}, func(v *Settings) **bool { return &v.StartRequestedTasks },
		"assistant.start_requested_tasks is %v for plans from now on.")
}

func (s *Service) setTaskWorkspacePreference(ctx context.Context, raw json.RawMessage) (*ownChange, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || !validWorkspacePreference(value) {
		return nil, fmt.Errorf("value must be prefer-existing or prefer-new")
	}
	before := s.taskWorkspacePreference(ctx)
	if before == value {
		return &ownChange{summary: "assistant.task_workspace_preference is unchanged"}, nil
	}
	if s.store() == nil {
		return nil, fmt.Errorf("no database on this manager")
	}
	return &ownChange{
		cardText: func(l i18n.Lang) string {
			return i18n.T(l, "assistant-settings_tool.change_task_workspace_preference", i18n.Args{"before": before, "after": value})
		},
		apply: func(ctx context.Context) (string, error) {
			if _, err := s.updateSettings(ctx, func(v *Settings) error { v.TaskWorkspacePreference = value; return nil }); err != nil {
				return "", err
			}
			s.settingsChanged(ctx)
			return "assistant.task_workspace_preference is " + value + " for new task plans", nil
		},
	}, nil
}

func (s *Service) setMainBackgroundFolded(ctx context.Context, v json.RawMessage) (*ownChange, error) {
	return s.setBoolSetting(ctx, v, s.mainBackgroundFolded, "assistant.main_background_folded is unchanged", func(l i18n.Lang, before, after string) string {
		return i18n.T(l, "assistant-settings_tool.change_main_background_folded", i18n.Args{"before": before, "after": after})
	}, func(v *Settings) **bool { return &v.MainBackgroundFolded },
		"assistant.main_background_folded is %v; the apps redraw the main conversation with it.")
}

func (s *Service) setKBTopics(ctx context.Context, v json.RawMessage) (*ownChange, error) {
	return s.setBoolSetting(ctx, v, s.kbTopics, "assistant.kb_topics is unchanged", func(l i18n.Lang, before, after string) string {
		return i18n.T(l, "assistant-settings_tool.change_kb_topics", i18n.Args{"before": before, "after": after})
	}, func(v *Settings) **bool { return &v.KBTopics },
		"assistant.kb_topics is %v from the knowledge base's next round on.")
}

func (s *Service) setRecordingDays(ctx context.Context, v json.RawMessage) (*ownChange, error) {
	var days int
	if err := json.Unmarshal(v, &days); err != nil || days < minRecordingDays || days > maxRecordingDays {
		return nil, errors.New("value must be a whole number of days, 1–365")
	}
	before := s.recordingDays(ctx)
	if days == before {
		return &ownChange{summary: "「录音在知识库里保留」没有变化"}, nil
	}
	if s.store() == nil {
		return nil, errors.New("no database on this hub")
	}
	return &ownChange{cardText: func(l i18n.Lang) string {
		return i18n.T(l, "assistant-settings_tool.change_recording_days", i18n.Args{"before": before, "after": days})
	},
		apply: func(ctx context.Context) (string, error) {
			if _, err := s.updateSettings(ctx, func(v *Settings) error { v.RecordingDays = &days; return nil }); err != nil {
				return "", err
			}
			s.settingsChanged(ctx)
			s.kbWake() // a shorter retention prunes now
			return fmt.Sprintf("assistant.recording_days is %d: recordings older than that leave the knowledge base.", days), nil
		}}, nil
}

// minutesDaysLabel spells out the forever sentinel instead of showing a misleading zero-day retention.
func minutesDaysLabel(l i18n.Lang, days int) string {
	if days == 0 {
		return i18n.T(l, "assistant-settings_tool.minutes_forever")
	}
	return i18n.T(l, "assistant-settings_tool.minutes_days_value", i18n.Args{"days": days})
}

// setMinutesDays compares the saved setting, not its recording_days floor. A finite value below that floor is saved
// for when recording_days becomes shorter. Unset and 0 both mean forever; the card names the effective before/after.
func (s *Service) setMinutesDays(ctx context.Context, v json.RawMessage) (*ownChange, error) {
	var days int
	var word string
	forever := json.Unmarshal(v, &word) == nil && strings.EqualFold(strings.TrimSpace(word), "forever")
	if !forever && (strings.TrimSpace(string(v)) == "null" || json.Unmarshal(v, &days) != nil || days < minMinutesDays || days > maxMinutesDays) {
		return nil, i18n.Err("assistant-profile.bad_minutes_days")
	}
	cur := s.settings(ctx)
	saved, recording := minutesDaysSetting(cur), recordingDaysOf(cur)
	before, after := kb.MinutesDaysFor(saved, recording), kb.MinutesDaysFor(days, recording)
	args := func(l i18n.Lang) i18n.Args {
		return i18n.Args{"before": minutesDaysLabel(l, before), "after": minutesDaysLabel(l, after),
			"saved": minutesDaysLabel(l, days), "previous": minutesDaysLabel(l, saved)}
	}
	if days == saved {
		return &ownChange{summary: i18n.T(s.lang(ctx), "assistant-settings_tool.minutes_unchanged", args(s.lang(ctx)))}, nil
	}
	if s.store() == nil {
		return nil, errors.New("no database on this hub")
	}
	return &ownChange{cardText: func(l i18n.Lang) string {
		if after == before {
			return i18n.T(l, "assistant-settings_tool.change_minutes_days_saved", args(l))
		}
		return i18n.T(l, "assistant-settings_tool.change_minutes_days", args(l))
	},
		apply: func(ctx context.Context) (string, error) {
			if _, err := s.updateSettings(ctx, func(v *Settings) error { v.MinutesDays = &days; return nil }); err != nil {
				return "", err
			}
			s.settingsChanged(ctx)
			s.kbWake() // a shorter retention prunes now
			return i18n.T(s.lang(ctx), "assistant-settings_tool.minutes_applied", args(s.lang(ctx))), nil
		}}, nil
}

// setMeetingAttendanceDefault changes the role meeting recordings pre-select; the card names the roles in the
// user's language.
func (s *Service) setMeetingAttendanceDefault(ctx context.Context, v json.RawMessage) (*ownChange, error) {
	var role string
	if err := json.Unmarshal(v, &role); err != nil || !slices.Contains(meetingRoles, strings.TrimSpace(role)) {
		return nil, errors.New("value must be off, listener or participant")
	}
	role = strings.TrimSpace(role)
	before := s.meetingAttendanceDefault(ctx)
	if role == before {
		return &ownChange{summary: "assistant.meeting_attendance_default is unchanged"}, nil
	}
	if s.store() == nil {
		return nil, errors.New("no database on this hub")
	}
	name := func(l i18n.Lang, role string) string { return i18n.T(l, "assistant-settings_tool.meeting_role_"+role) }
	return &ownChange{cardText: func(l i18n.Lang) string {
		return i18n.T(l, "assistant-settings_tool.change_meeting_attendance_default", i18n.Args{"before": name(l, before), "after": name(l, role)})
	},
		apply: func(ctx context.Context) (string, error) {
			if _, err := s.updateSettings(ctx, func(v *Settings) error { v.MeetingAttendanceDefault = &role; return nil }); err != nil {
				return "", err
			}
			s.settingsChanged(ctx)
			return fmt.Sprintf("assistant.meeting_attendance_default is %s for meeting recordings started from now on.", role), nil
		}}, nil
}

func (s *Service) setDoneNeedsDelivery(ctx context.Context, v json.RawMessage) (*ownChange, error) {
	return s.setBoolSetting(ctx, v, s.doneNeedsDelivery, "「上线并实测才算做完」没有变化", func(l i18n.Lang, before, after string) string {
		return i18n.T(l, "assistant-settings_tool.change_done_needs_delivery", i18n.Args{"before": before, "after": after})
	}, func(v *Settings) **bool { return &v.DoneNeedsDelivery },
		"assistant.done_needs_delivery is %v from the next judgment and task on.")
}

func (s *Service) setReviewSamplePercent(ctx context.Context, v json.RawMessage) (*ownChange, error) {
	var pct int
	if err := json.Unmarshal(v, &pct); err != nil || pct < 0 || pct > 100 {
		return nil, errors.New("value must be a whole number, 0–100")
	}
	before := s.reviewSamplePercent(ctx)
	if pct == before {
		return &ownChange{summary: "assistant.review_sample_percent is unchanged"}, nil
	}
	if s.store() == nil {
		return nil, errors.New("no database on this hub")
	}
	return &ownChange{cardText: func(l i18n.Lang) string {
		return i18n.T(l, "assistant-settings_tool.change_review_sample_percent", i18n.Args{"before": before, "after": pct})
	},
		apply: func(ctx context.Context) (string, error) {
			if _, err := s.updateSettings(ctx, func(v *Settings) error { v.ReviewSamplePercent = &pct; return nil }); err != nil {
				return "", err
			}
			s.settingsChanged(ctx)
			return fmt.Sprintf("assistant.review_sample_percent is %d from the next judgment on.", pct), nil
		}}, nil
}

func (s *Service) setInterjectBackground(ctx context.Context, v json.RawMessage) (*ownChange, error) {
	return s.setBoolSetting(ctx, v, s.interjectBackground, "assistant.interject_background is unchanged", func(l i18n.Lang, before, after string) string {
		return i18n.T(l, "assistant-settings_tool.change_interject_background", i18n.Args{"before": before, "after": after})
	}, func(v *Settings) **bool { return &v.InterjectBackground },
		"assistant.interject_background is %v from the next message on.")
}

func (s *Service) setAgentTasks(ctx context.Context, v json.RawMessage) (*ownChange, error) {
	return s.setBoolSetting(ctx, v, s.agentTasks, "assistant.agent_tasks is unchanged", func(l i18n.Lang, before, after string) string {
		return i18n.T(l, "assistant-settings_tool.change_agent_tasks", i18n.Args{"before": before, "after": after})
	}, func(v *Settings) **bool { return &v.AgentTasks },
		"assistant.agent_tasks is %v from the next start, turn end and report on (to-dos made before keep what they have).")
}

func (s *Service) setInboxSignals(ctx context.Context, v json.RawMessage) (*ownChange, error) {
	return s.setBoolSetting(ctx, v, s.inboxSignalsOn, "assistant.inbox_signals is unchanged", func(l i18n.Lang, before, after string) string {
		return i18n.T(l, "assistant-settings_tool.change_inbox_signals", i18n.Args{"before": before, "after": after})
	}, func(v *Settings) **bool { return &v.InboxSignals },
		"assistant.inbox_signals is %v from the next new Inbox message on (signals already waiting go out only while it is on).")
}

// launchers are the launchers of the assistant's machine, as the apps list them.
func (s *Service) launchers(ctx context.Context) ([]proto.Launcher, error) {
	machine := s.settings(ctx).MachineID
	if _, ok := s.device(ctx, machine); !ok {
		return nil, errors.New("the assistant's machine is not configured")
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ls, err := s.assistantLaunchers(cctx, machine)
	if err != nil {
		return nil, err
	}
	out := make([]proto.Launcher, 0, len(ls))
	for _, l := range ls {
		out = append(out, l.Launcher)
	}
	return out, nil
}

func (s *Service) getLauncher(ctx context.Context) (string, error) {
	now := s.launcherNow(ctx)
	ls, err := s.launchers(ctx)
	if err != nil {
		return "now: " + now + " (the machine's launchers could not be listed: " + err.Error() + ")", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "now: %s\navailable:\n", now)
	for _, l := range ls {
		fmt.Fprintf(&b, "- %s (model %s)\n", l.Name, orNone(l.Model))
	}
	return b.String(), nil
}

// setLauncher writes the launcher into Model configuration › Main chat, inheriting its own default model and effort.
func (s *Service) setLauncher(ctx context.Context, v json.RawMessage) (*ownChange, error) {
	var name string
	if err := json.Unmarshal(v, &name); err != nil || strings.TrimSpace(name) == "" {
		return nil, errors.New("value must be a launcher name")
	}
	name = strings.TrimSpace(name)
	before := s.launcherNow(ctx)
	ls, err := s.launchers(ctx)
	if err != nil {
		return nil, err
	}
	known := false
	for _, l := range ls {
		known = known || l.Name == name
	}
	if !known {
		return nil, fmt.Errorf("no launcher %q on the assistant's machine (get lists them): %w", name, i18n.Err("assistant-models.err_provider_unsupported"))
	}
	if name == before {
		return &ownChange{summary: "启动器没有变化"}, nil
	}
	if s.store() == nil {
		return nil, errors.New("no database on this hub")
	}
	providers := s.modelProviders(ctx)
	p, err := s.modelPurpose(ctx, "assistant", providers)
	if err != nil {
		return nil, err
	}
	provider, _ := json.Marshal("launcher." + name)
	binding, err := patchModel(p.Override, modelPatch{Provider: provider})
	if err != nil {
		return nil, err
	}
	base, _, err := s.modelBaseline(ctx, p)
	if err != nil {
		return nil, err
	}
	if err := s.validateModel(ctx, p, mergeModel(base, binding), providers); err != nil {
		return nil, err
	}
	rec := modelRecord{Revision: p.Revision, Binding: binding}
	return &ownChange{cardText: func(l i18n.Lang) string {
		return i18n.T(l, "assistant-settings_tool.change_launcher", i18n.Args{"before": before, "after": name})
	},
		apply: func(ctx context.Context) (string, error) {
			if err := s.saveModel(ctx, "assistant", rec); err != nil {
				return "", err
			}
			return "New Persona conversations start with " + name + ".", nil
		}}, nil
}
