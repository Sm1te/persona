//go:build unix

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// The soak's apps: connections to the hub's WebSocket that subscribe to a few sessions and read what the hub pushes,
// as an app left open does. They are what makes the hub fan the lists out; what they read is only counted.

type appClients struct {
	// lists is how many "sessions" pushes the clients read, messages how many messages of any type.
	lists, messages atomic.Int64
	connected       atomic.Int32
}

// run keeps n clients connected until ctx ends; each subscribes to three of the sessions.
func (a *appClients) run(ctx context.Context, hub, token string, n int, sessions [][2]string) {
	url := "ws" + strings.TrimPrefix(hub, "http") + "/api/v1/ws"
	for i := range n {
		var mine [][2]string
		for k := range 3 {
			if len(sessions) > 0 {
				mine = append(mine, sessions[(i*3+k)%len(sessions)])
			}
		}
		go func() {
			for ctx.Err() == nil {
				a.one(ctx, url, token, mine)
				select {
				case <-ctx.Done():
				case <-time.After(time.Second):
				}
			}
		}()
	}
}

func (a *appClients) one(ctx context.Context, url, token string, sessions [][2]string) {
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
	if err != nil {
		return
	}
	defer c.CloseNow()
	c.SetReadLimit(-1)
	a.connected.Add(1)
	defer a.connected.Add(-1)
	for _, s := range sessions {
		sub := fmt.Sprintf(`{"type":"subscribe","machine_id":%q,"session_id":%q,"since_seq":0}`, s[0], s[1])
		if c.Write(ctx, websocket.MessageText, []byte(sub)) != nil {
			return
		}
	}
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		a.messages.Add(1)
		var m struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &m) == nil && m.Type == "sessions" {
			a.lists.Add(1)
		}
	}
}
