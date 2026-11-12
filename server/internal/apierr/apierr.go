// Package apierr writes the JSON bodies of the hub's HTTP API (docs/protocol/protocol.md "Error body"): a result, or an error with a
// stable code. It is shared by the hub and the services whose requests the hub relays (internal/assistant), so an
// error reads the same whichever of them answered.
package apierr

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// ErrOffline: the machine a call was for has no connection to the hub.
var ErrOffline = errors.New("machine is offline")

// WriteJSON writes v as the response.
func WriteJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

// Write writes err with the code its status stands for.
func Write(w http.ResponseWriter, status int, err error) {
	WriteCode(w, status, "", err)
}

// WriteCode preserves error for old clients and adds a stable machine-readable code.
// An i18n.Message reads in the request's language (docs/protocol/i18n.md "Protocol").
func WriteCode(w http.ResponseWriter, status int, code string, err error) {
	l := i18n.OfWriter(w)
	body := LocalizedBody(status, code, err, l)
	if l != "" {
		// Only exact standard status text is ours to translate; arbitrary server/node details remain untouched.
		if key, ok := standardStatusKeys[status]; ok && err.Error() == http.StatusText(status) {
			body["error"] = i18n.T(l, key)
		}
	}
	WriteBody(w, status, body)
}

var standardStatusKeys = map[int]string{
	http.StatusBadRequest:            "http-status.bad_request",
	http.StatusUnauthorized:          "http-status.unauthorized",
	http.StatusForbidden:             "http-status.forbidden",
	http.StatusNotFound:              "http-status.not_found",
	http.StatusMethodNotAllowed:      "http-status.method_not_allowed",
	http.StatusRequestTimeout:        "http-status.timeout",
	http.StatusConflict:              "http-status.conflict",
	http.StatusGone:                  "http-status.gone",
	http.StatusRequestEntityTooLarge: "http-status.too_large",
	http.StatusTooManyRequests:       "http-status.rate_limited",
	http.StatusInternalServerError:   "http-status.internal",
	http.StatusBadGateway:            "http-status.bad_gateway",
	http.StatusServiceUnavailable:    "http-status.unavailable",
	http.StatusGatewayTimeout:        "http-status.timeout",
}

func WriteBody(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func Body(status int, code string, err error) map[string]any {
	if code == "" {
		code = StatusCode(status)
		if status >= 500 && code != "timeout" {
			code = NodeCode(err)
		}
	}
	body := map[string]any{"error": err.Error(), "code": code}
	switch code {
	case "invalid_token", "forbidden", "bad_request", "not_found", "method_not_allowed", "gone":
		body["retryable"] = false
	case "input_draft", "dialog_open", "prompt_changed":
		body["retryable"], body["delivered"] = false, "none"
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		body["delivered"] = "none"
	}
	if errors.Is(err, ErrOffline) {
		body["retryable"], body["delivered"] = true, "none"
	}
	return body
}

func StatusCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusUnauthorized:
		return "invalid_token"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return "timeout"
	case http.StatusConflict:
		return "conflict"
	case http.StatusGone:
		return "gone"
	case http.StatusRequestEntityTooLarge:
		return "too_large"
	case http.StatusTooManyRequests:
		return "rate_limited"
	default:
		return "internal"
	}
}

func NodeCode(err error) string {
	var ne net.Error
	var re *rpc.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	case errors.Is(err, ErrOffline), errors.Is(err, rpc.ErrClosed), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.As(err, &ne):
		return "node_unreachable"
	case errors.As(err, &re):
		// RPC loses the Go error type; recognize only the exact standard deadline message.
		if re.Message == context.DeadlineExceeded.Error() {
			return "timeout"
		}
	}
	return "internal"
}

