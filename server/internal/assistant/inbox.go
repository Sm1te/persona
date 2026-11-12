package assistant

import (
	"encoding/json"
	"slices"
	"strconv"
	"time"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/assistantwire"
)

// The unified inbox (docs/assistant/inbox.md) is the manager's: Feishu messages, mail and the connectors' threads in one list,
// one row per "thing" — the new messages in a chat, a mail thread — not per message and not per chat. The hub reads
// the sources when a round is due and hands what it read over (InboxCollected); the inbox keeps the items in Postgres
// (table inbox_items, only it reads and writes it) with their AI triage (inbox_triage.go) and whether the user has
// dealt with them, answers the /api/v1/inbox… routes the hub relays, and tells the apps {"type":"inbox"} (through the
// hub, assistantwire.EventInboxChanged) when the list changes.

const (
	inboxWindow   = 7 * 24 * time.Hour // older unread chats / mail are history, not inbox
	inboxKeepDone = 3 * 24 * time.Hour // done items stay listed (for undo) this long
	inboxForget   = 8 * 24 * time.Hour // and are held in memory (so old unread messages don't come back) this long
	// inboxKeepHistory: done items stay in the database this long, paged by GET /api/v1/inbox/done (inbox_history.go);
	// inboxPurgeEvery is how often the rounds delete those past it.
	inboxKeepHistory = 90 * 24 * time.Hour
	inboxPurgeEvery  = time.Hour
	inboxText        = 600 // characters of one message kept for triage
)

// Item kinds.
const (
	KindDM      = "dm"      // a person wrote to me
	KindMention = "mention" // @me / @all in a group
	KindGroup   = "group"   // new messages in a group I follow (not muted)
	KindBot     = "bot"     // a bot or app chat
	KindMail    = "mail"
)

// Triage tiers (inbox_triage.go).
const (
	TierAction = "action" // "Needs you"
	TierFYI    = "fyi"    // "FYI"
	TierNoise  = "noise"  // "Noise"
)

