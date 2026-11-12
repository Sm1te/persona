package assistant

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// "Test connection" / "Fetch models" for a provider being added or edited (#2455, docs/integrations/launchers.md): the manager itself talks to the
// gateway. POST /api/v1/launchers/test sends a short request using the configured protocol;
// POST /api/v1/launchers/discover reads model pages and fills in metadata from the built-in catalog.
// Neither ever puts the key or a header value in what it answers.

// probeTimeout bounds one request to the gateway (a var for the tests).
var probeTimeout = 30 * time.Second

// probeInput is the provider to talk to: a stored one by name, what the request gives overriding it.
type probeInput struct {
	Name    string  `json:"name"`
	API     string  `json:"api"`
	BaseURL string  `json:"base_url"`
	Auth    string  `json:"auth"`
	APIKey  string  `json:"api_key"`
	Headers *string `json:"headers"`
	Model   string  `json:"model"`
	// Preset fills in the address and the model left empty (GET /api/v1/launchers/presets), so a key alone is tested.
	Preset string `json:"preset"`
	// ViaMachine: the computer the requests go through (models_via.go); nil keeps the stored one, "" this manager.
	ViaMachine *string `json:"via_machine"`
}

// probeTarget is a resolved gateway: everything needed for one request.
type probeTarget struct {
	base, key, headers, model, proxy, noProxy, api, kind, via string
	// through sends the requests through via's node (models_via.go).
	through http.RoundTripper
}

func (s *Service) probeTarget(ctx context.Context, in probeInput) (probeTarget, error) {
	var t probeTarget
	if in.Name != "" {
		list := s.storedLaunchers(ctx)
		i := slices.IndexFunc(list, func(l storedLauncher) bool { return l.Name == in.Name })
		if i < 0 {
			return t, i18n.Err("assistant-models_probe.err_no_provider", i18n.Args{"name": in.Name})
		}
		l := list[i]
		key, err := s.openLauncherKey(ctx, l.KeySealed, l.Name, "")
		if err != nil {
			if _, ref := s.keyReference(l.KeySealed); ref {
				return t, err // the provider extension's words
			}
			return t, i18n.Err("assistant-models_probe.err_open_key")
		}
		headers, err := s.openKey(l.HeadersSealed)
		if err != nil {
			return t, i18n.Err("assistant-models_probe.err_open_headers")
		}
		t = probeTarget{base: l.BaseURL, key: key, headers: headers, model: l.Model, proxy: l.Proxy, noProxy: l.NoProxy, api: l.API, kind: l.Kind, via: l.ViaMachine}
		if t.kind == "" {
			t.kind = "launcher" // Stored entries without kind are legacy launchers.
		}
	}
	if b := strings.TrimSpace(in.BaseURL); b != "" {
		// The stored key and headers only go to the stored gateway: another address gets just what the request brings.
		// A trailing /v1 is the same gateway (saving drops it from a Messages base); the host never differs.
		if in.Name != "" && proto.MessagesBaseURL(b) != proto.MessagesBaseURL(t.base) {
			t.key, t.headers = "", ""
		}
		t.base = b
	}
	if k := strings.TrimSpace(in.APIKey); k != "" {
		t.key = k
	}
	if in.Headers != nil {
		t.headers = *in.Headers
	}
	if in.ViaMachine != nil {
		t.via = strings.TrimSpace(*in.ViaMachine)
	}
	if m := strings.TrimSpace(in.Model); m != "" {
		t.model = m
	}
	if in.Preset != "" {
		pr, ok := s.presetByID(ctx, in.Preset)
		if !ok {
			return t, errNoPreset
		}
		if in.Name == "" {
			t.kind = pr.Kind
			if t.kind == "" {
				t.kind = "launcher"
			}
		}
		if strings.TrimSpace(t.base) == "" {
			t.base = pr.BaseURL
		}
		if t.model == "" {
			t.model = presetModel(pr)
		}
		if t.api == "" {
			t.api = pr.API
		}
	}
	if in.API != "" {
		t.api = in.API
	}
	if t.api != "" && t.api != "messages" && t.api != "responses" && t.api != "chat" {
		return t, i18n.Err("assistant-models_launchers.err_api")
	}
	// An anonymous Messages probe also tests launchers, so only a stored provider or
	// preset identifies it as direct API. Anonymous Responses calls are direct API.
	if t.kind == "" && t.api == "responses" {
		t.kind = "api"
	}
	if err := checkAPITransport(t.kind, t.api, t.proxy, t.noProxy, strings.TrimSpace(t.headers) != ""); err != nil {
		return t, err
	}
	// Both credential headers go anyway (cindy's buildProbeRequest), so Auth only tidies a pasted "Bearer …".
	t.key = strings.TrimSpace(strings.TrimPrefix(t.key, "Bearer "))
	if err := checkGateway(t.base); err != nil {
		return t, err
	}
	if err := checkHeaders(t.headers); err != nil {
		return t, err
	}
	t.base = strings.TrimRight(strings.TrimSpace(t.base), "/")
	if t.via != "" {
		t.through = viaNode{s: s, machine: t.via}
	}
	return t, nil
}

