package hub

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The hand-off card's gate (docs/extensions/secret-providers.md "Who may approve a hand-off"). A hand-off card
// (proto.SecretHandoffTool) hands secret values to one Agent run once it is approved, so only a full device approves it,
// from the card itself: the approval request declares featureSecretHandoff in its own X-Persona-Features header
// (requestDeclares; a connection's client_hello does not count, or every request of that app, a lock-screen button's
// included, would). An answer-only device (the watch), an extension's token, the hub's own token and a request without
// the feature (Android, the Mac app, an older app, a notification's or a widget's button) get 403
// secret_handoff_elsewhere, and the refusal is recorded on every provider the card names
// (extwire.AuditSecretHandoffRefused), once per card, credential and reason in handoffRecordEvery. A plain no (deny,
// dismiss, cancel: proto.SecretHandoffDeclines) goes through from any of them.
//
// The feature is the client's word: it keeps a client that does not show the card from approving one blindly. It is no
// boundary between the owner's own full credentials, any of which may do whatever the owner may.
//
// The device that may approve goes to the manager in HTTPRequest.HandoffApprover, never in a header, on an answer to a
// hand-off card and on one to a card the hub does not hold (after a restart, before the manager reported its cards
// again): the manager approves a hand-off only with it, so it checks the cards the hub could not. A card's push never
// offers buttons (interactionPayloadIn), and the Live Activity shows it without Allow / Deny (assistant/fleet_live.go).

// featureSecretHandoff: the request approving a hand-off card comes from the card itself, which its client shows whole
// (what is handed over, from where, to which Agent on which computer). A client sends it on that request only, never in
// a header every request carries, nor from a notification, a widget, the Live Activity, the watch or Siri.
const featureSecretHandoff = "secret_handoff"

// handoffRecordEvery: a refusal of the same card, credential and reason is recorded once in this long; handoffRecordMost
// bounds what is remembered.
const (
	handoffRecordEvery = 10 * time.Minute
	handoffRecordMost  = 1024
)

// handoffRecorded remembers the refusals recorded lately, so one credential trying again and again fills no audit log.
type handoffRecorded struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

// first reports whether key was not recorded in the last handoffRecordEvery, and remembers it now. When full it forgets
// the stale keys, else the oldest: a flood of other keys never stops a new one being recorded.
func (m *handoffRecorded) first(key string, now time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if at, ok := m.seen[key]; ok && now.Sub(at) < handoffRecordEvery {
		return false
	}
	if len(m.seen) >= handoffRecordMost {
		oldest, oldestAt := "", now
		for k, at := range m.seen {
			if now.Sub(at) >= handoffRecordEvery {
				delete(m.seen, k)
			} else if !at.After(oldestAt) {
				oldest, oldestAt = k, at
			}
		}
		if len(m.seen) >= handoffRecordMost {
			delete(m.seen, oldest)
		}
	}
	if m.seen == nil {
		m.seen = map[string]time.Time{}
	}
	m.seen[key] = now
	return true
}

// forget drops key: what first remembered was not recorded after all.
func (m *handoffRecorded) forget(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.seen, key)
}

// Why a request may not approve a hand-off card (the record's reason).
const (
	handoffAnswerOnly = "answer_only" // the watch
	handoffExtension  = "extension"   // an extension's token
	handoffHubToken   = "hub_token"   // the hub's own token: no device
	handoffRole       = "role"        // any other credential that is not a full device's
	handoffClient     = "client"      // a full device whose client did not declare featureSecretHandoff
)

type handoffApproverKey struct{}

// handoffApprover is the device r comes from when it may approve a hand-off card, else why not.
func (h *Hub) handoffApprover(r *http.Request) (assistantwire.HandoffApprover, string) {
	p := principalOf(r.Context())
	switch {
	case p.Role == roleAnswer:
		return assistantwire.HandoffApprover{}, handoffAnswerOnly
	case p.Role == roleExtension:
		return assistantwire.HandoffApprover{}, handoffExtension
	case p.Role != roleFull:
		return assistantwire.HandoffApprover{}, handoffRole
	case p.ID == "":
		return assistantwire.HandoffApprover{}, handoffHubToken
	case !requestDeclares(r, featureSecretHandoff):
		return assistantwire.HandoffApprover{}, handoffClient
	}
	return assistantwire.HandoffApprover{DeviceID: p.ID, DeviceName: p.Name}, ""
}

