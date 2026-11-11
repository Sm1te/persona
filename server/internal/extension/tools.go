package extension

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
)

// Command-line programs an extension needs (docs/extensions/extensions.md "Command-line programs (tools)", #4507): the hub installs them
// into the extension's own directory at install time and puts them first on its PATH, so an extension built on a
// CLI works on a machine that does not have that CLI.

// ToolsMost is how many tools one manifest may list.
const ToolsMost = 8

var (
	toolNamePattern = lazyre.New(`^[a-z0-9][a-z0-9._-]{0,31}$`)
	semverInText    = lazyre.New(`(\d+)\.(\d+)\.(\d+)`)
	npmPackage      = lazyre.New(`^(@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*$`)
	sha256Hex       = lazyre.New(`^[0-9a-f]{64}$`)
)

// HostIntegrationSpec names a manager-configured CLI integration. It is not an
// executable path or a command line: the manager resolves the ID and owns every
// argument, timeout and output limit of its typed, read-only RPC.
type HostIntegrationSpec struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

func (h HostIntegrationSpec) Validate() error {
	if h.ID != "lark-cli" {
		return fmt.Errorf("host_integrations.id %q: unsupported integration", h.ID)
	}
	if _, err := ParseVersionRange(h.Version); err != nil {
		return fmt.Errorf("host_integrations.version: %v", err)
	}
	return nil
}

func (h HostIntegrationSpec) Allows(version string) bool {
	r, err := ParseVersionRange(h.Version)
	return err == nil && r.Allows(version)
}

// ToolSpec is one command-line program of manifest.tools.
type ToolSpec struct {
	// Name is the command put on PATH.
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
	// Requires are other tools of the manifest installed (and on PATH) before this one is checked or installed.
	Requires []string `json:"requires,omitempty"`
	// Check, when there is one, lets a copy already on the hub's PATH be used instead of installing one.
	Check *ToolCheck `json:"check,omitempty"`
	// Exactly one of Archive and NPM.
	Archive map[string]ToolArchive `json:"archive,omitempty"`
	NPM     *ToolNPM               `json:"npm,omitempty"`
}

// ToolCheck is how a copy on the machine is checked: <name> <args…> prints a version (the first x.y.z) of at least
// MinVersion.
type ToolCheck struct {
	Args       []string `json:"args,omitempty"`
	MinVersion string   `json:"min_version,omitempty"`
}

// ToolArchive is a platform's download: a .tar.gz, .tgz or .zip with the program at Path, or the program itself.
type ToolArchive struct {
	URL     string   `json:"url"`
	SHA256  string   `json:"sha256"`
	Path    string   `json:"path,omitempty"`
	Mirrors []string `json:"mirrors,omitempty"`
}

// ToolNPM is a package from an npm registry, unpacked as published: no install scripts, no dependencies.
type ToolNPM struct {
	Package   string `json:"package"`
	Version   string `json:"version"`
	Integrity string `json:"integrity"`
	Registry  string `json:"registry,omitempty"`
	Bin       string `json:"bin,omitempty"`
}

// AuthSpec is manifest.auth: the extension signs in through its auth.* methods (docs/extensions/extensions.md "Sign-in (auth)").
type AuthSpec struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	// Page is a plugin_page ui contribution: the sign-in page the extension draws itself, for apps that do not know
	// auth. Apps that do know it leave the page out of the extension's pages.
	Page string `json:"page,omitempty"`
}

// DefaultNPMRegistry is the registry of an npm tool that names none.
const DefaultNPMRegistry = "https://registry.npmjs.org"

// RegistryURL is the npm registry, without a trailing slash.
func (n *ToolNPM) RegistryURL() string {
	r := strings.TrimRight(strings.TrimSpace(n.Registry), "/")
	if r == "" {
		return DefaultNPMRegistry
	}
	return r
}

// TarballURL is where the registry keeps the package's tarball: <registry>/<package>/-/<name>-<version>.tgz.
func (n *ToolNPM) TarballURL() string {
	base := n.Package
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	return n.RegistryURL() + "/" + n.Package + "/-/" + base + "-" + n.Version + ".tgz"
}

// BinName is the key of package.json's bin the tool runs.
func (n *ToolNPM) BinName(tool string) string {
	if n.Bin != "" {
		return n.Bin
	}
	return tool
}

// Integrity is an SRI string's algorithm and digest.
func ParseIntegrity(s string) (algo string, sum []byte, err error) {
	algo, b64, ok := strings.Cut(strings.TrimSpace(s), "-")
	if !ok || (algo != "sha512" && algo != "sha256") {
		return "", nil, fmt.Errorf("integrity %q: sha512-<base64> or sha256-<base64>", s)
	}
	sum, err = base64.StdEncoding.DecodeString(b64)
	want := map[string]int{"sha512": 64, "sha256": 32}[algo]
	if err != nil || len(sum) != want {
		return "", nil, fmt.Errorf("integrity %q: not a %s digest", s, algo)
	}
	return algo, sum, nil
}

