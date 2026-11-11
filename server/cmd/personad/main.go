// Command personad is the Persona server: hub, node, hook and mcp modes in one binary.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/nwverse/persona/server/internal/extmgr"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/nodecmd"
	"github.com/nwverse/persona/server/internal/nodeproto"

	"github.com/nwverse/persona/server/internal/extension/sandbox"
	"github.com/nwverse/persona/server/internal/extension/sourcevalidate"
	"github.com/nwverse/persona/server/internal/hubcli"
	"github.com/nwverse/persona/server/internal/managercli"
	"github.com/nwverse/persona/server/internal/relay"
	"github.com/nwverse/persona/server/internal/shareproxy"
	"github.com/nwverse/persona/server/internal/supervise"
	"github.com/nwverse/persona/server/internal/update"
	"github.com/nwverse/persona/server/internal/voice"
)

// What the build wrote in (-X): the version for people, and for updates (docs/operations/hub-update.md) the channel
// (release / docker / dev), the build number (UTC YYYYMMDDHHMM), where the publisher's dl/ is, the commit it was
// built from ("-dirty" after it with changes not committed: a release has to contain it to be installed by itself),
// and for the Docker image the image it was built as (where to look for newer ones).
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
` + nodecmd.LaunchUsage + `
` + hubcli.Usage + `
  personad hub tailcat-key  print the built-in manager's tailcat key (created the first time), for "node tailcat init --hub"
  personad assistant        the assistant (the Persona tab's) as its own process on the hub's bus; the hub starts it
                           unless hub.json says assistant.external (token: stdin or PERSONA_ASSISTANT_TOKEN)
  personad assistant drop-shared-tables [--yes]  drop the built-in manager's old tables from the hub's database, once
                           its data moved into its own (without --yes: list them)
  personad assistant north-star [--date YYYY-MM-DD] [--split RFC3339] [--tz Area/City] [--brief en]
                           read the interruption and availability north star from the databases, read-only
  personad manager join|run|install|uninstall|status
                           another manager made in the app: trade its install code, run it, install it as a service
                           (docs/assistant/managers.md; personad manager for the details)
` + nodecmd.NodeUsage + `
` + nodecmd.Usage + `
  personad share-http --listen <address> --upstream <loopback HTTP origin> --public-origin <HTTP sharing origin> --hub-config <private configuration>
                           isolated anonymous sharing ingress on a separate unprivileged port (docs/server/agent-sharing.md)
  personad relay [config]   run the push relay (config ~/.persona/relay.json): pushes for hubs without the app's APNs
                           key, payloads sealed to the phone (docs/operations/push-relay.md)
  personad supervise [--manager-only [--hub <hub address>]]
                           the hub and the built-in manager side by side in one container, each started again when it
                           stops (the hub image runs it; docs/operations/docker.md)
  personad voice            the built-in voice extension: the hub runs it (hub.json extensions, docs/extensions/extensions.md "Built-in voice extension")
  personad extension …      extensions on the hub's machine: list, install, upgrade, rollback, enable, disable, config,
                           secret, uninstall, markets (docs/extensions/extensions.md "Command line"; personad extension for the details;
                           also personad ext …, and personad plugin … as before)
` + nodecmd.SysmonUsage + `
  personad release manifest|verify|check-next …
                           the publisher's release manifest and checks (scripts/release/publish.sh, docs/operations/hub-update.md "Public publishing")
  personad update-watch <pending>
                           the watchdog the hub starts over its own update (docs/operations/hub-update.md "Updating")
  personad version [-v]     the version; -v adds the channel and build number`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, named(usage))
		os.Exit(2)
	}
	if nodecmd.IsHelpArg(os.Args[1]) || os.Args[1] == "help" { // asked for: stdout and exit 0, as persona-hub and persona-manager
		fmt.Println(named(usage))
		return
	}
	if os.Args[1] == sandbox.ValidationRunCommand {
		os.Exit(sandbox.RunValidation(os.Args[2:]))
	}
	if os.Args[1] == sourcevalidate.Command {
		os.Exit(sourcevalidate.Run(os.Args[2:], os.Stdin, os.Stdout))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if os.Args[1] == sandbox.RunCommand { // an extension's sandbox (docs/extensions/extensions.md "Sandbox"); it passes signals on itself
		stop()
		os.Exit(sandbox.Run(os.Args[2:], os.Stderr))
	}
	var err error
	if handled, nerr := nodecmd.Run(ctx, stop, os.Args[1:], nodecmd.Build{Version: version, Program: nodeproto.ProgramPersonad}); handled {
		finish(nerr)
		return
	}
	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Println(version)
		if len(os.Args) > 2 && os.Args[2] == "-v" {
			b := buildInfo()
			fmt.Printf("channel %s, build %d\n", b.Channel, b.Number)
		}
	case "release":
		err = runRelease(os.Args[2:])
	case "update-watch": // the old binary watching an update come up (hub/selfupdate.go)
		err = hubcli.UpdateWatch(ctx, os.Args[2:])
	case "hub":
		if len(os.Args) > 2 && os.Args[2] == "tailcat-key" { // the built-in manager's key, as before (managercli)
			err = managercli.TailcatKey("hub tailcat-key", os.Args[3:])
			break
		}
		err = hubcli.Hub(ctx, buildInfo(), os.Args[2:])
	case "assistant": // the assistant as its own process on the hub's bus (the hub starts it, docs/assistant/assistant.md)
		err = managercli.Assistant(ctx, buildInfo(), os.Args[2:], os.Stdin)
	case "manager": // a manager made in the app: join, run, install as a service (docs/assistant/managers.md)
		err = managercli.Manager(ctx, buildInfo(), os.Args[2:])
	case "share-http":
		err = shareproxy.Run(ctx, os.Args[2:], os.Stderr)
	case "relay":
		if len(os.Args) > 3 { // only the config path: anything after it would be ignored
			err = usageError("usage: " + commandName() + " relay [config]")
			break
		}
		path := filepath.Join(identity.HomeDir(), "relay.json")
		if len(os.Args) > 2 {
			path = os.Args[2]
		}
		err = relay.Run(ctx, path)
	case "supervise": // the hub image's: the hub and the built-in manager side by side (docs/operations/docker.md)
		if err = supervise.Run(ctx, os.Args[2:], os.Stderr); errors.Is(err, supervise.ErrStopped) {
			os.Exit(1) // it said why in its log
		}
	case "voice":
		err = voice.Run(ctx)
	case "extension", "ext", "plugin": // plugin: its name before #1844
		err = extmgr.ExtensionCommand(ctx, os.Args[2:], os.Stdin, os.Stdout, os.Stderr)
	default:
		fmt.Fprintln(os.Stderr, named(usage))
		os.Exit(2)
	}
	finish(err)
}

// finish reports how a command ended: a usage mistake with exit 2, -h with 0, any other error fatally.
func finish(err error) {
	var ue usageError
	if errors.As(err, &ue) { // a usage mistake: what to type instead, exit 2 like the other usage mistakes
		fmt.Fprintln(os.Stderr, string(ue))
		os.Exit(2)
	}
	if errors.Is(err, flag.ErrHelp) { // -h on a subcommand's flags: the flag package printed its help, nothing failed
		os.Exit(0)
	}
	if err != nil {
		log.Fatal(err)
	}
}

// The usage helpers are the device commands' (nodecmd): one name for the program in all it prints.
type usageError = nodecmd.UsageError

func commandName() string      { return nodecmd.CommandName() }
func named(text string) string { return nodecmd.Named(text) }
