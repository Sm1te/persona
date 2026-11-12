package assistant

import (
	"cmp"
	"encoding/json"
	"errors"
	"maps"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/shellcmd"
)

// The formats a pasted configuration is read in (models_import_parse.go): pure functions of the text, none of which
// touches the store or a node, so a later import link can run its decoded JSON through parseCindy.

// ---- Claude Code launches: shell lines and settings.json ----

// launchGateway is a Claude Code launch's gateway address (its env's ANTHROPIC_BASE_URL) as a draft stores it, or
// the note code why it makes no draft: none, a shell expansion, a user in it.
func launchGateway(env map[string]string) (base, code string) {
	raw := strings.TrimSpace(env["ANTHROPIC_BASE_URL"])
	switch {
	case raw == "":
		return "", noteNoGateway
	case shellExpands(raw):
		return "", noteURLShell
	case urlWithUser(raw):
		return "", noteURLUser
	}
	return proto.MessagesBaseURL(raw), ""
}

// groupDraft is an alias group (or one launch, newImportGroup) as a draft: the fields an import would save, the key in
// the clear and the headers that are no credentials. Name is left to the caller (aliases only).
func groupDraft(g importGroup) pasteDraft {
	l := g.ManagedLauncher
	d := pasteDraft{EnvNames: g.EnvNames, SkippedEnv: g.SkippedEnv, Provider: pastedProvider{
		Kind: "launcher", Agent: "claude", API: "messages", BaseURL: l.BaseURL, Auth: l.Auth, Model: l.Model, Models: l.Models,
		OpusModel: l.OpusModel, SonnetModel: l.SonnetModel, HaikuModel: l.HaikuModel, FableModel: l.FableModel,
		SmallModel: l.SmallModel, SubagentModel: l.SubagentModel, SubagentForce: l.SubagentForce,
		CustomModel: l.CustomModel, CustomModelName: l.CustomModelName, CustomModelDescription: l.CustomModelDescription,
		Effort: l.Effort, CompactWindow: l.CompactWindow, MaxContext: l.MaxContext, TimeoutMS: l.TimeoutMS,
		Proxy: l.Proxy, NoProxy: l.NoProxy, Args: l.Args, Env: g.env,
	}}
	var hs []pasteHeader
	for line := range strings.Lines(g.headers) {
		if h, ok := headerLine(line); ok {
			hs = append(hs, h)
		}
	}
	d.Provider.Headers, d.SkippedHeaders = pasteHeaders(hs, false, false)
	d.setKey(g.key)
	return d
}

// envDraft is the draft of env alone (no claude command: export lines, a .env, settings.json's env), or the note code
// why there is none (launchGateway's).
func envDraft(env map[string]string) (pasteDraft, string) {
	base, code := launchGateway(env)
	if code != "" {
		return pasteDraft{}, code
	}
	return groupDraft(newImportGroup("", zshrcAlias{Env: env}, base)), ""
}

// setsClaude: env has a variable of Claude Code's (ANTHROPIC_…, CLAUDE_…).
func setsClaude(env map[string]string) bool {
	for k := range env {
		if strings.HasPrefix(k, "ANTHROPIC_") || strings.HasPrefix(k, "CLAUDE_") {
			return true
		}
	}
	return false
}

// shellLaunch is one claude command of pasted shell lines: an alias (named by alias) or a command line, defined on
// line, with the variables it sets itself.
type shellLaunch struct {
	alias string
	line  int
	env   map[string]string
	args  []string
}

// withEnv is env with own over it.
func withEnv(env, own map[string]string) map[string]string {
	out := maps.Clone(env)
	maps.Copy(out, own)
	return out
}

