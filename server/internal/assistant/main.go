package assistant

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/nwverse/persona/sdk/go/wire"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/bus"
	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/devices"
	"github.com/nwverse/persona/server/internal/devicewire"
	"github.com/nwverse/persona/server/internal/errreport"
	"github.com/nwverse/persona/server/internal/extmgr"
	"github.com/nwverse/persona/server/internal/extwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/managerconf"
	"github.com/nwverse/persona/server/internal/managerupdate"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/update"
)

// Main is `personad assistant`: the assistant as its own process, on the hub's bus. The hub starts one beside itself
// (hub/assistant_process.go) and hands it its token on stdin; one run elsewhere (hub.json assistant.external) gets it
// from PERSONA_ASSISTANT_TOKEN (`personad hub assistant-token` makes one); one that is a service of its own on the
// hub's machine (DeployService, hub.json assistant.mode service) has it in its manager.json (managerconf.Binding). It keeps its state in the hub's Postgres,
// found as the hub finds it: hub.json's database_url, PERSONA_DATABASE_URL over it.
func Main(ctx context.Context, build update.Build, stdin io.Reader) error {
	return MainWith(ctx, build, stdin, MainHooks{})
}

// MainHooks are what a caller of MainWith gets of the running manager besides.
type MainHooks struct {
	// Ready is given the manager's Idle once it is made (before it connects to the hub).
	Ready func(Idle)
}

// MainWith is Main with h.
func MainWith(ctx context.Context, build update.Build, stdin io.Reader, h MainHooks) error {
	errs := newErrorTee(build) // its log's error lines become error reports (docs/extensions/extensions.md "Error reports")
	logger := log.New(errs, "assistant: ", log.LstdFlags)
	cfg, err := readHubConfig()
	if err != nil {
		return err
	}
	if cfg.DatabaseURL == "" {
		return errors.New("no database_url in hub.json (or PERSONA_DATABASE_URL)")
	}
	deploy := identity.Getenv("PERSONA_ASSISTANT_DEPLOY")
	if deploy == "" {
		deploy = "external"
	}
	var token, hub, fingerprint string
	if deploy == DeployService {
		// A service of its own on the hub's machine (hub.json assistant.mode service): bound once with a one-time
		// code (`persona-manager join --builtin`), its hub, token and fingerprint in its manager.json; no stdin.
		b, err := managerconf.ReadBinding(identity.HomeDir())
		if err != nil {
			return err
		}
		token, hub, fingerprint = b.Token, b.Hub, b.Fingerprint
		// The hub takes the token before anything else of it happens (its configuration and files moved, its own
		// database made or its data moved, its computers and extensions run): any refusal is a revoke.
		var client *http.Client
		if fingerprint != "" {
			client = pinnedHTTPClient(fingerprint)
		}
		if err := tokenTaken(ctx, hub, token, client, logger.Printf); err != nil {
			return err
		}
	} else {
		token = strings.TrimSpace(identity.Getenv("PERSONA_ASSISTANT_TOKEN"))
		if token == "" && stdin != nil {
			r := bufio.NewReader(stdin)
			line, _ := r.ReadString('\n')
			token = strings.TrimSpace(line)
			// Handed the token on stdin (by the hub that started it): it stops when stdin closes, the hub gone.
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			defer cancel()
			go func() {
				_, _ = io.Copy(io.Discard, r)
				logger.Printf("stdin closed (the hub that started it is gone): stopping")
				cancel()
			}()
		}
		if token == "" {
			return errors.New("no bus token: set PERSONA_ASSISTANT_TOKEN (personad hub assistant-token prints one)")
		}
		if hub, fingerprint, err = externalHub(deploy, cfg.Listen); err != nil {
			return err
		}
	}
	home := identity.HomeDir()
	file := managerconf.DefaultFile(home)
	var drop []string
	if deploy != "child" && deploy != DeployService { // elsewhere: the hello's sections are the hub's hub.json's (legacy_config)
		drop = managerconf.HubOnly
	}
	ext, err := moveConfig(home, file, drop, logger.Printf)
	if err != nil {
		return err
	}
	moveFiles(home, logger.Printf)
	o := builtinFiles(home)
	o.ID, o.Hub, o.Token, o.Deploy, o.Errors = assistantwire.AssistantID, hub, token, deploy, errs
	// Its own service stops once the hub refuses its token (bound again, or a hub that starts the built-in manager
	// itself again): nothing of it runs next to the one that has the token (manager.RunBuiltin waits for a new one).
	o.Fingerprint, o.StopWhenRevoked, o.TokenFirst = fingerprint, deploy == DeployService, deploy == DeployService
	o.RotateSecretKey = true
	o.Logf, o.ExtConfig, o.Ready = logger.Printf, ext, h.Ready
	// Its own database, its data moved there once (owndb.go); the hub's while that cannot be yet.
	if o.DatabaseURL, o.OnHubDatabase, err = builtinDatabase(ctx, home, cfg.DatabaseURL, provisionOwn, logger.Printf); err != nil {
		return err
	}
	return run(ctx, build, o)
}

