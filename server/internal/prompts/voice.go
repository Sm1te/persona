package prompts

import "github.com/nwverse/persona/server/internal/lazyre"

// The voice extension's prompts (voice/, docs/assistant/voice.md "Prompts"). Every one comes in the user's language: its Lang is
// an i18n code, en or ja, anything else is Chinese.

// VoiceLine is one final line of a voice conversation, or of the conversation a call is bound to (role tool: the
// tools an agent used there, Text their names).
type VoiceLine struct {
	Role string // user | assistant | tool
	Text string
}

// VoiceInstructions is what the built-in voice plugin's realtime model is told about itself (voice/instructions).
type VoiceInstructions struct {
	Lang  string
	Owner string // "" is the word for "user" in Lang
	Notes string
	// Tools is which of the plugin's function tools (mcp/voice) the session declares: "all"; "ask_persona" (only that
	// one: another extension calling, docs/assistant/voice.md "Permissions"); "bound" (a call bound to a conversation, protocol 2:
	// search_knowledge, list_agents and delegate_task, which hands work to it); "" none, the model delegates.
	Tools string
	// History: what was said before the connection dropped (a reconnected session, a call picked up again), oldest
	// first.
	History []VoiceLine
	// Target is the conversation the call is bound to (protocol 2, voice/target); nil: the Persona main conversation
	// of the moment (protocol 1).
	Target *VoiceTarget
	// Waiting are the tasks handed over whose result has not come back yet (a reconnected session, a call picked up
	// again).
	Waiting []string
	// Paused: the session comes after the upstream was paused while the bound conversation worked (protocol 2,
	// docs/assistant/voice.md 4.9 "Suspension"), not after a drop: History is what was said before the pause.
	Paused bool
}

// VoiceTarget is the conversation a call is bound to, as its session starts (voice/target, docs/assistant/voice.md "Context for the
// fast layer"). Title, Goal, Work, the recent turns, the cards' questions and the agents' titles are other people's words:
// they go inside an <agent_output> block.
type VoiceTarget struct {
	Kind  string // main | assistant | agent
	Label string // what the voice calls it: the agent's name (Claude Code, Codex…); "" for main and assistant
	// Machine is the computer's display name, Folder the last part of its directory.
	Machine, Folder string
	State           string // proto.Session.State: needs_you, working, unread, idle, offline, unknown
	Title           string
	Goal            string
	Work            string // proto.WorkState.Line: what it concluded, or what it waits for
	Recent          []VoiceLine
	Cards           []VoiceCard
	Agents          []VoiceAgent // the agents on the user's computers (only when bound to the main conversation)
}

// VoiceCard is a card waiting on the user in the bound conversation, by its kind: question (Text: its first
// question; none in voice/needs_you), plan, permission (Tool: command, edit, web, mcp, other), screen, elicitation.
type VoiceCard struct {
	Kind, Tool, Text string
}

func RenderVoiceInstructions(d VoiceInstructions) string { return render("voice/instructions", d) }

// VoiceReport is what the voice model is told when a turn handed to the main conversation is over, or stops to wait for
// the user (voice/report). Its first sentence and the <agent_output> block around the answer are the wording measured
// to keep the model from doing what an agent wrote (docs/lessons/voice.md "Verbatim injection-test text").
type VoiceReport struct {
	Lang   string
	To     string   // who did it: an agent's name (Claude Code…); "" the Persona main conversation
	Status string   // done | failed | needs_you
	Tasks  []string // what the user asked for, oldest first (several: messages answered together)
	// Folded: the earlier tasks were handed over on calls of their own that are answered here (voice/folded).
	Folded bool
	// Answer is the turn's answer made speakable (voice/spoken.go), "" when it ended without words; Stopped why it
	// stopped then, when known.
	Answer, Stopped string
	// Background is the background work still running (done): its turn is answered too; Again: this is that answer.
	Background int
	Again      bool
	Reason     string // failed: why there is no answer
	// Failure is what sort of model error the turn ended on, said instead of its text (voice/failure): not_logged_in,
	// auth_failed, model_route, rate_limited (proto.Session.TurnErrorCode), transient (the model service failed for a
	// moment, and nobody went on), retried (it failed again after a "Continue"), error (another). failed: the turn has no
	// answer; done: Answer is what it wrote before that.
	Failure string
	// Observed: the turn was not handed over by voice: the bound conversation began it otherwise (typed on the phone or
	// at the computer, a wake-up, docs/assistant/voice.md "Per-round tracking"); Tasks is then empty.
	Observed bool
}

func RenderVoiceReport(d VoiceReport) string { return render("voice/report", d) }