// Call writes the error of a call that went to a machine's node (or further): offline is 503, a refusal the node
// coded is 409 with its details, anything else 502.
func Call(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	if errors.Is(err, ErrOffline) {
		status = http.StatusServiceUnavailable
	}
	code := NodeCode(err)
	var re *rpc.Error
	if errors.As(err, &re) && re.Code != "" {
		code = re.Code
		switch code {
		case "internal", "node_unreachable", "timeout":
			status = http.StatusBadGateway
		case proto.CodeTerminalNotReady: // the node's new tab would not take the start command; nothing ran
			status = http.StatusServiceUnavailable
		default:
			status = http.StatusConflict
		}
	}
	body := Body(status, code, err)
	// delivered stays what the node said (sendsafe.go): the iOS app treats "unknown" as "maybe typed" and stops
	// retrying by itself, so a bare timeout must not claim it. Newer clients read code (timeout, node_unreachable).
	if re != nil {
		// Keep refusal details flat for existing clients, including draft and clearable.
		var data map[string]any
		if json.Unmarshal(re.Data, &data) == nil {
			for key, value := range data {
				if key == "retryable" {
					if _, ok := value.(bool); !ok {
						continue
					}
				}
				if key == "delivered" && value != "none" && value != "unknown" && value != "yes" {
					continue
				}
				body[key] = value
			}
		}
		if re.Code != "" || len(re.Data) > 0 {
			body["error"] = re.Message
		}
		body["code"] = code
		if _, ok := codeActions[code]; ok {
			explain(body, code, re.Message, i18n.OfWriter(w))
		}
	}
	if re != nil && code == "assistant_control_failed" {
		// The SDK's reason must stay visible in clients that show only the summary.
		body["error"] = i18n.T(i18n.OfWriter(w), "apierr-apierr.assistant_control_failed", i18n.Args{"reason": re.Message})
		if _, hasDetail := body["detail"]; !hasDetail {
			body["detail"] = re.Message
		}
	} else if _, explained := codeActions[code]; !explained {
		localizeFailure(body, status, err, i18n.OfWriter(w))
	}
	WriteBody(w, status, body)
}

// LocalizedBody preserves all machine-readable outcome fields while translating a known failure summary.
func LocalizedBody(status int, code string, err error, l i18n.Lang) map[string]any {
	body := Body(status, code, err)
	localizeFailure(body, status, err, l)
	return body
}

var rpcFailureKeys = map[string]string{
	"timeout":          "rpc-errors.timeout",
	"node_unreachable": "rpc-errors.unreachable",
	"internal":         "rpc-errors.failed",
	"input_draft":      "rpc-errors.input_draft",
	"dialog_open":      "rpc-errors.dialog_open",
	// the first message to a Codex opened with no first message (node/codex_waiting.go, #4961)
	"codex_not_ready":          "rpc-errors.codex_not_ready",
	"codex_first_text":         "rpc-errors.codex_first_text",
	"codex_first_unconfirmed":  "rpc-errors.codex_first_unconfirmed",
	"codex_waiting":            "rpc-errors.codex_waiting",
	"codex_no_box":             "rpc-errors.codex_no_box",
	"prompt_changed":           "rpc-errors.prompt_changed",
	"attach_busy":              "rpc-errors.attach_busy",
	proto.CodeTerminalNotReady: "rpc-errors.terminal_not_ready",
	// a start's new tab stopped on its shell (node/shellsettle.go, #2570)
	proto.CodeShellWaiting:      "rpc-errors.shell_waiting",
	proto.CodeStartLineRejected: "rpc-errors.start_line_rejected",
	"node_outdated":             "rpc-errors.node_outdated",
	"readonly_unsupported":      "rpc-errors.readonly_unsupported",
	"assistant_in_doubt":        "rpc-errors.in_doubt",
	// queued messages sent now (#6088)
	"nothing_queued":        "rpc-errors.nothing_queued",
	"send_now_unsupported":  "rpc-errors.send_now_unsupported",
	"send_now_unrecognized": "rpc-errors.send_now_unrecognized", // #2467
	"send_now_in_doubt":     "rpc-errors.in_doubt",
	// a new agent's worktree (#6115)
	"not_a_repository":  "rpc-errors.not_a_repository",
	"not_our_worktree":  "rpc-errors.not_our_worktree",
	"worktree_in_use":   "rpc-errors.worktree_in_use",
	"worktree_dirty":    "rpc-errors.worktree_dirty",
	"worktree_unmerged": "rpc-errors.worktree_unmerged",
	// a Claude session outside Persona's terminals, through its peer inbox (node/claudepeer.go)
	"peer_unsupported": "rpc-errors.peer_unsupported",
	"peer_unreachable": "rpc-errors.peer_unreachable",
	"peer_too_long":    "rpc-errors.peer_too_long",
	"peer_unconfirmed": "rpc-errors.peer_unconfirmed",
	"peer_command":     "rpc-errors.peer_command",
	// a desktop app session nothing runs, which the app did not open in time (node/claudedesktop_open.go)
	"desktop_open_failed": "rpc-errors.desktop_open_failed",
	// the node menus' Git ▸ Pull / Push / Fetch and the Git pane's Pull (#1888); the *_failed ones carry git's words
	"no_directory":        "rpc-errors.no_directory",
	"detached_head":       "rpc-errors.detached_head",
	"no_upstream":         "rpc-errors.no_upstream",
	"uncommitted_changes": "rpc-errors.uncommitted_changes",
	"not_fast_forward":    "rpc-errors.not_fast_forward",
	"push_rejected":       "rpc-errors.push_rejected",
	"pull_failed":         "rpc-errors.pull_failed",
	"push_failed":         "rpc-errors.push_failed",
	"fetch_failed":        "rpc-errors.fetch_failed",
	// session.modes (node/modes.go, #1843)
	proto.CodeModesOnComputer: "rpc-errors.modes_on_computer",
	proto.CodeModesNotRunning: "rpc-errors.modes_not_running",
	// a message to a stopped agent: read-only, its terminal page's Resume brings it back (node stopped.go, #4754)
	"agent_stopped": "rpc-errors.agent_stopped",
	// a start in a directory whose name can't be typed into a terminal (node/hostorca_launch.go)
	proto.CodeDirectoryUntypable: "rpc-errors.directory_untypable",
	// a running Agent's switch to another launcher, picked and carried out by the next message (node/launcher_switch.go)
	proto.CodeSwitchStale:           "rpc-errors.switch_stale",
	proto.CodeSwitchPickModel:       "rpc-errors.switch_pick_model",
	proto.CodeSwitchUnderWay:        "rpc-errors.switch_under_way",
	proto.CodeSwitchUnsupported:     "rpc-errors.switch_unsupported",
	proto.CodeSwitchLauncherMissing: "rpc-errors.switch_launcher_missing",
	proto.CodeSwitchOtherAgent:      "rpc-errors.switch_other_agent",
	proto.CodeSwitchModelLocked:     "rpc-errors.switch_model_locked",
	proto.CodeSwitchConfigDir:       "rpc-errors.switch_config_dir",
	proto.CodeSwitchNotStopped:      "rpc-errors.switch_not_stopped",
	proto.CodeSwitchFailed:          "rpc-errors.switch_failed",
	proto.CodeSwitchFailedStopped:   "rpc-errors.switch_failed_stopped",
}

