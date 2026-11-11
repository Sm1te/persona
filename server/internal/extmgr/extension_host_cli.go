package extmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/extension"
	larkcliadapter "github.com/nwverse/persona/server/internal/larkcli"
	"github.com/nwverse/persona/server/internal/rpc"
)

const (
	hostCLIInvalid     = "invalid_params"
	hostCLIUnavailable = "unavailable"
	hostCLIRateLimited = "rate_limited"
)

// Extension host CLIs are selected by a closed manifest ID, never by a guest's
// executable, arguments or profile. Only explicitly enabled, bounded read-only
// operations cross this boundary; no shell or credentials are exposed.
type hostCLI interface {
	CheckVersion(ctx context.Context) error
	Version(ctx context.Context) (string, error)
	AuthStatus(ctx context.Context) (larkcliadapter.AuthStatus, error)
	ListChats(ctx context.Context, limit int) (larkcliadapter.ChatList, error)
	ListMessages(ctx context.Context, chatID, thread, before string, limit int) (larkcliadapter.MessagePage, error)
	SearchLimit(ctx context.Context, kind, query string, limit int) (json.RawMessage, error)
}

type hostCLIState struct {
	mu      sync.Mutex
	runner  hostCLI
	version string
	at      time.Time
	sem     chan struct{}
}

func (s *hostCLIState) call(ctx context.Context, fn func() (any, error)) (any, error) {
	if s.sem == nil {
		s.sem = make(chan struct{}, 4)
	}
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
		return fn()
	case <-ctx.Done():
		return nil, &rpc.Error{Code: extension.CodeTimeout, Message: "host CLI: waiting for an invocation slot"}
	}
}

func (s *hostCLIState) current(ctx context.Context, makeRunner func() hostCLI) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runner == nil {
		s.runner = makeRunner()
	}
	if s.version != "" && time.Since(s.at) < time.Minute {
		return s.version, nil
	}
	version, err := s.runner.Version(ctx)
	if err != nil {
		return "", err
	}
	s.version, s.at = version, time.Now()
	return version, nil
}

func (h *Manager) hostCLI(ctx context.Context, id string, spec extension.HostIntegrationSpec) (hostCLI, error) {
	if h == nil || h.cfg == nil || id == "" {
		return nil, &rpc.Error{Code: extension.CodePermissionDenied, Message: "host CLI: missing extension identity"}
	}
	if spec.ID != "lark-cli" {
		return nil, &rpc.Error{Code: extension.CodePermissionDenied, Message: "host CLI: integration is not declared"}
	}
	if h.cfg.Lark.CLI == nil || !h.cfg.Lark.CLI.Extensions {
		return nil, &rpc.Error{Code: extension.CodePermissionDenied, Message: "host CLI: integration is not enabled by the manager"}
	}
	h.hostCLIMu.Lock()
	if h.hostCLIState == nil {
		h.hostCLIState = &hostCLIState{}
	}
	state := h.hostCLIState
	h.hostCLIMu.Unlock()
	version, err := state.current(ctx, func() hostCLI {
		return larkcliadapter.New(larkcliadapter.Config{Bin: h.cfg.Lark.CLI.Bin, Profile: h.effectiveLarkCLIProfile(ctx), Timeout: 15 * time.Second})
	})
	if err != nil {
		return nil, publicHostCLIError(err)
	}
	if !spec.Allows(version) {
		return nil, &rpc.Error{Code: extension.CodeUnsupported, Message: fmt.Sprintf("host CLI: version %s does not satisfy %s", version, spec.Version)}
	}
	return state.runner, nil
}

type hostCLIRequest struct {
	Operation string `json:"operation"`
	Query     string `json:"query"`
	Kind      string `json:"kind"`
	ChatID    string `json:"chat_id"`
	Thread    string `json:"thread"`
	Before    string `json:"before"`
	Limit     int    `json:"limit"`
}

