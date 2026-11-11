package extension

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/channel"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
)

// Permissions are what a plugin asks for (docs/plans/plan-extensions.md "Key decisions"). The vocabulary is closed: a manifest naming
// a permission this hub does not know does not install, so nobody is shown one the hub cannot explain.
type Permissions struct {
	// Network are the hosts it reaches ("api.example.com", "*.example.com"). Shown and recorded; the hub does not
	// enforce them yet.
	Network []string `json:"network,omitempty"`
	// HubAPI: it gets the hub's address and full token (PERSONA_HUB_URL, PERSONA_HUB_TOKEN). The old way: a manifest
	// naming Hub scopes gets a token of those only, whatever HubAPI says.
	HubAPI bool `json:"hub_api,omitempty"`
	// Hub are the parts of the hub's API it calls (HubScopes): it gets a token that opens those routes and no other.
	Hub []string `json:"hub,omitempty"`
	// LauncherKeys: it reads launchers' quota with their keys — never seeing them: the hub (or the machine with the
	// launcher) sends its requests, to the launcher's own gateway only (docs/extensions/extensions.md "Quota").
	LauncherKeys bool `json:"launcher_keys,omitempty"`
	// GhLogin: it asks the hub for the GitHub token of a computer's gh login (the one the user picks for the GitHub
	// connector), held in its memory only (docs/extensions/extensions.md "Permissions"). For the GitHub connector moved out of the hub.
	GhLogin bool `json:"gh_login,omitempty"`
	// UsageGateways: it reads the account list of the account-pool gateways among the hub's usage sources
	// (usage.gateway_accounts: accounts, their status and quota windows, from the hub's last read), never a key
	// (docs/extensions/extensions.md "Quota", docs/server/usage-windows.md "The gateway's account list").
	UsageGateways bool `json:"usage_gateways,omitempty"`
	// AgentRuntimes: its program is an agent runtime that connects to this machine's node (docs/integrations/runtimes.md); in a
	// sandbox it gets a socket in its run directory that leads to the node's runtime.sock and nowhere else (#5413).
	AgentRuntimes bool `json:"agent_runtimes,omitempty"`
	// ErrorReports: it is given the error and crash reports of the hub and the user's devices, scrubbed first
	// (contributes.error_reports, docs/extensions/extensions.md "Error reports").
	ErrorReports bool `json:"error_reports,omitempty"`
	// AuthRelay: it is handed the authorization links the user's Agents stop at, the ones its contributes.auth_relay
	// names, to complete them with the account it is signed in with (docs/operations/auth-relay.md).
	AuthRelay bool `json:"auth_relay,omitempty"`
	// Storage is installation-scoped logical data, separate from credentials.
	Storage *StoragePermission `json:"storage,omitempty"`
}

