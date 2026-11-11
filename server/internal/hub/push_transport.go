package hub

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/nwverse/persona/server/internal/apns"
	"github.com/nwverse/persona/server/internal/pushcrypto"
	"github.com/nwverse/persona/server/internal/relayproto"
)

// sendDirect posts one push straight to APNs, tried again after each of p.retryWaits while the failure may pass: a
// network error (the dead connection is dropped by apns.Send, so the next try dials a new one) or APNs' 429 / 5xx. Its
// other answers (400, 403, 410…) stand. As for the relay (#2150): one blip used to lose the push (#3989). The same
// apns-collapse-id makes a push that did land twice show once. Callers run it in their own goroutine.
func (p *Pusher) sendDirect(d Device, r apns.Request) (apns.Result, error) {
	p.sendMu.RLock()
	defer p.sendMu.RUnlock()
	if !p.currentPushTarget(d) {
		return apns.Result{}, fmt.Errorf("push registration changed before dispatch")
	}
	res, err := p.direct.Send(context.Background(), r)
	tries := 1
	for _, wait := range p.retryWaits {
		if err == nil && !directRetryable(res.Status) {
			break
		}
		time.Sleep(wait)
		res, err = p.direct.Send(context.Background(), r)
		tries++
	}
	switch {
	case err != nil:
		p.pushedAs(d, r.PushType, err)
	case res.Status != 200 && !res.Gone():
		p.pushedAs(d, r.PushType, fmt.Errorf("APNs %d %s", res.Status, res.Reason))
	case res.Status == 200:
		p.pushedAs(d, r.PushType, nil)
	}
	if tries > 1 {
		if err == nil && res.Status == 200 {
			p.logf("apns %s: %s push went through on try %d", d.Name, r.PushType, tries)
		} else if err != nil {
			err = fmt.Errorf("%w (%d tries)", err, tries)
		}
	}
	return res, err
}

// directRetryable: APNs' answer is one that may pass (too many requests, its own trouble).
func directRetryable(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

type relayResult struct{ gone bool }

// postRelay hands one push to the device's relay (relayproto.PushRequest, docs/operations/push-relay.md "API"). A grant the relay
// no longer takes (its secret was replaced) is renewed once with the device's token, which the hub already has. A
// failure that may pass is tried again (postRelayRetrying): one dropped connection used to lose the push (#2150).
func (p *Pusher) postRelay(d Device, req relayproto.PushRequest) (res relayResult, err error) {
	p.sendMu.RLock()
	defer p.sendMu.RUnlock()
	if !p.currentPushTarget(d) {
		return res, fmt.Errorf("push registration changed before dispatch")
	}
	defer func() {
		if !res.gone {
			p.pushedAs(d, req.Type, err)
		}
	}()
	res, status, err := p.postRelayRetrying(d, req)
	if status != http.StatusUnauthorized {
		return res, err
	}
	d, rerr := p.renewGrant(d)
	if rerr != nil {
		return res, fmt.Errorf("%w; renewing the grant: %v", err, rerr)
	}
	res, _, err = p.postRelayRetrying(d, req)
	return res, err
}

// postRelayRetrying is postRelayOnce, tried again after each of p.retryWaits while the failure is one that may pass
// (relayRetryable). Its callers each run in their own goroutine, so the waits hold up nothing else; a Live Activity
// update that lands late is harmless, as its aps.timestamp makes iOS drop it behind a newer one.
func (p *Pusher) postRelayRetrying(d Device, req relayproto.PushRequest) (relayResult, int, error) {
	res, status, err := p.postRelayOnce(d, req)
	tries, first := 1, err
	for _, wait := range p.retryWaits {
		if err == nil || !relayRetryable(status) {
			break
		}
		time.Sleep(wait)
		res, status, err = p.postRelayOnce(d, req)
		tries++
	}
	switch {
	case tries == 1:
	case err == nil:
		p.logf("relay %s: %s push went through on try %d, after %v", d.Name, req.Type, tries, first)
	default:
		err = fmt.Errorf("%w (%d tries)", err, tries)
	}
	return res, status, err
}

// relayRetryable: a failure worth another try. That is the network's (no status: a TLS handshake or connection that
// timed out, a reset) or the relay's 5xx and 429; any other status is the relay's answer and stands.
func relayRetryable(status int) bool {
	return status == 0 || status == http.StatusTooManyRequests || status >= 500
}

func (p *Pusher) postRelayOnce(d Device, req relayproto.PushRequest) (relayResult, int, error) {
	body, _ := json.Marshal(req)
	hr, err := http.NewRequest("POST", d.RelayURL+"/v1/push", bytes.NewReader(body))
	if err != nil {
		return relayResult{}, -1, err // not a status: a request that cannot be made is not tried again
	}
	hr.Header.Set("Authorization", "Bearer "+d.RelayGrant)
	hr.Header.Set("Content-Type", "application/json")
	sent := time.Now()
	resp, err := p.relay.Do(hr)
	if err != nil {
		return relayResult{}, 0, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)) // read to the end, so an HTTP/1 connection is reused
		_ = resp.Body.Close()
	}()
	var out struct {
		Error  string `json:"error"`
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&out)
	switch {
	case resp.StatusCode == 200:
		if req.Type != "liveactivity" { // when the relay (and APNs behind it) took it: wake-up latency (docs/assistant/assistant.md)
			p.logf("relay %s: %s push accepted in %d ms", d.Name, req.Type, time.Since(sent).Milliseconds())
		}
		return relayResult{}, 200, nil
	case resp.StatusCode == http.StatusGone:
		p.logf("relay %s: %s token gone (%s)", d.Name, req.Type, out.Reason)
		return relayResult{gone: true}, resp.StatusCode, nil
	default:
		return relayResult{}, resp.StatusCode, fmt.Errorf("%d %s %s", resp.StatusCode, out.Error, out.Reason)
	}
}

