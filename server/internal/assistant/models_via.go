package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/nwverse/persona/server/internal/ai"
	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/devices"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// An API connection can send its requests through a computer (docs/assistant/model-config.md "Sending through a
// computer"): for a manager whose machine cannot reach the provider while one of the user's computers can. The
// manager still builds every request and reads every answer (internal/ai: the gate, the stream parsing, the errors);
// only the network hop moves to that computer's node (model.http). The key goes in that one call's headers.

// aiClient is the client for connection c: through c.ViaMachine's node when it names one, else this machine's network.
func (s *Service) aiClient(c ai.Config) *ai.Client {
	if c.ViaMachine == "" {
		return ai.New(c)
	}
	return ai.NewWith(c, viaNode{s: s, machine: c.ViaMachine})
}

// viaNode sends a request through a computer's node: one model.http call, its answer read to the end there.
type viaNode struct {
	s       *Service
	machine string
}

func (v viaNode) RoundTrip(r *http.Request) (*http.Response, error) {
	var body []byte
	if r.Body != nil {
		var err error
		body, err = io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			return nil, err
		}
	}
	ctx := r.Context()
	p := proto.ModelHTTPParams{Method: r.Method, URL: r.URL.String(), Header: r.Header, Body: body}
	if d, ok := ctx.Deadline(); ok {
		p.TimeoutMS = max(time.Until(d).Milliseconds(), 1)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var out json.RawMessage
	if v.s.devs == nil {
		err = devices.ErrNoMachine
	} else {
		out, err = v.s.devs.CallNode(ctx, v.machine, proto.MethodModelHTTP, raw)
	}
	clear(raw) // it holds the key
	if err != nil {
		return nil, v.err(ctx, err)
	}
	var a proto.ModelHTTPAnswer
	if err := json.Unmarshal(out, &a); err != nil {
		return nil, fmt.Errorf("send through a computer: bad answer: %w", err)
	}
	return &http.Response{Status: fmt.Sprintf("%d %s", a.Status, http.StatusText(a.Status)), StatusCode: a.Status,
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Header: http.Header(a.Header), Body: io.NopCloser(bytes.NewReader(a.Body)),
		ContentLength: int64(len(a.Body)), Request: r}, nil
}

// err says an offline computer as such, naming it: a call that can't go out, like a launcher purpose's on an offline
// computer; one this manager no longer has, as at a save. The node's own errors (an old node, the provider out of
// reach from there) go as they are.
func (v viaNode) err(ctx context.Context, err error) error {
	if errors.Is(err, devices.ErrNoMachine) {
		return errViaMachine(v.machine)
	}
	if !errors.Is(err, apierr.ErrOffline) && !errors.Is(err, rpc.ErrClosed) {
		return err
	}
	name := v.machine
	if d, ok := v.s.device(ctx, v.machine); ok && d.Name != "" {
		name = d.Name
	}
	return errors.New(i18n.T(v.s.lang(ctx), "assistant-models.err_via_offline", i18n.Args{"name": name}))
}
