package hub

import (
	"cmp"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"maps"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// pushWants is which devices' settings take a push.
type pushWants struct {
	Kind string        // "" every device | input | done
	Took time.Duration // done: how long the turn took
	Long time.Duration // done: what counts as a long one
}

func (w pushWants) fn() func(NotifyPrefs) bool {
	switch w.Kind {
	case "input":
		return func(p NotifyPrefs) bool { return p.Input }
	case "done":
		return func(p NotifyPrefs) bool { return p.wantsDone(w.Took, w.Long) }
	}
	return nil
}

// notifyDone delivers the same alert and preference decision through both channels. Only the center has apps and
// devices: a member's computers are the center's machines. owner is the manager that asked for it: the devices that
// turned its pushes off get none (NotifyPrefs.MutedManagers).
func (h *Hub) notifyDone(owner, kind, machineID, machine, sessionID, title, text string, wants pushWants) {
	h.notifyDoneUnder(owner, kind, machineID, machine, sessionID, title, text, wants, "")
}

// notifyDoneUnder is notifyDone with the push's apns-collapse-id ("": one of its own).
func (h *Hub) notifyDoneUnder(owner, kind, machineID, machine, sessionID, title, text string, wants pushWants, collapseID string) {
	build := func(l i18n.Lang) map[string]any { return donePayloadIn(l, machineID, machine, sessionID, title, text) }
	h.emitToExtensions(owner, "agent.finished", map[string]any{"machine_id": machineID, "machine": machine, "session_id": sessionID, "title": title})
	h.push.sendFrom(owner, build, collapseID, wants.fn(), wants.quiet(), nil)
	h.notifyWSFrom(owner, kind, build, wants.fn(), wants.quiet())
}

// managerPushCollapseID is the apns-collapse-id of manager's push with notification id (assistantwire.Notification.ID;
// "" without one): the same push asked for again once the hub no longer remembers handing it over (handedOverFor)
// takes the place of the one the phone shows instead of adding a second. A hash of the manager and the id, so it
// replaces only that manager's push of that id: never a card's, a decision's (td_), a notify API alert, an extension's
// push or the hub's own.
func managerPushCollapseID(manager, id string) string {
	if id == "" {
		return ""
	}
	manager = cmp.Or(manager, assistantwire.AssistantID)
	sum := sha256.New()
	sum.Write([]byte("push\x00"))
	sum.Write(binary.AppendUvarint(nil, uint64(len(manager))))
	sum.Write([]byte(manager))
	sum.Write([]byte(id))
	return "push:" + hex.EncodeToString(sum.Sum(nil)[:16])
}

// notifyReply reminds the devices that take "needs you" pushes of an agent still waiting on the user's answer (#5096).
// It is laid out as a finished turn's (answerable from the notification, opens the session), but nothing finished:
// the extensions hear no agent.finished. On the WebSocket it is a "reply" notify (not "done": Android puts away the
// finished notifications of seen sessions when it starts, and a reminder is about a seen one). owner is the manager
// that asked for it, as for notifyDone.
func (h *Hub) notifyReply(owner, machineID, machine, sessionID, title, text string) {
	build := func(l i18n.Lang) map[string]any { return donePayloadIn(l, machineID, machine, sessionID, title, text) }
	wants := pushWants{Kind: "input"}
	h.push.sendFrom(owner, build, "", wants.fn(), wants.quiet(), nil)
	h.notifyWSFrom(owner, "reply", build, wants.fn(), wants.quiet())
}

// notifyDecide pushes a to-do whose conclusion waits for the owner's decision and recommends an option (#2056) to the
// devices that take "needs you" pushes: category TODO_DECIDE, whose OK takes the recommendation (decidePayloadIn). On
// the WebSocket it is an "assistant" notify carrying the same `todo`: a client that does not know it shows a plain
// notification. Nothing finished: the extensions hear no agent.finished. One collapse id a to-do: a later round's push
// takes the place of the earlier one, whose OK could only answer "already handled".
func (h *Hub) notifyDecide(owner, machineID, machine, sessionID, title, text string, todo assistantwire.NotifyTodo) {
	build := func(i18n.Lang) map[string]any {
		return decidePayloadIn(owner, machineID, machine, sessionID, title, text, todo)
	}
	wants := pushWants{Kind: "input"}
	sum := sha256.Sum256([]byte("todo_decide\x00" + todo.ID))
	h.push.sendFrom(owner, build, "td_"+hex.EncodeToString(sum[:12]), wants.fn(), wants.quiet(), nil)
	h.notifyWSFrom(owner, "assistant", build, wants.fn(), wants.quiet())
}

// notifyInteraction pushes a new card. linkCovered: owner says its Persona Link summary rings for this card
// (assistantwire.Notification.LinkCovered).
func (h *Hub) notifyInteraction(owner string, ix proto.Interaction, machine string, linkCovered bool) {
	build := func(l i18n.Lang) map[string]any { return interactionPayloadIn(l, ix, machine) }
	summary := ix.Summary
	if ix.Private { // a Just me turn's card: what it asks stays in the app
		summary = ""
	}
	h.emitToExtensions(owner, "agent.needs_you", map[string]any{"machine_id": ix.MachineID, "machine": machine, "session_id": ix.SessionID,
		"interaction_id": ix.ID, "kind": ix.Kind, "summary": summary})
	wants := pushWants{Kind: "input"}
	if ix.Kind == proto.IxElicitation {
		// Only what can show an MCP form or link: the phones that registered for one (#2433; a tap opens the card,
		// the push has no buttons) and the connections that declared it.
		h.push.sendCard(owner, build, ix.ID, wants.fn(), wants.quiet(), takesElicitation)
		h.notifyWSFrom(owner, "interaction", build, wants.fn(), wants.quiet(), "elicitation")
	} else {
		// A card owner's Persona Link summary rings for: "Phone Muted" holds it back while one of owner's Persona Link
		// desktops is there too (desktops.go); which cards those are is owner's to say. The desktop app's online check
		// follows in sendCard and notifyWSFrom.
		fn := alertWanted(wants.fn(), linkCovered && h.linkDesktopOnline(owner))
		h.push.sendCard(owner, build, ix.ID, fn, wants.quiet(), nil)
		h.notifyWSFrom(owner, "interaction", build, fn, wants.quiet())
	}
}

// notifyWSFrom projects a push onto the opt-in WebSocket notification event, written in each connection's language
// (client.lang), by the authenticated device's preferences, even without an APNs token or key, and with what its quiet
// hours do to it (quiethours.go): skipped, or marked "quiet" for the client to post without a sound. manager is the
// one that asked for it: a device that turned that manager's pushes off (NotifyPrefs.MutedManagers) gets none, as with
// its push (Pusher.sendFrom); "" is the hub's own, for every device wants takes. Returns the selected count and the
// count of capable connections before preferences are applied; neither is a client delivery receipt. A device without a
// push route and without a ws_notify socket now keeps what its settings take for its next connection (notifyring.go);
// that is not counted.
func (h *Hub) notifyWSFrom(manager, kind string, build localized, wants func(NotifyPrefs) bool, rule quietRule, required ...string) (selected, reachable int) {
	msgs := map[i18n.Lang]map[string]any{}
	msgIn := func(l i18n.Lang) map[string]any {
		if m, ok := msgs[l]; ok {
			return m
		}
		m := notifyMessage(kind, build(l))
		msgs[l] = m
		return m
	}
	type form struct {
		lang  i18n.Lang
		quiet bool
	}
	encoded := map[form][]byte{}
	msgFor := func(l i18n.Lang, quiet bool) []byte {
		if b, ok := encoded[form{l, quiet}]; ok {
			return b
		}
		msg := msgIn(l)
		if quiet {
			msg = maps.Clone(msg)
			msg["quiet"] = true
		}
		b, err := json.Marshal(msg)
		if err != nil {
			b = nil // sent nowhere, as sendJSON did
		}
		encoded[form{l, quiet}] = b
		return b
	}
	// Mute phone holds these back while a desktop is online, with the same rule as the APNs pushes (alertWanted): an
	// Android phone alerts from them. desktopOnline takes h.mu, so it is read before.
	wants = alertWanted(wants, h.desktopOnline())
	// Read before h.mu as well: the user's time zone asks the assistant's profile.
	now, loc := time.Now(), (*time.Location)(nil)
	if rule != quietNone && h.push != nil {
		now, loc = h.push.now(), h.userLocation()
	}
	// wantsOn is whether device's settings take it, and whether inside its quiet hours (posted without a sound).
	wantsOn := func(device string) (ok, quiet bool) {
		prefs := defaultPrefs()
		if h.push != nil {
			prefs = h.push.prefs(device)
		}
		if prefs.mutes(manager) || wants != nil && !wants(prefs) {
			return false, false
		}
		quiet = rule != quietNone && prefs.Quiet.activeAt(now, loc)
		return !(quiet && rule == quietSkip), quiet
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	live := map[string]bool{} // the devices with a ws_notify socket now
	for c := range h.clients {
		if !c.has("ws_notify") {
			continue
		}
		live[c.device] = true
		if len(required) > 0 && !c.has(required[0]) {
			continue
		}
		reachable++
		ok, quiet := wantsOn(c.device)
		if !ok {
			continue
		}
		if b := msgFor(c.lang(), quiet); b != nil {
			c.queue(b)
		}
		selected++
	}
	h.ringNotify(live, required, wantsOn, msgIn, msgFor)
	return selected, reachable
}

func notifyMessage(kind string, payload map[string]any) map[string]any {
	payload = cleanAlert(payload)
	aps := payload["aps"].(map[string]any)
	alert := aps["alert"].(map[string]any)
	msg := map[string]any{
		"type": "notify", "kind": kind,
		"machine_id": payload["machine_id"], "session_id": payload["session_id"],
		"title": alert["title"], "subtitle": alert["subtitle"], "body": alert["body"],
	}
	if id, ok := payload["interaction_id"]; ok {
		msg["interaction_id"] = id
	}
	if id, ok := payload["id"]; ok { // alert: the caller's id (POST /api/v1/notify)
		msg["id"] = id
	}
	if en, ok := payload["extension_notification"]; ok {
		msg["extension_notification"] = en
	}
	if td, ok := payload["todo"]; ok { // a decision's push (decidePayloadIn)
		msg["todo"] = td
	}
	return msg
}