// renewGrant registers the device's token with its relay again and keeps the new grant.
func (p *Pusher) renewGrant(d Device) (Device, error) {
	if d.APNSToken == "" || d.Topic == "" {
		return d, fmt.Errorf("device has no token to register")
	}
	env := d.APNSEnv
	if env != "sandbox" {
		env = "production"
	}
	body, _ := json.Marshal(relayproto.RegisterRequest{APNSToken: d.APNSToken, Env: env, Topic: d.Topic})
	resp, err := p.relay.Post(d.RelayURL+"/v1/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return d, err
	}
	defer resp.Body.Close()
	var out struct {
		Grant string `json:"grant"`
		Error string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&out)
	if resp.StatusCode != 200 || out.Grant == "" {
		return d, fmt.Errorf("register: %d %s", resp.StatusCode, out.Error)
	}
	p.saveMu.Lock()
	defer p.saveMu.Unlock()
	p.mu.Lock()
	cur, ok := p.devices[d.DeviceID]
	kept := ok && cur.RelayURL == d.RelayURL && cur.APNSToken == d.APNSToken
	p.mu.Unlock()
	if !kept {
		return d, fmt.Errorf("push registration changed during grant renewal")
	}
	previous := cur.RelayGrant
	cur.RelayGrant = out.Grant
	if err := p.writeDevice(cur); err != nil {
		return d, err
	}
	p.mu.Lock()
	if previous != out.Grant {
		p.replaced[d.DeviceID] = previous
	}
	if latest := p.devices[d.DeviceID]; latest.LastPushAt.After(cur.LastPushAt) {
		cur.LastPushAt = latest.LastPushAt
	}
	p.devices[d.DeviceID] = cur
	p.mu.Unlock()
	p.logf("relay %s: grant renewed", d.Name)
	d.RelayGrant = out.Grant
	return d, nil
}

// sealFor seals a push's payload to the device's key, stamped with when it was sealed (the phone shows only
// "New messages" for one older than a day, so a replayed old one tells nothing new). Texts too long for one push (an
// ask_user question, a long command) are shortened until it fits: the full text is in the app.
func (p *Pusher) sealFor(d Device, payload map[string]any) (string, error) {
	pub, err := pushcrypto.PublicKey(d.PushKey)
	if err != nil {
		return "", err
	}
	stamped := make(map[string]any, len(payload)+1)
	for k, v := range payload {
		stamped[k] = v
	}
	stamped["iat"] = p.now().Unix()
	plain, err := fitAlert(stamped, relayMaxPlain)
	if err != nil {
		return "", err
	}
	sealed, err := pushcrypto.Seal(pub, plain)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// apnsMaxAlert is APNs' limit for an alert push's body (bytes); a bigger one is refused with 413 PayloadTooLarge.
const apnsMaxAlert = 4096

// relayMaxPlain is the most JSON one sealed push carries (bytes): what seals to relayproto.MaxSealed base64 characters.
const relayMaxPlain = relayproto.MaxSealed/4*3 - pushcrypto.MinSealed

// fitAlert is payload as JSON of at most most bytes: its alert texts are shortened (1000 characters, then half that…)
// until it fits; the full text is in the app. payload's "aps" is replaced in place by a shortened copy.
func fitAlert(payload map[string]any, most int) ([]byte, error) {
	b, _ := json.Marshal(payload)
	full := payload["aps"]
	for limit := 1000; len(b) > most; limit /= 2 {
		if limit < 20 {
			if _, ok := payload["ask"]; !ok {
				return nil, fmt.Errorf("payload of %d bytes does not fit", len(b))
			}
			// Option labels must remain complete. Without ask, the client opens the app;
			// retry from the full alert so dropping options does not leave a tiny preview.
			delete(payload, "ask")
			payload["aps"] = full
			b, _ = json.Marshal(payload)
			limit = 2000 // The loop halves this before retrying the alert at 1000 characters.
			continue
		}
		payload["aps"] = shortenAlert(payload["aps"], limit)
		b, _ = json.Marshal(payload)
	}
	return b, nil
}

// shortenAlert returns a copy of aps whose alert texts have at most n characters (the original is shared by all devices).
func shortenAlert(v any, n int) any {
	aps, ok := v.(map[string]any)
	if !ok {
		return v
	}
	alert, ok := aps["alert"].(map[string]any)
	if !ok {
		return v
	}
	a2 := make(map[string]any, len(alert))
	for k, t := range alert {
		if s, ok := t.(string); ok {
			t = clip(s, n)
		}
		a2[k] = t
	}
	out := make(map[string]any, len(aps))
	for k, t := range aps {
		out[k] = t
	}
	out["alert"] = a2
	return out
}

// relayCollapseID: the relay sees a hash of the collapse id (a card's, cardCollapseID, or another push's), not the id.
func relayCollapseID(id string) string {
	if id == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:16])
}
