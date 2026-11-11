//go:build unix

// Command fleetsoak measures what a fleet's churn costs the hub and its manager: it runs a throwaway hub (its own
// home directory, port and Postgres container) from the tree under test, with computers whose nodes it plays itself
// and app connections that read what the hub pushes, and reports the manager's and the hub's allocation and CPU time
// per minute (docs/operations/testing.md "Fleet soak").
//
//	cd server
//	go run ./internal/fleetsoak -label head -minutes 20
//	go run ./internal/fleetsoak -label base -minutes 20 -tree <a worktree of the base commit>/server
//	go run ./internal/fleetsoak -compare <base out>/report.json <head out>/report.json
//
// CPU time follows the machine's state: to compare it, run the two soaks at the same time (each in its own terminal,
// they share nothing) and -compare prints the second's CPU time over the first's in the same minute.
//
// It never reads or writes the machine's own installation: every process it starts gets an environment made here.
package main

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

//go:embed sampler.go
var samplerSource []byte

type options struct {
	tree, label, out             string
	machines, sessions, clients  int
	interval, warmup             time.Duration
	minutes, memRate             int
	keep                         bool
	compareBefore, compareAfter  string
	work, stats, hub, token, pgc string
}

func main() {
	if filepath.Base(os.Args[0]) == "ssh" { // the manager reaching one of the soak's computers (node.go)
		os.Exit(fakeSSH(os.Args[1:]))
	}
	var o options
	compare := flag.Bool("compare", false, "compare two reports: fleetsoak -compare <before>/report.json <after>/report.json")
	flag.StringVar(&o.tree, "tree", ".", "the server module to build personad from (a worktree's server directory)")
	flag.StringVar(&o.label, "label", "soak", "what the report calls this run")
	flag.StringVar(&o.out, "out", "", "where the report, samples, profiles and the hub's log go (default: a new directory under the system's temporary one)")
	flag.IntVar(&o.machines, "machines", 10, "computers")
	flag.IntVar(&o.sessions, "sessions", 50, "sessions each computer lists")
	flag.DurationVar(&o.interval, "interval", 2*time.Second, "how often each computer's list changes")
	flag.IntVar(&o.clients, "clients", 20, "app connections, each subscribed to three sessions")
	flag.DurationVar(&o.warmup, "warmup", time.Minute, "how long the fleet runs before the measured window")
	flag.IntVar(&o.minutes, "minutes", 20, "the measured window")
	flag.IntVar(&o.memRate, "memprofilerate", 0, "runtime.MemProfileRate of the measured processes (0: the default, 512 KiB)")
	flag.BoolVar(&o.keep, "keep", false, "keep the throwaway home directory")
	flag.Parse()
	if *compare {
		if flag.NArg() != 2 {
			fmt.Fprintln(os.Stderr, "usage: fleetsoak -compare <before>/report.json <after>/report.json")
			os.Exit(2)
		}
		if err := printComparison(os.Stdout, flag.Arg(0), flag.Arg(1)); err != nil {
			fmt.Fprintln(os.Stderr, "FAIL:", err)
			os.Exit(1)
		}
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	ctx, cancel := context.WithTimeout(ctx, o.warmup+time.Duration(o.minutes)*time.Minute+15*time.Minute)
	rep, err := run(ctx, &o)
	cancel()
	stop()
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		fmt.Printf("TIMEOUT: %v (output in %s)\n", err, o.out)
		os.Exit(1)
	case err != nil:
		fmt.Printf("FAIL: %v (output in %s)\n", err, o.out)
		os.Exit(1)
	}
	m := rep.Processes["manager"]
	fmt.Printf("PASS: %s: manager %.1f MB/min allocated, %.0f ms/min CPU over %d minutes (report in %s)\n",
		rep.Label, m.AllocMB.Median, m.CPUMs.Median, rep.Minutes, filepath.Join(o.out, "report.md"))
}