func localizeFailure(body map[string]any, status int, err error, l i18n.Lang) {
	if l == "" {
		return
	}
	original, _ := body["error"].(string)
	localized := i18n.Localize(err, l)
	// A typed structured error owns its summary; do not replace it with a generic transport message.
	if _, ok := err.(interface{ Text(i18n.Lang) string }); ok || localized != err.Error() {
		body["error"] = localized
		return
	}
	code, _ := body["code"].(string)
	key := rpcFailureKeys[code]
	if errors.Is(err, ErrOffline) {
		key = "rpc-errors.offline"
	}
	var syntax *json.SyntaxError
	var wrongType *json.UnmarshalTypeError
	if status == http.StatusBadRequest && (errors.As(err, &syntax) || errors.As(err, &wrongType) || errors.Is(err, io.EOF)) {
		key = "http-status.bad_request"
	}
	if key == "" {
		return
	}
	// a summary that names {reason} carries the original words (git's, for a failed pull or push)
	body["error"] = i18n.T(l, key, i18n.Args{"reason": original})
	if _, hasDetail := body["detail"]; !hasDetail && original != body["error"] {
		body["detail"] = original
	}
}

// RPCText localizes only canonical transport strings after a stream has lost the typed error.
func RPCText(l i18n.Lang, text string) string {
	if l == "" {
		return text
	}
	var key string
	switch text {
	case ErrOffline.Error():
		key = "rpc-errors.offline"
	case context.DeadlineExceeded.Error():
		key = "rpc-errors.timeout"
	case rpc.ErrClosed.Error(), io.EOF.Error(), io.ErrUnexpectedEOF.Error():
		key = "rpc-errors.unreachable"
	}
	if key != "" {
		return i18n.T(l, key)
	}
	return i18n.InboxText(l, text)
}

// codeActions are the refusals whose words the hub writes itself, wherever they came from (the assistant, a node),
// with what a client offers to fix each (docs/assistant/onboarding.md 4.6, 5.2).
var codeActions = map[string]string{
	proto.CodeNoMachine:       "add_computer",
	proto.CodeMachinePending:  "confirm_computer",
	proto.CodeClaudeMissing:   "install_claude",
	proto.CodeLauncherMissing: "connect_model",
}

