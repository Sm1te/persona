package hub

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/proto"
)

// POST …/modes with a launcher (#5035) goes to the node's session.modes as it came, launcher and from included; a
// launcher or from that is not a string is refused before the node is asked, and so is any launcher to a node that has
// not announced launcher_switch (it would drop the launcher and switch the model where the Agent runs), or that its
// manager has not reported yet (after a hub restart: it may be such a node).
func TestSessionModesPassesTheLauncherSwitch(t *testing.T) {
	switching := &proto.NodeInfo{Version: "test", Protocol: 1, Capabilities: []string{nodeproto.Sessions, nodeproto.LauncherSwitch}}
	older := &proto.NodeInfo{Version: "old", Protocol: 1, Capabilities: []string{nodeproto.Sessions}}
	for _, tc := range []struct {
		name, body string
		info       *proto.NodeInfo
		status     int
		code       string
		want       map[string]any // what session.modes got; nil: not called
	}{
		{"switch", `{"launcher":"work","from":"claude","model":"opus","effort":"high"}`, switching, http.StatusOK, "",
			map[string]any{"session_id": "s1", "launcher": "work", "from": "claude", "model": "opus", "effort": "high"}},
		{"cancel", `{"launcher":"claude","from":"claude"}`, switching, http.StatusOK, "",
			map[string]any{"session_id": "s1", "launcher": "claude", "from": "claude"}},
		{"launcher not a string", `{"launcher":7}`, switching, http.StatusBadRequest, "", nil},
		{"from not a string", `{"launcher":"work","from":false}`, switching, http.StatusBadRequest, "", nil},
		{"older node", `{"launcher":"work","model":"opus"}`, older, http.StatusConflict, proto.CodeNodeOutdated, nil},
		{"node not reported yet", `{"launcher":"work"}`, nil, http.StatusConflict, proto.CodeNodeOutdated, nil},
		{"older node, no launcher", `{"model":"opus"}`, older, http.StatusOK, "", map[string]any{"session_id": "s1", "model": "opus"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHub(t)
			var got map[string]any
			modelsNode(t, h, MachineConfig{ID: "box"}, func(method string, p map[string]any) (any, error) {
				if method == "session.modes" {
					got = p
				}
				return map[string]any{}, nil
			})
			if tc.info != nil {
				m := h.machineByID("box")
				m.mu.Lock()
				m.info = *tc.info
				m.mu.Unlock()
			}
			w := modelsRequest(h, "POST", "/api/v1/sessions/box/s1/modes", tc.body)
			if w.Code != tc.status || (tc.code != "" && !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`)) {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("session.modes got %v, want %v", got, tc.want)
			}
		})
	}
}

// A model picked with a launcher is remembered as used under that launcher, not the one the session runs on now.
func TestModesLauncher(t *testing.T) {
	s := proto.Session{Launcher: "mine"}
	for _, tc := range []struct {
		p    map[string]any
		s    proto.Session
		want string
	}{
		{map[string]any{"model": "opus", "launcher": "work"}, s, "work"},
		{map[string]any{"model": "opus", "launcher": " "}, s, "mine"},
		{map[string]any{"model": "opus"}, s, "mine"},
		{map[string]any{"model": "opus"}, proto.Session{}, "claude"},
	} {
		if got := modesLauncher(tc.p, tc.s); got != tc.want {
			t.Errorf("modesLauncher(%v, %q) = %q, want %q", tc.p, tc.s.Launcher, got, tc.want)
		}
	}
}

// The user's message that carries out a launcher switch (apply_switch) goes to the node's session.send with the flag;
// the hub waits for it longer than the node can take (switchSendWait).
func TestSendMessagePassesApplySwitch(t *testing.T) {
	h := newTestHub(t)
	var got map[string]any
	modelsNode(t, h, MachineConfig{ID: "box"}, func(method string, p map[string]any) (any, error) {
		if method == "session.send" {
			got = p
		}
		return map[string]any{}, nil
	})
	w := modelsRequest(h, "POST", "/api/v1/sessions/box/s1/messages", `{"text":"hello","apply_switch":true}`)
	if w.Code != http.StatusOK || got["apply_switch"] != true || got["text"] != "hello" {
		t.Fatalf("%d %s; node got %v", w.Code, w.Body, got)
	}
	if switchSendWait < 7*time.Minute {
		t.Errorf("the hub waits %v, less than the node's checks (1 min) and switch (5 min) with a margin", switchSendWait)
	}
	for _, tc := range []struct {
		p    map[string]any
		s    proto.Session
		want time.Duration
	}{
		{map[string]any{"text": "hi"}, proto.Session{Status: proto.StatusIdle}, 90 * time.Second},
		{map[string]any{"text": "hi"}, proto.Session{Status: proto.StatusOffline}, 5 * time.Minute},
		// a switch picked a moment ago, not on the cached row yet: the request says so
		{map[string]any{"text": "hi", "apply_switch": true}, proto.Session{Status: proto.StatusIdle}, switchSendWait},
	} {
		if got := sendWait(tc.p, tc.s); got != tc.want {
			t.Errorf("sendWait(%v, %s) = %v, want %v", tc.p, tc.s.Status, got, tc.want)
		}
	}
}
