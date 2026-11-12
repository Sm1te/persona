package node

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/transcript"
)

// creationResult covers synchronous terminal, async registration, launcher
// runtimes and engine runtimes. Resume never establishes new ownership.
func (n *Node) creationResult(s *proto.Session, err error, resume, creation, incarnation string) (any, error) {
	out := map[string]any{"session": s}
	result, id := "start_failed", ""
	if s != nil {
		id = s.SessionID
	}
	if err != nil || s == nil || resume != "" || creation == "" || incarnation == "" {
		if err == nil && s != nil {
			result = "identity_missing"
			if resume != "" {
				result = "resume_skipped"
			}
		}
		devicewire.ShareCreationLog(n.logf, id, "node_receipt", result)
		return out, err
	}
	state, ok := n.state.get(s.SessionID)
	if ok && state.CreationID != "" && (state.CreationID != creation || state.Incarnation != incarnation) {
		devicewire.ShareCreationLog(n.logf, id, "node_receipt", "identity_conflict")
		return out, err
	}
	if !ok {
		state = sessionState{Launcher: s.Launcher, Cwd: s.Cwd, StartedAt: time.Now().UTC()}
	}
	state.CreationID, state.Incarnation = creation, incarnation
	persistAvailable := n.state.put(s.SessionID, state)
	result = "state_updated"
	if !persistAvailable {
		result = "persist_unavailable"
	}
	devicewire.ShareCreationLog(n.logf, id, "node_receipt", result)
	out["creation_id"], out["incarnation"] = creation, incarnation
	return out, nil
}

func (n *Node) shareProjection(ctx context.Context, params json.RawMessage) (devicewire.ShareProjection, error) {
	var p devicewire.ShareProjectionRequest
	if err := json.Unmarshal(params, &p); err != nil {
		return devicewire.ShareProjection{}, err
	}
	deny := func(reason string) (devicewire.ShareProjection, error) {
		return devicewire.ShareProjection{Reason: reason, Messages: []devicewire.ShareProjectionMessage{}}, nil
	}
	_, codex := n.codexSource(p.SessionID)
	runtimeTranscript := n.runtimes.transcriptOf(p.SessionID)
	if p.Incarnation == "" || (!validID(p.SessionID) && !codex && runtimeTranscript == "") {
		return deny("source_unsupported")
	}
	if state, ok := n.state.get(p.SessionID); ok && state.Incarnation != "" {
		if p.Inspect {
			p.Incarnation = state.Incarnation
		} else if state.Incarnation != p.Incarnation {
			return deny("source_changed")
		}
	}
	// Runtime-native Claude transcripts use the same complete schema-backed
	// source. Runtime Item streams alone have no complete checkpoint contract.
	path := n.transcriptPath(p.SessionID)
	if path == "" {
		return deny("source_incomplete")
	}
	return transcript.CaptureShareProjection(ctx, path, p.Incarnation, p.Previous)
}
