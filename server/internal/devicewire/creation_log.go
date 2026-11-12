package devicewire

import (
	"encoding/json"
	"regexp"
)

// ShareCreationLog emits local diagnostic outcomes, not a wire DTO or a durable
// acknowledgement. Callers supply static step/result codes, never error text.
func ShareCreationLog(logf func(string, ...any), session, step, result string) {
	if logf == nil || !creationLogCodes[step][result] {
		return
	}
	if session == "" {
		session = "pending"
	} else if !creationLogSession.MatchString(session) {
		session = "invalid"
	}
	raw, _ := json.Marshal(struct {
		Session string `json:"session_id"`
		Step    string `json:"step"`
		Result  string `json:"result"`
	}{session, step, result})
	logf("share_creation %s", raw)
}

// Production Claude/Codex IDs are UUIDs. Synthetic IDs are useful to isolated
// tests. Other identifiers are deliberately not copied into diagnostic logs.
var creationLogSession = regexp.MustCompile(`^(?:[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|synthetic-[a-z0-9_-]{1,64})$`)

var creationLogCodes = map[string]map[string]bool{
	"manager_prepare": {
		"intent_created": true, "store_unavailable": true, "caller_unbound": true,
		"authority_missing": true, "authority_stale": true, "resume_skipped": true,
		"capability_missing": true, "prepare_error": true,
	},
	"node_reply":      {"receipt_match": true, "receipt_mismatch": true, "receipt_missing": true, "start_failed": true},
	"node_receipt":    {"state_updated": true, "start_failed": true, "resume_skipped": true, "identity_missing": true, "identity_conflict": true, "persist_unavailable": true},
	"node_notify":     {"sent_unacknowledged": true, "peer_unavailable": true, "send_error": true},
	"manager_persist": {"received_with_receipt": true, "received_without_receipt": true, "committed_with_receipt": true, "committed_without_receipt": true, "store_error": true, "store_unavailable": true, "invalid_state": true},
}
