package assistant

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/proto"
)

// decodeBody decodes the request's JSON body into v, reading at most limit bytes: a larger body stops the read with an
// *http.MaxBytesError (as the hub's decodeJSONBody).
func decodeBody(w http.ResponseWriter, r *http.Request, v any, limit int64) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(v)
}

// decodeLargeBody is decodeBody for the routes that take bodies of megabytes (a new conversation's attachments, a
// recording's transcript), with the same results and errors. A relayed body (serveHTTP) is in memory already: it is
// decoded where it is, as encoding/json's Decoder decodes it (jsontext reads the first value, json/v2 unmarshals it
// with encoding/json's options), where the Decoder reads it again through buffers it grows (89.5 MB for a 22.4 MB
// upload; it hides a *bytes.Buffer from jsontext, so that does not help). Any error, a body over limit, one that was
// read already, or v not pointing to a zero value: decodeBody, over the same bytes, says what it says.
func decodeLargeBody(w http.ResponseWriter, r *http.Request, v any, limit int64) error {
	if b, ok := r.Body.(*relayedBody); ok && b.r.Len() == len(b.b) && int64(len(b.b)) <= limit && zeroAt(v) {
		if err := decodeInPlace(b.b, v); err == nil {
			_, _ = b.r.Seek(0, io.SeekEnd) // read, as by the Decoder
			return nil
		}
		reflect.ValueOf(v).Elem().SetZero() // as before the try
	}
	return decodeBody(w, r, v, limit)
}

// decodeInPlace is encoding/json's Decoder.Decode of b's first value into v, reading b where it is.
func decodeInPlace(b []byte, v any) error {
	value, err := jsontext.NewDecoder(bytes.NewBuffer(b), json.DefaultOptionsV1()).ReadValue()
	if err != nil {
		return err
	}
	return jsonv2.Unmarshal(value, v, json.DefaultOptionsV1())
}

// zeroAt: v is a non-nil pointer to a zero value (what a failed try puts back).
func zeroAt(v any) bool {
	p := reflect.ValueOf(v)
	return p.Kind() == reflect.Pointer && !p.IsNil() && p.Elem().IsZero()
}

// relayedBody is the body of a request the hub relayed (serveHTTP), read as io.NopCloser(bytes.NewReader(b)) reads
// it; decodeLargeBody finds its bytes in b.
type relayedBody struct {
	r *bytes.Reader
	b []byte
}

func newRelayedBody(b []byte) *relayedBody { return &relayedBody{r: bytes.NewReader(b), b: b} }

func (b *relayedBody) Read(p []byte) (int, error)         { return b.r.Read(p) }
func (b *relayedBody) WriteTo(w io.Writer) (int64, error) { return b.r.WriteTo(w) }
func (b *relayedBody) Close() error                       { return nil }

// writeBodyErr answers a body decodeBody could not use: 413 (proto.CodeTooLarge) when err is over the limit, else 400
// with bad (the caller's own message, or err itself).
func writeBodyErr(w http.ResponseWriter, err, bad error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		apierr.WriteCode(w, http.StatusRequestEntityTooLarge, proto.CodeTooLarge, fmt.Errorf("request body is larger than %d bytes", tooLarge.Limit))
		return
	}
	apierr.Write(w, http.StatusBadRequest, bad)
}