// parseShell reads shell lines (an rc file's, a .env, a command): export and NAME=value lines collect an env E in
// order; each claude alias (with E as it is at the end, as when the alias runs) and each claude command line (with E
// as it is there) is a draft, the variables it sets itself over E's. Aliases group as an import groups them
// (mergeZshrcAliases), but a group whose aliases differ in more than the model is one draft per alias. Without any
// claude command, E alone is the draft. Lines it does not use are noted (in text order), comments and blank lines
// aside.
func parseShell(lines []shellcmd.Line) pasteResult {
	res := pasteResult{Format: "shell"}
	type placed struct {
		at   int
		note pasteNote
	}
	var told []placed
	tell := func(at int, code string, line int, name string) {
		told = append(told, placed{at, pasteNote{Code: code, Line: line, Name: name}})
	}
	env, setAt := map[string]string{}, map[string]int{}
	var aliases, commands []shellLaunch
	recognized := false
	for _, l := range lines {
		if t := strings.TrimSpace(l.Text); t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		segs, ok := shellcmd.Split(l.Text)
		if !ok {
			tell(l.Start, noteLineUnused, l.Start, "")
			continue
		}
		for _, seg := range segs {
			words := seg.Words
			switch {
			case len(words) == 0:
			case words[0] == "alias":
				defs := shellcmd.AliasDefs(words)
				if len(defs) == 0 {
					tell(l.Start, noteLineUnused, l.Start, "")
				}
				for _, d := range defs {
					recognized = true
					a, ok := shellcmd.ClaudeCommand(d.Value)
					if !ok {
						tell(l.Start, noteNotClaude, 0, d.Name)
						continue
					}
					// Defined again: the last definition counts, where it is (as the shell keeps it).
					aliases = append(slices.DeleteFunc(aliases, func(o shellLaunch) bool { return o.alias == d.Name }),
						shellLaunch{alias: d.Name, line: l.Start, env: a.Env, args: a.Args})
				}
			case words[0] == "export" || !slices.ContainsFunc(words, func(w string) bool { return !shellcmd.AssignRe.MatchString(w) }):
				for _, w := range words {
					if k, v, ok := strings.Cut(w, "="); ok && shellcmd.AssignRe.MatchString(w) {
						env[k], setAt[k], recognized = v, l.Start, true
					}
				}
			default:
				own := map[string]string{}
				i := shellcmd.CommandIndex(words, own)
				if i < 0 || filepath.Base(words[i]) != "claude" {
					tell(l.Start, noteNotClaude, l.Start, "")
					continue
				}
				recognized = true
				commands = append(commands, shellLaunch{line: l.Start, env: withEnv(env, own), args: slices.Clone(words[i+1:])})
			}
		}
	}
	if !recognized {
		return pasteResult{} // nothing of a configuration: unrecognized, the lines unnamed
	}
	var group []zshrcAlias
	for _, a := range aliases {
		e := withEnv(env, a.env)
		if _, code := launchGateway(e); code != "" {
			tell(a.line, code, 0, a.alias)
			continue
		}
		group = append(group, zshrcAlias{Alias: a.alias, Env: e, Args: a.args, Command: "claude"})
	}
	for _, g := range mergeZshrcAliases("", group, nil) {
		each := []importGroup{g}
		if len(g.Differs) > 0 {
			each = g.variants
		}
		for _, v := range each {
			d := groupDraft(v)
			d.Label, d.Aliases, d.Provider.Name = strings.Join(v.Aliases, ", "), v.Aliases, v.Name
			res.Drafts = append(res.Drafts, d)
		}
	}
	for _, c := range commands {
		base, code := launchGateway(c.env)
		if code != "" {
			tell(c.line, code, c.line, "")
			continue
		}
		res.Drafts = append(res.Drafts, groupDraft(newImportGroup("", zshrcAlias{Env: c.env, Args: c.args}, base)))
	}
	if len(aliases) == 0 && len(commands) == 0 {
		switch d, code := envDraft(env); {
		case code == "":
			res.Drafts = append(res.Drafts, d)
		case code != noteNoGateway:
			tell(setAt["ANTHROPIC_BASE_URL"], code, setAt["ANTHROPIC_BASE_URL"], "")
		case setsClaude(env): // a .env of something else is no Claude Code at all
			tell(len(lines)+1, noteNoGateway, 0, "")
		}
	}
	slices.SortStableFunc(told, func(a, b placed) int { return a.at - b.at })
	for _, t := range told {
		addNote(&res.Notes, t.note)
	}
	return res
}

// parseSettings reads a Claude Code settings.json: its env (string values; others are not variables Claude Code
// sets), its model as ANTHROPIC_MODEL when env has none, and an apiKeyHelper (it runs a command for the key, so
// there is no key to take) as the draft of that env alone.
func parseSettings(obj map[string]any) pasteResult {
	env := map[string]string{}
	if m, ok := obj["env"].(map[string]any); ok {
		for k, v := range m {
			if s, ok := v.(string); ok {
				env[k] = s
			}
		}
	}
	if m, ok := obj["model"].(string); ok && strings.TrimSpace(env["ANTHROPIC_MODEL"]) == "" {
		env["ANTHROPIC_MODEL"] = m
	}
	res := pasteResult{Format: "settings"}
	d, code := envDraft(env)
	if code != "" {
		res.note(code, 0, "")
		return res
	}
	if helper, _ := obj["apiKeyHelper"].(string); strings.TrimSpace(helper) != "" {
		d.Notes = slices.DeleteFunc(d.Notes, func(n pasteNote) bool { return n.Code == noteNoKey })
		addNote(&d.Notes, pasteNote{Code: noteKeyHelper})
	}
	res.Drafts = append(res.Drafts, d)
	return res
}

