package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// modelHTTPTimeout is model.http's bound when the manager names none, and modelHTTPMax the most it may name, should
// the manager's rpc.cancel never come.
const (
	modelHTTPTimeout = 60 * time.Second
	modelHTTPMax     = 10 * time.Minute
)

// modelHTTPClient sends model.http: this computer's network, its proxy settings (HTTPS_PROXY…) included. A redirect is
// answered as is, never followed here: Go keeps x-api-key and custom headers on one to another host, and the manager's
// own client decides about it as it would without the node.
var modelHTTPClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// modelHTTP answers model.http: one request (a reply's POST, or a model list's GET) to a model provider for an API connection that sends through this computer
// (docs/assistant/model-config.md "Sending through a computer"). The key is in the headers for this call only: it is
// never logged, kept or put in an error (an error names the URL at most, which holds no credential: the manager's
// gateway check refuses user info and queries). params are zeroed once read.
func modelHTTP(ctx context.Context, params json.RawMessage) (any, error) {
	var p proto.ModelHTTPParams
	err := json.Unmarshal(params, &p)
	clear(params)
	if err != nil {
		return nil, errors.New("model.http: bad params") // the decoder's error is not repeated: it may quote them
	}
	u, err := url.Parse(p.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, errors.New("model.http: need an http(s) URL")
	}
	method := p.Method
	if method == "" {
		method = http.MethodPost
	}
	if method != http.MethodPost && method != http.MethodGet {
		return nil, errors.New("model.http: only GET or POST")
	}
	timeout := modelHTTPTimeout
	if p.TimeoutMS > 0 {
		timeout = min(time.Duration(p.TimeoutMS)*time.Millisecond, modelHTTPMax)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(p.Body))
	if err != nil {
		return nil, errors.New("model.http: bad request")
	}
	req.Header = http.Header(p.Header)
	resp, err := modelHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("model.http: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, proto.ModelHTTPMaxBody+1))
	if err != nil {
		return nil, fmt.Errorf("model.http: read answer: %w", err)
	}
	if len(body) > proto.ModelHTTPMaxBody {
		return nil, fmt.Errorf("model.http: the answer is over %d MB", proto.ModelHTTPMaxBody>>20)
	}
	return proto.ModelHTTPAnswer{Status: resp.StatusCode, Header: resp.Header, Body: body}, nil
}
