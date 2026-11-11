// Command vncbench measures a VNC server the way the app's live screen uses it (apple/Persona/Screen,
// docs/extensions/computer-use.md "Live view"): which encodings and pseudo-encodings it really supports, and, scene by
// scene, the bytes on the wire, the frame rate, the time from an input to the pixels it changes, and the picture's
// quality against a lossless copy of the same moment.
//
//	go run ./cmd/vncbench probe                       # macOS Screen Sharing on this Mac
//	go run ./cmd/vncbench run -scenes all -config app # the app's encodings, every scene
//
// It signs in with VNCBENCH_ACCOUNT / VNCBENCH_PASSWORD, or the login keychain's "persona-vnc-bench" password
// (never printed). -hub and -machine go through a hub's port relay as the app does, with a device token in
// VNCBENCH_TOKEN. -delay and -rate add a phone link's latency and bandwidth.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	cmd, args := os.Args[1], os.Args[2:]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:5900", "the VNC server, when not through a hub")
	hub := fs.String("hub", os.Getenv("VNCBENCH_HUB"), "a hub's URL: connect through its port relay")
	machine := fs.String("machine", os.Getenv("VNCBENCH_MACHINE"), "the machine id on that hub")
	delay := fs.Duration("delay", 0, "one-way delay added each way")
	rate := fs.Float64("rate", 0, "downstream bandwidth in Mbit/s (0: unlimited)")
	var run runFlags
	if cmd == "run" {
		run.register(fs)
	}
	recConfig := fs.String("rec-config", "app", "record: the encodings (as run's -config)")
	recMode := fs.String("rec-mode", "stopwait", "record: how updates are asked for (as run's -mode)")
	recOut := fs.String("out", "vnc.vncrec", "record: the file")
	recFor := fs.Duration("for", 10*time.Second, "record: how long")
	shotScale := fs.Int("scale", 2, "shot: scaled down by this")
	privLo := fs.Int("lo", 1000, "private: the first encoding")
	privHi := fs.Int("hi", 1199, "private: the last encoding")
	privStep := fs.Int("step", 20, "private: encodings asked at once")
	dumpEnc := fs.Int("enc", 1000, "dump: the encoding")
	traceScene := fs.String("scene", "video", "trace: the bench page's scene")
	traceArea := fs.String("trace-area", "", "trace: after the first update, ask for this part only: x,y,w,h, page (the bench page) or stage (where the scene draws)")
	dumpN := fs.Int("n", 256, "dump: bytes shown")
	dumpOut := fs.String("raw", "", "dump: also write every byte that came to this file")
	relayPort := fs.Int("port", 38777, "relay: the echo server's port on this machine")
	echoOnly := fs.Bool("echo-only", false, "relay: only serve the echo server (the client runs elsewhere)")
	noEcho := fs.Bool("no-echo", false, "relay: the echo server runs on the machine measured, not here")
	_ = fs.Parse(args)
	t := target{Addr: *addr, Hub: *hub, Machine: *machine, Token: os.Getenv("VNCBENCH_TOKEN"), Delay: *delay, Rate: *rate * 1e6 / 8}
	if t.Hub != "" && (t.Machine == "" || t.Token == "") {
		fail(fmt.Errorf("-hub needs -machine and VNCBENCH_TOKEN"))
	}
	ctx := context.Background()
	if cmd == "relay" {
		if *echoOnly {
			var cancel context.CancelFunc
			ctx, cancel = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer cancel()
		}
		if err := relayBench(ctx, t, *relayPort, *echoOnly, *noEcho); err != nil {
			fail(err)
		}
		return
	}
	cred, err := loadCredentials()
	if err != nil {
		fail(err)
	}
	switch cmd {
	case "probe":
		err = probe(ctx, t, cred)
	case "run":
		err = runScenes(ctx, t, cred, run)
	case "shot":
		err = shot(ctx, t, cred, *recOut, *shotScale)
	case "dump":
		err = dump(ctx, t, cred, int32(*dumpEnc), *dumpN, 3*time.Second, *dumpOut)
	case "trace":
		err = trace(ctx, t, cred, *traceScene, *recMode, *recFor, *traceArea)
	case "private":
		probePrivate(ctx, t, cred, int32(*privLo), int32(*privHi), int32(*privStep))
	case "record":
		err = recordOnly(ctx, t, cred, *recConfig, *recMode, *recOut, *recFor)
	default:
		usage()
	}
	if err != nil {
		fail(err)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: vncbench probe|private|dump|shot|trace|run|record|relay [flags]  (vncbench run -h for its flags)")
	os.Exit(2)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "vncbench:", err)
	os.Exit(1)
}