// endpoint is <base>/v1/<path>, a base already ending in /v1 not doubled. A Messages base is read as the node hands it
// to Claude Code (proto.MessagesBaseURL), so a test passes for the address Agents will call.
func (t probeTarget) endpoint(path string) string {
	if messagesAPI(t.api) {
		return proto.MessagesBaseURL(t.base) + "/v1/" + path
	}
	if strings.HasSuffix(t.base, "/v1") {
		return t.base + "/" + path
	}
	return t.base + "/v1/" + path
}

// modelsEndpoint keeps discovery separate from the inference protocol. DeepSeek documents /models at the
// official origin, not under its /anthropic Messages base (https://api-docs.deepseek.com/api/list-models/).
// Only its known HTTPS bases qualify; custom gateway prefixes and authorities stay untouched.
func (t probeTarget) modelsEndpoint() string {
	u, err := url.Parse(t.base)
	if err == nil && u.Scheme == "https" && u.User == nil && u.RawQuery == "" && u.Fragment == "" &&
		(strings.EqualFold(u.Host, "api.deepseek.com") || strings.EqualFold(u.Host, "api.deepseek.com:443")) {
		switch u.EscapedPath() {
		case "", "/v1", "/anthropic", "/anthropic/v1":
			u.Path, u.RawPath = "/models", ""
			return u.String()
		}
	}
	return t.endpoint("models")
}

// do sends one request with the custom headers first, then the version and both credential headers.
func (t probeTarget) do(ctx context.Context, method, endpoint string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	for _, hd := range parseHeaders(t.headers) {
		req.Header.Set(hd[0], hd[1])
	}
	if req.Header.Get("anthropic-version") == "" {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	if body != nil {
		req.Header.Set("content-type", "application/json")
		if t.api == "responses" || t.api == "chat" {
			req.Header.Set("accept", "text/event-stream")
		}
	}
	if t.key != "" {
		req.Header.Set("x-api-key", t.key)
		req.Header.Set("authorization", "Bearer "+t.key)
	}
	// A redirect is answered as is, never followed: Go keeps x-api-key and the custom headers on a redirect to another
	// host, and the key must only go to the gateway asked.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if t.proxy != "" {
		// A gateway behind its own proxy: a one-off transport that keeps no connection behind.
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.DisableKeepAlives = true
		if u, err := url.Parse(t.proxy); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
		client.Transport = transport
	}
	if t.through != nil {
		client.Transport = t.through
	}
	return client.Do(req)
}

// parseHeaders reads "Name: value" lines (ANTHROPIC_CUSTOM_HEADERS); lines without a name are skipped.
func parseHeaders(raw string) [][2]string {
	var out [][2]string
	for _, line := range strings.Split(raw, "\n") {
		if name, value, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && strings.TrimSpace(name) != "" {
			out = append(out, [2]string{strings.TrimSpace(name), strings.TrimSpace(value)})
		}
	}
	return out
}

// redact takes the key and every header value out of a message about the gateway.
func (t probeTarget) redact(s string) string {
	secrets := []string{t.key}
	for _, hd := range parseHeaders(t.headers) {
		secrets = append(secrets, hd[1])
	}
	for _, v := range secrets {
		if len(v) >= 4 {
			s = strings.ReplaceAll(s, v, "***")
		}
	}
	return s
}

// pt is an assistant-models_probe text in l.
func pt(l i18n.Lang, key string, args ...i18n.Args) string {
	return i18n.T(l, "assistant-models_probe."+key, args...)
}

// probeCodeRegionBlocked is the code of a failure that is Anthropic refusing the network's region before looking at
// the key (#4482): no key fixes it, another vendor does.
const probeCodeRegionBlocked = "region_blocked"

