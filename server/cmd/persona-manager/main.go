// Command persona-manager is the managers' program: the built-in manager (the assistant), a manager made in the app,
// their extensions and the computers they connect (and the node of this computer they start), without the hub
// (persona-hub). personad, the compatibility entry, runs the same commands as `personad assistant`, `personad manager
// …`, `personad extension …` (internal/managercli; docs/server/server.md "Programs").
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/nwverse/persona/server/internal/clitext"
	"github.com/nwverse/persona/server/internal/extension/sandbox"
	"github.com/nwverse/persona/server/internal/extension/sourcevalidate"
	"github.com/nwverse/persona/server/internal/extmgr"
	"github.com/nwverse/persona/server/internal/managercli"
	"github.com/nwverse/persona/server/internal/nodecmd"
	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/update"
	"github.com/nwverse/persona/server/internal/voice"
)

// What the build wrote in (-X), as personad's (the hub's update checks a program by `version -v`).
var (
	version   = "dev"
	channel   = ""
	build     = ""
	updateURL = ""
	commit    = ""
	image     = ""
)

func buildInfo() update.Build {
	b := update.Make(version, channel, build, updateURL)
	b.Commit, b.Image = commit, image
	return b
}

const usage = `usage:
  persona-manager assistant  the built-in manager (the Persona tab's assistant) on the hub's bus; the hub starts it
                           unless hub.json says assistant.external (token: stdin or PERSONA_ASSISTANT_TOKEN)
  persona-manager assistant drop-shared-tables [--yes]
                           drop the built-in manager's old tables from the hub's database, once its data moved
  persona-manager assistant north-star [--date YYYY-MM-DD] [--split RFC3339] [--tz Area/City] [--brief en]
                           read the interruption and availability north star from the databases, read-only
  persona-manager join --builtin <hub address> [<code>] [--install]
                           bind the built-in manager's own service to the hub (persona-hub manager-code prints a code;
                           left out, it is read from PERSONA_MANAGER_CODE, which other users of the machine cannot
                           read); --install: then install that service (persona-hub setup does both on a new hub)
  persona-manager install|uninstall|status --builtin
                           the built-in manager's own service: install (and start) it, remove it, show it
  persona-manager join|run|install|uninstall|status …
                           a manager made in the app: trade its install code, run it, install it as a service
                           (docs/assistant/managers.md; persona-manager manager -h for the details)
  persona-manager extension …  the extensions on this machine (docs/extensions/extensions.md "Command line")
  persona-manager voice     the built-in voice extension (the manager runs it)
  persona-manager tailcat-key  print the built-in manager's tailcat key (created the first time)
  persona-manager node …    this computer's node, when the manager runs it as itself: with no persona-node or
                           personad of its build beside it, or PERSONA_SPLIT_PROGRAMS=0 (personad node …)
  persona-manager version [-v]  the version; -v adds the channel and build number`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if clitext.IsHelpArg(args[0]) || args[0] == "help" {
		fmt.Println(usage)
		return
	}
	// An extension's sandbox and its source check run this program again (docs/extensions/extensions.md "Sandbox").
	if args[0] == sandbox.ValidationRunCommand {
		os.Exit(sandbox.RunValidation(args[1:]))
	}
	if args[0] == sourcevalidate.Command {
		os.Exit(sourcevalidate.Run(args[1:], os.Stdin, os.Stdout))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if args[0] == sandbox.RunCommand { // it passes signals on itself
		stop()
		os.Exit(sandbox.Run(args[1:], os.Stderr))
	}
	// This computer's node, when the manager starts it as itself (`<self> node --stdio`), and the commands it runs. The
	// manager starts persona-node or personad of its build beside it instead, and itself only when neither is there or
	// with PERSONA_SPLIT_PROGRAMS=0 (devices.Machine.localNode).
	if handled, err := nodecmd.Run(ctx, stop, args, nodecmd.Build{Version: version, Program: nodeproto.ProgramPersonad}); handled {
		finish(err)
		return
	}
	if args[0] == "manager" { // persona-manager manager <command>: personad's form
		finish(managercli.Manager(ctx, buildInfo(), args[1:]))
		return
	}
	var err error
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Println(version)
		if len(args) > 1 && args[1] == "-v" {
			b := buildInfo()
			fmt.Printf("channel %s, build %d\n", b.Channel, b.Number)
		}
	case "assistant":
		err = managercli.Assistant(ctx, buildInfo(), args[1:], os.Stdin)
	case "tailcat-key":
		err = managercli.TailcatKey("tailcat-key", args[1:])
	case "extension", "ext", "plugin":
		err = extmgr.ExtensionCommand(ctx, args[1:], os.Stdin, os.Stdout, os.Stderr)
	case "voice":
		err = voice.Run(ctx)
	default: // join, run, install, uninstall, status (manager.Main says what it does not know)
		err = managercli.Manager(ctx, buildInfo(), args)
	}
	finish(err)
}

// finish reports how a command ended: a usage mistake with exit 2, -h with 0, any other error fatally.
func finish(err error) {
	var ue clitext.UsageError
	if errors.As(err, &ue) {
		fmt.Fprintln(os.Stderr, string(ue))
		os.Exit(2)
	}
	if errors.Is(err, flag.ErrHelp) {
		os.Exit(0)
	}
	if err != nil {
		log.Fatal(err)
	}
}
