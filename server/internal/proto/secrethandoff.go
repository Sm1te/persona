package proto

import "encoding/json"

// The hand-off card (docs/extensions/secret-providers.md "Who may approve a hand-off"): the card the manager shows
// before it hands secret values to one Agent run (secret.stage). Only the manager's own Agent start makes one; the hub
// lets only a device that may approve it say yes to it.

// SecretHandoffTool is the hand-off card's tool name, reserved as SecretRunTool is: a card from a node, a runtime or an
// extension that names it is refused.
const SecretHandoffTool = "secret_handoff"

// CodeSecretHandoffElsewhere is the hub's 403 to an approval of a hand-off card from a device or client that may not
// give one (an answer-only device, an extension's token, the hub's own token, an app that does not show the card):
// it is approved on the iPhone or the web.
const CodeSecretHandoffElsewhere = "secret_handoff_elsewhere"

// SecretHandoffInput is the hand-off card's ToolInput: the variables it hands over, each with the provider it comes from
// and the item as the provider describes it (secrets.describe), never a reference or a value: the card goes to every
// client and to extensions that read cards, and the manager keeps the references. The hub reads the providers only, to
// record a refused approval on each.
type SecretHandoffInput struct {
	Variables []SecretHandoffVariable `json:"variables"`
	// Agent is the Agent's name (its tab's), MachineName the computer it starts on, Cwd and Prompt where it runs and
	// the start of what it is asked: for the card to show (Cwd and Prompt not in its summary, which is also the push's
	// text).
	Agent       string `json:"agent,omitempty"`
	MachineName string `json:"machine_name,omitempty"`
	Cwd         string `json:"cwd,omitempty"`
	Prompt      string `json:"prompt,omitempty"`
}

// SecretHandoffVariable is one environment variable a hand-off card hands over.
type SecretHandoffVariable struct {
	Name         string `json:"name"`
	Provider     string `json:"provider"`
	ProviderName string `json:"provider_name,omitempty"`
	Title        string `json:"title,omitempty"`
}

// SecretHandoffApproves reports whether r says yes to a hand-off card: allow, and neither dismiss nor cancel. Only such an
// answer, from a device the hub vouched for, hands anything over; every other answer, a no with extra fields included
// ("deny, don't ask again"), declines.
func SecretHandoffApproves(r Resolution) bool {
	return r.Allow != nil && *r.Allow && !r.Dismiss && !r.Cancel
}

// SecretHandoffDeclines reports whether r only says no to a hand-off card: deny (with or without a message), dismiss or
// cancel, and nothing else set. The hub and the manager take every other answer as an approval, which only a device
// that may approve gives. What is left once the no is taken out must encode as {} (every Resolution field is
// omitempty), so a field added later counts as more than a no until it is thought about here.
func SecretHandoffDeclines(r Resolution) bool {
	if !(r.Allow != nil && !*r.Allow || r.Dismiss || r.Cancel) {
		return false
	}
	if r.Allow != nil && !*r.Allow {
		r.Allow = nil
	}
	r.Dismiss, r.Cancel = false, false
	r.Message, r.Feedback, r.AnsweredBy, r.Reason = "", "", "", ""
	b, err := json.Marshal(r)
	return err == nil && string(b) == "{}"
}
