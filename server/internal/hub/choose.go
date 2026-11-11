package hub

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"strings"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

const (
	codeNotPending = "not_pending"
	codeAskChanged = "ask_changed"
)

// serveChoose maps a notification's option indexes to full labels and forwards the
// same Resolution.Answers as the app to the card's manager.
func (h *Hub) serveChoose(w http.ResponseWriter, r *http.Request) {
	refuse := func(status int, code string) {
		writeErrCode(w, status, code, errors.New(i18n.T(h.requestLang(r), "hub-choose."+code)))
	}
	var in struct {
		Option   *int   `json:"option"`
		Options  []*int `json:"options"`
		Rev      string `json:"rev"`
		ClientID string `json:"client_id"`
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<10))
	if bodyTooLarge(err) {
		refuse(http.StatusRequestEntityTooLarge, proto.CodeTooLarge)
		return
	}
	if err != nil || json.Unmarshal(raw, &in) != nil {
		refuse(http.StatusBadRequest, "bad_request")
		return
	}
	id := r.PathValue("id")
	ix, pending := h.pendingInteraction(id)
	if !pending {
		// The hub retains pending cards only: unknown, answered and withdrawn IDs
		// all mean that a notification can no longer answer this card.
		refuse(http.StatusConflict, codeNotPending)
		return
	}
	q, ok := askQuestion(ix)
	if !ok || ix.ToolName == proto.SecretHandoffTool { // a hand-off card is approved on the card only (secret_handoff.go)
		refuse(http.StatusBadRequest, "not_choosable")
		return
	}
	picks := in.Options
	if in.Option != nil {
		picks = []*int{in.Option}
	}
	if in.Rev == "" || len(picks) == 0 || in.Option != nil && in.Options != nil {
		refuse(http.StatusBadRequest, "bad_request")
		return
	}
	if in.Rev != askRev(q) {
		refuse(http.StatusConflict, codeAskChanged)
		return
	}
	if !q.MultiSelect && len(picks) > 1 {
		refuse(http.StatusBadRequest, "single_select")
		return
	}
	chosen := make([]bool, len(q.Options))
	for _, pick := range picks {
		if pick == nil || *pick < 0 || *pick >= len(q.Options) || chosen[*pick] {
			refuse(http.StatusBadRequest, "invalid_choice")
			return
		}
		chosen[*pick] = true
	}
	var labels []string
	for i, option := range q.Options {
		if chosen[i] {
			labels = append(labels, option.Label)
		}
	}
	// Bind to the validated snapshot even if a resolved event removes it now.
	manager, ok := h.owner(w, r, func(r *http.Request) (string, error) {
		return h.extensionsOwn(r, ix.Manager)
	})
	if !ok {
		return
	}
	body, _ := json.Marshal(proto.Resolution{Answers: map[string]string{q.Question: strings.Join(labels, ", ")}})
	fwd := r.Clone(r.Context())
	fwd.URL.Path, fwd.URL.RawPath = "/api/v1/interactions/"+id+"/resolve", ""
	fwd.Body, fwd.ContentLength = io.NopCloser(bytes.NewReader(body)), int64(len(body))
	got := &clientOpWriter{header: make(http.Header)}
	// A dropped connection after sending is uncertain, not safe to retry.
	h.forwardSend(routeLanguageWriter{ResponseWriter: got, language: h.requestLang(r)}, fwd, manager)
	got.WriteHeader(http.StatusOK)
	var result struct {
		Already bool `json:"already"`
	}
	_ = json.Unmarshal(got.body.Bytes(), &result)
	if got.status == http.StatusGone || got.status == http.StatusOK && result.Already {
		refuse(http.StatusConflict, codeNotPending)
		return
	}
	maps.Copy(w.Header(), got.header)
	w.WriteHeader(got.status)
	_, _ = w.Write(got.body.Bytes())
}