// ---- headers ----

// pasteHeader is one request header of a paste.
type pasteHeader struct{ name, value string }

// headerLine is a "Name: value" line as a header; false for a line that is none.
func headerLine(line string) (pasteHeader, bool) {
	name, value, ok := strings.Cut(line, ":")
	h := pasteHeader{strings.TrimSpace(name), strings.TrimSpace(value)}
	return h, ok && h.name != ""
}

// protocolHeaders are the request basics a curl command or a cindy endpoint sends that Claude Code and the manager
// set themselves.
var protocolHeaders = []string{"anthropic-version", "anthropic-beta", "content-type", "accept", "accept-encoding", "user-agent"}

// credentialHeader: a header name with a word (between - and _, any case) that names a secret (importCredentialWords,
// AUTHORIZATION, AUTH).
func credentialHeader(name string) bool {
	for _, w := range strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '_' }) {
		if w = strings.ToUpper(w); slices.Contains(importCredentialWords, w) || w == "AUTHORIZATION" || w == "AUTH" {
			return true
		}
	}
	return false
}

// pasteHeaders sorts a paste's headers into the provider's ("Name: value" lines) and the ones left out with why:
// request basics (when protocol: a curl command's, a cindy endpoint's), credentials, and every other header of a
// direct API provider (api), which takes none.
func pasteHeaders(hs []pasteHeader, protocol, api bool) (kept string, skipped []skippedHeader) {
	var lines []string
	for _, h := range hs {
		why := ""
		switch {
		case protocol && slices.Contains(protocolHeaders, strings.ToLower(h.name)):
			why = headerSkipProtocol
		case credentialHeader(h.name):
			why = headerSkipCredential
		case api:
			why = headerSkipUnsupported
		}
		if why != "" {
			skipped = append(skipped, skippedHeader{Name: h.name, Reason: why})
			continue
		}
		lines = append(lines, h.name+": "+h.value)
	}
	return strings.Join(lines, "\n"), skipped
}

// ---- curl ----

// curlLine is the first command line of lines (comments and blank lines aside) when it runs curl, after any
// variables set for it: its first line and the words after curl.
func curlLine(lines []shellcmd.Line) (start int, args []string, ok bool) {
	for _, l := range lines {
		if t := strings.TrimSpace(l.Text); t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		segs, ok := shellcmd.Split(l.Text)
		if !ok || len(segs) == 0 {
			return 0, nil, false
		}
		words := segs[0].Words
		i := shellcmd.CommandIndex(words, map[string]string{})
		if i < 0 || filepath.Base(words[i]) != "curl" {
			return 0, nil, false
		}
		return l.Start, words[i+1:], true
	}
	return 0, nil, false
}

// curlQuiet are the curl options (short ones also run together, -sSL) that say nothing about the provider: left out
// without a note. -X / --request take a value, also left out.
var curlQuiet = lazyre.New(`^(-[sSivNL]+|--silent|--show-error|--no-buffer|--compressed|--location)$`)

// curlValued are the curl options that take a value (the ones read among them), so a value is not read as the URL.
var curlValued = []string{"-u", "--user", "-A", "--user-agent", "-e", "--referer", "-b", "--cookie", "-c", "--cookie-jar",
	"-o", "--output", "-m", "--max-time", "--connect-timeout", "-w", "--write-out", "-F", "--form", "--form-string",
	"--data-urlencode", "--data-ascii", "-T", "--upload-file", "--cacert", "--capath", "-E", "--cert", "--cert-type",
	"--key", "--key-type", "--pass", "--resolve", "--connect-to", "--retry", "--retry-delay", "--retry-max-time", "-r",
	"--range", "-K", "--config", "--oauth2-bearer", "-U", "--proxy-user", "--proxy-header", "--limit-rate",
	"--max-filesize", "--interface", "--unix-socket", "-Y", "--speed-limit", "-y", "--speed-time", "-z", "--time-cond",
	"--aws-sigv4", "-H", "--header", "-d", "--data", "--data-raw", "--data-binary", "--json", "-x", "--proxy", "--noproxy",
	"-X", "--request", "--url"}

