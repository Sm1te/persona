package proto

// model.http: a model request an API connection sends through a computer instead of the manager's own network
// (docs/assistant/model-config.md "Sending through a computer", docs/protocol/protocol.md "hub ↔ node RPC").

// MethodModelHTTP is the node method.
const MethodModelHTTP = "model.http"

// ModelHTTPMaxBody bounds the answer's body the node reads (a whole event stream of one reply).
const ModelHTTPMaxBody = 16 << 20

// ModelHTTPParams are model.http's params: one request. Header carries the key for this call only; the node neither
// keeps nor logs it.
type ModelHTTPParams struct {
	Method    string              `json:"method,omitempty"` // GET or POST ("": POST)
	URL       string              `json:"url"`
	Header    map[string][]string `json:"header,omitempty"`
	Body      []byte              `json:"body,omitempty"`       // base64 in JSON
	TimeoutMS int64               `json:"timeout_ms,omitempty"` // the whole request; the node caps it
}

// ModelHTTPAnswer is the upstream's answer, its body read to the end (a streamed reply's events, all of them).
type ModelHTTPAnswer struct {
	Status int                 `json:"status"`
	Header map[string][]string `json:"header,omitempty"`
	Body   []byte              `json:"body,omitempty"`
}