// run is one soak: build, start, warm up, measure, stop, report. Everything it started is gone when it returns.
func run(ctx context.Context, o *options) (*report, error) {
	tree, err := filepath.Abs(o.tree)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(tree, "cmd", "personad")); err != nil {
		return nil, fmt.Errorf("-tree %s is not a server module: %w", tree, err)
	}
	if o.out == "" {
		o.out = filepath.Join(os.TempDir(), fmt.Sprintf("fleetsoak-%s-%s", o.label, time.Now().Format("20060102-150405")))
	}
	if o.out, err = filepath.Abs(o.out); err != nil {
		return nil, err
	}
	o.stats = filepath.Join(o.out, "stats")
	if err := os.MkdirAll(o.stats, 0o700); err != nil {
		return nil, err
	}
	// A short path: the home directory holds unix sockets.
	if o.work, err = os.MkdirTemp("/tmp", "fleetsoak-"); err != nil {
		return nil, err
	}
	if !o.keep {
		defer os.RemoveAll(o.work)
	}
	rep := &report{Label: o.label, Tree: tree, GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(),
		Machines: o.machines, Sessions: o.sessions, Clients: o.clients, Interval: o.interval.String(),
		WarmupSeconds: int(o.warmup.Seconds()), Minutes: o.minutes, MemProfileRate: o.memRate}
	rep.Commit = strings.TrimSpace(output(ctx, tree, "git", "rev-parse", "--short", "HEAD"))
	rep.Dirty = strings.TrimSpace(output(ctx, tree, "git", "status", "--porcelain", "--", ".")) != ""

	say("building personad from %s (%s)", tree, rep.Commit)
	bin := filepath.Join(o.work, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		return nil, err
	}
	if err := buildMeasured(ctx, tree, o.work, filepath.Join(bin, "personad")); err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if err := os.Symlink(self, filepath.Join(bin, "ssh")); err != nil {
		return nil, err
	}

	say("starting Postgres")
	o.pgc = fmt.Sprintf("persona-ci-db-fleetsoak-%d", os.Getpid())
	defer func() { _ = exec.Command("docker", "rm", "-f", o.pgc).Run() }()
	database, err := startPostgres(ctx, o.pgc)
	if err != nil {
		return nil, err
	}

	port, err := freePort()
	if err != nil {
		return nil, err
	}
	o.hub, o.token = fmt.Sprintf("http://127.0.0.1:%d", port), randomToken()
	home := filepath.Join(o.work, "home")
	if err := writeHubConfig(filepath.Join(home, ".persona"), port, o.token, database, o.machines); err != nil {
		return nil, err
	}
	logFile, err := os.Create(filepath.Join(o.out, "hub.log"))
	if err != nil {
		return nil, err
	}
	defer logFile.Close()
	hub := exec.Command(filepath.Join(bin, "personad"), "hub")
	hub.Dir, hub.Stdout, hub.Stderr = o.work, logFile, logFile
	hub.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Made here, not inherited: nothing of the machine's own installation reaches the hub, its manager or the nodes.
	hub.Env = []string{"HOME=" + home, "PERSONA_HOME=" + filepath.Join(home, ".persona"), "TMPDIR=" + filepath.Join(o.work, "tmp"),
		"PATH=" + bin + ":/usr/bin:/bin:/usr/sbin:/sbin", "LANG=en_US.UTF-8", "USER=" + os.Getenv("USER"),
		"PERSONA_HUB_EDGE=0", "PERSONA_SPLIT_PROGRAMS=0", "PERSONA_LOCAL_NODE=0",
		soakStatsEnv + "=" + o.stats, fmt.Sprintf("%s=%d", soakSessionsEnv, o.sessions), soakIntervalEnv + "=" + o.interval.String()}
	if o.memRate > 0 {
		hub.Env = append(hub.Env, fmt.Sprintf("%s=%d", soakMemRateEnv, o.memRate))
	}
	if err := os.MkdirAll(filepath.Join(o.work, "tmp"), 0o700); err != nil {
		return nil, err
	}
	say("starting the hub on %s", o.hub)
	if err := hub.Start(); err != nil {
		return nil, err
	}
	exited := make(chan error, 1)
	go func() { exited <- hub.Wait() }()
	defer func() {
		_ = syscall.Kill(-hub.Process.Pid, syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(15 * time.Second):
			_ = syscall.Kill(-hub.Process.Pid, syscall.SIGKILL)
			<-exited
		}
		_ = syscall.Kill(-hub.Process.Pid, syscall.SIGKILL) // whatever of its group is left
	}()
	alive := func() error {
		select {
		case err := <-exited:
			exited <- err
			return fmt.Errorf("the hub exited (%v): see hub.log", err)
		default:
			return ctx.Err()
		}
	}

	if err := waitFor(ctx, alive, 90*time.Second, "the hub's health", func() bool {
		return o.get(ctx, "/api/v1/health", nil) == nil
	}); err != nil {
		return nil, err
	}
	if err := waitFor(ctx, alive, 3*time.Minute, "every computer online", func() bool {
		var res struct {
			Machines []struct {
				Online bool `json:"online"`
			} `json:"machines"`
		}
		if o.get(ctx, "/api/v1/machines", &res) != nil || len(res.Machines) != o.machines {
			return false
		}
		for _, m := range res.Machines {
			if !m.Online {
				return false
			}
		}
		return true
	}); err != nil {
		return nil, err
	}
	rep.Catalog = o.loadMarket(ctx)
	say("market catalog: %d entries, %d with a channel; the channel list has %d from the market",
		rep.Catalog.Entries, rep.Catalog.WithChannels, rep.Catalog.MarketChannels)

	var apps appClients
	var subscribed [][2]string
	for m := 1; m <= o.machines; m++ {
		subscribed = append(subscribed, [2]string{machineID(m), sessionID(m, m%o.sessions)})
	}
	apps.run(ctx, o.hub, o.token, o.clients, subscribed)

	say("warming up for %s", o.warmup)
	if err := sleep(ctx, alive, o.warmup); err != nil {
		return nil, err
	}
	rep.LoadBefore = loadAverage(ctx)
	if err := os.WriteFile(filepath.Join(o.stats, soakStartFile), nil, 0o600); err != nil {
		return nil, err
	}
	start := time.Now()
	rep.WindowStart = start.UTC().Truncate(time.Second)
	lists, messages := apps.lists.Load(), apps.messages.Load()
	say("measuring for %d minutes (load %s)", o.minutes, rep.LoadBefore)
	for minute := 1; minute <= o.minutes; minute++ {
		// A sample past the minute's end is there six seconds later: they come every five.
		if err := sleep(ctx, alive, time.Until(start.Add(time.Duration(minute)*time.Minute+6*time.Second))); err != nil {
			return nil, err
		}
		if p, err := readProcess(o.stats, "assistant", start, minute); err == nil && len(p.PerMinute) == minute {
			last := p.PerMinute[minute-1]
			say("minute %d: manager %.1f MB allocated, %.0f ms CPU, %.1f MB live", minute, last.AllocMB, last.CPUMs, last.LiveMB)
		}
	}
	rep.ClientLists, rep.ClientMessages = apps.lists.Load()-lists, apps.messages.Load()-messages
	rep.ClientsConnected = int(apps.connected.Load())
	rep.LoadAfter = loadAverage(ctx)
	if err := os.WriteFile(filepath.Join(o.stats, soakStopFile), nil, 0o600); err != nil {
		return nil, err
	}
	if err := sleep(ctx, alive, 3*time.Second); err != nil { // the samplers look once a second, then write their profiles
		return nil, err
	}

	rep.Processes = map[string]*processReport{}
	for name, role := range map[string]string{"manager": "assistant", "hub": "hub"} {
		p, err := readProcess(o.stats, role, start, o.minutes)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		rep.Processes[name] = p
		writeTops(ctx, o.out, name, p.file)
	}
	if rep.ClientLists == 0 {
		return nil, errors.New("the app connections read no session list: the fleet's changes did not reach the hub's clients")
	}
	if err := writeReport(o.out, rep); err != nil {
		return nil, err
	}
	printReport(os.Stdout, rep)
	return rep, nil
}

