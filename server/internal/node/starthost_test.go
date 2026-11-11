package node

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/proto"
)

// launchHost is a setHost that can start sessions; its Spaces fails, so a start stops there (nothing typed) and the
// test sees which host was asked.
type launchHost struct {
	*setHost
	mu     sync.Mutex
	spaces int
}

func (h *launchHost) Spaces(context.Context) ([]host.Space, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.spaces++
	return nil, errors.New("not running")
}
func (h *launchHost) CreateSpace(context.Context, string, string) (*host.Created, error) {
	return nil, errors.New("not running")
}
func (h *launchHost) CreateSlot(context.Context, string, string, string) (*host.Created, error) {
	return nil, errors.New("not running")
}
func (h *launchHost) Run(context.Context, host.PaneRef, string) error       { return nil }
func (h *launchHost) NameAgent(context.Context, host.PaneRef, string) error { return nil }
func (h *launchHost) asked() int                                            { h.mu.Lock(); defer h.mu.Unlock(); return h.spaces }

// startNode drives {herdr, herdr-core, orca} (default herdr); herdr and Orca can start sessions, the herdr-core here (a
// stand-in without Launcher) cannot.
func startNode(t *testing.T) (*Node, *launchHost, *launchHost) {
	t.Helper()
	n, _, _, _ := setNode(t)
	hh := &launchHost{setHost: newSetHost(host.Herdr)}
	oh := &launchHost{setHost: newSetHost(host.Orca)}
	n.hosts = host.NewRegistry(hh, newSetHost(host.HerdrCore), oh)
	c := proto.EngineChoice{Hosts: []string{"herdr", "herdr-core", "orca"}, Default: "herdr"}
	n.eng.name, n.eng.set = proto.LegacyEngine(c), &c
	return n, hh, oh
}

func TestCheckStartHost(t *testing.T) {
	n, _, _ := startNode(t)
	for _, c := range []struct {
		kind, code string
		ok         bool
	}{
		{"", "", true},
		{"herdr", "", true},
		{"orca", "", true},
		{"herdr-core", engineMissing, false}, // enabled, cannot start sessions (the stand-in)
		{"paseo", engineMissing, false},      // not enabled
		{"tmux", "", false},                  // not a host
	} {
		err := n.checkStartHost(c.kind)
		if c.ok != (err == nil) {
			t.Errorf("%q: %v", c.kind, err)
			continue
		}
		if c.code != "" {
			if code, _ := refusalOf(t, err); code != c.code {
				t.Errorf("%q: code %q", c.kind, code)
			}
		}
	}
}

// A start that names its host opens there; one that names none, in the default host. The refusal names the host
// that was asked.
func TestStartOpensInTheNamedHost(t *testing.T) {
	n, hh, oh := startNode(t)
	_, err := n.start(withStartHost(context.Background(), "orca"), t.TempDir(), "", "hi", "", "", "", "", "", "", "")
	if oh.asked() != 1 || hh.asked() != 0 {
		t.Fatalf("orca asked %d, herdr asked %d (%v)", oh.asked(), hh.asked(), err)
	}
	if err == nil || !strings.Contains(err.Error(), "Orca") {
		t.Errorf("refusal does not name Orca: %v", err)
	}
	_, err = n.start(context.Background(), t.TempDir(), "", "hi", "", "", "", "", "", "", "")
	if hh.asked() != 1 || oh.asked() != 1 {
		t.Errorf("default start: herdr asked %d, orca asked %d (%v)", hh.asked(), oh.asked(), err)
	}
	// A TRAEX resume named into Orca is refused before any host is asked (Orca continues only Claude, #5548); a
	// Claude resume opens there.
	const id = "0d000000-0000-4000-8000-0000000000ee"
	if _, err := n.start(withStartHost(context.Background(), "orca"), t.TempDir(), traexAgent, "", id, "", "", "", "", "", ""); !isRefusal(err, "orca_resume") {
		t.Errorf("TRAEX resume into Orca: %v", err)
	}
	if oh.asked() != 1 {
		t.Errorf("orca asked for a refused resume")
	}
	_, err = n.start(withStartHost(context.Background(), "orca"), t.TempDir(), "", "", id, "", "", "", "", "", "")
	if oh.asked() != 2 || hh.asked() != 1 {
		t.Errorf("Claude resume into Orca: orca asked %d, herdr asked %d (%v)", oh.asked(), hh.asked(), err)
	}
}

// session.start with a host the machine does not enable is refused at once, nothing started.
func TestSessionStartRefusesAHostNotEnabled(t *testing.T) {
	n, hh, oh := startNode(t)
	for _, body := range []string{
		`{"cwd":"/tmp","prompt":"hi","host":"paseo"}`,
		`{"cwd":"/tmp","prompt":"hi","host":"herdr-core","async":true,"session_id":"0d000000-0000-4000-8000-0000000000ef"}`,
	} {
		_, err := n.handle(context.Background(), "session.start", json.RawMessage(body))
		if code, _ := refusalOf(t, err); code != engineMissing {
			t.Errorf("%s: %v", body, err)
		}
	}
	if hh.asked()+oh.asked() != 0 {
		t.Errorf("a host was asked: herdr %d orca %d", hh.asked(), oh.asked())
	}
}