// tokenTaken dials the hub's bus once with token and hangs up (no handshake: no connection of the manager's is taken
// over): bus.ErrRevoked when the hub refuses the token in any way, nil when it took it, and nil too when it did not
// answer (the hub away: the manager runs as usual, and its bus stops it on a refusal later).
func tokenTaken(ctx context.Context, hub, token string, client *http.Client, logf func(string, ...any)) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ws, err := bus.Dial(ctx, hub, token, client)
	switch {
	case err == nil:
		_ = ws.Close(websocket.StatusNormalClosure, "token check")
		return nil
	case errors.Is(err, bus.ErrRevoked), errors.Is(err, bus.ErrRejected):
		return fmt.Errorf("%w (%v)", bus.ErrRevoked, err)
	}
	logf("the hub did not answer (%v): starting; the bus connects when it can", err)
	return nil
}

// DeployService is PERSONA_ASSISTANT_DEPLOY for the built-in manager as a service of its own on the hub's machine
// (its unit sets it): the binding in its manager.json, no token on stdin, hub.json's sections its own to take over
// (the same machine, as for the hub's child).
const DeployService = "service"

// builtinFiles are where the built-in manager's things are in the data directory home: its manager.json and its keys
// in its own directory (managers/assistant, managerconf.OwnFile), its knowledge base where manager.json says (by
// default the data directory's data/), the extensions in the data directory (until they are a part of their own).
func builtinFiles(home string) runOptions {
	return runOptions{Dir: home, ConfigFile: managerconf.DefaultFile(home),
		SecretKey:  managerconf.OwnFile(home, managerconf.SecretKey),
		HandlesDir: filepath.Dir(managerconf.OwnFile(home, managerconf.HandlesKey)),
		TailcatKey: managerconf.OwnFile(home, managerconf.TailcatKey),
		KBDir:      managerconf.KBDir(home)}
}

// moveFiles copies the built-in manager's keys from the data directory into its own directory, once
// (managerconf.MoveFiles), and says what it did; one it could not copy is read where it was (managerconf.OwnFile).
func moveFiles(home string, logf func(string, ...any)) {
	copied, err := managerconf.MoveFiles(home)
	if len(copied) > 0 {
		logf("files: copied %s from %s into %s (once; the data directory keeps them for an older build)",
			strings.Join(copied, ", "), home, managerconf.DefaultDir(home))
	}
	if err != nil {
		logf("files: %s: %v (a key not copied is read where it was)", managerconf.DefaultDir(home), err)
	}
}

