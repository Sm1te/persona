package extension

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/lazyre"
)

// ManifestMost is the largest persona-plugin.json read.
const ManifestMost = 256 << 10

// SecretsMost is how many secrets a plugin may declare.
const SecretsMost = 32

var (
	idPattern     = lazyre.New(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	secretPattern = lazyre.New(`^[a-z][a-z0-9_]{0,31}$`)
	envPattern    = lazyre.New(`^[A-Za-z_][A-Za-z0-9_]*$`)
	platformKey   = lazyre.New(`^[a-z0-9]+-[a-z0-9]+$`)
)

// ValidID says whether id is a plugin id: a-z0-9_-, at most 32, not starting with - or _ (those are the hub's).
func ValidID(id string) bool { return idPattern.MatchString(id) }

// Manifest is persona-plugin.json (docs/plans/plan-extensions.md "3").
type Manifest struct {
	ManifestVersion int    `json:"manifest_version"`
	ID              string `json:"id"`
	Name            string `json:"name"`
	Version         string `json:"version"`
	Description     string `json:"description,omitempty"`
	Publisher       string `json:"publisher,omitempty"`
	Homepage        string `json:"homepage,omitempty"`
	// Tags are what the market shows and searches (docs/extensions/extensions.md "Extension market"). Packages and indexes carry the
	// manifest without them (PackagedManifest): hubs before tags read manifests strictly and would refuse it.
	Tags             []string                   `json:"tags,omitempty"`
	Engines          Engines                    `json:"engines,omitzero"`
	Runtime          *Runtime                   `json:"runtime,omitempty"`
	Node             *NodeRuntime               `json:"node,omitempty"`
	ActivationEvents []string                   `json:"activation_events,omitempty"`
	Contributes      map[string]json.RawMessage `json:"contributes,omitempty"`
	// OffByDefault are contributions ("<point>/<id>") that start switched off: at install, and at an upgrade for the
	// ones the version before did not have (docs/extensions/extensions.md "Turning contributions on and off"). A connector's or a bound channel's
	// switch is not the extension's, so they cannot be listed.
	OffByDefault []string `json:"off_by_default,omitempty"`
	// Depends, Exports and ExtensionPack are the extension model (docs/extensions/extensions.md "Dependencies, exports and packs", model.go).
	Depends       []Dependency    `json:"depends,omitempty"`
	Exports       *Exports        `json:"exports,omitempty"`
	ExtensionPack []PackMember    `json:"extension_pack,omitempty"`
	Configuration json.RawMessage `json:"configuration,omitempty"`
	Secrets       []SecretSpec    `json:"secrets,omitempty"`
	// SettingsCheck: the program checks a change of its settings or secrets before the hub saves it (settings.check,
	// configured.go, #5906).
	SettingsCheck bool        `json:"settings_check,omitempty"`
	Permissions   Permissions `json:"permissions,omitzero"`
	// Tools are command-line programs the hub installs for it (tools.go); Auth says it signs in (auth.* methods).
	// Hubs before them refuse the manifest (strict), so an extension using them asks for a newer hub in engines.
	Tools []ToolSpec `json:"tools,omitempty"`
	Auth  *AuthSpec  `json:"auth,omitempty"`
	// HostIntegrations declares typed, read-only access to a configured host
	// CLI. Unlike Tools, it never installs a program or accepts a command line.
	HostIntegrations []HostIntegrationSpec `json:"host_integrations,omitempty"`
}

// Engines are what the extension runs on.
type Engines struct {
	// Persona is a range of hub build numbers (">=202611050000 <202701010000").
	Persona string
	// ExtensionProtocol is "extension_protocol"; its old name "plugin_protocol" is read too (docs/extensions/extensions.md
	// "Naming and compatibility"). It is still written out as "plugin_protocol", so the manifests the hub stores stay readable to a hub
	// rolled back to a build before #1844.
	ExtensionProtocol int
}

func (e Engines) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Persona  string `json:"persona,omitempty"`
		Protocol int    `json:"plugin_protocol,omitempty"`
	}{e.Persona, e.ExtensionProtocol})
}