// OnMachine is err naming the machine (its display name) it was refused on, when it is one of the refusals the hub
// words itself and does not name one yet: what the sentence says instead of "this computer". Anything else is err.
func OnMachine(err error, name string) error {
	var re *rpc.Error
	if name == "" || !errors.As(err, &re) {
		return err
	}
	if _, ok := codeActions[re.Code]; !ok {
		return err
	}
	data := map[string]any{}
	if len(re.Data) > 0 && json.Unmarshal(re.Data, &data) != nil {
		return err
	}
	if n, _ := data["machine_name"].(string); n != "" {
		return err
	}
	data["machine_name"] = name
	raw, merr := json.Marshal(data)
	if merr != nil {
		return err
	}
	return &rpc.Error{Code: re.Code, Message: re.Message, Data: raw}
}

// explain makes body say code's refusal in plain words in l (none: the default language): error is the sentence for
// people, detail what the caller wrote (the shell's own words: only for the "Details"), action what to do unless the caller
// said. The caller's data may name the machine (machine_name, OnMachine; else the text says "this computer"), the launcher,
// and where it failed (Sentence).
func explain(body map[string]any, code, message string, l i18n.Lang) {
	body["error"] = Sentence(l, code, body)
	if d, _ := body["detail"].(string); d == "" {
		body["detail"] = message
	}
	if a, _ := body["action"].(string); a == "" {
		body["action"] = codeActions[code]
	}
}

// Sentence is the plain words in l of code, one of the refusals the hub words itself ("" for any other): a start refused
// on its request with nothing to name (readonly_unsupported, directory_untypable) as it is, and codeActions' by their
// data: machine_name (else "this computer"), launcher, and of a start the new tab's shell could not run (where
// "terminal", node/shellmiss.go) what it could not run (missing, kind), that shell (pane_shell) and the shell and rc
// file the launchers are listed in (launcher_shell, rc_file). What a 409 says, and a session's start_error.
func Sentence(l i18n.Lang, code string, data map[string]any) string {
	switch code {
	case "readonly_unsupported", proto.CodeDirectoryUntypable: // the request refused, with nothing to name
		return i18n.T(l, rpcFailureKeys[code])
	}
	if code == proto.CodeShellWaiting || code == proto.CodeStartLineRejected { // a start's new tab (node/shellsettle.go, #2570)
		machine, _ := data["machine_name"].(string)
		line, _ := data["line"].(string)
		key := "apierr-apierr." + code
		if machine == "" {
			key += "_here"
		}
		return i18n.T(l, key, i18n.Args{"machine": machine, "line": line})
	}
	if _, ok := codeActions[code]; !ok {
		return ""
	}
	arg := func(k string) string { s, _ := data[k].(string); return s }
	key := "apierr-apierr." + code
	if arg("where") == "terminal" {
		v := key + "_terminal"
		if k := arg("kind"); (k == "no_such_file" || k == "permission_denied") && i18n.Has(v+"_path") {
			v += "_path" // a path the shell could not run (a managed launcher's personad)
		}
		if i18n.Has(v) {
			key = v
		}
	}
	if arg("machine_name") == "" && i18n.Has(key+"_here") {
		key += "_here" // "this computer"
	}
	pane := arg("pane_shell")
	if pane == "" && arg("where") == "terminal" { // the node could not tell which shell the tab runs
		pane = i18n.T(l, "apierr-apierr.terminal_default_shell")
	}
	return i18n.T(l, key, i18n.Args{"machine": arg("machine_name"), "launcher": arg("launcher"), "missing": arg("missing"),
		"pane_shell": pane, "launcher_shell": arg("launcher_shell"), "rc_file": arg("rc_file")})
}

// Reason is the failure in an error body read back from another hub or service: the original text when the summary
// was localized (localizeFailure moves it to detail), so a reason travels across hops instead of the generic summary.
func Reason(b []byte) string {
	var e struct {
		Error  string          `json:"error"`
		Detail json.RawMessage `json:"detail"`
	}
	if json.Unmarshal(b, &e) != nil {
		return ""
	}
	var detail string
	if json.Unmarshal(e.Detail, &detail) == nil && detail != "" {
		return detail
	}
	return e.Error
}

// Describe is an error body read back for a person with no "Details" to open (the command line): the summary and,
// when the summary was localized or explained away from it, the cause after it (#5728).
func Describe(b []byte) string {
	var e struct {
		Error  string          `json:"error"`
		Detail json.RawMessage `json:"detail"`
	}
	if json.Unmarshal(b, &e) != nil {
		return ""
	}
	var detail string
	if json.Unmarshal(e.Detail, &detail) != nil || detail == "" || detail == e.Error {
		return e.Error
	}
	if e.Error == "" {
		return detail
	}
	return e.Error + "：" + detail
}