func say(format string, args ...any) {
	fmt.Printf("%s fleetsoak: %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

// buildMeasured builds the tree's personad with the sampler laid over its package.
func buildMeasured(ctx context.Context, tree, work, to string) error {
	sampler := filepath.Join(work, "sampler.go")
	if err := os.WriteFile(sampler, samplerSource, 0o600); err != nil {
		return err
	}
	overlay, _ := json.Marshal(map[string]any{"Replace": map[string]string{
		filepath.Join(tree, "cmd", "personad", "zz_fleetsoak_sampler.go"): sampler}})
	if err := os.WriteFile(filepath.Join(work, "overlay.json"), overlay, 0o600); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "go", "build", "-overlay", filepath.Join(work, "overlay.json"), "-o", to, "./cmd/personad")
	cmd.Dir, cmd.Env = tree, append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go build: %v\n%s", err, out)
	}
	return nil
}

// startPostgres starts the soak's own database container and returns its URL once it answers.
func startPostgres(ctx context.Context, name string) (string, error) {
	if out, err := exec.CommandContext(ctx, "docker", "run", "--detach", "--name", name, "-e", "POSTGRES_USER=persona",
		"-e", "POSTGRES_PASSWORD=ci-only", "-e", "POSTGRES_DB=persona_soak", "-p", "127.0.0.1::5432", "postgres:18-alpine").CombinedOutput(); err != nil {
		return "", fmt.Errorf("docker run: %v\n%s", err, out)
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		if exec.CommandContext(ctx, "docker", "exec", name, "pg_isready", "-h", "127.0.0.1", "-U", "persona", "-d", "persona_soak").Run() == nil {
			break
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			return "", errors.New("Postgres did not come up in 90 s")
		}
		time.Sleep(time.Second) // sleep-ok: polling a container
	}
	out, err := exec.CommandContext(ctx, "docker", "port", name, "5432/tcp").Output()
	if err != nil {
		return "", fmt.Errorf("docker port: %w", err)
	}
	addr := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	return "postgres://persona:ci-only@" + addr + "/persona_soak?sslmode=disable", nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func randomToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func machineID(n int) string { return fmt.Sprintf("soak%02d", n) }

// sessionID is session i of computer n, as its node lists it (newFakeNode).
func sessionID(n, i int) string { return fmt.Sprintf("0e%06d-0000-4000-8000-%012d", n, i) }

// writeHubConfig writes the throwaway hub's hub.json: loopback only, no LAN discovery, the soak's computers over ssh.
func writeHubConfig(dir string, port int, token, database string, machines int) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	no := false
	type machine struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		Kind           string `json:"kind"`
		SSHHost        string `json:"ssh_host"`
		Botmux         *bool  `json:"botmux"`
		HerdrAutostart *bool  `json:"herdr_autostart"`
		UsageWindows   *bool  `json:"usage_windows"`
	}
	list := make([]machine, machines)
	for i := range list {
		id := machineID(i + 1)
		list[i] = machine{ID: id, Name: fmt.Sprintf("Soak %02d", i+1), Kind: "ssh", SSHHost: id, Botmux: &no, HerdrAutostart: &no, UsageWindows: &no}
	}
	b, err := json.MarshalIndent(map[string]any{
		"listen": fmt.Sprintf("127.0.0.1:%d", port), "token": token, "public_url": fmt.Sprintf("http://127.0.0.1:%d", port),
		"database_url": database, "machines": list, "mesh": map[string]any{"discovery": false},
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "hub.json"), b, 0o600)
}