func (e *Engines) UnmarshalJSON(data []byte) error {
	var v struct {
		Persona  string `json:"persona,omitempty"`
		Protocol int    `json:"extension_protocol,omitempty"`
		Legacy   int    `json:"plugin_protocol,omitempty"`
	}
	if err := strict(data, &v); err != nil {
		return err
	}
	if v.Protocol != 0 && v.Legacy != 0 && v.Protocol != v.Legacy {
		return fmt.Errorf("engines: extension_protocol %d and plugin_protocol %d differ", v.Protocol, v.Legacy)
	}
	e.Persona, e.ExtensionProtocol = v.Persona, max(v.Protocol, v.Legacy)
	return nil
}

// Command is one path, or one per platform ("linux-amd64": "bin/x").
type Command struct {
	Path       string
	ByPlatform map[string]string
}

func (c *Command) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, &c.Path)
	}
	return json.Unmarshal(b, &c.ByPlatform)
}

func (c Command) MarshalJSON() ([]byte, error) {
	if c.ByPlatform != nil {
		return json.Marshal(c.ByPlatform)
	}
	return json.Marshal(c.Path)
}

// For is the command on platform ("linux-amd64"); false when there is none for it.
func (c Command) For(platform string) (string, bool) {
	if c.ByPlatform == nil {
		return c.Path, c.Path != ""
	}
	p, ok := c.ByPlatform[platform]
	return p, ok && p != ""
}

// Platforms are the platforms it has a program for, sorted (none for a single path).
func (c Command) Platforms() []string {
	out := make([]string, 0, len(c.ByPlatform))
	for p, path := range c.ByPlatform {
		if path != "" {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// SecretSpec is a secret the plugin needs.
type SecretSpec struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	// Hint "last4": once set, the settings show its last four characters (configured.go, #5906).
	Hint string `json:"hint,omitempty"`
}

// Activation events.
const (
	OnStartup = "on_startup"
	OnCall    = "on_call"
	OnHTTP    = "on_http"
)

// Builtin commands: the hub's own binary (a built-in plugin's).
var builtinCommands = []string{"personad", compat.DaemonBinary}

// IsBuiltinCommand says whether a runtime command names the hub's own binary.
func IsBuiltinCommand(c string) bool { return slices.Contains(builtinCommands, c) }

// Parse reads a manifest strictly (no unknown fields anywhere) and checks it (Validate).
func Parse(data []byte) (*Manifest, error) {
	if len(data) > ManifestMost {
		return nil, fmt.Errorf("%s: %d bytes, more than %d", ManifestFile, len(data), ManifestMost)
	}
	var m Manifest
	if err := strict(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestFile, err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// strict decodes one JSON value into v, refusing unknown fields and anything after it.
func strict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("something after the JSON value")
	}
	return nil
}

// checkTags holds tags to what a market entry may have: at most marketTagMost, each 1 to 24 characters.
func checkTags(tags []string) error {
	if len(tags) > marketTagMost {
		return fmt.Errorf("more than %d", marketTagMost)
	}
	for _, t := range tags {
		if t == "" || utf8.RuneCountInString(t) > 24 {
			return errors.New("a tag is empty or longer than 24 characters")
		}
	}
	return nil
}

// PackagedManifest is the manifest as a package and a market index carry it: data without tags, which the market
// entry carries instead. A hub from before tags reads manifests strictly (no unknown fields) and would refuse one
// with them; the same bytes go in as persona-extension.json and as LegacyManifestFile.
func PackagedManifest(data []byte) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := strict(data, &fields); err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestFile, err)
	}
	if _, ok := fields["tags"]; !ok {
		return data, nil
	}
	delete(fields, "tags")
	out, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// Activation is what starts the program: on_call when the manifest names nothing.
func (m *Manifest) Activation() []string {
	if len(m.ActivationEvents) == 0 && m.Runtime != nil {
		return []string{OnCall}
	}
	return m.ActivationEvents
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
