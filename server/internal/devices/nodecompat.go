package devices

import (
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
)

// Codes of proto.Machine.NodeIncompatible.
const (
	incompatibleNodeOld = "node_protocol_old" // an older protocol, or a required capability missing: update the node
	incompatibleNodeNew = "node_protocol_new" // the node needs a newer manager
)

// nodeCompat is what the manager makes of a node's handshake (docs/protocol/protocol.md "Node handshake").
type nodeCompat struct {
	// Incompatible is a NodeIncompatible code, "" when the manager can drive the node.
	Incompatible string
	// Missing are the required capabilities the node did not announce.
	Missing []string
}

// compatOf judges a node by its protocol and capabilities, never by its build.
func compatOf(info proto.NodeInfo) nodeCompat {
	if info.Protocol == 0 { // from before the handshake: driven as before (nodeHas reads its old flags)
		return nodeCompat{}
	}
	var c nodeCompat
	for _, want := range nodeproto.Required {
		if !slices.Contains(info.Capabilities, want) {
			c.Missing = append(c.Missing, want)
		}
	}
	switch {
	case info.Protocol < nodeproto.MinVersion || len(c.Missing) > 0:
		c.Incompatible = incompatibleNodeOld
	case info.ProtocolMin > nodeproto.Version:
		c.Incompatible = incompatibleNodeNew
	}
	return c
}

// nodeHas says whether the node can do capability cap: what it announced, or for a node from before the handshake,
// the node.info flag that said so then (a capability without one is taken as there: those nodes all had it).
func nodeHas(info proto.NodeInfo, cap string) bool {
	if info.Protocol > 0 {
		return slices.Contains(info.Capabilities, cap)
	}
	switch cap {
	case nodeproto.AssistantConfigDir:
		return false // no pre-handshake node honors config_dir
	case nodeproto.ModelConfiguration:
		return info.Version == "" || info.ModelConfiguration // "": a test's or a dev node that said nothing
	case nodeproto.NodeUpdate:
		return info.NodeUpdate
	case nodeproto.Ultracode, nodeproto.HealthSpaces, nodeproto.SecretRun, nodeproto.ModelHTTP: // came after the handshake: no node from before it has them
		return info.Version == ""
	}
	return true
}

// buildDiffers: the node's personad is another build than the hub's. Builds are named <commit>[-dirty]-<time>
// (deploy.sh); dev builds ("dev", empty) are never flagged. The local node is the hub binary itself. This is an
// upgrade hint (the update offer, the hub's automatic redeploy), not compatibility: compatOf is.
func buildDiffers(hub, node string) bool {
	if hub == "" || node == "" || hub == "dev" || node == "dev" {
		return false
	}
	return hub != node
}

// nodeMismatch is proto.Machine.NodeMismatch for a connected node: the redeploy offer, for another build or for a
// node this manager cannot drive.
func nodeMismatch(hub string, info proto.NodeInfo) bool {
	return buildDiffers(hub, info.Version) || compatOf(info).Incompatible != ""
}

// usableWhileIncompatible are the node methods the manager still calls on a node it cannot drive otherwise: enough
// to tell it is there, whether it is idle, and to update it.
func usableWhileIncompatible(method string) bool {
	return method == "ping" || method == "sessions.list" || method == "node.info" || strings.HasPrefix(method, "node.update.")
}
