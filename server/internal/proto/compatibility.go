package proto

import (
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"slices"
	"sync"
)

// Both keys carry the same value on all hub/node/manager boundaries. Old peers
// ignore the canonical key. Presence, not boolean truth, gives it precedence.
type sessionJSON Session

// sessionWire is a Session as sent: its fields, its hierarchy's (SessionHierarchy, keys of the row itself), then the
// legacy key. The session is embedded by pointer, so encoding one copies none of its ~1 KB of fields; a nil hierarchy
// writes nothing.
type sessionWire struct {
	*sessionJSON
	*SessionHierarchy
	LegacyStarted bool `json:"started_by_jarvis"`
}

// wireOf is s as sent (sessionWire).
func wireOf(s *sessionJSON) *sessionWire {
	return &sessionWire{sessionJSON: s, SessionHierarchy: s.Hierarchy, LegacyStarted: s.StartedByPersona}
}

// legacyStartedMember is sessionWire's legacy key as appendSession (session_wire.go) writes it: the literal stays in
// this compatibility file.
const legacyStartedMember = `"started_by_jarvis":`

func (s Session) MarshalJSON() ([]byte, error) {
	if b, ok := appendSession(make([]byte, 0, 1024), &s); ok {
		return b, nil
	}
	return json.Marshal(wireOf((*sessionJSON)(&s)))
}

// MarshalJSONTo writes MarshalJSON's bytes straight into the encoder that asked: the hub encodes every session of a
// list on each push, and MarshalJSON's bytes were then checked and copied again by that encoder. On a pointer, so the
// session is not copied either; encoding/json calls it for an addressable session (a pointer, a slice element) and
// MarshalJSON for the rest. Encoded with encoding/json's options, as MarshalJSON is, so an encoder that does not
// escape HTML still gets it escaped; the caller's indentation applies, as it did to MarshalJSON's output. Its bytes are
// appendSession's when the encoder writes them as they are (writesRawAsIs), as encoding/json's encoders do.
func (s *Session) MarshalJSONTo(enc *jsontext.Encoder) error {
	if writesRawAsIs(enc.Options()) {
		buf := sessionScratch.Get().(*[]byte)
		defer sessionScratch.Put(buf)
		if b, ok := appendSession((*buf)[:0], s); ok {
			if cap(b) <= 64<<10 { // a rare huge session's buffer is not kept
				*buf = b
			}
			return enc.WriteValue(b)
		}
	}
	return jsonv2.MarshalEncode(enc, wireOf((*sessionJSON)(s)), json.DefaultOptionsV1())
}

// sessionScratch holds the buffers MarshalJSONTo writes a session into before the encoder copies it. Not the encoder's
// AvailableBuffer: that is sized by the values the encoder wrote since it was reset, and encoding/json resets its
// pooled encoder for each value, so a session would often regrow it.
var sessionScratch = sync.Pool{New: func() any { return new([]byte) }}

// writesRawAsIs reports whether an encoder with these options writes appendSession's bytes as the reflect encoder
// would have written the session: it keeps escapes as written (another one would turn them back into characters), keeps
// keys in their order and numbers as they are, and has no marshalers of the caller's own (the reflect encoder would
// call them on the session's fields). Its whitespace applies to both alike. encoding/json's encoders are such.
func writesRawAsIs(o jsonv2.Options) bool {
	raw, _ := jsonv2.GetOption(o, jsontext.PreserveRawStrings)
	reorder, _ := jsonv2.GetOption(o, jsontext.ReorderRawObjects)
	ints, _ := jsonv2.GetOption(o, jsontext.CanonicalizeRawInts)
	floats, _ := jsonv2.GetOption(o, jsontext.CanonicalizeRawFloats)
	marshalers, _ := jsonv2.GetOption(o, jsonv2.WithMarshalers)
	return raw && !reorder && !ints && !floats && marshalers == nil
}