// failure is what went wrong, said for a person (in ctx's language): the status first, then what the gateway said
// (redacted, clipped); code is set for the failures a client can do more about (probeCodeRegionBlocked).
func (t probeTarget) failure(ctx context.Context, res *http.Response, err error) (msg, code string) {
	l := i18n.From(ctx)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			return pt(l, "timeout", i18n.Args{"seconds": int(probeTimeout / time.Second)}), ""
		}
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err // the URL is the user's own; the cause is what helps
		}
		return clip(oneLine(t.redact(pt(l, "unreachable", i18n.Args{"error": err.Error()}))), 300), ""
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	if res.StatusCode == http.StatusForbidden && regionBlocked(raw, t.atAnthropic()) {
		// The gateway's own words ("Request not allowed") only mislead here, so they are left out.
		return pt(l, "region_blocked", i18n.Args{"vendors": regionOpenVendors(l)}), probeCodeRegionBlocked
	}
	said := gatewayMessage(raw)
	var head string
	switch res.StatusCode {
	case http.StatusUnauthorized:
		head = pt(l, "status_401")
		code = "auth"
	case http.StatusForbidden:
		head = pt(l, "status_403")
		code = "auth"
	case http.StatusNotFound:
		head = pt(l, "status_404")
		code = "not_found"
	case http.StatusMethodNotAllowed, http.StatusUnsupportedMediaType:
		head = pt(l, "status_protocol", i18n.Args{"status": res.StatusCode})
		code = "wrong_protocol"
	case http.StatusTooManyRequests:
		head = pt(l, "status_429")
	default:
		head = pt(l, "status_other", i18n.Args{"status": res.StatusCode})
	}
	if said != "" {
		head = pt(l, "said", i18n.Args{"head": head, "said": said})
	}
	return clip(oneLine(t.redact(head)), 300), code
}

// atAnthropic: the target is Anthropic's own API (not a relay or another vendor).
func (t probeTarget) atAnthropic() bool {
	u, err := url.Parse(strings.TrimSpace(t.base))
	return err == nil && strings.EqualFold(u.Hostname(), "api.anthropic.com")
}

// regionBlocked: a 403 body is Anthropic's region block, {"type":"error","error":{"type":"forbidden","message":
// "Request not allowed"}}, sent before the key is looked at. A key without access is a permission_error with another
// message, and other vendors' 403s say other things, so those keep the plain 403 text. Without the "forbidden" type
// the bare phrase is generic, so it only counts when the target is Anthropic itself (anthropic).
func regionBlocked(raw []byte, anthropic bool) bool {
	var body struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return false
	}
	typ := strings.ToLower(strings.TrimSpace(body.Error.Type))
	return (typ == "forbidden" || typ == "" && anthropic) &&
		strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(body.Error.Message), "."), "Request not allowed")
}

// regionOpenVendors names, in l, the built-in vendors that open without a VPN or a foreign card where Anthropic says
// no (the presets whose region is not global), in the file's order.
func regionOpenVendors(l i18n.Lang) string {
	var names []string
	for _, pr := range builtinLauncherPresets(l) {
		if i := slices.IndexFunc(builtinPresets(), func(b builtinPreset) bool { return b.ID == pr.ID }); i >= 0 && builtinPresets()[i].Region != "global" {
			names = append(names, pr.DisplayName)
		}
	}
	return strings.Join(names, pt(l, "list_sep"))
}

// gatewayMessage is the error message in a gateway's answer ({"error":{"message"}}, {"error":"…"}, {"message"}), or
// the start of the body.
func gatewayMessage(raw []byte) string {
	var body struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(raw, &body) == nil {
		var e struct {
			Message string `json:"message"`
		}
		var s string
		switch {
		case json.Unmarshal(body.Error, &e) == nil && e.Message != "":
			return e.Message
		case json.Unmarshal(body.Error, &s) == nil && s != "":
			return s
		case body.Message != "":
			return body.Message
		}
	}
	return clip(oneLine(string(raw)), 160)
}