// get is a GET of the hub's API as the hub's owner on this machine; out nil only checks the status.
func (o *options) get(ctx context.Context, path string, out any) error {
	return o.do(ctx, http.MethodGet, path, out, 20*time.Second)
}

func (o *options) do(ctx context.Context, method, path string, out any, within time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, within)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, o.hub+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+o.token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s: %s: %.200s", method, path, res.Status, body)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

// loadMarket has the manager fetch the official market's index, as a hub in use has, and counts what the catalog
// and the channel list then hold. Without the network the catalog stays empty: the report says so.
func (o *options) loadMarket(ctx context.Context) catalogReport {
	var c catalogReport
	var markets struct {
		Markets []struct {
			ID       string `json:"id"`
			Official bool   `json:"official"`
		} `json:"markets"`
	}
	if err := o.get(ctx, "/api/v1/extensions/-/markets", &markets); err != nil {
		say("markets: %v", err)
		return c
	}
	for _, m := range markets.Markets {
		if !m.Official {
			continue
		}
		if err := o.do(ctx, http.MethodPost, "/api/v1/extensions/-/markets/"+m.ID+"/refresh", nil, 90*time.Second); err != nil {
			say("refreshing the official market: %v", err)
		}
	}
	var catalog struct {
		Extensions []struct {
			Contributes map[string][]string `json:"contributes"`
		} `json:"extensions"`
	}
	if err := o.get(ctx, "/api/v1/extensions/-/catalog", &catalog); err != nil {
		say("catalog: %v", err)
		return c
	}
	c.Entries = len(catalog.Extensions)
	for _, e := range catalog.Extensions {
		if len(e.Contributes["channel"]) > 0 {
			c.WithChannels++
		}
	}
	var channels struct {
		Channels []struct {
			Source string `json:"source"`
		} `json:"channels"`
	}
	if err := o.get(ctx, "/api/v1/channels", &channels); err == nil {
		for _, ch := range channels.Channels {
			if ch.Source == "market" {
				c.MarketChannels++
			}
		}
	}
	return c
}

// waitFor polls ok until it holds, the hub is gone or the time is up.
func waitFor(ctx context.Context, alive func() error, within time.Duration, what string, ok func() bool) error {
	deadline := time.Now().Add(within)
	for !ok() {
		if err := alive(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no %s after %s", what, within)
		}
		time.Sleep(500 * time.Millisecond) // sleep-ok: polling another process
	}
	return nil
}

// sleep waits d, ending early when the hub is gone or ctx ends.
func sleep(ctx context.Context, alive func() error, d time.Duration) error {
	t := time.NewTimer(max(d, 0))
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
	return alive()
}

func output(ctx context.Context, dir, name string, args ...string) string {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, _ := cmd.Output()
	return string(out)
}

// loadAverage is the machine's load as uptime prints it: other work on the machine moves the CPU numbers.
func loadAverage(ctx context.Context) string {
	out := strings.TrimSpace(output(ctx, "", "uptime"))
	if i := strings.LastIndex(out, "load average"); i >= 0 {
		return strings.TrimSpace(strings.TrimLeft(out[i:], "loadavergs: "))
	}
	return out
}