// For is the archive for platform.
func (t *ToolSpec) For(platform string) (ToolArchive, bool) {
	a, ok := t.Archive[platform]
	return a, ok
}

// Hosts are the hosts the tool downloads from, sorted.
func (t *ToolSpec) Hosts() []string {
	var hosts []string
	add := func(raw string) {
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			hosts = append(hosts, strings.ToLower(u.Hostname()))
		}
	}
	if t.NPM != nil {
		add(t.NPM.RegistryURL())
	}
	for _, a := range t.Archive {
		add(a.URL)
		for _, m := range a.Mirrors {
			add(m)
		}
	}
	slices.Sort(hosts)
	return slices.Compact(hosts)
}

// consent is what installing the tool is agreed to as: its name, how and where from. Its version is not in it, so a
// newer example-cli from the same place is not asked about again.
func (t *ToolSpec) consent() string {
	kind := "archive"
	if t.NPM != nil {
		kind = "npm:" + t.NPM.Package
	}
	return t.Name + " " + kind + " " + strings.Join(t.Hosts(), ",")
}

// Describe is the tool for people: "example-cli 2.3.4 (npm: registry.example.com)", with full-width punctuation.
func (t *ToolSpec) Describe() string { return t.DescribeIn(i18n.Default) }

// DescribeIn formats tool provenance without translating its author-provided title or package data.
func (t *ToolSpec) DescribeIn(l i18n.Lang) string {
	name := t.Name
	if t.Title != "" && t.Title != t.Name {
		name = permissionText(l, "tool_name", i18n.Args{"title": t.Title, "name": t.Name})
	}
	from := strings.Join(t.Hosts(), permissionText(l, "list_separator"))
	if t.NPM != nil {
		from = permissionText(l, "tool_npm", i18n.Args{"hosts": from})
	}
	return permissionText(l, "tool_description", i18n.Args{"name": name, "version": t.Version, "from": from})
}

// ToolsConsent are the tools as agreed to, sorted; nil without any.
func (m *Manifest) ToolsConsent() []string {
	var out []string
	for i := range m.Tools {
		out = append(out, m.Tools[i].consent())
	}
	slices.Sort(out)
	return out
}

// ToolOrder is the tools with each after the ones it requires (validate has refused cycles).
func (m *Manifest) ToolOrder() []ToolSpec {
	var out []ToolSpec
	done := map[string]bool{}
	var visit func(t ToolSpec)
	visit = func(t ToolSpec) {
		if done[t.Name] {
			return
		}
		done[t.Name] = true
		for _, r := range t.Requires {
			if i := slices.IndexFunc(m.Tools, func(o ToolSpec) bool { return o.Name == r }); i >= 0 {
				visit(m.Tools[i])
			}
		}
		out = append(out, t)
	}
	for _, t := range m.Tools {
		visit(t)
	}
	return out
}

func checkHTTPS(at, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return fmt.Errorf("%s: an https URL", at)
	}
	return nil
}