// moveConfig takes the manager's sections out of the hub.json in hubDir into its manager.json, once
// (managerconf.MoveExcept), says what it did, and reads the configuration the extensions, connectors and bots run with
// (extmgr.LoadConfig), which run hands on as it is. A failed move, or a configuration that does not read, is an error
// the manager does not start with: it would run without its sections, the user's gate rules (lark_gate,
// connector_gate) among them, and the node would fall back to its own, which let every read through. Whatever starts
// it again (the hub's backoff for the built-in manager, the service for one made in the app) has it say why again,
// until the file is fixed.
func moveConfig(hubDir, file string, drop []string, logf func(string, ...any)) (*extmgr.Config, error) {
	rep, err := managerconf.MoveExcept(hubDir, file, drop)
	if err != nil {
		return nil, fmt.Errorf("configuration: %w (%s)", err, notStarting)
	}
	if len(rep.Moved) > 0 {
		logf("configuration: moved %s from hub.json into %s (once; hub.json keeps its copy for an older build, "+
			"which nothing here reads; the sections as they were are in %s)", strings.Join(rep.Moved, ", "), file, rep.Backup)
	}
	if len(rep.Ignored) > 0 {
		logf("configuration: %s: hub.json's copy differs from %s's, which is the one read; hub.json's is not used "+
			"(kept in %s)", strings.Join(rep.Ignored, ", "), file, rep.Backup)
	}
	if len(rep.Stale) > 0 {
		logf("configuration: hub.json's %s changed after they moved to %s, which is the one read: make the change there",
			strings.Join(rep.Stale, ", "), file)
	}
	for _, u := range rep.Untrimmed {
		logf("configuration: could not cut a copy of hub.json down to the moved sections (it may still hold "+
			"hub.json's secrets): %s", u)
	}
	cfg, err := extmgr.LoadConfig(hubDir, file)
	if err != nil {
		return nil, fmt.Errorf("configuration: %w (%s)", err, notStarting)
	}
	if _, err := readOwnConfig(file); err != nil { // the hello's sections (owner, Agents, kb, models: ownconfig.go)
		return nil, fmt.Errorf("configuration: %s: %w (%s)", file, err, notStarting)
	}
	return cfg, nil
}

// notStarting ends moveConfig's errors.
const notStarting = "the manager does not start without its configuration: fix it, and it starts the next time"

// runOptions are who a manager is and where its things are: the assistant the hub starts (Main), or a manager made
// in the app with a manager.json of its own (RunManager, manager.go).
type runOptions struct {
	ID, Hub, Token, Deploy string
	// Fingerprint pins the hub's TLS key (its public port's self-signed certificate); "" trusts the system's CAs.
	Fingerprint string
	// Dir is its data directory: the extensions; and, unless the fields below say otherwise, the knowledge base
	// (data/), secret.key and handles.key.
	Dir string
	// SecretKey, HandlesDir (handles.key's directory), TailcatKey and KBDir are its own files (builtinFiles for the
	// built-in manager); "" are Dir's (TailcatKey: the machine's, tunnel.HubKeyPath).
	SecretKey, HandlesDir, TailcatKey, KBDir string
	// ConfigFile is its manager.json, with its sections of the configuration (internal/managerconf).
	ConfigFile string
	// ExtConfig is the configuration of its extensions, connectors and bots as moveConfig read it (nil: read there).
	ExtConfig   *extmgr.Config
	DatabaseURL string
	// OnHubDatabase: DatabaseURL is the database the built-in manager shares with the hub (its data not moved into
	// its own yet, owndb.go): it holds the move's lock share there while it runs, so another process's move waits.
	OnHubDatabase bool
	// RotateSecretKey: the built-in manager's, whose secret.key started as a copy of the hub's, rotates it to a key
	// of its own once it runs on its own database (keyrotate.go). A manager made in the app made its own.
	RotateSecretKey bool
	// StopWhenRevoked: once the hub refuses the token, stop (bus.ErrRevoked) rather than try it every minute.
	StopWhenRevoked bool
	// TokenFirst (the built-in manager's own service, whose Main checked the token before anything else): any refusal
	// of it on a reconnect, a plain 401 too, stops it (bus.ErrRevoked): a hub that refuses it starts a built-in manager
	// of its own (a version from before this mode), or bound another service.
	TokenFirst bool
	Logf       func(format string, args ...any)
	// Ready is given the manager's Idle once its service is made (MainHooks.Ready); nil: nobody asks.
	Ready func(Idle)
	// Errors is the process's log writer that reports its error lines (newErrorTee); nil: run makes one.
	Errors *errorTee
}