var hostPattern = lazyre.New(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func (p Permissions) validate() error {
	var errs []error
	for _, h := range p.Network {
		if _, ok := ParseConfigNetwork(h); ok {
			continue // a settings field, held to the schema by the manifest (checkConfigured)
		}
		if !hostPattern.MatchString(h) || len(h) > 253 {
			errs = append(errs, fmt.Errorf("permissions.network: %q is not a host name (lowercase, no scheme, port or path; *. allowed first)", h))
		}
	}
	for _, s := range p.Hub {
		if _, ok := ScopeTitle(s); !ok {
			errs = append(errs, fmt.Errorf("permissions.hub: %q is not a scope of the hub's API (%s, %s<extension id>)", s,
				strings.Join(HubScopes, ", "), ScopeCallPrefix))
		}
	}
	if err := p.Storage.validate(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// normalized is p with the hosts and scopes sorted and once each.
func (p Permissions) normalized() Permissions {
	out := p
	out.Network = slices.Compact(slices.Sorted(slices.Values(p.Network)))
	out.Hub = slices.Compact(slices.Sorted(slices.Values(p.Hub)))
	out.Storage = p.Storage.normalized()
	return out
}

// FullHub says whether it gets the hub's full token: hub_api, the old way, without scopes.
func (p Permissions) FullHub() bool { return p.HubAPI && len(p.Hub) == 0 }

// Covers says whether p grants everything q asks (an upgrade asking for no more needs no new consent). The full token
// covers every scope but agents.permission_mode, which only a grant naming it covers; scopes never cover the full token.
func (p Permissions) Covers(q Permissions) bool {
	if q.FullHub() && !p.FullHub() || q.LauncherKeys && !p.LauncherKeys || q.GhLogin && !p.GhLogin ||
		q.UsageGateways && !p.UsageGateways || q.AgentRuntimes && !p.AgentRuntimes || q.ErrorReports && !p.ErrorReports ||
		q.AuthRelay && !p.AuthRelay || !p.Storage.covers(q.Storage) {
		return false
	}
	for _, s := range q.Hub {
		if !(p.FullHub() && InEveryScope(s)) && !slices.Contains(p.Hub, s) {
			return false
		}
	}
	for _, h := range q.Network {
		if !slices.Contains(p.Network, h) {
			return false
		}
	}
	return true
}

// nodeConsent is what the user agrees to of a program on computers: the systems it runs on, what it asks for there,
// and which secrets go to the computers.
type nodeConsent struct {
	Systems     []string        `json:"systems"`
	Permissions NodePermissions `json:"permissions"`
	Secrets     []string        `json:"secrets,omitempty"`
}

// secretNames are the secrets its computers get (none without a program).
func (r *NodeRuntime) secretNames() []string {
	if r == nil {
		return nil
	}
	return r.Secrets
}

func (r *NodeRuntime) consent() *nodeConsent {
	if r == nil {
		return nil
	}
	c := &nodeConsent{Systems: r.Systems(), Permissions: r.Permissions, Secrets: slices.Clone(r.Secrets)}
	c.Permissions.Network = slices.Compact(slices.Sorted(slices.Values(r.Permissions.Network)))
	slices.Sort(c.Secrets)
	return c
}

// Digest is what the user agrees to when installing: the permissions and whether it runs code, as a sha256 of their
// canonical JSON. Install and upgrade carry the digest of the preview the user saw, so what installs is what they saw.
// A plugin without a node program digests as it did before there were any (what installed plugins were granted holds),
// and so does one whose connectors do not send as the user.
func Digest(m *Manifest) string {
	b, _ := json.Marshal(struct {
		RunsCode    bool         `json:"runs_code"`
		Permissions Permissions  `json:"permissions"`
		Events      []string     `json:"events,omitempty"`
		Surfaces    []string     `json:"surfaces,omitempty"`
		Node        *nodeConsent `json:"node,omitempty"`
		Tools       []string     `json:"tools,omitempty"`
		SendsAsUser []string     `json:"sends_as_user,omitempty"`
		// HostIntegrations are existing configured CLI profiles, not downloaded
		// tools or arbitrary host commands; a new integration needs consent.
		HostIntegrations []string `json:"host_integrations,omitempty"`
		// SecretSchemes: supplying secret values is agreed to like a permission (empty for any other extension, so
		// their digests are what they were).
		SecretSchemes []string `json:"secret_schemes,omitempty"`
	}{m.Runtime != nil, m.Permissions.normalized(), m.Topics(), m.Surfaces(), m.NodeProgram().consent(), m.ToolsConsent(), sendsAsUserIDs(m),
		hostIntegrationConsent(m), sortedSchemes(m)})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// hostIntegrationConsent identifies the integration without its version: a
// compatible version-range change is not a new host capability grant.
func hostIntegrationConsent(m *Manifest) []string {
	out := make([]string, 0, len(m.HostIntegrations))
	for _, h := range m.HostIntegrations {
		out = append(out, h.ID)
	}
	slices.Sort(out)
	return out
}

// sortedSchemes are the schemes m's secret_providers claim, sorted: what the digest holds of them.
func sortedSchemes(m *Manifest) []string {
	out := slices.Clone(m.Schemes())
	slices.Sort(out)
	return slices.Compact(out)
}

// sendsAsUserIDs are the ids of m's connectors that send as the user, sorted: what the digest holds of them.
func sendsAsUserIDs(m *Manifest) []string {
	var out []string
	for _, c := range m.SendsAsUser() {
		if !slices.Contains(out, c.ID) {
			out = append(out, c.ID)
		}
	}
	slices.Sort(out)
	return out
}

// newSendsAsUser are m's connectors that send as the user and that old did not declare so: a connector that sends in
// the user's name on its own, outside the write gate, is something to agree to again.
func newSendsAsUser(old, m *Manifest) []ConnectorItem {
	was := sendsAsUserIDs(old)
	var out []ConnectorItem
	for _, c := range m.SendsAsUser() {
		if !slices.Contains(was, c.ID) {
			out = append(out, c)
		}
	}
	return out
}

// sendsAsUserLine is the line for connectors that send as the user.
func sendsAsUserLine(l i18n.Lang, title string, list []ConnectorItem) PermissionLine {
	var names []string
	for _, c := range list {
		names = append(names, c.Name)
	}
	return PermissionLine{Key: "sends_as_user", Title: title,
		Detail: permissionText(l, "sends_as_user_detail", i18n.Args{"names": strings.Join(names, permissionText(l, "list_separator"))})}
}

// toolsLine is the tools as the install screen shows them.
func toolsLine(l i18n.Lang, tools []ToolSpec, title string) PermissionLine {
	var what []string
	for i := range tools {
		what = append(what, tools[i].DescribeIn(l))
	}
	return PermissionLine{Key: "tools", Title: title,
		Detail: permissionText(l, "tools_detail", i18n.Args{"tools": strings.Join(what, permissionText(l, "list_separator"))})}
}

// hostIntegrationLines makes a host CLI grant visible at install/upgrade.
func hostIntegrationLines(l i18n.Lang, integrations []HostIntegrationSpec, title string) PermissionLine {
	parts := make([]string, 0, len(integrations))
	for _, h := range integrations {
		parts = append(parts, h.ID+" "+h.Version)
	}
	return PermissionLine{Key: "host_integrations", Title: title,
		Detail: permissionText(l, "host_integrations_detail", i18n.Args{"tools": strings.Join(parts, permissionText(l, "list_separator"))})}
}

// NewTools are m's tools old did not install the same way (a new name, or from somewhere else).
func NewTools(old, m *Manifest) []ToolSpec {
	was := old.ToolsConsent()
	var out []ToolSpec
	for i := range m.Tools {
		if !slices.Contains(was, m.Tools[i].consent()) {
			out = append(out, m.Tools[i])
		}
	}
	return out
}

// PermissionLine is one line of what a plugin may do, as the install screen shows it.
type PermissionLine struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
}

// permissionText and permissionLine generate only hub-owned presentation text.
func permissionText(l i18n.Lang, key string, args ...i18n.Args) string {
	return i18n.T(l, "extension-permissions."+key, args...)
}

func permissionLine(l i18n.Lang, key string) PermissionLine {
	return PermissionLine{Key: key, Title: permissionText(l, key+"_title"), Detail: permissionText(l, key+"_detail")}
}

// secretProviderLine is the line for an extension that supplies the values of references of these schemes
// (secret_providers): it learns which of its references Persona uses, and for what.
func secretProviderLine(l i18n.Lang, title string, schemes []string) PermissionLine {
	shown := make([]string, len(schemes))
	for i, s := range schemes {
		shown[i] = s + "://"
	}
	return PermissionLine{Key: "secret_provider", Title: permissionText(l, title),
		Detail: permissionText(l, "secret_provider_detail", i18n.Args{"schemes": strings.Join(shown, permissionText(l, "list_separator"))})}
}

// nodeRunsCode is the line for a program that runs on the computers the user picks.
func nodeRunsCode(l i18n.Lang, r *NodeRuntime) PermissionLine {
	var on []string
	for _, sys := range r.Systems() {
		on = append(on, permissionText(l, "system_"+sys))
	}
	return PermissionLine{Key: "node_runs_code", Title: permissionText(l, "node_runs_code_title"),
		Detail: permissionText(l, "node_run_detail", i18n.Args{"systems": strings.Join(on, permissionText(l, "list_separator"))})}
}

// nodeLines are the lines of what r asks for on the computers, of all it asks (old nil) or what it asks more than old.
func nodeLines(l i18n.Lang, r, old *NodeRuntime) []PermissionLine {
	var out []PermissionLine
	if old == nil {
		out = append(out, nodeRunsCode(l, r))
	} else if slices.ContainsFunc(r.Systems(), func(sys string) bool { return !slices.Contains(old.Systems(), sys) }) {
		line := nodeRunsCode(l, r)
		line.Title = permissionText(l, "node_more_title")
		out = append(out, line)
	}
	var hosts []string
	for _, h := range r.consent().Permissions.Network {
		if old == nil || !slices.Contains(old.Permissions.Network, h) {
			hosts = append(hosts, h)
		}
	}
	if len(hosts) > 0 {
		out = append(out, PermissionLine{Key: "node_network", Title: permissionText(l, "node_network_title"), Detail: strings.Join(hosts, permissionText(l, "list_separator"))})
	}
	if r.Permissions.Files && (old == nil || !old.Permissions.Files) {
		out = append(out, permissionLine(l, "node_files"))
	}
	if r.Permissions.MacOSPrivacy && (old == nil || !old.Permissions.MacOSPrivacy) {
		out = append(out, permissionLine(l, "node_macos_privacy"))
	}
	return out
}

// nodeSecrets are the titles of the secrets m sends to the computers that old (nil: none) did not.
func nodeSecrets(m, old *Manifest) []string {
	var out []string
	for _, name := range m.NodeProgram().secretNames() {
		if old.NodeProgram() != nil && slices.Contains(old.Node.Secrets, name) {
			continue
		}
		for _, s := range m.Secrets {
			if s.Name == name {
				out = append(out, s.Title)
			}
		}
	}
	return out
}

// NodeAdds says whether m's program on computers asks for more than old's: a first one, more systems, hosts,
// permissions or secrets sent to the computers.
func NodeAdds(old, m *Manifest) bool {
	return m.NodeProgram() != nil && (len(nodeLines(i18n.Default, m.Node, old.NodeProgram())) > 0 || len(nodeSecrets(m, old)) > 0)
}

// Explain is every line the user is shown before installing m.
func Explain(m *Manifest) []PermissionLine { return ExplainIn(i18n.Default, m) }

// ExplainIn generates permission disclosures without changing manifest or consent data.
func ExplainIn(l i18n.Lang, m *Manifest) []PermissionLine { return ExplainWith(l, m, nil) }

// ExplainWith is ExplainIn naming the address an installed extension's settings hold now (config, nil when not
// installed yet) for each "config:" entry of permissions.network.
func ExplainWith(l i18n.Lang, m *Manifest, config json.RawMessage) []PermissionLine {
	out := []PermissionLine{} // never null on the wire: nothing to agree to is an empty list
	if m.Runtime != nil {
		out = append(out, permissionLine(l, "runs_code"))
	}
	if hosts := m.Permissions.hostNames(); len(hosts) > 0 {
		out = append(out, PermissionLine{Key: "network", Title: permissionText(l, "network_title"),
			Detail: permissionText(l, "network_detail", i18n.Args{"hosts": strings.Join(hosts, permissionText(l, "list_separator"))})})
	}
	out = append(out, configuredLines(l, m, m.Permissions.ConfiguredNetworks(), config)...)
	if m.Permissions.FullHub() {
		out = append(out, permissionLine(l, "hub_api"))
	} else if hub := m.Permissions.normalized().Hub; len(hub) > 0 {
		out = append(out, hubLine(l, permissionText(l, "hub_title"), hub))
	}
	if m.Permissions.LauncherKeys {
		out = append(out, permissionLine(l, "launcher_keys"))
	}
	if m.Permissions.GhLogin {
		out = append(out, permissionLine(l, "gh_login"))
	}
	if m.Permissions.UsageGateways {
		out = append(out, permissionLine(l, "usage_gateways"))
	}
	if m.Permissions.AgentRuntimes {
		out = append(out, permissionLine(l, "agent_runtimes"))
	}
	if m.Permissions.ErrorReports {
		out = append(out, permissionLine(l, "error_reports"))
	}
	if grant := m.Permissions.Storage; grant != nil {
		out = append(out, storageLine(l, grant))
	}
	if m.Permissions.AuthRelay {
		out = append(out, permissionLine(l, "auth_relay"))
	}
	if schemes := m.Schemes(); len(schemes) > 0 {
		out = append(out, secretProviderLine(l, "secret_provider_title", schemes))
	}
	if to := m.ContributesTo(); len(to) > 0 {
		out = append(out, PermissionLine{Key: "contributes_to", Title: permissionText(l, "contributes_title"),
			Detail: permissionText(l, "contributes_detail", i18n.Args{"names": strings.Join(to, permissionText(l, "list_separator"))})})
	}
	if chans := m.Channels(); len(chans) > 0 {
		var names []string
		for _, c := range chans {
			names = append(names, c.Name)
		}
		out = append(out, PermissionLine{Key: "channel", Title: permissionText(l, "channel_title"),
			Detail: permissionText(l, "channel_detail", i18n.Args{"names": strings.Join(names, permissionText(l, "list_separator"))})})
	}
	if sends := m.SendsAsUser(); len(sends) > 0 {
		out = append(out, sendsAsUserLine(l, permissionText(l, "sends_as_user_title"), sends))
	}
	// What it can take back is shown, not agreed to: only here, never in the digest, in AddedIn or in MorePermissions, as a
	// recall happens only once the user asked for it on their own device.
	if recalls := m.Recalls(); len(recalls) > 0 {
		var names []string
		for _, c := range recalls {
			names = append(names, c.Name)
		}
		out = append(out, PermissionLine{Key: "recalls_sent", Title: permissionText(l, "recalls_sent_title"),
			Detail: permissionText(l, "recalls_sent_detail", i18n.Args{"names": strings.Join(names, permissionText(l, "list_separator"))})})
	}
	if topics := m.Topics(); len(topics) > 0 {
		var what []string
		for _, t := range topics {
			what = append(what, permissionText(l, "event_"+strings.ReplaceAll(t, ".", "_")))
		}
		out = append(out, PermissionLine{Key: "events", Title: permissionText(l, "events_title"), Detail: strings.Join(what, permissionText(l, "sentence_separator"))})
	}
	if surfaces := m.Surfaces(); len(surfaces) > 0 {
		title := permissionText(l, "surfaces_title")
		if slices.Contains(surfaces, "capsule_card") {
			title = permissionText(l, "surfaces_external_title")
		}
		out = append(out, PermissionLine{Key: "ui_surfaces", Title: title, Detail: surfacesDetail(l, m, surfaces)})
	}
	if len(m.Secrets) > 0 {
		names := make([]string, 0, len(m.Secrets))
		for _, s := range m.Secrets {
			names = append(names, s.Title)
		}
		out = append(out, PermissionLine{Key: "secrets", Title: permissionText(l, "secrets_title"), Detail: strings.Join(names, permissionText(l, "list_separator"))})
	}
	if len(m.Tools) > 0 {
		out = append(out, toolsLine(l, m.Tools, permissionText(l, "tools_title")))
	}
	if len(m.HostIntegrations) > 0 {
		out = append(out, hostIntegrationLines(l, m.HostIntegrations, permissionText(l, "host_integrations_title")))
	}
	if m.NodeProgram() != nil {
		out = append(out, nodeLines(l, m.Node, nil)...)
		if names := nodeSecrets(m, nil); len(names) > 0 {
			out = append(out, PermissionLine{Key: "node_secrets", Title: permissionText(l, "node_secrets_title"), Detail: strings.Join(names, permissionText(l, "list_separator"))})
		}
	}
	return out
}

// Added is what upgrading from old to m adds to what the plugin may do, in Explain's words: running a program when old
// ran none, the hosts and secrets old did not have, the hub's token or launchers' keys when old did not get them, the
// channels and the extensions it contributes to that old did not have, and the connectors that send as the user
// that old did not. Nil when nothing is added.
func Added(old, m *Manifest) []PermissionLine { return AddedIn(i18n.Default, old, m) }

// AddedIn generates only the permissions added by an upgrade, in the requested language.
func AddedIn(l i18n.Lang, old, m *Manifest) []PermissionLine {
	var out []PermissionLine
	if m.Runtime != nil && old.Runtime == nil {
		out = append(out, permissionLine(l, "runs_code"))
	}
	if more := NewTools(old, m); len(more) > 0 {
		out = append(out, toolsLine(l, more, permissionText(l, "tools_more_title")))
	}
	var moreIntegrations []HostIntegrationSpec
	for _, h := range m.HostIntegrations {
		if !slices.Contains(old.HostIntegrations, h) {
			moreIntegrations = append(moreIntegrations, h)
		}
	}
	if len(moreIntegrations) > 0 {
		out = append(out, hostIntegrationLines(l, moreIntegrations, permissionText(l, "host_integrations_title")))
	}
	var hosts []string
	var configured []ConfiguredNetwork
	for _, h := range m.Permissions.normalized().Network {
		if slices.Contains(old.Permissions.Network, h) {
			continue
		}
		if c, ok := ParseConfigNetwork(h); ok {
			configured = append(configured, c) // agreed to again like any host (the consent digest has it as written)
		} else {
			hosts = append(hosts, h)
		}
	}
	if len(hosts) > 0 {
		out = append(out, PermissionLine{Key: "network", Title: permissionText(l, "network_more_title"), Detail: strings.Join(hosts, permissionText(l, "list_separator"))})
	}
	out = append(out, configuredLines(l, m, configured, nil)...)
	if m.Permissions.FullHub() && !old.Permissions.FullHub() {
		out = append(out, permissionLine(l, "hub_api"))
	} else if !m.Permissions.FullHub() {
		var more []string
		for _, s := range m.Permissions.normalized().Hub {
			if !(old.Permissions.FullHub() && InEveryScope(s)) && !slices.Contains(old.Permissions.Hub, s) {
				more = append(more, s)
			}
		}
		if len(more) > 0 {
			out = append(out, hubLine(l, permissionText(l, "hub_more_title"), more))
		}
	}
	if m.Permissions.LauncherKeys && !old.Permissions.LauncherKeys {
		out = append(out, permissionLine(l, "launcher_keys"))
	}
	if m.Permissions.GhLogin && !old.Permissions.GhLogin {
		out = append(out, permissionLine(l, "gh_login"))
	}
	if m.Permissions.UsageGateways && !old.Permissions.UsageGateways {
		out = append(out, permissionLine(l, "usage_gateways"))
	}
	if m.Permissions.AgentRuntimes && !old.Permissions.AgentRuntimes {
		out = append(out, permissionLine(l, "agent_runtimes"))
	}
	if m.Permissions.ErrorReports && !old.Permissions.ErrorReports {
		out = append(out, permissionLine(l, "error_reports"))
	}
	if !old.Permissions.Storage.covers(m.Permissions.Storage) {
		out = append(out, storageLine(l, m.Permissions.Storage))
	}
	if m.Permissions.AuthRelay && !old.Permissions.AuthRelay {
		out = append(out, permissionLine(l, "auth_relay"))
	}
	var schemes []string
	for _, s := range m.Schemes() {
		if !slices.Contains(old.Schemes(), s) {
			schemes = append(schemes, s)
		}
	}
	if len(schemes) > 0 {
		out = append(out, secretProviderLine(l, "secret_provider_more_title", schemes))
	}
	var to []string
	for _, owner := range m.ContributesTo() {
		if !slices.Contains(old.ContributesTo(), owner) {
			to = append(to, owner)
		}
	}
	if len(to) > 0 {
		out = append(out, PermissionLine{Key: "contributes_to", Title: permissionText(l, "contributes_more_title"),
			Detail: permissionText(l, "contributes_detail", i18n.Args{"names": strings.Join(to, permissionText(l, "list_separator"))})})
	}
	if chans := newChannels(old, m); len(chans) > 0 {
		var names []string
		for _, c := range chans {
			names = append(names, c.Name)
		}
		out = append(out, PermissionLine{Key: "channel", Title: permissionText(l, "channel_more_title"),
			Detail: permissionText(l, "channel_detail", i18n.Args{"names": strings.Join(names, permissionText(l, "list_separator"))})})
	}
	if sends := newSendsAsUser(old, m); len(sends) > 0 {
		out = append(out, sendsAsUserLine(l, permissionText(l, "sends_as_user_more_title"), sends))
	}
	var topics []string
	for _, t := range m.Topics() {
		if !slices.Contains(old.Topics(), t) {
			topics = append(topics, permissionText(l, "event_"+strings.ReplaceAll(t, ".", "_")))
		}
	}
	if len(topics) > 0 {
		out = append(out, PermissionLine{Key: "events", Title: permissionText(l, "events_more_title"), Detail: strings.Join(topics, permissionText(l, "sentence_separator"))})
	}
	var surfaces []string
	for _, sl := range m.Surfaces() {
		if !slices.Contains(old.Surfaces(), sl) {
			surfaces = append(surfaces, sl)
		}
	}
	if len(surfaces) > 0 {
		out = append(out, PermissionLine{Key: "ui_surfaces", Title: permissionText(l, "surfaces_more_title"), Detail: surfacesDetail(l, m, surfaces)})
	}
	var names []string
	for _, s := range newSecrets(old, m) {
		names = append(names, s.Title)
	}
	if len(names) > 0 {
		out = append(out, PermissionLine{Key: "secrets", Title: permissionText(l, "secrets_more_title"), Detail: strings.Join(names, permissionText(l, "list_separator"))})
	}
	if m.NodeProgram() != nil {
		out = append(out, nodeLines(l, m.Node, old.NodeProgram())...)
		if names := nodeSecrets(m, old); len(names) > 0 {
			out = append(out, PermissionLine{Key: "node_secrets", Title: permissionText(l, "node_secrets_more_title"), Detail: strings.Join(names, permissionText(l, "list_separator"))})
		}
	}
	return out
}

// newSecrets are m's secrets whose names old did not declare, whether required or optional.
func newSecrets(old, m *Manifest) []SecretSpec {
	var out []SecretSpec
	for _, s := range m.Secrets {
		if !slices.ContainsFunc(old.Secrets, func(o SecretSpec) bool { return o.Name == s.Name }) {
			out = append(out, s)
		}
	}
	return out
}

// newChannels are m's channel contributions whose ids old does not contribute. A channel's messages reach the
// assistant as the owner's own (and can answer the owner's Agent asks), so a new one needs the user's consent.
func newChannels(old, m *Manifest) []channel.Item {
	var out []channel.Item
	for _, c := range m.Channels() {
		if !slices.ContainsFunc(old.Channels(), func(o channel.Item) bool { return o.ID == c.ID }) {
			out = append(out, c)
		}
	}
	return out
}

// surfacesDetail describes consented UI placements while preserving extension-authored titles.
func surfacesDetail(l i18n.Lang, m *Manifest, surfaces []string) string {
	var parts []string
	for _, name := range surfaces {
		if name == SurfaceShortcuts {
			var titles []string
			for _, s := range m.Shortcuts() {
				titles = append(titles, s.Title)
			}
			parts = append(parts, permissionText(l, "shortcuts_title")+strings.Join(titles, permissionText(l, "list_separator")))
			continue
		}
		sl, _ := Slot(name)
		var titles []string
		for _, u := range m.UIs() {
			if u.Placement == name {
				titles = append(titles, u.Title)
			}
		}
		parts = append(parts, permissionText(l, "slot_"+sl.Name)+permissionText(l, "label_separator")+strings.Join(titles, permissionText(l, "list_separator")))
	}
	return strings.Join(parts, permissionText(l, "sentence_separator"))
}

// configuredLines say what each "config:" entry lets the extension reach: the address the user writes in its
// settings, the one there now when config has it, and that http is allowed when the manifest says so.
func configuredLines(l i18n.Lang, m *Manifest, cs []ConfiguredNetwork, config json.RawMessage) []PermissionLine {
	var out []PermissionLine
	sc, _ := ParseSchema(m.Configuration)
	for _, c := range cs {
		field := c.Field
		if sc != nil {
			for _, f := range sc.Fields {
				if f.Name == c.Field && f.Title != "" {
					field = f.Title
				}
			}
		}
		now := permissionText(l, "network_config_unset")
		if t, _ := ConfiguredTarget(c, config); t.Host != "" {
			now = net.JoinHostPort(t.Host, t.Port)
		}
		key := "network_config_detail"
		if c.HTTP {
			key = "network_config_detail_http"
		}
		out = append(out, PermissionLine{Key: "network_config", Title: permissionText(l, "network_config_title"),
			Detail: permissionText(l, key, i18n.Args{"field": field, "host": now})})
	}
	return out
}
