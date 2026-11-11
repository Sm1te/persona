// Package managercli is the managers' command line: `persona-manager <command>` (cmd/persona-manager) and personad's
// `assistant`, `manager …` and `hub tailcat-key` (cmd/personad, the compatibility entry) run the same code. It links
// only the manager's side (docs/server/server.md "Programs"): the hub's commands are internal/hubcli's.
package managercli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/nwverse/persona/server/internal/assistant"
	"github.com/nwverse/persona/server/internal/clitext"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/manager"
	"github.com/nwverse/persona/server/internal/managerconf"
	"github.com/nwverse/persona/server/internal/osproc"
	"github.com/nwverse/persona/server/internal/tunnel"
	"github.com/nwverse/persona/server/internal/update"
)

// Assistant is `assistant [drop-shared-tables …]`: the built-in manager on the hub's bus (the hub starts it, token on
// stdin), or the cleanup of its old tables in the hub's database (docs/server/server.md "The default manager's own
// database").
func Assistant(ctx context.Context, build update.Build, args []string, stdin io.Reader) error {
	if len(args) > 0 && args[0] == "drop-shared-tables" {
		return assistant.DropSharedCommand(ctx, args[1:], os.Stdout, os.Stderr)
	}
	if len(args) > 0 && args[0] == "north-star" {
		return assistant.NorthStarCommand(ctx, args[1:], os.Stdout, os.Stderr)
	}
	if len(args) > 0 {
		return clitext.UsageError("usage: " + clitext.CommandName() + " assistant [drop-shared-tables [--yes] | north-star [--date D] [--split T] [--tz Z]]")
	}
	// What a parent left inheritable (an older hub's listening socket) stops here, not in every node, extension and
	// agent the manager starts. Here, not in main: the sandbox's launcher hands its descriptors on by exec.
	osproc.SealInherited()
	// A service of its own starts again in place (manager.RunBuiltin: a new binary, a new token): what the previous
	// image started is this one's child then, and only this reaps it.
	osproc.ReapInherited()
	if identity.Getenv("PERSONA_ASSISTANT_DEPLOY") == assistant.DeployService {
		// A service of its own (its unit says so): on its binding, waiting when the hub refuses it. Not bound is a
		// configuration mistake: exit 2, which its unit does not restart.
		err := manager.RunBuiltin(ctx, build, os.Stdout)
		var ce manager.ConfigError
		if errors.As(err, &ce) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		return err
	}
	return assistant.Main(ctx, build, stdin)
}

// Manager is `manager join|run|install|uninstall|status …`: a manager made in the app (docs/assistant/managers.md).
// A usage mistake prints the usage and exits 2, -h exits 0, and a configuration mistake exits 2: restarting cannot fix
// it, so its service does not (RestartPreventExitStatus=2).
func Manager(ctx context.Context, build update.Build, args []string) error {
	err := manager.Main(ctx, build, args, os.Stdout)
	if code, done := managerExit(err, os.Stderr); done {
		os.Exit(code)
	}
	return err
}

// managerExit is how Manager ends on err: a usage mistake prints what was wrong (a flag's message, when it says) and
// the usage on stderr with exit 2, -h exits 0, a configuration mistake prints it with exit 2. done false: err is the
// caller's to report.
func managerExit(err error, stderr io.Writer) (code int, done bool) {
	var ce manager.ConfigError
	switch {
	case errors.Is(err, manager.ErrUsage):
		if err != manager.ErrUsage { // it says what was wrong
			fmt.Fprintln(stderr, err)
		}
		fmt.Fprint(stderr, clitext.Named(manager.Usage))
		return 2, true
	case errors.Is(err, manager.ErrHelp):
		return 0, true
	case errors.As(err, &ce):
		fmt.Fprintln(stderr, err)
		return 2, true
	}
	return 0, false
}

// TailcatKey prints the built-in manager's tailcat key, made the first time: the computers reached over tailcat trust
// it (it dials them; managerconf.OwnFile, docs/assistant/managers.md). command is how it was typed ("tailcat-key",
// personad's "hub tailcat-key").
func TailcatKey(command string, args []string) error {
	if len(args) > 0 {
		return clitext.UsageError(clitext.CommandName() + " " + command + " takes no arguments")
	}
	k, err := tunnel.LoadOrCreateHubKey(managerconf.OwnFile(identity.HomeDir(), managerconf.TailcatKey))
	if err != nil {
		return err
	}
	fmt.Println(k.Public())
	return nil
}