// run is a manager's process: its database, the computers, the extensions and the assistant itself on the hub's
// bus, until ctx ends (or the token is refused, with StopWhenRevoked: bus.ErrRevoked).
func run(ctx context.Context, build update.Build, o runOptions) error {
	if o.Errors == nil {
		o.Errors = newErrorTee(build)
	}
	stderr := o.Errors
	version := build.Version
	hub, token, deploy, home := o.Hub, o.Token, o.Deploy, o.Dir
	logf := o.Logf
	if o.SecretKey == "" {
		o.SecretKey = filepath.Join(home, managerconf.SecretKey)
	}
	if o.HandlesDir == "" {
		o.HandlesDir = home
	}
	if o.KBDir == "" {
		o.KBDir = filepath.Join(home, "data")
	}
	var httpClient *http.Client
	if o.Fingerprint != "" {
		httpClient = pinnedHTTPClient(o.Fingerprint)
	}
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	if o.OnHubDatabase {
		// Still on the hub's database: the share of the move's lock while it runs, on a connection of its own let go
		// last (after the database closes), so another process's move waits for it (owndb.go).
		share, err := db.HoldManagerShare(ctx, o.DatabaseURL)
		if err != nil {
			return fmt.Errorf("database: %w", err)
		}
		if movedOut(ctx, o.DatabaseURL) { // another start moved its data while this one waited for the share
			share.Release()
			return errors.New("database: its data moved into its own database meanwhile (or the hub's database cannot tell): the next start runs there")
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		watched := make(chan struct{})
		go func() {
			defer close(watched)
			holdShare(ctx, share, o.DatabaseURL, cancel, logf)
		}()
		defer func() { // after the database closed (deferred later): the watcher stopped, then the share goes
			cancel()
			<-watched
			share.Release()
		}()
	}
	database, err := db.Open(ctx, o.DatabaseURL, db.ManagerSet)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer database.Close()
	// Its own secret.key (keyrotate.go), before anything of it reads a secret; the old key's watcher, if any, stops
	// before the database closes.
	keyDone := make(chan struct{})
	if o.RotateSecretKey && !o.OnHubDatabase {
		pending, err := rotateSecretKey(ctx, database, home, o.SecretKey, logf)
		if err != nil {
			return err
		}
		if pending {
			go func() {
				defer close(keyDone)
				keepDroppingOldKey(ctx, database, home, o.SecretKey, logf)
			}()
		} else {
			close(keyDone)
		}
	} else {
		close(keyDone)
	}
	defer func() {
		stop()
		<-keyDone
	}()
	// The user's computers are the manager's: it connects them itself (internal/devices, docs/server/machines.md "Who connects to computers").
	var bh *BusHub
	var svc *Service
	var ext *extmgr.Manager
	// ConfigFile: its usage section (the account-pool gateways and their keys) is the devices', read and written there.
	devs := devices.New(devices.Options{Store: database, ConfigFile: o.ConfigFile, TailcatKey: o.TailcatKey, ManagerID: o.ID,
		Logf: log.New(stderr, "devices: ", log.LstdFlags).Printf,
		Answer: func(ctx context.Context, machine, method string, params json.RawMessage) (any, bool, error) {
			if !waitsOnPerson(method) { // a tool call, a write the gate forwarded: idle (idle.go) waits for it
				svc.nodeAnswering.Add(1)
				defer svc.nodeAnswering.Add(-1)
			}
			if err := svc.machineTrust(ctx, machine); err != nil { // not confirmed yet: nothing of the assistant's (machine_trust.go)
				return nil, true, err
			}
			if out, ok, err := svc.NodeAnswer(ctx, machine, method, params); ok {
				return out, ok, err
			}
			return svc.NodeRequest(ctx, machine, method, params) // the interactions, the MCP tools (fleet_events.go)
		},
		Event:    func(machine, event string, params json.RawMessage) bool { return svc.NodeEvent(machine, event, params) },
		Detached: func(machine string) { svc.NodeDetached(machine) },
		Location: func() *time.Location { return svc.location(context.Background()) },
		// The extensions' part of a node's connection and its events (internal/extmgr).
		Own:         func(ctx context.Context, machine string) map[string]json.RawMessage { return ext.Own(ctx, machine) },
		OnConnected: func(machine string) { ext.NodeConnected(machine) },
		OnEvent:     func(machine, event string, params json.RawMessage) bool { return ext.NodeEvent(machine, event, params) },
		// A node's new connection gets the launchers added in Persona, with their keys (models_launchers_push.go).
		// Not one waiting to be confirmed: it gets them when it is (trustChanged, machine_trust.go).
		NodeCalls: func(ctx context.Context, machine string) []devicewire.NodeCall {
			return svc.managedNodeCalls(ctx, machine)
		},
		DialRelay: func(ctx context.Context, query string) (*websocket.Conn, error) {
			return bus.DialAt(ctx, hub, token, httpClient, devicewire.RelayPath, query)
		},
		Connected: func() bool { return bh.Client().Peer() != nil },
		Hub:       lazyHost{c: func() *bus.Client { return bh.Client() }}})
	// The extensions, connectors and bots (internal/extmgr): the data directory here, its own sections in manager.json
	// (as moveConfig read and checked them) and its own secret.key.
	ext = extmgr.New(extmgr.Options{Dir: home, ConfigFile: o.ConfigFile, Config: o.ExtConfig, SecretKey: o.SecretKey,
		Store: database, Build: build, ManagerID: o.ID, HubURL: hub, HubClient: httpClient, HubProofKey: extwire.ProofKey(token),
		Logf: log.New(stderr, "extensions: ", log.LstdFlags).Printf,
		Hub:  lazyHost{c: func() *bus.Client { return bh.Client() }, b: func() *BusHub { return bh }}, Devices: devs,
		Assistant: func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
			return bh.Answer(ctx, method, params)
		},
		AssistantEvent: func(event string, params json.RawMessage) { bh.TellLocal(event, params) },
		AssistantTools: func() []proto.Tool { return svc.toolsNow() },
		// The model providers' keys that are references, for their provider extensions' details (launcher_secrets.go).
		LauncherReferences: func(ctx context.Context) []extmgr.LauncherReference { return svc.launcherReferences(ctx) },
		AssistantMachine:   func() string { return svc.mainMachine(context.Background()) },
		Ask:                func(ctx context.Context, ix proto.Interaction) (proto.Resolution, error) { return svc.AskCard(ctx, ix) },
		DialRelay: func(ctx context.Context, query string) (*websocket.Conn, error) {
			return bus.DialAt(ctx, hub, token, httpClient, devicewire.RelayPath, query)
		},
		Connected: func() bool { return bh.Client().Peer() != nil },
		Connection: func() any {
			if p := bh.Client().Peer(); p != nil {
				return p
			}
			return nil
		},
		Lang:     func() i18n.Lang { return svc.lang(context.Background()) },
		Timezone: func() string { return svc.userTZ() }})
	o.Errors.ext.Store(ext)
	bh = NewBusHub(BusOptions{ID: o.ID, Hub: hub, Token: token, HTTPClient: httpClient, Version: version, Deploy: deploy,
		ConfigFile: o.ConfigFile, Outbox: database,
		Logf: logf, Devices: devs, Extensions: ext, StopWhenRevoked: o.StopWhenRevoked, StopWhenRejected: o.TokenFirst})
	// The knowledge base in its directory, kept as its manager.json's kb section says (over the hub's hello). The model
	// providers' keys are sealed with its own secret.key.
	svc = New(Options{Hub: bh, Store: func() Store { return database }, Logf: logf, Devices: devs,
		KB: &KBOptions{Dir: o.KBDir, KeyDir: o.HandlesDir}, SecretKey: o.SecretKey,
		Trust: devs, ID: o.ID, Build: version})
	svc.SetExtensions(ext) // the connectors', Cua's and ego's tools, in this process (fleet_tools.go)
	// When the north star's recording began (north_star.go).
	go svc.northStarStarted(ctx)
	if o.Ready != nil {
		o.Ready(func(ctx context.Context) (bool, string) { return managerIdle(ctx, bh, svc, ext) })
	}
	logf("connecting to %s as %s", hub, o.ID)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = devs.Run(ctx) // after ctx: hands the machines over to the next manager, then lets them go
	}()
	extDone := make(chan struct{})
	go func() {
		defer close(extDone)
		_ = ext.Run(ctx) // after ctx: stops the extensions' programs (they are the manager's children)
	}()
	if o.ID != "assistant" {
		go func() {
			if err := bh.Wait(ctx); err == nil {
				if err := managerupdate.MarkReady(home, build); err != nil {
					logf("manager update readiness: %v", err)
				}
			}
		}()
	}
	err = bh.Run(ctx, svc)
	stop() // a refused token ends Run before ctx: the computers and the extensions stop with it
	<-done
	<-extDone
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// lazyHost is the bus client, made after the devices that use it. Its apps' live updates (ws.broadcast) go through
// the outbox like the assistant's (outbox.go), when b gives one.
type lazyHost struct {
	c func() *bus.Client
	b func() *BusHub
}