type InboxItem struct {
	ID          string     `json:"id"`
	Source      string     `json:"source"` // lark | mail
	Kind        string     `json:"kind"`
	Account     string     `json:"account"`      // "feishu" or a mail account id
	AccountName string     `json:"account_name"` // Lark (Feishu) / the mail address
	Title       string     `json:"title"`        // chat name / mail subject
	Sender      string     `json:"sender,omitempty"`
	Avatar      string     `json:"avatar,omitempty"`
	Preview     string     `json:"preview"`
	Count       int        `json:"count"`
	StartedAt   time.Time  `json:"started_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	Tier        string     `json:"tier,omitempty"` // empty until triaged
	Summary     string     `json:"summary,omitempty"`
	Reason      string     `json:"reason,omitempty"`
	Read        bool       `json:"read,omitempty"` // read somewhere else (Feishu, another mail client)
	DoneAt      *time.Time `json:"done_at,omitempty"`
	DoneBy      string     `json:"done_by,omitempty"` // user | dismissed | read | replied | superseded
	Lark        *InboxLark `json:"lark,omitempty"`
	// Conversation is where the item opens in a messaging source (docs/extensions/extension-messaging.md "9", #2138): listed
	// only, from Lark; an app with the source opens it there, an older one uses Lark.
	Conversation *wire.MessagingConversationRef `json:"conversation,omitempty"`
	Mail         *InboxMail                     `json:"mail,omitempty"`
	Doc          *InboxDoc                      `json:"doc,omitempty"` // a Feishu document's @me / comment (inbox_blocks.go)
	// Translations by target language: new foreign mail, translated before it is opened (inbox_translate.go).
	Translations map[string]*InboxTranslation `json:"translations,omitempty"`
	Moment       *InboxMoment                 `json:"moment,omitempty"` // a Moments (company feed) post's comments / @me (inbox_blocks.go)
	// Connector: a thread of a connector's (GitHub…), Source being its id (inbox_connectors.go).
	Connector *InboxConnector `json:"connector,omitempty"`
	// Agent is the AI twin's work on the item: judged by model 2, handled and drafted by the agent (inbox_agent.go).
	Agent *InboxAgent `json:"agent,omitempty"`
	// Mutes are what it can be muted by (listed only; inbox_mutes.go).
	Mutes []MuteOption `json:"mutes,omitempty"`
	// Version names what the item holds now (listed only): a bulk "Done" sends it back, so an item that took new
	// messages since the list was drawn is not filed with them (inbox_bulk.go).
	Version string `json:"version,omitempty"`
	// TriagedBy says who sorted an event (inbox_group.go): "model" (its title, summary and tier are the model's) or
	// "rules" (the connector grouped it and its rule tier stands, "By rule").
	TriagedBy string `json:"triaged_by,omitempty"`
	// Parent is what the item grew out of: another item, a todo or an agent session (inbox_parent.go, #2128).
	Parent *InboxParent `json:"parent,omitempty"`
	// Folder is the "/"-separated folder path the item is filed under ("" none); FolderBy says who filed it, "model"
	// or "user" (inbox_folders.go, #2463).
	Folder   string `json:"folder,omitempty"`
	FolderBy string `json:"folder_by,omitempty"`

	// The event's part (inbox_events.go, inbox_handling.go): its section (listed only), urgent (someone waits, a
	// deadline is near), the manager's recap, the processing conversation, the one current suggestion, the handling chain
	// and the items related to it across atoms.
	Status     string            `json:"status,omitempty"`
	Urgent     bool              `json:"urgent,omitempty"`
	Recap      string            `json:"recap,omitempty"`
	Handling   *InboxHandling    `json:"handling,omitempty"`
	Suggestion *InboxSuggestion  `json:"suggestion,omitempty"`
	Chain      []InboxChainEntry `json:"chain,omitempty"`
	// ChainCount is set only in a list asked with chain=last (inbox_list_memo.go, #4742): a done item's chain is then
	// its newest entry alone, and this is how many it has. GET /api/v1/inbox/{id} has them all.
	ChainCount int            `json:"chain_count,omitempty"`
	Related    []InboxRelated `json:"related,omitempty"`
	// Handoffs are the latest conversations the event was handed to (inbox_handoff.go), oldest first.
	Handoffs []InboxHandoff `json:"handoffs,omitempty"`

	// Bookkeeping: persisted, not listed (GET /api/v1/inbox/{id} returns Context).
	Last      string `json:"last,omitempty"`       // newest message position / mail uid in the item
	Context   string `json:"context,omitempty"`    // the conversation as text, for triage and "Hand to AI"
	TriagedAt string `json:"triaged_at,omitempty"` // Last when it was triaged
	BodyOf    string `json:"body_of,omitempty"`    // mail: Last when the full text was read into Preview / Context
	JudgedAt  string `json:"judged_at,omitempty"`  // Last when model 2 looked at it
	FolderOf  string `json:"folder_of,omitempty"`  // Last when the model was asked for its folder
	// Rules are the item's own triage rules, from its connector (InboxThing.Rules): they replace inbox.rules for it.
	Rules string `json:"rules,omitempty"`
	// ArrivedAt is the clock here when the item first held Last (ArrivedOf), as listed or saved: a bulk "Done" over a
	// filter leaves what arrived after the list it acted on (inbox_bulk.go). Rows stored before have UpdatedAt.
	ArrivedOf string    `json:"arrived_of,omitempty"`
	ArrivedAt time.Time `json:"arrived_at,omitzero"`
	// Meta is an event's meta as its connector wrote it (which messages are in it…): kept and handed back as it is,
	// never read here. Group is the event's conversation (inbox_group.go).
	Meta  json.RawMessage `json:"meta,omitempty"`
	Group *InboxGroup     `json:"group,omitempty"`
	// ChainSeq numbers the chain's entries; SuggestSeq is the last suggestion's version (a new one is the next).
	ChainSeq   int `json:"chain_seq,omitempty"`
	SuggestSeq int `json:"suggest_seq,omitempty"`
}

type InboxLark struct {
	AccountID int `json:"account_id,omitempty"` // zero in legacy rows means primary account 1
	// OLAccount is what the apps pass as ol_account to the /api/v1/lark routes about this item: AccountID when that
	// is not lark.account, else zero (larkRequestAccount).
	OLAccount       int    `json:"ol_account,omitempty"`
	FeedID          string `json:"feed_id"`
	IsGroup         bool   `json:"is_group"`
	IsTopic         bool   `json:"is_topic,omitempty"`
	StartPosition   string `json:"start_position,omitempty"`   // the first new message (open the chat there)
	ReadPosition    string `json:"read_position,omitempty"`    // the chat's read position in Feishu, as of the last collection
	MentionPosition string `json:"mention_position,omitempty"` // the unread @me
	AtAll           bool   `json:"at_all,omitempty"`
	AtMe            bool   `json:"at_me,omitempty"` // an unread @me (not only @all): kept while the item is open
	// ReplyTo is the newest message from someone else; ThreadKind says where it sits: "" in the chat, "topic" in a
	// topic group's topic, "thread" in a thread of a normal group. A reply from the inbox goes there (the plugin's).
	ReplyTo    string `json:"reply_to,omitempty"`
	ThreadKind string `json:"thread_kind,omitempty"`
	// SenderID is who sent the newest message from someone else (a group's sender, to mute them).
	SenderID string `json:"sender_id,omitempty"`
}

// Values of InboxLark.ThreadKind.
const (
	LarkThreadTopic  = "topic"
	LarkThreadThread = "thread"
)

// MailAddress is a mail's sender or recipient.
type MailAddress = assistantwire.MailAddress

type InboxMail struct {
	AccountID string      `json:"account_id"`
	Mailbox   string      `json:"mailbox"`
	UIDs      []uint32    `json:"uids"` // oldest first
	MessageID string      `json:"message_id"`
	From      MailAddress `json:"from"`
	Direct    bool        `json:"direct,omitempty"` // to me, not cc / a list
	Thread    string      `json:"thread"`           // normalized subject: replies join the same item
}

func (it *InboxItem) open() bool { return it.DoneAt == nil }

// conversationOf is where a chat item opens in its messaging source: the source is the connector's id ("lark", the
// Lark extension's source of the same id), the account the one the apps would pass as ol_account.
func conversationOf(it *InboxItem) *wire.MessagingConversationRef {
	if it.Lark == nil || it.Lark.FeedID == "" || it.Source == "" {
		return nil
	}
	ref := &wire.MessagingConversationRef{Source: it.Source, ID: it.Lark.FeedID, StartPosition: it.Lark.StartPosition,
		ReadPosition: it.Lark.ReadPosition, MentionPosition: it.Lark.MentionPosition}
	if it.Lark.OLAccount > 0 {
		ref.Account = strconv.Itoa(it.Lark.OLAccount)
	}
	return ref
}

// listed is the item as the list shows it (no context).
func (it *InboxItem) listed() InboxItem {
	c := *it
	c.Context, c.Last, c.TriagedAt, c.BodyOf, c.JudgedAt, c.Rules, c.ArrivedOf, c.FolderOf = "", "", "", "", "", "", "", ""
	c.ArrivedAt = time.Time{}
	c.Meta, c.Group = nil, nil
	c.ChainSeq, c.SuggestSeq = 0, 0
	c.Status = it.status()
	c.Chain = listedChain(it)
	c.Related = slices.Clone(it.Related)
	c.Handoffs = listedHandoffs(it.Handoffs)
	if it.Handling != nil {
		h := *it.Handling
		c.Handling = &h
	}
	if g := currentSuggestion(it); g != nil {
		g := *g
		g.Changes, g.Cwd = slices.Clone(g.Changes), ""
		c.Suggestion = &g
		if c.Handling == nil && g.Conversation == nil && it.Agent != nil { // an old draft: its conversation is done with this round
			c.Handling = &InboxHandling{Owner: HandlingOwner, State: HandlingDone, MachineID: it.Agent.MachineID, SessionID: it.Agent.SessionID}
		}
	}
	c.Mutes = mutesOf(it)
	c.Version = inboxVersion(it)
	c.Conversation = conversationOf(it)
	if it.Agent != nil { // the copy is encoded after the lock is released
		a := *it.Agent
		a.Pending, a.Wake, a.SentOf, a.Lost = nil, false, "", nil
		c.Agent = &a
	}
	if it.Mail != nil {
		m := *it.Mail
		m.UIDs = append([]uint32(nil), m.UIDs...)
		c.Mail = &m
	}
	if it.Doc != nil {
		d := *it.Doc
		c.Doc = &d
	}
	if len(it.Translations) > 0 { // the list shows subject and preview; the body is fetched when the mail opens
		c.Translations = map[string]*InboxTranslation{}
		for k, t := range it.Translations {
			if t != nil && t.Of == it.Last {
				c.Translations[k] = &InboxTranslation{Subject: t.Subject, Preview: t.Preview, Clipped: t.Clipped}
			}
		}
	}
	if it.Moment != nil {
		m := *it.Moment
		m.Notices = append([]string(nil), it.Moment.Notices...)
		c.Moment = &m
	}
	if it.Connector != nil {
		k := *it.Connector
		c.Connector = &k
	}
	return c
}

// Listed is the item as GET /api/v1/inbox lists it (the hub's wire fixtures are made from it).
func (it InboxItem) Listed() InboxItem { return it.listed() }

// arrived stamps when the item came to hold what it holds (ArrivedAt), the first time it is listed or saved so. Under
// b.mu.
func (it *InboxItem) arrived(now time.Time) {
	if it.ArrivedOf != it.Last || it.ArrivedAt.IsZero() {
		it.ArrivedOf, it.ArrivedAt = it.Last, now
	}
}

// arrivedAfter: the item took what it holds after asOf (or has not been stamped yet: it is coming in now). Under b.mu.
func (it *InboxItem) arrivedAfter(asOf time.Time) bool {
	return it.ArrivedOf != it.Last || it.ArrivedAt.After(asOf)
}

func (it *InboxItem) done(at time.Time, by string) {
	t := at
	it.DoneAt, it.DoneBy = &t, by
}