// validateTools checks manifest.tools and manifest.auth, and the agent_runtimes permission that, like tools, needs
// a program on the hub.
func (m *Manifest) validateTools() []error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if len(m.Tools) > 0 && m.Runtime == nil {
		bad("tools without a runtime: nothing would run them")
	}
	if len(m.HostIntegrations) > 0 {
		if m.Runtime == nil || !m.Runtime.SpeaksRPC() {
			bad("host_integrations without an RPC runtime: nothing would call them")
		}
		if len(m.HostIntegrations) > 1 {
			bad("host_integrations: at most one")
		}
		for _, h := range m.HostIntegrations {
			if err := h.Validate(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if m.Permissions.AgentRuntimes && m.Runtime == nil {
		bad("permissions.agent_runtimes without a runtime: no program on the hub would connect to its node")
	}
	if len(m.Tools) > ToolsMost {
		bad("tools: at most %d", ToolsMost)
	}
	names := map[string]bool{}
	for _, t := range m.Tools {
		at := "tools." + t.Name
		if !toolNamePattern.MatchString(t.Name) {
			bad("tools: name %q: a-z0-9._-, at most 32, a letter or digit first", t.Name)
		}
		if names[t.Name] {
			bad("tools: %q twice", t.Name)
		}
		names[t.Name] = true
		if strings.TrimSpace(t.Version) == "" || len(t.Version) > 64 {
			bad("%s: version (what is installed), at most 64 characters", at)
		}
		if t.Check != nil {
			if t.Check.MinVersion != "" && !semverInText.MatchString(t.Check.MinVersion) {
				bad("%s: check.min_version %q: x.y.z", at, t.Check.MinVersion)
			}
			if len(t.Check.Args) > 8 {
				bad("%s: check.args: at most 8", at)
			}
		}
		switch {
		case (t.Archive == nil) == (t.NPM == nil):
			bad("%s: exactly one of archive and npm", at)
		case t.NPM != nil:
			n := t.NPM
			if !npmPackage.MatchString(n.Package) || len(n.Package) > 214 {
				bad("%s: npm.package %q is not an npm package name", at, n.Package)
			}
			if _, err := ParseVersion(n.Version); err != nil {
				bad("%s: npm.version %q: an exact version", at, n.Version)
			}
			if _, _, err := ParseIntegrity(n.Integrity); err != nil {
				bad("%s: npm.%v", at, err)
			}
			if n.Registry != "" {
				if err := checkHTTPS(at+": npm.registry", n.Registry); err != nil {
					errs = append(errs, err)
				}
			}
			if n.Bin != "" && !toolNamePattern.MatchString(n.Bin) {
				bad("%s: npm.bin %q", at, n.Bin)
			}
		default:
			if len(t.Archive) == 0 {
				bad("%s: archive: no platform", at)
			}
			for _, platform := range sortedKeys(t.Archive) {
				a := t.Archive[platform]
				pat := at + ".archive." + platform
				if !platformKey.MatchString(platform) {
					bad("%s: a <GOOS>-<GOARCH> key", pat)
				}
				if err := checkHTTPS(pat+": url", a.URL); err != nil {
					errs = append(errs, err)
				}
				for _, mirror := range a.Mirrors {
					if err := checkHTTPS(pat+": mirrors", mirror); err != nil {
						errs = append(errs, err)
					}
				}
				if len(a.Mirrors) > 4 {
					bad("%s: mirrors: at most 4", pat)
				}
				if !sha256Hex.MatchString(a.SHA256) {
					bad("%s: sha256: 64 lowercase hex digits", pat)
				}
				if a.Path != "" {
					if clean := path.Clean(a.Path); path.IsAbs(a.Path) || clean == "." || !inside(clean) {
						bad("%s: path %q: a relative path inside the archive", pat, a.Path)
					}
				} else if isArchiveName(a.URL) {
					bad("%s: path: which file of the archive is the program", pat)
				}
			}
		}
	}
	for _, t := range m.Tools {
		for _, r := range t.Requires {
			if r == t.Name || !names[r] {
				bad("tools.%s: requires %q, which is not another of the manifest's tools", t.Name, r)
			}
		}
	}
	if cyc := toolCycle(m.Tools); cyc != "" {
		bad("tools: requires go round in a circle (%s)", cyc)
	}
	if a := m.Auth; a != nil {
		if n := len([]rune(strings.TrimSpace(a.Title))); n == 0 || n > 40 {
			bad("auth.title: 1 to 40 characters")
		}
		if !m.Runtime.SpeaksRPC() {
			bad("auth: the program answers auth.status, auth.step and auth.logout, so it needs a runtime that speaks the protocol")
		}
		if a.Page != "" && !slices.ContainsFunc(m.UIs(), func(u UIItem) bool {
			return u.ID == a.Page && (u.Placement == "plugin_page" || u.Placement == "extension_page")
		}) {
			bad("auth.page %q: not one of its extension_page (plugin_page) ui contributions", a.Page)
		}
	}
	return errs
}

func toolCycle(tools []ToolSpec) string {
	req := map[string][]string{}
	for _, t := range tools {
		req[t.Name] = t.Requires
	}
	state := map[string]int{}
	var found string
	var walk func(n string, trail []string)
	walk = func(n string, trail []string) {
		if found != "" {
			return
		}
		switch state[n] {
		case 1:
			found = strings.Join(append(trail, n), " → ")
			return
		case 2:
			return
		}
		state[n] = 1
		for _, r := range req[n] {
			if r != n {
				walk(r, append(trail, n))
			}
		}
		state[n] = 2
	}
	for _, t := range tools {
		walk(t.Name, nil)
	}
	return found
}

// isArchiveName says whether a download is an archive to unpack (by its URL path), not the program itself.
func isArchiveName(raw string) bool {
	p := raw
	if u, err := url.Parse(raw); err == nil {
		p = u.Path
	}
	p = strings.ToLower(p)
	return strings.HasSuffix(p, ".tar.gz") || strings.HasSuffix(p, ".tgz") || strings.HasSuffix(p, ".zip")
}

// specHash is a short hash of what installing the tool on platform takes: the same hash is the same install, kept
// across upgrades.
func (t *ToolSpec) specHash(platform string) string {
	var b strings.Builder
	b.WriteString(t.Name + "\n")
	if t.NPM != nil {
		fmt.Fprintf(&b, "npm %s %s %s %s", t.NPM.Package, t.NPM.Version, t.NPM.Integrity, t.NPM.BinName(t.Name))
	} else if a, ok := t.For(platform); ok {
		fmt.Fprintf(&b, "archive %s %s", a.SHA256, a.Path)
	}
	return hex.EncodeToString(sha256Sum([]byte(b.String())))[:12]
}