func (l lazyHost) Call(ctx context.Context, method string, params, out any) error {
	return l.c().Call(ctx, method, params, out)
}

func (l lazyHost) Notify(event string, params any) error {
	if bc, ok := params.(Broadcast); ok && event == assistantwire.EventBroadcast && l.b != nil {
		if bh := l.b(); bh != nil && bh.out != nil {
			return bh.sendBroadcast(context.Background(), bc)
		}
	}
	return l.c().Notify(event, params)
}

// hubConfig is what the assistant reads of the hub's hub.json.
type hubConfig struct {
	DatabaseURL string `json:"database_url"`
	Listen      string `json:"listen"`
}

// readHubConfig reads hub.json in the data directory (~/.persona, or ~/.persona) with the environment over it, as the
// hub does (its envOverrides); a missing file is no error.
func readHubConfig() (hubConfig, error) {
	cfg := hubConfig{Listen: ":18660"}
	b, err := os.ReadFile(filepath.Join(identity.HomeDir(), "hub.json"))
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &cfg); err != nil {
			return cfg, fmt.Errorf("hub.json: %w", err)
		}
	case !os.IsNotExist(err):
		return cfg, err
	}
	if v := identity.Getenv("PERSONA_DATABASE_URL"); v != "" {
		cfg.DatabaseURL = v
	}
	if v := identity.Getenv("PERSONA_LISTEN"); v != "" {
		cfg.Listen = v
	}
	return cfg, nil
}