func (h *Manager) extensionHostCLICall(ctx context.Context, id string, spec extension.HostIntegrationSpec, raw json.RawMessage) (any, error) {
	var in hostCLIRequest
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, &rpc.Error{Code: hostCLIInvalid, Message: "host CLI: invalid typed request"}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, &rpc.Error{Code: hostCLIInvalid, Message: "host CLI: invalid typed request"}
	}
	for name := range fields {
		switch name {
		case "operation", "query", "kind", "chat_id", "thread", "before", "limit":
		default:
			return nil, &rpc.Error{Code: hostCLIInvalid, Message: "host CLI: unknown input field"}
		}
	}
	if in.Limit < 0 || in.Limit > 50 {
		return nil, &rpc.Error{Code: hostCLIInvalid, Message: "host CLI: limit must be between 0 and 50"}
	}
	if len(in.Query) > 1000 || len(in.ChatID) > 256 || len(in.Thread) > 256 || len(in.Before) > 512 {
		return nil, &rpc.Error{Code: hostCLIInvalid, Message: "host CLI: input exceeds its bound"}
	}
	runner, err := h.hostCLI(ctx, id, spec)
	if err != nil {
		return nil, err
	}
	state := h.hostCLIState
	return state.call(ctx, func() (any, error) {
		return h.invokeHostCLI(ctx, spec, runner, in)
	})
}

func (h *Manager) invokeHostCLI(ctx context.Context, spec extension.HostIntegrationSpec, runner hostCLI, in hostCLIRequest) (any, error) {
	var out any
	switch in.Operation {
	case "auth_status":
		auth, err := runner.AuthStatus(ctx)
		if err != nil {
			return nil, publicHostCLIError(err)
		}
		// Keep account identity useful without exporting app IDs, token
		// metadata or a profile's configuration.
		out = map[string]any{"logged_in": auth.LoggedIn, "expired": auth.Expired, "user_name": auth.UserName}
	case "chats":
		chats, err := runner.ListChats(ctx, in.Limit)
		if err != nil {
			return nil, publicHostCLIError(err)
		}
		out = chats
	case "messages":
		if strings.TrimSpace(in.ChatID) == "" && strings.TrimSpace(in.Thread) == "" {
			return nil, &rpc.Error{Code: hostCLIInvalid, Message: "host CLI: chat_id or thread is required"}
		}
		messages, err := runner.ListMessages(ctx, in.ChatID, in.Thread, in.Before, in.Limit)
		if err != nil {
			return nil, publicHostCLIError(err)
		}
		out = messages
	case "search":
		if strings.TrimSpace(in.Query) == "" {
			return nil, &rpc.Error{Code: hostCLIInvalid, Message: "host CLI: query is required"}
		}
		switch in.Kind {
		case "", "messages", "chats", "docs", "people":
		default:
			return nil, &rpc.Error{Code: hostCLIInvalid, Message: "host CLI: unsupported search kind"}
		}
		data, err := runner.SearchLimit(ctx, in.Kind, in.Query, in.Limit)
		if err != nil {
			return nil, publicHostCLIError(err)
		}
		if len(data) > 1<<20 {
			return nil, &rpc.Error{Code: hostCLIInvalid, Message: "host CLI: result exceeds 1 MiB"}
		}
		out = map[string]json.RawMessage{"data": data}
	default:
		return nil, &rpc.Error{Code: hostCLIInvalid, Message: "host CLI: unsupported operation"}
	}
	return boundedHostCLIResult(map[string]any{"integration": spec.ID, "result": out})
}

func boundedHostCLIResult(answer any) (any, error) {
	raw, err := json.Marshal(answer)
	if err != nil || len(raw) > 1<<20 {
		return nil, &rpc.Error{Code: hostCLIInvalid, Message: "host CLI: result exceeds 1 MiB"}
	}
	return answer, nil
}

// publicHostCLIError deliberately drops command output, paths and stderr. The
// adapter's classification is sufficient for the extension to tell a missing
// login from a transient failure.
func publicHostCLIError(err error) error {
	if err == nil {
		return nil
	}
	kind := larkcliadapter.KindOf(err)
	var adapterErr *larkcliadapter.Error
	_ = errors.As(err, &adapterErr)
	code := hostCLIUnavailable
	switch kind {
	case larkcliadapter.KindNotInstalled, larkcliadapter.KindUnsupportedVersion:
		code = extension.CodeUnsupported
	case larkcliadapter.KindNotLoggedIn, larkcliadapter.KindExpired, larkcliadapter.KindMissingPermission:
		code = extension.CodePermissionDenied
	case larkcliadapter.KindRateLimited:
		code = hostCLIRateLimited
	case larkcliadapter.KindTimeout:
		code = extension.CodeTimeout
	}
	return &rpc.Error{Code: code, Message: "host CLI: " + string(kind)}
}