// providerTest sends a short request using the configured protocol.
func (s *Service) providerTest(ctx context.Context, in probeInput) map[string]any {
	out := map[string]any{"ok": false, "ms": 0, "model": in.Model}
	t, err := s.probeTarget(ctx, in)
	if err != nil {
		out["error"] = i18n.Localize(err, i18n.From(ctx))
		return out
	}
	if out["model"] = t.model; t.model == "" {
		out["error"] = i18n.T(i18n.From(ctx), "assistant-models_launchers.err_model_required")
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	path := "messages"
	payload := map[string]any{"model": t.model, "max_tokens": 1,
		"messages": []any{map[string]any{"role": "user", "content": "ping"}}}
	if t.api == "responses" {
		path = "responses"
		// The same shape as the real calls (ai.respond: the system prompt as a developer message), so a key that
		// passes is one those calls work with.
		payload = map[string]any{"model": t.model, "input": []any{map[string]any{"role": "developer", "content": "Reply in one word."},
			map[string]any{"role": "user", "content": "ping"}}, "stream": true, "store": false}
	} else if t.api == "chat" {
		path = "chat/completions"
		payload = map[string]any{"model": t.model, "messages": []any{map[string]any{"role": "user", "content": "ping"}}, "stream": true, "max_tokens": 256}
	}
	body, _ := json.Marshal(payload)
	started := time.Now()
	res, err := t.do(ctx, http.MethodPost, t.endpoint(path), bytes.NewReader(body))
	if err == nil && t.api == "chat" && res.StatusCode == http.StatusBadRequest {
		raw, readErr := io.ReadAll(io.LimitReader(res.Body, 4096))
		res.Body.Close()
		if readErr == nil && ai.ChatRequiresCompletionTokens(raw) {
			delete(payload, "max_tokens")
			payload["max_completion_tokens"] = 256
			body, _ = json.Marshal(payload)
			res, err = t.do(ctx, http.MethodPost, t.endpoint(path), bytes.NewReader(body))
		} else {
			res.Body = io.NopCloser(bytes.NewReader(raw))
		}
	}
	if err != nil || res.StatusCode/100 != 2 {
		msg, code := t.failure(ctx, res, err)
		out["error"] = msg
		if code != "" {
			out["code"] = code
		}
	} else if t.api == "responses" || t.api == "chat" {
		if t.api == "chat" {
			out["ok"] = chatProbeCompleted(res.Body)
		} else {
			out["ok"] = responsesProbeCompleted(res.Body)
		}
		if out["ok"] == false {
			if ctx.Err() != nil {
				out["error"], _ = t.failure(ctx, nil, ctx.Err())
			} else {
				out["error"] = pt(i18n.From(ctx), "response_unfinished")
			}
		}
	} else {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
		out["ok"] = true
	}
	if res != nil {
		res.Body.Close()
	}
	out["ms"] = int(time.Since(started).Milliseconds())
	return out
}

// responsesProbeCompleted requires the same terminal event as the runtime, within the response budget, reading the
// stream's events the way the runtime does (ai.SSEEvents).
func responsesProbeCompleted(body io.Reader) bool {
	completed := false
	err := ai.SSEEvents(io.LimitReader(body, 8<<20), func(data string) bool {
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(data), &event) != nil {
			return false
		}
		switch event.Type {
		case "response.completed":
			completed = true
			return true
		case "response.failed", "response.incomplete", "error":
			return true
		}
		return false
	})
	return err == nil && completed
}

