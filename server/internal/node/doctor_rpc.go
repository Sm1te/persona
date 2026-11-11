package node

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"

	"github.com/nwverse/persona/server/internal/nodeclient"
)

// The web machine page's "What this computer can do" card asks the node for the same report `personad node doctor` prints
// (docs/server/machines.md "Doctor: what this computer can do"). The checks run in this process, so the sockets are looked at by the node that owns them.
// node.doctor reads and nothing else; the only thing either method may write is what `--fix` writes
// (writeGeneratedConfig, Persona's own generated files), and only when the caller says fix. The LaunchAgent fix is
// the command line's alone (InsideNode).

// doctorOptions are the places this node looks at: the directory and program it writes its generated files for, the
// sockets it serves, and the user's Claude Code settings (read, never written).
func (n *Node) doctorOptions() DoctorOptions {
	if n.doctorAt != nil {
		return n.doctorAt()
	}
	opts := DoctorOptions{
		Dir: n.dir, Bin: n.bin,
		NodeSock: nodeclient.SocketPath(), RuntimeSock: RuntimeSocketPath(),
		ClaudeConfigDir: claudeDirOf(n.home), ClaudeVersion: claudeVersion, ClaudeProbe: claudeProbe,
	}
	if def, err := DefaultDoctorOptions(); err == nil {
		opts.HerdrSock = def.HerdrSock
	}
	// The LaunchAgent is looked at, never reloaded from here: that would stop this node in the middle of the call.
	opts.withLaunchd(n.home)
	opts.InsideNode = true
	if runtime.GOOS != "windows" {
		opts.Preflight = n.preflight
	}
	return opts
}

// doctor is node.doctor ({"fix": bool, "preflight": bool}) and node.doctor.fix (always fix). The report is
// DoctorReport as it is. "preflight": false leaves the path-access check out: the assistant's readiness only wants
// Claude Code's version, asks every few minutes, and should neither wait for the reads nor make them each time.
func (n *Node) doctor(ctx context.Context, params json.RawMessage, fix bool) (any, error) {
	var a struct {
		Fix       bool  `json:"fix"`
		Preflight *bool `json:"preflight"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &a); err != nil {
			return nil, errors.New("bad node.doctor params")
		}
	}
	opts := n.doctorOptions()
	opts.Fix = fix || a.Fix
	if a.Preflight != nil && !*a.Preflight {
		opts.Preflight = nil
	}
	return RunDoctor(ctx, opts), nil
}
