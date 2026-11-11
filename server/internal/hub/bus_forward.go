package hub

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/memtrim"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The apps' requests on the assistant's routes (assistantwire.Routes, assistant_link.go) are its to answer: the hub
// passes each on as the bus request http.request and writes back what the assistant answers, unchanged; what the
// assistant sends the apps over their WebSocket (ws.broadcast) goes to them unchanged.
//
//	http.request {method, path, query, header, body} → {status, header, body}
//	ws.broadcast {message}   (an event: message is a JSON object with a string "type": assistant_schedule…)
//
// header maps a name to its values; body is base64 (JSON []byte); status 0 is 200, and one outside 200-599 reaches the
// app as a 502. X-Persona-Role carries the role of the device that asked, as the hub's auth found it (any the app sent
// is replaced). forwardHTTP is the codec of the assistant's Handler.ServeHTTP over the bus, relayBroadcastFrom the
// hub's side of its Broadcast.

func init() { handleBusEvent(assistantwire.EventBroadcast, (*Hub).busBroadcast) }

// busHTTPLimit bounds a forwarded request's body (a message on the bus is at most 64 MiB, base64 included).
const busHTTPLimit = 32 << 20

// hopHeaders are not passed on either way: they are about one connection, not the request.
var hopHeaders = map[string]bool{"Connection": true, "Keep-Alive": true, "Te": true, "Trailer": true, "Transfer-Encoding": true,
	"Upgrade": true, "Proxy-Authorization": true, "Proxy-Connection": true, "Content-Length": true}

// forwardHTTP passes r on to manager id as http.request and writes its answer.
func (h *Hub) forwardHTTP(w http.ResponseWriter, r *http.Request, id string) {
	h.relayHTTP(w, r, id, false, false)
}

// forwardAdminHTTP is forwardHTTP for one of the admin UI's routes, with a signed-in admin session: the manager is
// told so (assistantwire.AdminHeader).
func (h *Hub) forwardAdminHTTP(w http.ResponseWriter, r *http.Request, id string) {
	h.relayHTTP(w, r, id, false, true)
}

// forwardSend is forwardHTTP for a request that sends something on (an inbox reply). When the link drops or the time
// runs out while the request is with the manager, it may have been done: 502 assistant_in_doubt, delivered "unknown"
// (assistant_offline says nothing went, which only a request that never reached it can say).
func (h *Hub) forwardSend(w http.ResponseWriter, r *http.Request, id string) {
	h.relayHTTP(w, r, id, true, false)
}

// errSendInDoubt is what forwardSend answers when it can't tell whether the request was done.
var errSendInDoubt = i18n.Err("hub-bus_forward.send_in_doubt")

// readRelayed reads r's body for the bus with io.ReadAll's results: the bytes to its end, or the error that stopped
// the read (an *http.MaxBytesError past busHTTPLimit). A body whose Content-Length is known and within the limit is read
// into one buffer of that size, where io.ReadAll grows buffers and copies them into one more (49 MB for a 22 MB
// upload). Bytes past the Content-Length are read on as io.ReadAll reads them; a body that ends early is what it was.
func readRelayed(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	body := http.MaxBytesReader(w, r.Body, busHTTPLimit)
	size := r.ContentLength
	if size < 0 || size > busHTTPLimit {
		return io.ReadAll(body)
	}
	b := make([]byte, size)
	for n := 0; n < len(b); {
		m, err := body.Read(b[n:])
		n += m
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return b[:n], err
		}
	}
	var one [1]byte
	for {
		m, err := body.Read(one[:])
		if m > 0 {
			b = append(b, one[0])
			if err == nil {
				var rest []byte
				rest, err = io.ReadAll(body)
				b = append(b, rest...)
			}
		}
		if err != nil || m > 0 {
			if err == io.EOF {
				err = nil
			}
			return b, err
		}
	}
}

