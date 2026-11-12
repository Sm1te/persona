package node

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// sentRows is rows sessions as a busy machine sends them in sessions.changed, about 700 bytes each.
func sentRows(rows int) []proto.Session {
	list := make([]proto.Session, rows)
	reply := strings.Repeat("The build passes and every test is green; the branch is ready for review. ", 3)[:200]
	for i := range list {
		list[i] = proto.Session{SessionID: listID(i), Agent: "claude", Launcher: "claude", Host: "herdr", Status: proto.StatusIdle,
			PaneID: fmt.Sprintf("w%d:p%d", i/8, i), Title: fmt.Sprintf("Fix the flaky upload test %d", i), Cwd: fmt.Sprintf("/work/repo%d", i%6),
			RepoRoot: fmt.Sprintf("/work/repo%d", i%6), Worktree: fmt.Sprintf("/work/repo%d", i%6), Branch: "main", Model: "claude-sonnet-4-5",
			LastText: reply, AsksRead: true, ContextTokens: int32(41000 + i), ContextWindow: 200000, UpdatedAt: listT0.Add(time.Duration(i) * time.Second),
			Rev: int64(20000 + 37*i)}
	}
	return list
}

// BenchmarkSendSessions is sendSessions of a list the manager holds already (the bytes are built to be compared, and
// nothing is written), as notifySessions does for each hook event of a machine with 80 or 300 sessions.
func BenchmarkSendSessions(b *testing.B) {
	for _, rows := range []int{80, 300} {
		b.Run(fmt.Sprint(rows), func(b *testing.B) {
			n, _ := testNode(b)
			n.peer = rpc.New("n", io.Discard)
			list := sentRows(rows)
			n.sendSessions(list)
			b.ReportAllocs()
			for b.Loop() {
				n.sendSessions(list)
			}
		})
	}
}

// BenchmarkSendSessionsChanged is sendSessions of a list one session of which changed since the last one: the list
// goes out (here to io.Discard), as on each change of a machine with 80 or 300 sessions.
func BenchmarkSendSessionsChanged(b *testing.B) {
	for _, rows := range []int{80, 300} {
		b.Run(fmt.Sprint(rows), func(b *testing.B) {
			n, _ := testNode(b)
			n.peer = rpc.New("n", io.Discard)
			list := sentRows(rows)
			n.sendSessions(list)
			b.ReportAllocs()
			for b.Loop() {
				list[0].Rev++
				n.sendSessions(list)
			}
		})
	}
}

// A list the manager holds already is encoded into the buffer kept for it and dropped, allocating nothing (#2826): it
// was 9 allocations and 67 KB for 80 rows.
func TestSessionsChangedSkipAllocatesNothing(t *testing.T) {
	n, _ := testNode(t)
	n.peer = rpc.New("n", io.Discard)
	list := sentRows(80)
	n.sendSessions(list)
	if a := testing.AllocsPerRun(20, func() { n.sendSessions(list) }); a != 0 {
		t.Errorf("a list held already allocates %.0f times, want none", a)
	}
}

// sessions.changed carries the bytes json.Marshal gives for {"sessions": list}: null for no list, [] for an empty one,
// HTML characters, U+2028 and U+2029 escaped, invalid UTF-8 replaced. A list json.Marshal cannot encode sends nothing,
// and the next list goes out whatever the last one was.
func TestSessionsChangedBytesAreJSONMarshal(t *testing.T) {
	n, _ := testNode(t)
	w := &wireRec{}
	n.peer = rpc.New("n", w)
	escapes := sentRows(3)
	escapes[0].Title = `<b>fix</b> & "ship" \ it`
	escapes[1].LastText = "line next end \x00\x1f\u007f"
	escapes[2].Title = "bad \xff\xfe utf-8 \xe2\x82"
	farOff := sentRows(2)
	farOff[1].UpdatedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) // json.Marshal refuses it
	for i, c := range []struct {
		list []proto.Session
		sent bool
	}{
		{nil, true},
		{[]proto.Session{}, true},
		{escapes, true},
		{sentRows(40), true},
		{farOff, false},
		{sentRows(40), true}, // the same bytes as two lists ago, but the manager kept no list since the refused one
	} {
		before := len(w.changed())
		n.sendSessions(c.list)
		got := w.changed()
		if sent := len(got) > before; sent != c.sent {
			t.Fatalf("list %d: sent %v, want %v", i, sent, c.sent)
		}
		if !c.sent {
			continue
		}
		params, err := json.Marshal(map[string]any{"sessions": c.list})
		if err != nil {
			t.Fatal(err)
		}
		if want := `{"event":"sessions.changed","params":` + string(params) + "}\n"; string(got[len(got)-1]) != want {
			t.Fatalf("list %d: wrote\n%s\nwant\n%s", i, got[len(got)-1], want)
		}
	}
}

// A node with no session, or none its scope keeps, lists null, not [], as it always did: the manager takes [] from a
// sessions.list as its list emptied and null as no list given (devices.go).
func TestSessionListOfNoneSendsNull(t *testing.T) {
	for _, rows := range []int{0, 5} {
		n := runtimeListNode(t, rows)
		if rows > 0 { // every row outside the scope
			n.sessionScope = &nodeSessionScope{dir: "/nowhere", claude: filepath.Join(n.home, ".claude"), codex: filepath.Join(n.home, ".codex")}
		}
		w := &wireRec{}
		n.peer = rpc.New("n", w)
		n.sendSessions(n.sessionList(context.Background(), false))
		got := w.changed()
		if want := `{"event":"sessions.changed","params":{"sessions":null}}` + "\n"; len(got) != 1 || string(got[0]) != want {
			t.Errorf("%d rows, none kept: wrote %q, want %q", rows, got, want)
		}
	}
}
