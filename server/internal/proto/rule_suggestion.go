package proto

// RuleSuggestion is the line a card carries once the owner gave the same answer to its kind of card, for the same
// scope, three times within 14 days (#4929, docs/assistant/unified-entry.md "A rule after the same answer three
// times"): "Always do this?" with two actions, POST /api/v1/assistant/rule-suggestions/{id}/always and …/not-now.
// The manager counts and words it; a client that does not know the field shows the card as before.
type RuleSuggestion struct {
	// ID names the kind and scope; it is what the two actions take.
	ID string `json:"id"`
	// Kind: "plan_start" (Start on plan cards for tasks that change files in one directory), "standing_authorization"
	// (the same go-ahead for merge, push or deploy-test in one directory) or "delegate" (questions a supervisor passed
	// up, answered with its recommendation).
	Kind string `json:"kind"`
	// Text is the line, in the request's language (the user's for a card sent as an event).
	Text  string    `json:"text"`
	Scope RuleScope `json:"scope"`
	// Creates is what Always stores: "tasks.trust", "standing_authorization", or "" when it stores no rule and
	// Setting names the setting to open instead ("agents.delegate").
	Creates string `json:"creates"`
	Setting string `json:"setting,omitempty"`
	// Count is how many identical answers were counted.
	Count int `json:"count"`
}

// RuleScope is what a suggested rule would cover; empty for a "delegate" suggestion, whose setting is not per directory.
type RuleScope struct {
	MachineID string `json:"machine_id,omitempty"`
	Machine   string `json:"machine,omitempty"` // its name, for display
	Directory string `json:"directory,omitempty"`
	// Action and Conditions: a standing authorization's action and the conditions it would be granted under.
	Action     string   `json:"action,omitempty"`
	Conditions []string `json:"conditions,omitempty"`
	// Quote is what the standing authorization would keep as the owner's words: their latest answer.
	Quote string `json:"quote,omitempty"`
}
