package node

import (
	"context"
	"github.com/nwverse/persona/server/internal/compat"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/identity"
)

// The agents Persona starts get Persona's skills (persona-office, #1300) as a plugin in the node's own directory,
// loaded for that session only (claude --plugin-dir), like its --settings and --mcp-config: nothing goes into the
// machine's Claude or Codex skills, so sessions the user starts themselves are left as they are. The assistant gets
// them in its directory instead (assistantskills.Sync).

// pluginMinVersion is the first Claude Code taken to know --plugin-dir: an older one would refuse to start over it,
// so it starts without the skills.
var pluginMinVersion = [3]int{2, 1, 0}

func (n *Node) pluginDir() string { return filepath.Join(n.dir, "agent-plugin") }

// The office skill's script runs without a card on the phone: the start allows exactly `sh <its script> …`, at the
// absolute path the node itself wrote it to (--allowedTools: Claude Code's own rule, for that session only, added to
// whatever settings the launcher brings). Not a pattern: `*/persona-office/scripts/office.sh` would also let through a
// script of that name the agent wrote in its own directory (acceptEdits writes there without asking) — tested, it ran
// (#1300). Nor the skill's allowed-tools: that makes starting the skill itself a permission request, and a static
// SKILL.md cannot name the node's path. Claude Code splits `a; b` and `a && b` and asks for every part, and does not
// write under .claude/ without asking, so the assistant's copy cannot be changed behind the rule either.
func officeRule(script string) string { return "Bash(sh " + script + " *)" }

func (n *Node) agentOfficeScript() string {
	return filepath.Join(n.pluginDir(), "skills", "persona-office", "scripts", "office.sh")
}

// agentAllowedTools is the --allowedTools of a Claude Agent Persona starts or resumes (claudeCommandLine): its own
// Persona tools (assistantwire.AgentOwnTools, #4966) under both names the node's mcp.json registers them by, and with
// the plugin the office skill's script. Like the office rule it is Claude Code's own rule for that process only:
// sessions the user starts themselves, the machine's Claude settings and every other tool or MCP server are left as
// they are, and a deny rule of the user's still wins. The phone's tools, the connectors' and dispatch keep their card.
func (n *Node) agentAllowedTools(plugins bool) string {
	tools := compat.MCPAllowedToolNames(assistantwire.AgentOwnTools, true)
	if plugins {
		tools = append(tools, officeRule(n.agentOfficeScript()))
	}
	return strings.Join(tools, ",")
}

// assistantTools are the assistant's --allowedTools: Persona's tools, and its copy of the office skill's script.
func (n *Node) assistantTools(legacyMCP bool) string {
	tools := assistantAllowedTools
	if legacyMCP {
		tools += ",mcp__" + compat.MCPServer
	}
	return tools + "," + officeRule(n.assistantOfficeScript())
}

func (n *Node) assistantOfficeScript() string {
	return filepath.Join(identity.AssistantDir(n.home), ".claude", "skills", "persona-office", "scripts", "office.sh")
}

// claudeLoadsPlugins says whether this machine's claude takes --plugin-dir; not when its version is not known.
func (n *Node) claudeLoadsPlugins(ctx context.Context) bool {
	return versionAtLeast(n.claudeV.get(ctx), pluginMinVersion)
}

// claudeVersionCache is claude's version as last asked: at each hello, and at a start when it is older than
// claudeVersionFor (asking runs a login shell).
type claudeVersionCache struct {
	mu sync.Mutex
	v  string
	at time.Time
}

const claudeVersionFor = 10 * time.Minute

// claudeVersionOf is how the version is asked (tests replace it).
var claudeVersionOf = claudeVersion

func (c *claudeVersionCache) set(v string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.v, c.at = v, time.Now()
	return v
}

func (c *claudeVersionCache) get(ctx context.Context) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.at.IsZero() || time.Since(c.at) > claudeVersionFor {
		c.v, c.at = claudeVersionOf(ctx), time.Now()
	}
	return c.v
}

// versionAtLeast says whether version v ("2.1.318", "2.1.0-beta") is min or later; false when it is not one.
func versionAtLeast(v string, min [3]int) bool {
	parts := strings.SplitN(strings.SplitN(v, "-", 2)[0], ".", 3)
	if len(parts) != 3 {
		return false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return false
		}
		if n != min[i] {
			return n > min[i]
		}
	}
	return true
}
