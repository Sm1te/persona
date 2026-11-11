package proto

import (
	"crypto/ecdh"
	"crypto/hpke"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// secret.stage: values a provider extension resolved for one Agent start, after the user approved the hand-off card,
// kept by that computer's node until the start's wrapper fetches them (docs/extensions/secret-providers.md "The
// hand-off to one Agent run", docs/protocol/protocol.md "hub ↔ node"). Only the manager that connects the computer
// sends it, over its own connection: the hub never carries a value.

// MethodSecretStage is the node method.
const MethodSecretStage = "secret.stage"

// Limits on one hand-off, the same for the manager that asks and the node that keeps them.
const (
	SecretEnvMost       = 8               // variables in one start
	SecretEnvValueMost  = 16 << 10        // bytes in one value (a provider's values are at most this, extension.SecretMost)
	SecretEnvValueLeast = 16              // bytes: a shorter value could match ordinary words and paths where it is hidden
	SecretStageKeep     = 2 * time.Minute // a stage key, and a staged ticket, are good for at most this long
	// CodeSecretEnvGone: a start named a ticket that is used, dropped or expired.
	CodeSecretEnvGone = "secret_env_gone"
)

// SecretEnvVar is one variable of a hand-off, inside the sealed payload (SealSecretEnv). Whoever builds or opens it
// zeroes Value once it is no longer needed.
type SecretEnvVar struct {
	Name  string `json:"name"`
	Value []byte `json:"value"`
}

// SecretStageParams are secret.stage's params. Check asks for a one-time key to seal a hand-off to (and whether the
// node can take one at all); the hand-off itself is sealed to that key, so whatever relays the manager's connection
// to the computer (the hub, for a Persona Link or a mesh computer) sees no value.
type SecretStageParams struct {
	Check  bool   `json:"check,omitempty"`
	KeyID  string `json:"key_id,omitempty"`
	Sealed []byte `json:"sealed,omitempty"` // SealSecretEnv's result, base64 in JSON
}

// SecretStageResult is secret.stage's answer: to a check, the key (base64 of an uncompressed P-256 point) and its id;
// to a hand-off, the one-time ticket the start carries (session.start's secret_ticket), good until ExpiresAt.
type SecretStageResult struct {
	Key       string    `json:"key,omitempty"`
	KeyID     string    `json:"key_id,omitempty"`
	Ticket    string    `json:"ticket,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
}

// secretStageInfo binds a sealed hand-off to this use and to its key's id.
func secretStageInfo(keyID string) []byte { return []byte("persona secret stage v1\x00" + keyID) }

// SealSecretEnv seals env to the node's one-time key (key, keyID from a secret.stage check): HPKE base mode,
// DHKEM(P-256, HKDF-SHA256) + HKDF-SHA256 + AES-256-GCM. The JSON it encrypts is zeroed before it returns.
func SealSecretEnv(key, keyID string, env []SecretEnvVar) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return nil, fmt.Errorf("stage key: %w", err)
	}
	pub, err := ecdh.P256().NewPublicKey(raw)
	if err != nil {
		return nil, fmt.Errorf("stage key: %w", err)
	}
	pk, err := hpke.NewDHKEMPublicKey(pub)
	if err != nil {
		return nil, err
	}
	plain, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	defer clear(plain)
	return hpke.Seal(pk, hpke.HKDFSHA256(), hpke.AES256GCM(), secretStageInfo(keyID), plain)
}

// OpenSecretEnv is SealSecretEnv's inverse, on the node with the key's private half.
func OpenSecretEnv(priv *ecdh.PrivateKey, keyID string, sealed []byte) ([]SecretEnvVar, error) {
	k, err := hpke.NewDHKEMPrivateKey(priv)
	if err != nil {
		return nil, err
	}
	plain, err := hpke.Open(k, hpke.HKDFSHA256(), hpke.AES256GCM(), secretStageInfo(keyID), sealed)
	if err != nil {
		return nil, errors.New("the hand-off does not open with this key")
	}
	defer clear(plain)
	var env []SecretEnvVar
	if err := json.Unmarshal(plain, &env); err != nil {
		return nil, errors.New("the hand-off does not read")
	}
	return env, nil
}

var secretEnvName = lazy(`^[A-Z_][A-Z0-9_]{0,63}$`)

// secretEnvReserved are names a hand-off may not set: what the shell, the node and the launcher set for the Agent,
// and what changes how a program is loaded or run.
var secretEnvReserved = []string{
	// the shell's and the session's
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "PWD", "OLDPWD", "TMPDIR", "TMP", "TEMP", "TERM", "LANG", "IFS", "ENV",
	"BASH_ENV", "ZDOTDIR", "SHELLOPTS", "BASHOPTS", "PS1", "PS2", "PS4", "PROMPT_COMMAND", "CDPATH",
	// what loads or changes a program
	"NODE_OPTIONS", "NODE_PATH", "PYTHONPATH", "PYTHONHOME", "PYTHONSTARTUP", "PYTHONWARNINGS", "PYTHONBREAKPOINT",
	"PYTHONINSPECT", "PERL5LIB", "PERL5OPT", "PERL5DB", "PERLLIB", "RUBYOPT", "RUBYLIB", "JAVA_TOOL_OPTIONS",
	"_JAVA_OPTIONS", "JDK_JAVA_OPTIONS", "GCONV_PATH",
	// where traffic goes and whom it trusts
	"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "FTP_PROXY", "NODE_TLS_REJECT_UNAUTHORIZED",
	"NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "SSL_CERT_DIR", "REQUESTS_CA_BUNDLE", "CURL_CA_BUNDLE",
	// commands other programs run
	"SSH_ASKPASS", "SSH_AUTH_SOCK", "EDITOR", "VISUAL", "PAGER", "MANPAGER", "LESSOPEN", "LESSCLOSE", "BROWSER",
	// more that load code or change where things go
	"OPENSSL_CONF", "OPENSSL_ENGINES", "GOFLAGS", "RUSTC_WRAPPER", "RUSTC_WORKSPACE_WRAPPER", "CARGO_BUILD_RUSTC_WRAPPER",
	"ELECTRON_RUN_AS_NODE", "SSLKEYLOGFILE", "GLIBC_TUNABLES", "PHPRC", "PHP_INI_SCAN_DIR", "DOCKER_HOST", "DOCKER_CONFIG",
	"KUBECONFIG", "BUN_OPTIONS", "DOTNET_STARTUP_HOOKS", "GOTOOLCHAIN", "GOPROXY", "PS0", "CLASSPATH", "RUSTFLAGS",
	"PYTHONUSERBASE", "TERMINFO", "CC", "CXX", "CFLAGS", "CXXFLAGS", "LDFLAGS",
	// the Agent's own settings: its launcher's
	"CODEX_HOME"}

var secretEnvReservedPrefixes = []string{"PERSONA_", "LD_", "DYLD_", "LC_", "HERDR_", "GIT_", "XDG_", "NPM_CONFIG_",
	"ANTHROPIC_", "CLAUDE_", "OPENAI_BASE", "SUDO_", "LUA_INIT", "CORECLR_"}

// SecretEnvNameProblem is why name cannot be a hand-off's variable, "" when it can: an environment variable name
// (capital letters, digits, underscores, not starting with a digit, at most 64), none of the reserved ones. The old
// names' prefix is checked by compat.SecretEnvNameProblem, which callers use.
func SecretEnvNameProblem(name string) string {
	if !secretEnvName().MatchString(name) {
		return "it is not an environment variable name (capital letters, digits and _, at most 64)"
	}
	if slices.Contains(secretEnvReserved, name) {
		return "the computer or Persona sets it"
	}
	for _, p := range secretEnvReservedPrefixes {
		if strings.HasPrefix(name, p) {
			return "names starting with " + p + " are the computer's or Persona's"
		}
	}
	return ""
}