// curlRequest is what a curl command says: its URL, headers (as written), body, proxy and bypass list (with the
// option each was given by), and the options it does not use.
type curlRequest struct {
	url, body, proxy, proxyOpt, noProxy, noProxyOpt string
	headers                                         []string
	unused                                          []string
}

// readCurl reads curl's arguments. The URL is --url's, else the first argument that is no option and has a scheme,
// else the first one that is no option.
func readCurl(args []string) curlRequest {
	var c curlRequest
	var plain []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		opt, value, attached := a, "", false
		if len(a) > 2 && a[0] == '-' && a[1] != '-' && slices.Contains(curlValued, a[:2]) {
			opt, value, attached = a[:2], a[2:], true // -Hname: value
		}
		if !attached && slices.Contains(curlValued, opt) && i+1 < len(args) {
			i++
			value = args[i]
		}
		switch {
		case opt == "--url":
			c.url = value
		case opt == "-H" || opt == "--header":
			c.headers = append(c.headers, value)
		case opt == "-d" || opt == "--data" || opt == "--data-raw" || opt == "--data-binary" || opt == "--json":
			c.body = value
		case opt == "-x" || opt == "--proxy":
			c.proxy, c.proxyOpt = value, opt
		case opt == "--noproxy":
			c.noProxy, c.noProxyOpt = value, opt
		case opt == "-X" || opt == "--request" || curlQuiet.MatchString(a):
		case strings.HasPrefix(a, "--"):
			c.unused = append(c.unused, opt)
		case strings.HasPrefix(a, "-") && len(a) > 1:
			c.unused = append(c.unused, a[:2])
		default:
			plain = append(plain, a)
		}
	}
	if c.url == "" {
		if i := slices.IndexFunc(plain, func(w string) bool { return strings.Contains(w, "://") }); i >= 0 {
			c.url = plain[i]
		} else if len(plain) > 0 {
			c.url = plain[0]
		}
	}
	return c
}

// curlEndpoint is an endpoint path a curl URL may end in, and the provider that says: the base URL is what is before
// the path, and keep.
type curlEndpoint struct{ path, keep, kind, api string }

var curlEndpoints = []curlEndpoint{
	{"/messages", "", "launcher", "messages"},
	{"/v1/chat/completions", "/v1", "api", "chat"},
	{"/v1/responses", "/v1", "api", "responses"},
	{"/v1/models", "", "launcher", "messages"},
}

// parseCurl reads a pasted curl command (args, the words after curl on the command line at line start; the other
// lines are noted): the endpoint its URL calls says the provider (Messages: a Claude Code gateway; Chat Completions or
// Responses: a direct API), x-api-key or Authorization: Bearer the key, the JSON body's model the model.
func parseCurl(lines []shellcmd.Line, start int, args []string) pasteResult {
	res := pasteResult{Format: "curl"}
	switch d, code := curlDraft(readCurl(args)); code {
	case "":
		res.Drafts = append(res.Drafts, d)
	case noteUnrecognized:
		return pasteResult{} // no URL that reads: nothing to draft
	default:
		res.note(code, start, "")
	}
	for _, l := range lines {
		if t := strings.TrimSpace(l.Text); l.Start > start && t != "" && !strings.HasPrefix(t, "#") {
			res.note(noteLineUnused, l.Start, "")
		}
	}
	return res
}