// httpParams is req as http.request's params, its body appended in base64 straight into the request's line. As a
// value, the encoder writes the body into a buffer of its own and copies it out, and the line's encoder copies it
// once more: three copies of a 22 MB upload, of 30 MB each, and the encoder's buffer kept for the next. The bytes are
// the ones rpc.Call writes for req as a value: the rest of it goes through the same encoder (rpc.Marshal), the body
// is its last field, and base64 has nothing to escape.
func httpParams(req assistantwire.HTTPRequest) any {
	body := req.Body
	if len(body) == 0 {
		return req
	}
	req.Body = nil
	head, err := rpc.Marshal(req)
	if err != nil {
		req.Body = body
		return req
	}
	head = head[:len(head)-1] // its closing brace; method and path are always there before the body
	const mid, tail = `,"body":"`, `"}`
	return rpc.Appended{
		Size: len(head) + len(mid) + base64.StdEncoding.EncodedLen(len(body)) + len(tail),
		Append: func(b []byte) []byte {
			b = append(append(b, head...), mid...)
			return append(base64.StdEncoding.AppendEncode(b, body), tail...)
		},
	}
}

func (h *Hub) relayHTTP(w http.ResponseWriter, r *http.Request, id string, send, admin bool) {
	body, err := readRelayed(w, r)
	if err != nil {
		writeBodyErr(w, err, err)
		return
	}
	defer memtrim.AfterLarge(len(body)) // a large upload's buffers go back to the system after it
	req := assistantwire.HTTPRequest{Method: r.Method, Path: r.URL.EscapedPath(), Query: r.URL.RawQuery, Header: map[string][]string{}, Body: body}
	for k, v := range r.Header {
		if !hopHeaders[k] && k != "Authorization" && k != "Cookie" { // the hub's credentials stay with the hub
			req.Header[k] = v
		}
	}
	p := principalOf(r.Context())
	req.Header[assistantwire.RoleHeader] = []string{p.Role}
	delete(req.Header, assistantwire.ExtensionHeader) // only the hub says which extension asks
	delete(req.Header, assistantwire.AdminHeader)     // and whether an admin session asks
	delete(req.Header, assistantwire.DeviceNameHeader)
	if p.Name != "" { // and which device: the manager records who switched a model profile
		req.Header[assistantwire.DeviceNameHeader] = []string{p.Name}
	}
	if admin {
		req.Header[assistantwire.AdminHeader] = []string{"1"}
	}
	if p.Role == roleExtension && p.Extension != "" {
		req.Header[assistantwire.ExtensionHeader] = []string{p.Extension}
	}
	req.HandoffApprover = handoffApproverOf(r.Context()) // only the resolve route's gate sets it (secret_handoff.go)
	ctx := r.Context()
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
	}
	// What may change the manager's inbox: a merged list fetched while it is under way is not kept, and one kept from
	// before it answered is not given again (inbox_answers.go).
	changes := r.Method != http.MethodGet && r.Method != http.MethodHead
	if changes {
		h.inboxAnswers.changed(id)
	}
	var res assistantwire.HTTPResponse
	err = h.callBus(ctx, id, assistantwire.MethodHTTP, httpParams(req), &res)
	if changes {
		h.inboxAnswers.changed(id)
	}
	if err != nil {
		var coded *rpc.Error
		switch {
		case send && (errors.Is(err, errBusDropped) || errors.Is(err, context.DeadlineExceeded)):
			body := errorBody(http.StatusBadGateway, assistantwire.CodeAssistantInDoubt, errSendInDoubt)
			body["error"] = i18n.Localize(errSendInDoubt, h.requestLang(r))
			body["delivered"], body["retryable"] = "unknown", false
			writeErrorBody(w, http.StatusBadGateway, body)
		case errors.Is(err, errBusOffline):
			writeErrCode(w, http.StatusServiceUnavailable, assistantwire.CodeAssistantOffline, errAssistantOffline)
		case errors.As(err, &coded):
			writeErrCode(w, http.StatusBadGateway, coded.Code, err)
		default:
			writeErr(w, http.StatusBadGateway, err)
		}
		return
	}
	if res.Status == 0 {
		res.Status = http.StatusOK
	}
	// A manager is third-party code: a status that is not a final HTTP status makes net/http panic (outside 100-999)
	// or leaves the app's connection unusable (1xx). The app gets a 502, none of the manager's header or body.
	if res.Status < http.StatusOK || res.Status > 599 {
		h.logf("bus: [%s] http.request %s %s: answered HTTP status %d, the app gets 502", id, r.Method, r.URL.Path, res.Status)
		writeErr(w, http.StatusBadGateway, fmt.Errorf("the manager answered HTTP status %d", res.Status))
		return
	}
	for k, v := range res.Header {
		if !hopHeaders[http.CanonicalHeaderKey(k)] {
			w.Header()[http.CanonicalHeaderKey(k)] = v
		}
	}
	if res.Status >= http.StatusBadRequest && strings.HasPrefix(r.URL.Path, "/api/v1/machines/") && strings.HasSuffix(r.URL.Path, "/deploy") {
		res.Body = localizedDeploymentError(h.requestLang(r), res.Body)
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/inbox") || strings.HasPrefix(r.URL.Path, "/api/v1/translate") {
		if raw := inboxListRawOf(r); raw != nil && res.Status < http.StatusMultipleChoices {
			raw.kept = true // an inbox list: localized item by item as it is merged (inbox_forms.go)
		} else if localized := i18n.InboxJSON(h.requestLang(r), res.Body); !bytes.Equal(localized, res.Body) {
			res.Body = localized
			w.Header().Del("Content-Length")
		}
	}
	w.WriteHeader(res.Status)
	_, _ = w.Write(res.Body)
}

