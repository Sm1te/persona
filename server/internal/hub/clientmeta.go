package hub

import (
	"encoding/json"
	"github.com/nwverse/persona/server/internal/compat"
	"math/rand/v2"

	"github.com/nwverse/persona/server/internal/i18n"
)

// clientMeta is what a client says about itself in client_hello. A zero value is a legacy iOS client.
// All access is protected by client.mu; identity here never replaces the authenticated device or role.
type clientMeta struct {
	DeviceID string          `json:"device_id"`
	Platform string          `json:"platform"`
	Version  string          `json:"version"`
	Build    json.RawMessage `json:"build"`
	// Locale: the interface language the client shows (zh-Hans / en / ja, docs/protocol/i18n.md); kept when a later hello omits it.
	Locale string `json:"locale"`
	OS     struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		SDK     int    `json:"sdk"`
	} `json:"os"`
	features  map[string]bool
	heartbeat heartbeatConfig
	updates   chan heartbeatConfig
	done      <-chan struct{}
}

type heartbeatConfig struct {
	Interval    int `json:"interval"`
	PongTimeout int `json:"pong_timeout"`
	negotiated  bool
}

func defaultHeartbeat() heartbeatConfig {
	return heartbeatConfig{Interval: 20, PongTimeout: 15}
}

// has reports whether the client declared a capability in client_hello.
func (c *client) has(feature string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.meta.features[feature]
}

// wants reports whether the client asked for feature on the WebSocket URL (?features=) or in client_hello: the
// check for opt-in events (toSubscribersWith). The older capabilities keep their own (has, client.delta).
func (c *client) wants(feature string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.urlFeatures[feature] || c.meta.features[feature]
}

func (h *Hub) onClientHello(c *client, data []byte) {
	var msg struct {
		clientMeta
		Features  []string `json:"features"`
		Heartbeat *struct {
			Interval    *int `json:"interval"`
			PongTimeout *int `json:"pong_timeout"`
		} `json:"heartbeat"`
	}
	if json.Unmarshal(data, &msg) != nil {
		return
	}
	wasElicitation := c.has("elicitation")
	wasDesktopsLink := c.wants(featureDesktopsLink)
	wasLang := c.lang()
	c.mu.Lock()
	hb := c.meta.heartbeat
	if hb.Interval == 0 {
		hb = defaultHeartbeat()
	}
	if msg.Heartbeat != nil {
		if msg.Heartbeat.Interval != nil {
			interval := max(10, min(900, *msg.Heartbeat.Interval))
			// Choose once per negotiation so the ack is the exact timer interval, in whole seconds.
			spread := min(interval-10, interval/10)
			hb.Interval = interval - rand.IntN(spread+1)
		}
		if msg.Heartbeat.PongTimeout != nil {
			hb.PongTimeout = max(5, min(60, *msg.Heartbeat.PongTimeout))
		}
		hb.negotiated = true
	}
	msg.features = make(map[string]bool, len(msg.Features))
	for _, f := range msg.Features {
		msg.features[f] = true
	}
	msg.heartbeat, msg.updates, msg.done = hb, c.meta.updates, c.meta.done
	if !i18n.Valid(i18n.Lang(msg.Locale)) {
		msg.Locale = c.meta.Locale
	}
	c.canonicalContracts.Store(msg.features[compat.Feature])
	c.meta = msg.clientMeta
	updates, done := c.meta.updates, c.meta.done
	c.mu.Unlock()
	// Last, after the cards it can now answer: the notify events kept while it had no ws_notify socket (notifyring.go).
	defer h.replayNotifyRing(c)
	go h.fleetClientsChanged() // its features (elicitation) count now (fleet_link.go)
	h.noteClientApp(c)         // its version, for the assistant (client_apps.go)
	if c.role == roleFull && c.hub != nil {
		// Only this device changing its language changes the user's (locale.go); an invalid one changes nothing.
		c.hub.noteUserLang(deviceLangKey(c, msg.clientMeta), i18n.Lang(msg.Locale))
	}
	if c.role == roleFull && wasLang != c.lang() {
		c.sendJSON(map[string]any{"type": "machines", "machines": localizedMachines(c.lang(), h.machineList())})
	}
	if !wasElicitation && c.has("elicitation") {
		defer h.sendPendingInteractions(c, true)
	}
	if wasDesktopsLink != c.wants(featureDesktopsLink) {
		// The list it has left the Persona Link desktops out, or holds ones it will not hear end.
		defer h.sendDesktops(c)
	}
	if updates == nil {
		c.helloAck(hb)
		return
	}
	select {
	case updates <- hb:
	case <-done:
	}
}

func (c *client) helloAck(hb heartbeatConfig) {
	c.sendJSON(map[string]any{"type": "client_hello_ack", "heartbeat": hb})
}

// onAppPing echoes a string id, including an empty string, without interpreting it.
func (h *Hub) onAppPing(c *client, data []byte) {
	var msg struct {
		ID *string `json:"id"`
	}
	if json.Unmarshal(data, &msg) != nil || msg.ID == nil {
		return
	}
	c.sendJSON(map[string]any{"type": "pong", "id": *msg.ID})
}