// curlDraft is a curl request as a draft, or the note code why there is none (unrecognized: no URL that reads).
func curlDraft(c curlRequest) (pasteDraft, string) {
	raw := c.url
	switch {
	case raw == "":
		return pasteDraft{}, noteUnrecognized
	case shellExpands(raw):
		return pasteDraft{}, noteURLShell
	case !strings.Contains(raw, "://"):
		raw = "http://" + raw // as curl takes it
	}
	u, err := url.Parse(raw)
	switch {
	case urlWithUser(raw):
		return pasteDraft{}, noteURLUser
	case err != nil || u.Host == "":
		return pasteDraft{}, noteUnrecognized
	}
	d := pasteDraft{Provider: pastedProvider{Kind: "launcher", Agent: "claude", API: "messages"}}
	p := &d.Provider
	root, path := u.Scheme+"://"+u.Host, strings.TrimRight(u.EscapedPath(), "/")
	i := slices.IndexFunc(curlEndpoints, func(e curlEndpoint) bool { return strings.HasSuffix(path, e.path) })
	if i < 0 {
		addNote(&d.Notes, pasteNote{Code: noteCurlPath, Name: u.Path})
		p.BaseURL = proto.MessagesBaseURL(root + path)
	} else if e := curlEndpoints[i]; e.kind == "api" {
		p.Kind, p.Agent, p.API = e.kind, "", e.api
		p.BaseURL = root + strings.TrimSuffix(path, e.path) + e.keep
	} else {
		p.BaseURL = proto.MessagesBaseURL(root + strings.TrimSuffix(path, e.path))
	}
	api := p.Kind == "api"
	for _, opt := range c.unused {
		addNote(&d.Notes, pasteNote{Code: noteCurlOption, Name: opt})
	}
	switch {
	case c.proxyOpt == "":
	case api || urlWithUser(c.proxy): // a direct API takes no proxy; one with a user is stored in the clear
		addNote(&d.Notes, pasteNote{Code: noteCurlOption, Name: c.proxyOpt})
	default:
		p.Proxy = c.proxy
	}
	if api && c.noProxyOpt != "" {
		addNote(&d.Notes, pasteNote{Code: noteCurlOption, Name: c.noProxyOpt})
	} else {
		p.NoProxy = c.noProxy
	}
	var body struct {
		Model string `json:"model"`
	}
	if json.Unmarshal([]byte(c.body), &body) == nil && strings.TrimSpace(body.Model) != "" {
		p.Model = strings.TrimSpace(body.Model)
		p.Models = []proto.ProviderModel{{ID: p.Model}}
	}
	var hs []pasteHeader
	key, found := "", false
	for _, line := range c.headers {
		h, ok := headerLine(line)
		if !ok {
			continue
		}
		f := strings.Fields(h.value)
		switch {
		case found: // a second key is a credential header like any
		case strings.EqualFold(h.name, "x-api-key"):
			key, p.Auth, found = h.value, "api_key", true
			continue
		case strings.EqualFold(h.name, "authorization") && len(f) == 2 && strings.EqualFold(f[0], "bearer"):
			key, p.Auth, found = f[1], "bearer", true
			continue
		}
		hs = append(hs, h)
	}
	p.Headers, d.SkippedHeaders = pasteHeaders(hs, true, api)
	d.setKey(key)
	return d, ""
}

// ---- cindy ----

// cindyProvider is one provider of a cindy provider import (v1: a cindy:// link's JSON, or a provider file's; cindy's
// docs/provider-import-links.md), as far as Persona reads it.
type cindyProvider struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Auth struct {
		Method string `json:"method"`
		APIKey string `json:"apiKey"`
	} `json:"auth"`
	Endpoints []cindyEndpoint `json:"endpoints"`
	Models    []cindyModel    `json:"models"`
}

// cindyEndpoint is one of a cindy provider's endpoints: an API (protocol) at an address.
type cindyEndpoint struct {
	Protocol string         `json:"protocol"`
	BaseURL  string         `json:"baseUrl"`
	APIKey   string         `json:"apiKey"`
	Headers  map[string]any `json:"headers"`
	Models   []cindyModel   `json:"models"`
}

// cindyModel is a model as cindy lists it: its id alone, or an object.
type cindyModel struct {
	ID                     string   `json:"id"`
	Name                   string   `json:"name"`
	ContextWindow          int      `json:"contextWindow"`
	ReasoningEfforts       []string `json:"reasoningEfforts"`
	ReasoningDefaultEffort string   `json:"reasoningDefaultEffort"`
}

func (m *cindyModel) UnmarshalJSON(b []byte) error {
	if json.Unmarshal(b, &m.ID) == nil {
		return nil
	}
	type plain cindyModel
	var typed *json.UnmarshalTypeError
	if err := json.Unmarshal(b, (*plain)(m)); err != nil && !errors.As(err, &typed) {
		return err
	}
	return nil // a field of another type is left out, as anywhere else in a provider
}

// The cindy endpoint protocols Persona has an API for.
const (
	cindyMessages  = "anthropic-messages"
	cindyResponses = "openai-responses"
	cindyChat      = "openai-chat"
)