// VoiceProgress is what the voice model is told while the bound conversation still works on what was handed over
// (voice/progress, docs/assistant/voice.md "Announcements"): a heartbeat after a long quiet (working: the sort of step it is on and how
// long it has worked, nothing it wrote), or once that it stopped to wait on something outside (waiting).
type VoiceProgress struct {
	Lang string
	To   string // an agent's name; "" the Persona main conversation
	Do   string // working | waiting
	// Activity is the sort of step it is on (running tests, reading, editing…), Elapsed how long it has worked on it, in Lang.
	Activity, Elapsed string
	// On is what it waits on (waiting): external (a deploy, CI, a review, a time) or agent (another agent finishing);
	// Wait what it says it waits for and Agent the agent it waits on: the hub's triage of its words, inside
	// <agent_output>.
	On, Wait, Agent string
}

func RenderVoiceProgress(d VoiceProgress) string { return render("voice/progress", d) }

// VoiceObserved is what the voice model is told when a turn of the bound Persona conversation that came from elsewhere
// (a chat app's bot, a scheduled task) is over (voice/observed): one line that there is news there, never its words.
type VoiceObserved struct {
	Lang    string
	To      string // "" the Persona main conversation; Persona for another of its conversations
	Channel string // the channel's name (WeChat, Lark bot…); "" not a channel (a scheduled task, a wake-up)
}

func RenderVoiceObserved(d VoiceObserved) string { return render("voice/observed", d) }

// VoiceNudge is what the voice model is told, once, when it owes the user an answer and has said nothing for a while
// (voice/nudge, docs/assistant/voice.md "Context for the fast layer" rule 9): nothing of what was said or handed to it,
// which it has already.
type VoiceNudge struct {
	Lang string
	// Cause is what it owes an answer to: user (a line the user said or typed) or result (what was just handed to it: a
	// tool's or a turn's result, a refusal, a status).
	Cause string
}

func RenderVoiceNudge(d VoiceNudge) string { return render("voice/nudge", d) }

// VoiceReadAloud is what a realtime session that only reads texts aloud is told (voice/read_aloud, the extension's
// POST /speak: the iPhone's spoken meeting answers outside a call, #5004). Others hear it, so it takes only the owner's
// name, never their notes.
type VoiceReadAloud struct {
	Lang  string
	Owner string // "" is the word for "user" in Lang
	// TextLang is the language the texts are usually in (BCP 47, the hub's word for the answer's), "" when not known:
	// the text's own language still wins.
	TextLang string
}

func RenderVoiceReadAloud(d VoiceReadAloud) string { return render("voice/read_aloud", d) }

// VoiceRefused is what the voice model is told when what it handed over was not sent (voice/refused).
type VoiceRefused struct {
	Lang string
	// Code: unprompted (the user had not spoken since the last agent's words, or not lately: docs/assistant/voice.md "User speech
	// gate"), empty (no task was heard), unknown (the send broke off and may have been typed), unreachable (the hub
	// did not answer before anything was sent), on_screen (only a word that approves or refuses, while a card waits:
	// cards are answered on the screen), dialog_open (a dialog holds the agent's terminal; Plan: it is the plan that
	// waits), input_draft (its input box holds words typed at the computer), failed (Reason says why, when the hub
	// said; any other refusal of the hub's).
	Code   string
	Task   string
	Reason string
	Plan   bool
	// Agent: it was for an agent (the send_to_agent tool), not the Persona main conversation; To names who it was for
	// (a call bound to an agent: its name).
	Agent bool
	To    string
}

func RenderVoiceRefused(d VoiceRefused) string { return render("voice/refused", d) }

// VoiceDelegated is the message a voice conversation sends the main conversation (voice/delegated): the task on its
// first line (a run finds the message by it when the transcript has no client_msg_id), then a context block.
type VoiceDelegated struct {
	Lang    string
	Task    string
	History []VoiceLine // the last lines of the voice conversation (a delegation's own text can be terse)
}

func RenderVoiceDelegated(d VoiceDelegated) string { return render("voice/delegated", d) }

// VoiceAgentMessage is the message a call bound to an agent sends it (voice/agent_message): the task on its first line,
// then a context block that says it came by voice and may be misheard. Unlike voice/delegated it does not ask for a
// short reply: the agent's work is what it is.
type VoiceAgentMessage struct {
	Lang    string
	Task    string
	History []VoiceLine
}

func RenderVoiceAgentMessage(d VoiceAgentMessage) string { return render("voice/agent_message", d) }

// VoiceStatus is what the voice model is told about the bound conversation without sending it anything
// (voice/status, docs/assistant/voice.md "Status queries"): a status asked for, or a stop.
type VoiceStatus struct {
	Lang string
	To   string // an agent's name; "" the Persona main conversation
	// Do: status (what it does now); stopped (it was stopped); stop_failed (Reason: why not, when the hub said);
	// idle (asked to stop while it does nothing); gone (it is no longer listed).
	Do       string
	State    string // proto.Session.State
	Activity string // what sort of step it is on: running tests, reading, editing…
	Work     string // proto.WorkState.Line
	Elapsed  string // how long it has worked on this turn, in Lang ("" not known)
	Latest   string // its latest words, made speakable
	Cards    []VoiceCard
	Reason   string
}

func RenderVoiceStatus(d VoiceStatus) string { return render("voice/status", d) }

