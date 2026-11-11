package assistant

import (
	"net/url"
	"slices"
	"strings"
	"unicode"

	"github.com/nwverse/persona/server/internal/lazyre"
)

// An imported alias's environment (docs/assistant/model-config.md "Importing providers from shell aliases"): the
// variables Persona has a field for fill the field; the others go into the provider's sealed env, except credentials,
// values the shell would expand, and names tied to one computer. The preview names what goes and what does not,
// never a value.

// Why a variable of an alias is not imported.
const (
	envSkipCredential = "credential" // a key, token, password, cookie, or a URL with a user in it
	envSkipShell      = "shell"      // the shell would expand it, or it does not fit on one env line
	envSkipComputer   = "computer"   // it names something of one computer; the provider runs on every one in scope
	envSkipLimit      = "limit"      // beyond the env's envLimit lines (checkEnv)
)

// skippedEnv is one variable of an alias that is not imported, and why.
type skippedEnv struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// importMappedEnv are the variables an import turns into fields of the provider (newImportGroup).
var importMappedEnv = []string{
	"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_CUSTOM_HEADERS", "ANTHROPIC_MODEL",
	"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL",
	"ANTHROPIC_DEFAULT_FABLE_MODEL", "ANTHROPIC_SMALL_FAST_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL",
	"CLAUDE_CODE_SUBAGENT_MODEL_FORCE", "CLAUDE_CODE_EFFORT_LEVEL", "CLAUDE_CODE_AUTO_COMPACT_WINDOW",
	"CLAUDE_CODE_MAX_CONTEXT_TOKENS", "API_TIMEOUT_MS", "ANTHROPIC_CUSTOM_MODEL_OPTION",
	"ANTHROPIC_CUSTOM_MODEL_OPTION_NAME", "ANTHROPIC_CUSTOM_MODEL_OPTION_DESCRIPTION",
}

// importProxyEnv and importNoProxyEnv are the proxy variables, in the order the provider's proxy and bypass list are
// taken from (importProxy).
var (
	importProxyEnv   = []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"}
	importNoProxyEnv = []string{"NO_PROXY", "no_proxy"}
)

// importComputerEnv are the variables that belong to one computer.
var importComputerEnv = []string{"PATH", "HOME", "PWD", "OLDPWD", "SHELL", "USER", "LOGNAME", "TMPDIR", "CLAUDE_CONFIG_DIR"}

// importCredentialWords: a variable with one of these words (between underscores, any case) in its name holds a
// secret. TOKENS is not one: CLAUDE_CODE_MAX_OUTPUT_TOKENS is a number.
var importCredentialWords = []string{"KEY", "KEYS", "APIKEY", "TOKEN", "SECRET", "SECRETS", "PASSWORD", "PASSWD",
	"CREDENTIAL", "CREDENTIALS", "COOKIE", "COOKIES"}

// urlWithUser: v carries a user (and maybe a password): its authority (after an optional scheme:// or a leading //, up
// to the first /, ? or #) has an @, or v is no URL Go parses and has an @ anywhere. A parse alone proves nothing: Go
// refuses ^ { } | < > and blanks in a password that Node (Claude Code) takes and percent-encodes, and a value without
// a scheme (user:pass@host:8080) parses as a path. An e-mail address counts too: left out is the safe side.
func urlWithUser(v string) bool {
	v = strings.TrimSpace(v)
	authority := urlScheme.ReplaceAllString(v, "")
	if authority == v {
		authority = strings.TrimPrefix(v, "//")
	}
	if end := strings.IndexAny(authority, "/?#"); end >= 0 {
		authority = authority[:end]
	}
	if strings.Contains(authority, "@") {
		return true
	}
	_, err := url.Parse(v)
	return err != nil && strings.Contains(v, "@")
}

var urlScheme = lazyre.New(`^[A-Za-z][A-Za-z0-9+.-]*://`)

// importProxy is the provider's proxy (the first proxy variable set; none when it carries a user, as the proxy is
// stored and shown in the clear) and bypass list.
func importProxy(env map[string]string) (proxy, noProxy string) {
	if proxy = firstEnv(env, importProxyEnv); urlWithUser(proxy) {
		proxy = ""
	}
	return proxy, firstEnv(env, importNoProxyEnv)
}

// firstEnv is the first of names set in env, trimmed.
func firstEnv(env map[string]string, names []string) string {
	for _, k := range names {
		if v := strings.TrimSpace(env[k]); v != "" {
			return v
		}
	}
	return ""
}

// importEnvSkip is why variable k=v of an alias stays out of the provider's env ("" when it goes in). Mapped ones
// (fields) are not asked.
func importEnvSkip(k, v string) string {
	if slices.Contains(importComputerEnv, k) {
		return envSkipComputer
	}
	for _, w := range strings.Split(k, "_") {
		if slices.Contains(importCredentialWords, strings.ToUpper(w)) {
			return envSkipCredential
		}
	}
	if urlWithUser(v) {
		return envSkipCredential
	}
	if strings.ContainsAny(v, "$`") || strings.HasPrefix(v, "~") {
		return envSkipShell // Persona stores text: what the shell would make of it is not known here
	}
	if _, ok := importEnvLine(k, v); !ok {
		return envSkipShell
	}
	return ""
}

// importEnvLine is k=v as one line of a provider's env that the node's envLine (node/launcherenv.go) reads back as
// exactly v: quoted with "…" when v has a blank, # or a quote, with '…' when it has a " (there is no escape). False
// when no line holds it: a line break or NUL, both quotes, or a name that is not a shell variable's.
func importEnvLine(k, v string) (string, bool) {
	if !envName.MatchString(k) || strings.ContainsAny(v, "\n\r\x00") {
		return "", false
	}
	if !strings.ContainsFunc(v, func(r rune) bool {
		return unicode.IsSpace(r) || r == '\uFEFF' || r == '#' || r == '"' || r == '\''
	}) {
		return k + "=" + v, true
	}
	switch {
	case !strings.Contains(v, `"`):
		return k + `="` + v + `"`, true
	case !strings.Contains(v, "'"):
		return k + "='" + v + "'", true
	}
	return "", false
}

// importEnv is an alias's variables as a provider's env: the lines (sorted by name), their names, and the variables
// left out with why (sorted by name). A proxy variable with a user in it is a credential left out.
func importEnv(env map[string]string) (lines string, names []string, skipped []skippedEnv) {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var kept []string
	for _, k := range keys {
		v := env[k]
		if slices.Contains(importProxyEnv, k) || slices.Contains(importNoProxyEnv, k) {
			if urlWithUser(v) {
				skipped = append(skipped, skippedEnv{Name: k, Reason: envSkipCredential})
			}
			continue
		}
		if slices.Contains(importMappedEnv, k) {
			continue
		}
		if why := importEnvSkip(k, v); why != "" {
			skipped = append(skipped, skippedEnv{Name: k, Reason: why})
			continue
		}
		if len(names) == envLimit {
			skipped = append(skipped, skippedEnv{Name: k, Reason: envSkipLimit})
			continue
		}
		line, _ := importEnvLine(k, v)
		kept = append(kept, line)
		names = append(names, k)
	}
	return strings.Join(kept, "\n"), names, skipped
}