// parseCindy reads a cindy v1 provider import's JSON: one provider, or {providers: […]}. A custom provider is one
// draft: its first Messages endpoint as a Claude Code gateway, else its first Responses or Chat endpoint as a direct
// API; its other endpoints are noted. Presets and built-in key slots name cindy's own catalog: noted, as is OAuth.
// A field of another type than cindy's is left out; data that is no JSON is invalid_json.
func parseCindy(data []byte) pasteResult {
	var batch struct {
		Providers []json.RawMessage `json:"providers"`
	}
	var typed *json.UnmarshalTypeError
	if err := json.Unmarshal(data, &batch); err != nil && !errors.As(err, &typed) {
		return pasteResult{Notes: []pasteNote{{Code: noteInvalidJSON}}}
	}
	if batch.Providers == nil {
		batch.Providers = []json.RawMessage{data}
	}
	res := pasteResult{Format: "cindy"}
	for _, raw := range batch.Providers {
		var p cindyProvider
		if err := json.Unmarshal(raw, &p); err != nil && !errors.As(err, &typed) {
			return pasteResult{Notes: []pasteNote{{Code: noteInvalidJSON}}}
		}
		res.cindyProvider(p)
	}
	return res
}

// cindyProvider adds p's draft to res, or the notes why there is none.
func (res *pasteResult) cindyProvider(p cindyProvider) {
	if kind := cmp.Or(p.Kind, "custom"); kind != "custom" {
		res.note(noteCindyKind, 0, kind)
		return
	}
	if p.Auth.Method == "oauth" {
		res.note(noteCindyAuth, 0, p.Auth.Method)
		return
	}
	usable := func(protocols ...string) int {
		return slices.IndexFunc(p.Endpoints, func(e cindyEndpoint) bool {
			return slices.Contains(protocols, e.Protocol) && strings.TrimSpace(e.BaseURL) != ""
		})
	}
	pick := usable(cindyMessages)
	if pick < 0 {
		pick = usable(cindyResponses, cindyChat)
	}
	if pick < 0 {
		for _, e := range p.Endpoints {
			res.note(noteCindyEndpoint, 0, e.Protocol)
		}
		return
	}
	e := p.Endpoints[pick]
	raw := strings.TrimSpace(e.BaseURL)
	if urlWithUser(raw) {
		res.note(noteURLUser, 0, p.Name)
		return
	}
	d := pasteDraft{Label: p.Name, Provider: pastedProvider{DisplayName: p.Name}}
	pr := &d.Provider
	if e.Protocol == cindyMessages {
		pr.Kind, pr.Agent, pr.API, pr.BaseURL, pr.Auth = "launcher", "claude", "messages", proto.MessagesBaseURL(raw), "api_key"
	} else {
		pr.Kind, pr.API, pr.BaseURL, pr.Auth = "api", strings.TrimPrefix(e.Protocol, "openai-"), strings.TrimRight(raw, "/"), "bearer"
	}
	models := e.Models
	if len(models) == 0 {
		models = p.Models
	}
	for _, m := range models {
		if pm := cindyProviderModel(m); pm.ID != "" && !slices.ContainsFunc(pr.Models, func(o proto.ProviderModel) bool { return o.ID == pm.ID }) {
			pr.Models = append(pr.Models, pm)
		}
	}
	if len(pr.Models) > 0 {
		pr.Model = pr.Models[0].ID
	}
	var hs []pasteHeader
	for _, name := range slices.Sorted(maps.Keys(e.Headers)) {
		if v, ok := e.Headers[name].(string); ok {
			hs = append(hs, pasteHeader{name, v})
		}
	}
	pr.Headers, d.SkippedHeaders = pasteHeaders(hs, true, pr.Kind == "api")
	if p.Auth.Method == "none" {
		pr.Auth = ""
		addNote(&d.Notes, pasteNote{Code: noteCindyAuth, Name: p.Auth.Method})
	} else {
		d.setKey(strings.TrimSpace(cmp.Or(e.APIKey, p.Auth.APIKey)))
	}
	for i, other := range p.Endpoints {
		if i != pick {
			addNote(&d.Notes, pasteNote{Code: noteCindyEndpoint, Name: other.Protocol})
		}
	}
	res.Drafts = append(res.Drafts, d)
}

// cindyProviderModel is a cindy model as a provider's: only the thinking levels Persona knows, and the default only
// when it is one of them.
func cindyProviderModel(m cindyModel) proto.ProviderModel {
	pm := proto.ProviderModel{ID: strings.TrimSpace(m.ID), Name: strings.TrimSpace(m.Name), ContextWindow: max(m.ContextWindow, 0)}
	for _, e := range m.ReasoningEfforts {
		if slices.Contains(proto.EffortLevels, e) && !slices.Contains(pm.Efforts, e) {
			pm.Efforts = append(pm.Efforts, e)
		}
	}
	if slices.Contains(pm.Efforts, m.ReasoningDefaultEffort) {
		pm.DefaultEffort = m.ReasoningDefaultEffort
	}
	return pm
}