// VoiceAsk is a question a conversation asked, as the voice model is to ask it (voice/ask, docs/assistant/voice.md "Questions and cards"):
// from the card's own data, one question at a time, the options by their names. Not channel.QuestionText: that has the
// options' meanings and the typing hints in it, and a cut drops the last options. The question and the options are
// the agent's words: they go inside an <agent_output> block.
type VoiceAsk struct {
	Lang string
	To   string // an agent's name; "" the Persona main conversation
	// Tool is what the model hands the answer over with: delegate_task, ask_persona, or "" (a delegation of its own).
	Tool string
	// Bound: the call is bound to the conversation (protocol 2): the "status:" and "stop:" prefixes (in Chinese) are not answers.
	Bound            bool
	Header, Question string
	Options          []VoiceOption
	Multi            bool // several options may be picked
	N, Total         int  // which question this is (from 1) of how many the card has
	Again            bool // the last answer was not heard: ask once more
}

// VoiceOption is an option of a question: its name, and what it means (said only when the user asks).
type VoiceOption struct{ Label, Description string }

func RenderVoiceAsk(d VoiceAsk) string { return render("voice/ask", d) }

// VoiceNeedsYou is what the voice model is told of cards that are answered on the screen only (voice/needs_you): a
// plan, a permission, a terminal dialog, a form, a question voice cannot answer. Only their kinds: never a command, a
// path or what a plan says (docs/assistant/voice.md "Questions and cards").
type VoiceNeedsYou struct {
	Lang  string
	To    string
	Cards []VoiceCard
	// Here: the cards show on the screen of this call (protocol 2); else they are on the phone, in the conversation.
	Here bool
}

func RenderVoiceNeedsYou(d VoiceNeedsYou) string { return render("voice/needs_you", d) }

// VoiceAnswered is what the voice model is told when a question it asked by voice is over (voice/answered).
type VoiceAnswered struct {
	Lang string
	To   string
	// By: voice (the user's answer went to it); elsewhere (answered on the screen or another device while it was being
	// asked: said by itself); late (an answer the user gave by voice came after it was answered elsewhere); unknown (the
	// connection broke off while it was passed on, and the question no longer waits); failed (the answer did not get
	// there: Code, a node's refusal said in words of its own (input_draft, dialog_open), else Reason, the hub's own
	// words, when it said); screen (the answer was not heard twice: it is left to the screen).
	By, Code, Reason string
	Here             bool // as VoiceNeedsYou's
	// Unfollowed (voice): no run follows the conversation, so what it does next is not told by itself (#3503).
	Unfollowed bool
}

func RenderVoiceAnswered(d VoiceAnswered) string { return render("voice/answered", d) }

// VoiceSpoken is a stand-in for what an answer holds that is not to be read out (voice/spoken): code, a table, a
// link, a path, a hash; omitted marks a long answer's cut middle.
type VoiceSpoken struct {
	Lang string
	Kind string // code | table | link | image | path | line | hash | snippet | omitted
	N    int    // code: its lines; table: its rows; line: the line number
	Name string // path, line: the file's name
}

func RenderVoiceSpoken(d VoiceSpoken) string { return render("voice/spoken", d) }

// VoiceToolResult is what one of the voice model's function tools answers it (voice/tool_result): what it found, with
// what other people wrote in it (an agent's last words, a chat line in the knowledge base) inside the <agent_output>
// framing measured to keep the model from doing what it says (docs/lessons/voice.md row 5).
type VoiceToolResult struct {
	Lang string
	Tool string // search_knowledge | list_agents | send_to_agent (another name: unknown_tool)
	// Code: "" it worked; bad_args (the arguments are not what the tool takes); unknown_tool; failed (Reason: the hub's
	// words, when it said).
	Code, Reason string
	Hits         []VoiceHit   // search_knowledge
	Agents       []VoiceAgent // list_agents
}

// VoiceHit is a line the knowledge base search found.
type VoiceHit struct{ Path, Line string }

// VoiceAgent is one agent as list_agents tells it: ID machine/session (what send_to_agent takes), State its
// proto.Session state (needs_you, working, unread, idle), Last its last words.
type VoiceAgent struct{ ID, Title, State, Last string }

func RenderVoiceToolResult(d VoiceToolResult) string { return render("voice/tool_result", d) }

// agentOutputTag is anything a reader could take for the start or end of an <agent_output> block.
var agentOutputTag = lazyre.New(`(?i)<\s*/?\s*agent_output\b[^>]*>`)

// AgentOutput is an agent's words to go inside an <agent_output> block, with nothing left in them that opens or closes
// one, so none of them can stand outside it (docs/assistant/voice.md "Context for the fast layer" rule 5). Like ContextBody, a tag the removal
// puts together has its `<` written as `&lt;`. Templates call it as `output`.
func AgentOutput(s string) string {
	s = agentOutputTag.ReplaceAllString(s, "")
	return agentOutputTag.ReplaceAllStringFunc(s, func(tag string) string { return "&lt;" + tag[1:] })
}