// handoffGate checks answer res to card id before it is relayed: r to relay (carrying the approver when the manager
// needs it), or false when it was refused and answered here. An answer to another card passes untouched.
func (h *Hub) handoffGate(w http.ResponseWriter, r *http.Request, id string, res proto.Resolution) (*http.Request, bool) {
	ix, known := h.pendingInteraction(id)
	if known && ix.ToolName != proto.SecretHandoffTool {
		return r, true
	}
	a, why := h.handoffApprover(r)
	if why == "" {
		return r.WithContext(context.WithValue(r.Context(), handoffApproverKey{}, a)), true
	}
	if !known || proto.SecretHandoffDeclines(res) { // a card the hub does not hold is the manager's to refuse
		return r, true
	}
	h.refuseHandoff(w, r, ix, why)
	return r, false
}

// handoffApproverOf is the approver handoffGate put on r's context.
func handoffApproverOf(ctx context.Context) *assistantwire.HandoffApprover {
	if a, ok := ctx.Value(handoffApproverKey{}).(assistantwire.HandoffApprover); ok && a.DeviceID != "" {
		return &a
	}
	return nil
}

// refuseHandoff answers 403 to an approval of hand-off card ix that r may not give (why), and has the card's manager
// record it on each provider the card names (once in handoffRecordEvery): who tried, from which role, on which card.
// Nothing of what the card hands over (variables, items) is in it.
func (h *Hub) refuseHandoff(w http.ResponseWriter, r *http.Request, ix proto.Interaction, why string) {
	p := principalOf(r.Context())
	actor := "hub token"
	switch {
	case p.Role == roleExtension && p.Extension != "":
		actor = "extension:" + p.Extension
	case p.ID != "":
		actor = "device:" + p.ID
	}
	detail := map[string]any{"interaction_id": rowText(ix.ID, 128), "machine_id": rowText(ix.MachineID, 128),
		"session_id": rowText(ix.SessionID, 128), "role": p.Role, "reason": why}
	if p.Name != "" && p.Role != roleExtension {
		detail["device_name"] = rowText(p.Name, 128)
	}
	defer writeErrCode(w, http.StatusForbidden, proto.CodeSecretHandoffElsewhere,
		errors.New(i18n.T(h.requestLang(r), "hub-secret_handoff.approve_elsewhere")))
	key := rowText(ix.ID, 128) + "\x00" + rowText(actor, 160) + "\x00" + why
	if !h.handoffRecorded.first(key, time.Now()) {
		return // recorded lately
	}
	h.logf("hand-off card %s: an approval from %s refused (%s)", clip(ix.ID, 64), clip(actor, 96), why)
	providers := handoffProviders(ix)
	if len(providers) == 0 {
		h.logf("hand-off card %s names no provider: the refusal is not recorded", clip(ix.ID, 64))
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	manager := cmp.Or(ix.Manager, assistantwire.AssistantID)
	recorded, passing := false, false
	for _, prov := range providers {
		audit := extwire.Audit{Extension: prov, Action: extwire.AuditSecretHandoffRefused, Actor: actor, Detail: detail}
		err := h.extCallTo(ctx, manager, extwire.HubAudit, audit, nil)
		if err == nil {
			recorded = true
			continue
		}
		h.logf("hand-off card %s: the refusal is not recorded on %s: %v", clip(ix.ID, 64), clip(prov, 64), err)
		var coded *rpc.Error
		if !errors.As(err, &coded) || coded.Code != bus.CodeInvalidParams {
			passing = true // the manager away or slow, not a provider it does not have
		}
	}
	if !recorded && passing {
		h.handoffRecorded.forget(key) // the next try records it
	}
}

// handoffProviders are the providers hand-off card ix names, each once, at most proto.SecretEnvMost.
func handoffProviders(ix proto.Interaction) []string {
	var in proto.SecretHandoffInput
	if json.Unmarshal(ix.ToolInput, &in) != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, v := range in.Variables {
		prov := rowText(v.Provider, 128)
		if prov == "" || seen[prov] {
			continue
		}
		if len(out) == proto.SecretEnvMost {
			break
		}
		seen[prov] = true
		out = append(out, prov)
	}
	return out
}