// busBroadcast is ws.broadcast from a manager.
func (h *Hub) busBroadcast(c *busConn, params json.RawMessage) {
	var p struct {
		Message json.RawMessage `json:"message"`
	}
	err := json.Unmarshal(params, &p)
	if err == nil {
		err = h.relayBroadcastFrom(c.managerID(), p.Message)
	}
	if err != nil {
		h.logf("bus: [%s] ws.broadcast: %v", c.ident.ID, err)
	}
}

// An install's steps (docs/protocol/protocol.md "Extension install, upgrade and uninstall progress") go only to the apps that declared they know them,
// and not into the event log: a download pushes once a second, and an app that comes back lists them again.
const (
	extensionOperationEvent    = "extension_operation"
	extensionOperationsFeature = "extension_operations"
)

// toWanting sends b to the apps that asked for feature (?features= or client_hello), outside the event log. An
// extension operation is localized once per language, not once per app.
func (h *Hub) toWanting(feature string, b []byte) {
	forms := map[i18n.Lang][]byte{}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.wants(feature) {
			if feature == extensionOperationsFeature {
				c.queue(localizedOnce(forms, c.lang(), b, i18n.ExtensionEvent))
			} else {
				c.queue(b)
			}
		}
	}
}

// localizedOnce is localize(l, b), built on the first call for l and reused from forms after: a broadcast's localized
// form depends only on its language, so every app that reads that language is queued the same bytes (client.queue
// does not change what it is given).
func localizedOnce(forms map[i18n.Lang][]byte, l i18n.Lang, b []byte, localize func(i18n.Lang, []byte) []byte) []byte {
	if form, ok := forms[l]; ok {
		return form
	}
	form := localize(l, b)
	forms[l] = form
	return form
}

// toFullWanting is toWanting for the full devices only: a computer to computer transfer's progress (file_transfer,
// docs/protocol/file-transfer.md) names files, which an answer-only device (the watch) has no business with.
func (h *Hub) toFullWanting(feature string, b []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.role == roleFull && c.wants(feature) {
			c.queue(b)
		}
	}
}