// A start on a default host that cannot start sessions gets the same refusal as one naming it, not "unsupported:
// herdr-core cannot do this".
func TestStartOnADefaultHostThatCannotStartIsRefused(t *testing.T) {
	n, hh, oh := startNode(t)
	c := proto.EngineChoice{Hosts: []string{"herdr", "herdr-core", "orca"}, Default: "herdr-core"}
	n.eng.name, n.eng.set = proto.LegacyEngine(c), &c
	_, err := n.start(context.Background(), t.TempDir(), "", "hi", "", "", "", "", "", "", "")
	if code, _ := refusalOf(t, err); code != engineMissing {
		t.Fatalf("code %q: %v", code, err)
	}
	if errors.Is(err, host.ErrUnsupported) || !strings.Contains(err.Error(), "Herdr App 不支持从手机新开会话") {
		t.Errorf("refusal: %v", err)
	}
	if hh.asked()+oh.asked() != 0 {
		t.Errorf("a host was asked: herdr %d orca %d", hh.asked(), oh.asked())
	}
}

// Rows say which host they live in.
func TestSessionRowsCarryTheirHost(t *testing.T) {
	n, _, _, _ := setNode(t)
	for pane, want := range map[string]string{herdrPane: "herdr", orcaPane: "orca", "": ""} {
		s := proto.Session{SessionID: sidOnly, PaneID: pane}
		n.decorate(&s)
		if s.Host != want {
			t.Errorf("pane %q: host %q, want %q", pane, s.Host, want)
		}
	}
}

func TestResumeHostSelection(t *testing.T) {
	n, _, _ := startNode(t)
	choice := proto.EngineChoice{Hosts: []string{proto.EngineHerdr, proto.EngineOrca, proto.EnginePaseo}, Default: proto.EnginePaseo}
	n.eng.name, n.eng.set = proto.LegacyEngine(choice), &choice
	n.state.put(sidA, sessionState{Pane: "w1:p1"})
	for _, tc := range []struct {
		name, id, explicit, want string
	}{
		{"remembered terminal", sidA, "", proto.EngineHerdr},
		{"explicit host", sidA, proto.EngineOrca, proto.EngineOrca},
		{"unknown session", sidB, "", proto.EnginePaseo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := n.withResumeHost(withStartHost(context.Background(), tc.explicit), tc.id)
			if got := n.startKind(ctx); got != tc.want {
				t.Fatalf("host = %q, want %q", got, tc.want)
			}
		})
	}
	n.runtimes = newRuntimeRegistry()
	n.runtimes.known[sidA] = knownSession{runtime: proto.EnginePaseo}
	if got := n.startKind(n.withResumeHost(context.Background(), sidA)); got != proto.EnginePaseo {
		t.Fatalf("runtime ownership = %q", got)
	}
	delete(n.runtimes.known, sidA)
	n.state.put(sidA, sessionState{Pane: "orca:t1:l1"})
	if got := n.startKind(n.withResumeHost(context.Background(), sidA)); got != proto.EngineOrca {
		t.Fatalf("recorded Orca host = %q", got)
	}
	n.eng.set = &proto.EngineChoice{Hosts: []string{proto.EnginePaseo}, Default: proto.EnginePaseo}
	if got := n.startKind(n.withResumeHost(context.Background(), sidA)); got != proto.EnginePaseo {
		t.Fatalf("disabled previous host = %q", got)
	}
}

func TestSessionStartResumesInRecordedHost(t *testing.T) {
	for _, def := range []string{proto.EnginePaseo, proto.EngineHerdr} {
		t.Run(def, func(t *testing.T) {
			n, hh, oh := startNode(t)
			choice := proto.EngineChoice{Hosts: []string{proto.EngineHerdr, proto.EngineOrca, proto.EnginePaseo}, Default: def}
			n.eng.name, n.eng.set = proto.LegacyEngine(choice), &choice
			n.state.put(sidA, sessionState{Pane: "w1:p1", Cwd: t.TempDir()})
			_, err := request(t, n, "session.start", map[string]any{"resume_session_id": sidA})
			if hh.asked() != 1 || oh.asked() != 0 {
				t.Fatalf("resume asked herdr %d, Orca %d: %v", hh.asked(), oh.asked(), err)
			}
			_, err = request(t, n, "session.start", map[string]any{"resume_session_id": sidA, "host": proto.EngineOrca})
			if hh.asked() != 1 || oh.asked() != 1 {
				t.Fatalf("explicit host asked herdr %d, Orca %d: %v", hh.asked(), oh.asked(), err)
			}
		})
	}
}
