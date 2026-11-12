package node

import (
	"strings"

	"github.com/nwverse/persona/server/internal/proto"
)

// Context usage of a session for the apps' agent rows and session header (#2430). The numbers come from the
// transcript tail readQuickMetaSince already parses: the latest reply's input tokens (transcript.Meta.ContextTokens)
// and, for Codex, the window its rollout records. Nothing here reads a file.

const (
	claudeWindow   = 200_000   // every Claude model without the 1M variant
	claudeWindow1M = 1_000_000 // the [1m] variants
)

// contextWindowOf is the model's context window in tokens, 0 when it cannot be told. recorded is the window the
// transcript itself says (Codex); otherwise Claude models are known by id: a "[1m]" suffix means the 1M variant,
// and so does a reply that already saw more than 200k (the transcript records the id without the suffix). A model
// that is not a Claude one (a gateway's own) has no window we know: the apps then show the tokens only.
func contextWindowOf(model string, recorded, used int) int {
	if recorded > 0 {
		return recorded
	}
	id := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(id, "[1m]"):
		return claudeWindow1M
	case strings.HasPrefix(id, "claude-"):
		if used > claudeWindow {
			return claudeWindow1M
		}
		return claudeWindow
	}
	return 0
}

// setContext copies cached title provenance and cumulative output alongside the context fields.
// The context fields stay zero (and so off the wire) while the transcript has no reply yet.
func setContext(s *proto.Session, m quickMeta) {
	s.OutputTokens = m.outputTokens
	s.TitleManual = s.TitleManual || m.titleManual
	if m.title != "" && s.Title == m.title || m.title == "" && m.titleSource == "prompt" {
		// A terminal's tool-only fallback still has known transcript provenance
		// once the complete scan proves that no custom/AI title exists.
		s.TitleSource = proto.ParseTitleSource(m.titleSource)
	}
	if m.contextTokens <= 0 {
		return
	}
	s.ContextTokens = proto.Tokens32(m.contextTokens)
	s.ContextWindow = proto.Tokens32(contextWindowOf(m.model, m.contextWindow, m.contextTokens))
}

// setRuntimeContext is setContext for a runtime's session whose conversation is a transcript here (Paseo). The model
// the runtime runs it as decides the window when it names the 1M variant: the transcript records the id without the
// suffix, and behind a gateway another id altogether, so such a session read as 200k, or as no window at all, until
// a reply passed 200k (#4496). A window the transcript records itself stays, and so does the transcript's word while
// the session answers as another model: a refusal's fallback, or a reply from a Claude model that is not the one the
// runtime names (an overload fallback).
func setRuntimeContext(s *proto.Session, m quickMeta) {
	setContext(s, m)
	if s.ContextTokens == 0 || m.contextWindow > 0 || m.modelFallback != nil {
		return
	}
	run := strings.ToLower(strings.TrimSpace(s.Model))
	if !strings.HasSuffix(run, oneMillionSuffix) {
		return
	}
	run = strings.TrimSuffix(run, oneMillionSuffix)
	said := strings.ToLower(strings.TrimSpace(m.model))
	// Two Claude ids that are not one model (neither begins the other: a transcript may add the model's date).
	if strings.HasPrefix(run, "claude-") && strings.HasPrefix(said, "claude-") && !strings.HasPrefix(said, run) && !strings.HasPrefix(run, said) {
		return
	}
	s.ContextWindow = claudeWindow1M
}
