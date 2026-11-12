package proto

import (
	"strings"
	"time"
)

// IsCodexSubagent identifies delegated tasks from source metadata, even when
// their parent is not in the current session list. Titles never imply ancestry.
func (s Session) IsCodexSubagent() bool {
	if s.Agent != "codex" && !strings.HasPrefix(s.SourceKind, "codex_") {
		return false
	}
	return s.IsSubagent || s.ParentSessionID != "" || s.ParentThreadID != "" ||
		(s.RootSessionID != "" && s.RootSessionID != s.SessionID)
}

// IsSubtask says whether a session is listed and counted under the session that started it rather than on its own:
// a Codex subagent, or a runtime's own session that names its parent, whichever agent it runs (SourceRuntime: an
// agent another Paseo agent created, #4494). Unlike a Codex subagent, which its parent drives, such a runtime session
// can need the user. While it does it is no subtask: every place that counts sessions has it, and none folds it
// away. (The node lists it without its parent then; a card can still reach the hub before that row does.)
func (s Session) IsSubtask() bool {
	if s.SourceKind == SourceRuntime {
		return (s.IsSubagent || s.ParentSessionID != "") && s.State != StateNeedsYou
	}
	return s.IsCodexSubagent()
}

// SessionHierarchy is a Session's place among the sessions that started one another (docs/protocol/protocol.md
// "Session hierarchy") and when it began. Each member is a key of the row itself on the wire. A Session holds it by
// pointer (Session.Hierarchy, nil when every member is empty): the four fields inline would take every row past its
// size class (TestSessionFitsItsSizeClass), and only a few rows have any of them.
type SessionHierarchy struct {
	// StartedAt is when the session began: its transcript's first line, a runtime's created time. Set by the node where
	// it knows it (Codex rollouts, runtime rows); zero when it does not.
	StartedAt time.Time `json:"started_at,omitzero"`
	// AskingParentSessionID names the parent of a runtime child that waits for the user (status blocked). Such a row
	// has is_subagent, parent_session_id and root_session_id cleared, so that apps which fold every child under its
	// parent still list it while it needs the user (#4494); this keeps its parent known to those that fold it as a card
	// in the parent's chat. Set by the node.
	AskingParentSessionID string `json:"asking_parent_session_id,omitempty"`
	// FoldedUnder is the listed session on the same machine this child folds under: its topmost listed ancestor. Set by
	// the manager (foldSessions, assistant/fleet_fold.go); empty for a session that is not a child, an orphan and a
	// member of a cycle.
	FoldedUnder string `json:"folded_under,omitempty"`
	// Children counts the sessions folded under this one, all depths, by their own states. Set by the manager on the
	// topmost listed ancestor only.
	Children *SessionChildren `json:"children,omitempty"`
}

// SessionChildren is the rollup a parent row carries of the children folded under it (SessionHierarchy.Children).
type SessionChildren struct {
	Total    int `json:"total"`
	Working  int `json:"working,omitempty"`
	NeedsYou int `json:"needs_you,omitempty"`
}

func (h *SessionHierarchy) empty() bool {
	return h.StartedAt.IsZero() && h.AskingParentSessionID == "" && h.FoldedUnder == "" && h.Children == nil
}

// hierarchy is a copy of s's hierarchy to change, its children copied too: the one s holds is shared.
func (s *Session) hierarchy() SessionHierarchy {
	if s.Hierarchy == nil {
		return SessionHierarchy{}
	}
	h := *s.Hierarchy
	if h.Children != nil {
		c := *h.Children
		h.Children = &c
	}
	return h
}

// setHierarchy makes h the session's, nil when it is empty.
func (s *Session) setHierarchy(h SessionHierarchy) {
	if h.empty() {
		s.Hierarchy = nil
		return
	}
	s.Hierarchy = &h
}

// StartedAt is when the session began (SessionHierarchy.StartedAt); zero when not known.
func (s *Session) StartedAt() time.Time {
	if s.Hierarchy == nil {
		return time.Time{}
	}
	return s.Hierarchy.StartedAt
}

// AskingParentSessionID is the parent of a blocked runtime child (SessionHierarchy.AskingParentSessionID).
func (s *Session) AskingParentSessionID() string {
	if s.Hierarchy == nil {
		return ""
	}
	return s.Hierarchy.AskingParentSessionID
}

// FoldedUnder is the listed session this one folds under (SessionHierarchy.FoldedUnder); "" when none.
func (s *Session) FoldedUnder() string {
	if s.Hierarchy == nil {
		return ""
	}
	return s.Hierarchy.FoldedUnder
}

// Children is the rollup of the sessions folded under this one; the zero value when none are.
func (s *Session) Children() SessionChildren {
	if s.Hierarchy == nil || s.Hierarchy.Children == nil {
		return SessionChildren{}
	}
	return *s.Hierarchy.Children
}

// SetStartedAt sets when the session began (zero: not known), with no new hierarchy when it is already so.
func (s *Session) SetStartedAt(t time.Time) {
	if s.StartedAt().Equal(t) && s.StartedAt().Location() == t.Location() {
		return
	}
	h := s.hierarchy()
	h.StartedAt = t
	s.setHierarchy(h)
}

// SetAskingParentSessionID sets the parent a blocked runtime child names ("" for none).
func (s *Session) SetAskingParentSessionID(id string) {
	if s.AskingParentSessionID() == id {
		return
	}
	h := s.hierarchy()
	h.AskingParentSessionID = id
	s.setHierarchy(h)
}

// SetFold sets what folding says of the session: the session it folds under ("" for none) and the rollup of those
// folded under it (nil for none). Nothing new is made when the session already says so.
func (s *Session) SetFold(under string, children *SessionChildren) {
	cur := s.Hierarchy
	if cur == nil && under == "" && children == nil {
		return
	}
	if cur != nil && cur.FoldedUnder == under && (cur.Children == nil) == (children == nil) &&
		(children == nil || *cur.Children == *children) {
		return
	}
	h := s.hierarchy()
	h.FoldedUnder = under
	h.Children = nil
	if children != nil {
		c := *children
		h.Children = &c
	}
	s.setHierarchy(h)
}

// IsChild says whether the session was started by another one, as the hierarchy reads it: a subagent, a session that
// names its parent (also while it waits for the user, AskingParentSessionID) or a root other than itself.
func (s *Session) IsChild() bool {
	return s.IsSubagent || s.ParentSessionID != "" || s.AskingParentSessionID() != "" ||
		(s.RootSessionID != "" && s.RootSessionID != s.SessionID)
}

// FoldedState is the state a row shows with its folded children (docs/protocol/protocol.md "Session hierarchy"): it
// needs the user when it or one of them does; it works when one of them works, unless it has a result not looked at
// yet or awaits the user's reply; otherwise its own.
func (s *Session) FoldedState() string {
	c := s.Children()
	switch {
	case s.State == StateNeedsYou || c.NeedsYou > 0:
		return StateNeedsYou
	case c.Working > 0 && s.State != StateUnread && !(s.State == StateIdle && s.AwaitsReply):
		return StateWorking
	}
	return s.State
}
