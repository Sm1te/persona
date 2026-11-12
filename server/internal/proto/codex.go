package proto

// CodexConfig is the non-secret inventory remembered by the hub. Live owners and connection generations are never persisted.
type CodexConfig struct {
	BlockedIDs []string          `json:"blocked_ids,omitempty"`
	Discovery  *bool             `json:"discovery,omitempty"`
	Homes      []string          `json:"homes,omitempty"`
	LegacyHome string            `json:"legacy_home,omitempty"`
	Servers    []CodexServer     `json:"servers,omitempty"`
	Joined     map[string]string `json:"joined,omitempty"` // public session id -> stable instance id
}

type CodexServer struct {
	ID       string `json:"id"`
	Home     string `json:"home"`
	Endpoint string `json:"endpoint"`
	Name     string `json:"name,omitempty"`
}

type SessionCapabilities struct {
	Read      bool `json:"read"`
	Send      bool `json:"send"`
	Interrupt bool `json:"interrupt"`
	Approve   bool `json:"approve"`
	// AnswerQuestions permits asynchronous question replies without granting permission approvals.
	AnswerQuestions bool `json:"answer_questions,omitempty"`
	Terminal        bool `json:"terminal"`
	Attach          bool `json:"attach"`
	// Close is independent of terminal access. Absent on older nodes, where clients retain terminal-only closing.
	Close *bool `json:"close,omitempty"`
	// Modes: the apps' permission mode and effort chips can change this session (session.modes). Absent (nil) from a node
	// older than it, where the apps keep guessing from agent and source_kind; false where a change would only be refused
	// (a Claude outside Persona's terminals, a runtime that did not declare caps.modes). A pointer so a hub that decodes
	// an older node's row and encodes it again keeps it absent.
	Modes *bool `json:"modes,omitempty"`
	// Model explicitly permits changing the model through session.modes. Absent preserves older clients' heuristics.
	Model *bool `json:"model,omitempty"`
}

// CanModes is a Modes value.
func CanModes(on bool) *bool { return &on }