// chatProbeCompleted follows the first choice to a terminal event, including errors after its finish reason.
func chatProbeCompleted(body io.Reader) bool {
	sc := bufio.NewScanner(io.LimitReader(body, 8<<20))
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	finished, hasText := false, false
	var lines []string
	// An SSE event can span several data lines. A terminal result returns stop=true.
	consume := func() (stop, success bool) {
		if len(lines) == 0 {
			return false, false
		}
		data := strings.Join(lines, "\n")
		lines = nil
		if strings.TrimSpace(data) == "[DONE]" {
			return true, hasText
		}
		var event struct {
			Error   json.RawMessage `json:"error"`
			Choices []struct {
				Index        int    `json:"index"`
				FinishReason string `json:"finish_reason"`
				Delta        struct {
					Content string `json:"content"`
					Refusal string `json:"refusal"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &event) != nil || len(event.Error) > 0 && string(event.Error) != "null" {
			return true, false
		}
		for _, choice := range event.Choices {
			if choice.Index != 0 {
				continue
			}
			if choice.FinishReason == "content_filter" || choice.Delta.Refusal != "" {
				return true, false
			}
			hasText = hasText || strings.TrimSpace(choice.Delta.Content) != ""
			finished = finished || choice.FinishReason != ""
		}
		return false, false
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if stop, success := consume(); stop {
				return success
			}
			continue
		}
		if data, ok := strings.CutPrefix(line, "data:"); ok {
			lines = append(lines, strings.TrimPrefix(data, " "))
		}
	}
	if sc.Err() != nil {
		return false
	}
	if stop, success := consume(); stop {
		return success
	}
	return hasText && finished
}

// providerDiscover reads the provider's model-list endpoint, its models filled in from the catalog.
func (s *Service) providerDiscover(ctx context.Context, in probeInput) map[string]any {
	models := []proto.ProviderModel{}
	enriched := func() []proto.ProviderModel {
		out := make([]proto.ProviderModel, len(models))
		for i, m := range models {
			out[i] = enrichDiscoveredModel(m)
		}
		return out
	}
	fail := func(msg string) map[string]any { return map[string]any{"models": enriched(), "error": msg} }
	t, err := s.probeTarget(ctx, in)
	if err != nil {
		return fail(i18n.Localize(err, i18n.From(ctx)))
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	// failed is fail with what failure says, and its code when it has one
	failed := func(res *http.Response, err error) map[string]any {
		msg, code := t.failure(ctx, res, err)
		if err == nil && res != nil && res.StatusCode == http.StatusNotFound {
			msg = pt(i18n.From(ctx), "models_not_found")
		}
		out := fail(msg)
		if code != "" {
			out["code"] = code
		}
		return out
	}
	const maxPages = 100
	seenModels := map[string]int{}
	seenCursors := map[string]bool{}
	query := url.Values{}
	modelsEndpoint := t.modelsEndpoint()
	for page := 0; page < maxPages; page++ {
		endpoint := modelsEndpoint
		if len(query) > 0 {
			endpoint += "?" + query.Encode()
		}
		res, err := t.do(ctx, http.MethodGet, endpoint, nil)
		if err != nil || res.StatusCode/100 != 2 {
			out := failed(res, err)
			if res != nil {
				res.Body.Close()
			}
			return out
		}
		raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
		res.Body.Close()
		if err != nil {
			return failed(nil, err)
		}
		listed, ok := parseModelsList(raw)
		if !ok {
			return fail(pt(i18n.From(ctx), "models_unreadable"))
		}
		for _, m := range listed {
			if i, ok := seenModels[m.ID]; ok {
				models[i] = mergeDiscoveredMetadata(models[i], m)
			} else {
				seenModels[m.ID] = len(models)
				models = append(models, m)
			}
		}
		var meta struct {
			HasMore            bool            `json:"has_more"`
			LastID             string          `json:"last_id"`
			Next               json.RawMessage `json:"next"`
			NextCursor         string          `json:"next_cursor"`
			NextPage           json.RawMessage `json:"next_page"`
			NextPageToken      string          `json:"next_page_token"`
			NextPageTokenCamel string          `json:"nextPageToken"`
			Links              json.RawMessage `json:"links"`
		}
		if len(bytes.TrimSpace(raw)) > 0 && bytes.TrimSpace(raw)[0] == '{' {
			if err := json.Unmarshal(raw, &meta); err != nil {
				return fail(pt(i18n.From(ctx), "models_pagination"))
			}
		}
		query = url.Values{}
		var nextPage string
		if len(meta.NextPage) > 0 && string(meta.NextPage) != "null" {
			if json.Unmarshal(meta.NextPage, &nextPage) != nil {
				var n json.Number
				if json.Unmarshal(meta.NextPage, &n) != nil {
					return fail(pt(i18n.From(ctx), "models_pagination"))
				}
				nextPage = n.String()
			}
		}
		var next string
		if len(meta.Next) > 0 && string(meta.Next) != "null" {
			if json.Unmarshal(meta.Next, &next) != nil {
				var n json.Number
				if json.Unmarshal(meta.Next, &n) != nil {
					return fail(pt(i18n.From(ctx), "models_pagination"))
				}
				if nextPage == "" {
					nextPage = n.String()
				}
			}
		}
		if next == "" && len(meta.Links) > 0 {
			var links struct {
				Next string `json:"next"`
			}
			if json.Unmarshal(meta.Links, &links) == nil {
				next = links.Next
			}
		}
		switch {
		case meta.NextCursor != "":
			query.Set("cursor", meta.NextCursor)
		case meta.NextPageToken != "":
			query.Set("page_token", meta.NextPageToken)
		case meta.NextPageTokenCamel != "":
			query.Set("pageToken", meta.NextPageTokenCamel)
		case nextPage != "":
			query.Set("page", nextPage)
		case next != "":
			base, _ := url.Parse(modelsEndpoint)
			ref, err := url.Parse(next)
			if err != nil {
				return fail(pt(i18n.From(ctx), "models_pagination"))
			}
			resolved := base.ResolveReference(ref)
			if resolved.User != nil || resolved.Scheme != base.Scheme || resolved.Host != base.Host || resolved.Path != base.Path || resolved.Fragment != "" || resolved.RawQuery == "" {
				return fail(pt(i18n.From(ctx), "models_pagination"))
			}
			query = resolved.Query()
		case meta.HasMore && meta.LastID != "":
			query.Set("after_id", meta.LastID)
		case meta.HasMore:
			return fail(pt(i18n.From(ctx), "models_pagination"))
		default:
			return map[string]any{"models": enriched()}
		}
		cursor := query.Encode()
		if seenCursors[cursor] {
			return fail(pt(i18n.From(ctx), "models_pagination"))
		}
		seenCursors[cursor] = true
	}
	return fail(pt(i18n.From(ctx), "models_pagination"))
}

// mergeDiscoveredMetadata keeps the first value and fills gaps from a repeated model.
func mergeDiscoveredMetadata(first, next proto.ProviderModel) proto.ProviderModel {
	if first.Name == "" {
		first.Name = next.Name
	}
	if first.ContextWindow == 0 {
		first.ContextWindow = next.ContextWindow
	}
	return first
}

// enrichDiscoveredModel prefers an exact catalog window and keeps the server's other metadata.
// A window inherited from an earlier generation only fills a gap; a window the server reported wins.
func enrichDiscoveredModel(m proto.ProviderModel) proto.ProviderModel {
	if c, ok := catalogModel(m.ID); ok && c.InheritedFrom == "" && c.ContextWindow > 0 {
		m.ContextWindow = c.ContextWindow
	}
	return enrichModel(m)
}

// parseModelsList reads the three shapes of a model list (cindy's parseModelsListResponse): {"data":[…]},
// {"models":[…]} and a bare array, of objects or of id strings; duplicates dropped, order kept.
func parseModelsList(raw []byte) ([]proto.ProviderModel, bool) {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		var wrapped struct {
			Data   []json.RawMessage `json:"data"`
			Models []json.RawMessage `json:"models"`
		}
		if json.Unmarshal(raw, &wrapped) != nil {
			return nil, false
		}
		switch {
		case wrapped.Data != nil:
			list = wrapped.Data
		case wrapped.Models != nil:
			list = wrapped.Models
		default:
			return nil, false
		}
	}
	var out []proto.ProviderModel
	seen := map[string]int{}
	for _, item := range list {
		var m proto.ProviderModel
		var id string
		if json.Unmarshal(item, &id) != nil {
			var fields map[string]json.RawMessage
			if json.Unmarshal(item, &fields) != nil {
				continue
			}
			str := func(k string) string {
				var s string
				_ = json.Unmarshal(fields[k], &s)
				return strings.TrimSpace(s)
			}
			num := func(k string) int {
				var f float64
				if json.Unmarshal(fields[k], &f) != nil || f <= 0 || f > 1e9 {
					return 0
				}
				return int(f)
			}
			for _, k := range []string{"id", "slug", "key", "model_name", "model_id"} {
				if id = str(k); id != "" {
					break
				}
			}
			if id == "" {
				continue
			}
			m.Name = str("display_name")
			if m.Name == "" {
				m.Name = str("name")
			}
			if m.Name == id {
				m.Name = ""
			}
			for _, k := range []string{"max_model_len", "context_length", "context_window", "max_context_length", "max_input_tokens"} {
				if m.ContextWindow = num(k); m.ContextWindow != 0 {
					break
				}
			}
		}
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		m.ID = id
		if i, ok := seen[id]; ok {
			out[i] = mergeDiscoveredMetadata(out[i], m)
			continue
		}
		seen[id] = len(out)
		out = append(out, m)
	}
	return out, true
}

func (s *Service) providerProbeRoutes() {
	s.handle("POST /api/v1/launchers/test", s.probeRoute(s.providerTest))
	s.handle("POST /api/v1/launchers/discover", s.probeRoute(s.providerDiscover))
}

// probeRoute is a test / discover handler: the request is a probeInput, the answer always 200 with ok / error inside.
func (s *Service) probeRoute(run func(context.Context, probeInput) map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in probeInput
		if err := decodeBody(w, r, &in, 1<<20); err != nil {
			writeBodyErr(w, err, i18n.Err("assistant-models_probe.err_bad_request"))
			return
		}
		// the answers' texts read in the request's language, else the user's (failure, providerTest)
		apierr.WriteJSON(w, run(i18n.With(r.Context(), s.requestCtxLang(r.Context())), in))
	}
}