// externalHub is the hub an assistant the hub didn't bind connects to, and its TLS pin. PERSONA_ASSISTANT_HUB is the
// hub (the one on this machine, from listen, without it). PERSONA_ASSISTANT_FINGERPRINT pins an https hub's public key,
// the fp `personad hub pair` prints (docs/operations/network.md): what an assistant elsewhere (assistant.external)
// needs to reach a hub with its own self-signed certificate, as a manager's manager.json has it (#1255). Without it
// the system's CAs are trusted. The hub's own child dials its local address and takes no pin.
func externalHub(deploy, listen string) (hub, fingerprint string, err error) {
	hub = strings.TrimSpace(identity.Getenv("PERSONA_ASSISTANT_HUB"))
	if hub == "" {
		hub = managerconf.LocalHubURL(listen)
	}
	if deploy == "child" {
		return hub, "", nil
	}
	fingerprint = strings.TrimSpace(identity.Getenv("PERSONA_ASSISTANT_FINGERPRINT"))
	if fingerprint == "" {
		return hub, "", nil
	}
	if sum, err := base64.RawURLEncoding.DecodeString(fingerprint); err != nil || len(sum) != sha256.Size {
		return "", "", errors.New("PERSONA_ASSISTANT_FINGERPRINT is not a hub fingerprint: 43 characters of base64url, " +
			"the fp personad hub pair prints")
	}
	if u, err := url.Parse(hub); err != nil || u.Scheme != "https" {
		return "", "", fmt.Errorf("PERSONA_ASSISTANT_FINGERPRINT pins an https hub, and PERSONA_ASSISTANT_HUB is %q", hub)
	}
	return hub, fingerprint, nil
}

// errorTee is the manager process's log writer: lines go to stderr, and its error lines become the manager's error
// reports while an extension takes them (ext is set once the manager exists).
type errorTee struct {
	*errreport.Tee
	ext *atomic.Pointer[extmgr.Manager]
}

func newErrorTee(build update.Build) *errorTee {
	ref := &atomic.Pointer[extmgr.Manager]{}
	return &errorTee{ext: ref, Tee: &errreport.Tee{W: os.Stderr, Build: managerErrorBuild(build),
		On:   func() bool { return ref.Load().ErrorReportsAsked() },
		Sink: func(r wire.ErrorReport) { ref.Load().ReportOwn(r) }}}
}

// managerErrorBuild is what the manager says of itself in its error reports.
func managerErrorBuild(b update.Build) errreport.Build {
	out := errreport.Build{Source: wire.ErrorSourceManager, Release: b.Version, Commit: b.Commit}
	if b.Number > 0 {
		out.Build = strconv.FormatInt(b.Number, 10)
	}
	return out
}