// sessionWireIn is a Session as received: its fields and its hierarchy's, with both started keys kept apart (the
// session is embedded by pointer, so it is decoded in place; the hierarchy is decoded into a copy of its own, made
// only when a key of it comes or the session had one). The canonical key, when present (null included), beats the
// legacy one.
type sessionWireIn struct {
	*sessionJSON
	*SessionHierarchy
	LegacyStarted  startedKey `json:"started_by_jarvis"`
	CurrentStarted startedKey `json:"started_by_persona"`
}

// startedKey is one started key as received: kind is 0 when it was absent, else its value's first byte ('t', 'f',
// 'n'). Any other value is kept as it came, so it fails, as a bool would, only if it is the key used.
type startedKey struct {
	kind  jsontext.Kind
	other []byte
}

func (k *startedKey) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	val, err := dec.ReadValue()
	if err != nil {
		return err
	}
	*k = startedKey{kind: val.Kind()} // the last of a repeated key counts
	if k.kind != 't' && k.kind != 'f' && k.kind != 'n' {
		k.other = slices.Clone(val)
	}
	return nil
}

// UnmarshalJSONFrom decodes a session straight from the caller's decoder, into the session itself: the hub and the
// manager decode every session of every list a node or a manager sends. UnmarshalJSON, which encoding/json called
// before, got each session's bytes read out, then decoded them again into a copy of the session. Decoded with
// encoding/json's options, as UnmarshalJSON was; a session that fails to decode is left as it was, as before (its
// error names the field's whole path now).
func (s *Session) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	was := *s
	in := sessionWireIn{sessionJSON: (*sessionJSON)(s)}
	if s.Hierarchy != nil {
		h := s.hierarchy() // decoded into a copy: the one s holds is shared with its other copies
		in.SessionHierarchy = &h
	}
	if err := jsonv2.UnmarshalDecode(dec, &in, json.DefaultOptionsV1()); err != nil {
		*s = was
		return err
	}
	if s.Hierarchy = in.SessionHierarchy; s.Hierarchy != nil && s.Hierarchy.empty() {
		s.Hierarchy = nil
	}
	key := in.LegacyStarted
	if in.CurrentStarted.kind != 0 {
		key = in.CurrentStarted
	}
	switch key.kind {
	case 0, 'n': // absent or null: left as it was
	case 't', 'f':
		s.StartedByPersona = key.kind == 't'
	default:
		if err := json.Unmarshal(key.other, &s.StartedByPersona); err != nil {
			*s = was
			return err
		}
	}
	return nil
}

func (s *Session) UnmarshalJSON(data []byte) error {
	return jsonv2.Unmarshal(data, s, json.DefaultOptionsV1())
}

// Only the managed-launcher enum is projected; focus-source IDs and arbitrary
// extension source strings do not pass through this adapter.
const legacyLauncherSource = "jarvis"

// Managed launcher source is a wire enum, independent of the MCP namespace.
func CanonicalLauncherSource(value string) string {
	if value == legacyLauncherSource {
		return "persona"
	}
	return value
}
func LegacyLauncherSource(value string) string {
	if value == "persona" {
		return legacyLauncherSource
	}
	return value
}

// LauncherEnvelope is an explicit transport projection. Launcher itself has no
// JSON methods: embedding it must not hide health, agent-type or other siblings.
type LauncherEnvelope Launcher

func (v LauncherEnvelope) MarshalJSON() ([]byte, error) {
	copy := Launcher(v)
	copy.Source = LegacyLauncherSource(copy.Source)
	return json.Marshal(copy)
}
func (v *LauncherEnvelope) UnmarshalJSON(raw []byte) error {
	copy := Launcher(*v)
	if err := json.Unmarshal(raw, &copy); err != nil {
		return err
	}
	copy.Source = CanonicalLauncherSource(copy.Source)
	*v = LauncherEnvelope(copy)
	return nil
}
