package proto

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// reflectSession is a Session encoded the reflect way, as it was before appendSession: the reference.
type reflectSession Session

func (s reflectSession) MarshalJSON() ([]byte, error) {
	return json.Marshal(wireOf((*sessionJSON)(&s)))
}

func (s *reflectSession) MarshalJSONTo(enc *jsontext.Encoder) error {
	return jsonv2.MarshalEncode(enc, wireOf((*sessionJSON)(s)), json.DefaultOptionsV1())
}

// wireStrings are the strings every string field is tried with: escapes, HTML, unicode, line separators, bad UTF-8.
var wireStrings = []string{"plain", `say "hi" \ back/slash`, "<a href='x'>&amp;</a>", "\x00\x01\b\f\n\r\t\x1f\x7f end",
	"é 你好 😀 \u2028\u2029 \ufffd", "bad \xff \xed\xa0\x80 \xc3 \xe4\xbd", strings.Repeat("x", 600)}

// wireTimes are the times every time field is tried with; from the year 10000 on, ones the reflect encoder refuses.
var wireTimes = []time.Time{{}, time.Date(2026, 11, 9, 1, 2, 3, 0, time.UTC), time.Date(2026, 11, 9, 1, 2, 3, 4500, time.UTC),
	time.Date(2026, 1, 2, 3, 4, 5, 600, time.FixedZone("", 5*3600+30*60+7)), time.Unix(1700000000, 1).In(time.FixedZone("PST", -8*3600)),
	time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("", 24*3600-1)), time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC),
	time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC),
	time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("", 24*3600)), time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("", -25*3600))}

var timeType = reflect.TypeFor[time.Time]()

// wireSamples are the values a field of type t is tried with, besides its zero value.
func wireSamples(t *testing.T, ty reflect.Type) []reflect.Value {
	var out []reflect.Value
	add := func(v any) { out = append(out, reflect.ValueOf(v).Convert(ty)) }
	switch {
	case ty == reflect.TypeFor[TitleSource]():
		for _, source := range []TitleSource{TitleSourceUnknown, TitleSourceManual, TitleSourceAuto, TitleSourcePrompt, TitleSourceRuntime, 255} {
			add(source)
		}
	case ty == timeType:
		for _, tm := range wireTimes {
			add(tm)
		}
		return out
	case ty.Kind() == reflect.String:
		for _, s := range append([]string{""}, wireStrings...) {
			add(s)
		}
	case ty.Kind() == reflect.Bool:
		add(true)
	case ty.Kind() == reflect.Int || ty.Kind() == reflect.Int64:
		for _, n := range []int64{-7, 1 << 40, math.MaxInt64, math.MinInt64} {
			add(n)
		}
	case ty.Kind() == reflect.Int32:
		for _, n := range []int32{-7, 1 << 20, math.MaxInt32, math.MinInt32} {
			add(n)
		}
	case ty.Kind() == reflect.Uint16:
		for _, n := range []uint16{1, 7, math.MaxUint16} {
			add(n)
		}
	case ty.Kind() == reflect.Pointer:
		for _, v := range append([]reflect.Value{reflect.Zero(ty.Elem())}, wireSamples(t, ty.Elem())...) {
			p := reflect.New(ty.Elem())
			p.Elem().Set(v)
			out = append(out, p)
		}
	case ty.Kind() == reflect.Slice:
		out = append(out, reflect.MakeSlice(ty, 0, 0))
		for _, v := range wireSamples(t, ty.Elem()) {
			out = append(out, reflect.Append(reflect.MakeSlice(ty, 0, 2), reflect.Zero(ty.Elem()), v))
		}
	case ty.Kind() == reflect.Map:
		out = append(out, reflect.MakeMap(ty))
		m := reflect.MakeMap(ty)
		for i, k := range []string{"b", "a", "é", "<k>", "", "A"} {
			m.SetMapIndex(reflect.ValueOf(k), reflect.ValueOf(wireStrings[i%len(wireStrings)]))
		}
		out = append(out, m)
	case ty.Kind() == reflect.Struct:
		out = append(out, wireFilled(t, ty))
	default:
		t.Fatalf("no samples for %s: add them here", ty)
	}
	return out
}

// wireFilled is a struct of type ty with every field set to a sample (the second, the first being an empty one where
// there is one).
func wireFilled(t *testing.T, ty reflect.Type) reflect.Value {
	v := reflect.New(ty).Elem()
	for i := range ty.NumField() {
		if ty.Field(i).IsExported() {
			s := wireSamples(t, ty.Field(i).Type)
			v.Field(i).Set(s[min(1, len(s)-1)])
		}
	}
	return v
}

// wireEncodings are the ways sessions are encoded: on their own (MarshalJSONTo), in a list (the legacy list), one at a
// time by an Encoder (the hub's push rows), in an interface (MarshalJSON); with encoding/json and json/v2, indented and
// not, escaping HTML and not.
var wireEncodings = []struct {
	name string
	enc  func(any) ([]byte, error)
}{
	{"json.Marshal", json.Marshal},
	{"json.MarshalIndent", func(v any) ([]byte, error) { return json.MarshalIndent(v, ">", "\t") }},
	{"Encoder", func(v any) ([]byte, error) {
		var b bytes.Buffer
		err := json.NewEncoder(&b).Encode(v)
		return b.Bytes(), err
	}},
	{"Encoder without HTML escaping, indented", func(v any) ([]byte, error) {
		var b bytes.Buffer
		e := json.NewEncoder(&b)
		e.SetEscapeHTML(false)
		e.SetIndent("", "  ")
		err := e.Encode(v)
		return b.Bytes(), err
	}},
	{"json/v2", func(v any) ([]byte, error) { return jsonv2.Marshal(v) }},
	{"json/v2 multiline", func(v any) ([]byte, error) { return jsonv2.Marshal(v, jsontext.Multiline(true)) }},
	{"json/v2 keeping escapes", func(v any) ([]byte, error) { return jsonv2.Marshal(v, jsontext.PreserveRawStrings(true)) }},
	{"json/v2 keeping escapes, not escaping HTML, multiline", func(v any) ([]byte, error) {
		return jsonv2.Marshal(v, jsontext.PreserveRawStrings(true), jsontext.EscapeForHTML(false), jsontext.Multiline(true))
	}},
	{"json/v2 with encoding/json's options", func(v any) ([]byte, error) { return jsonv2.Marshal(v, json.DefaultOptionsV1()) }},
	{"json/v2 keeping escapes, spaced", func(v any) ([]byte, error) {
		return jsonv2.Marshal(v, jsontext.PreserveRawStrings(true), jsontext.SpaceAfterColon(true), jsontext.SpaceAfterComma(true))
	}},
	{"json/v2 keeping escapes, canonical numbers", func(v any) ([]byte, error) {
		return jsonv2.Marshal(v, jsontext.PreserveRawStrings(true), jsontext.CanonicalizeRawInts(true), jsontext.CanonicalizeRawFloats(true))
	}},
	{"json/v2 keeping escapes, reordering objects", func(v any) ([]byte, error) {
		return jsonv2.Marshal(v, jsontext.PreserveRawStrings(true), jsontext.ReorderRawObjects(true))
	}},
	{"json/v2 keeping escapes, with a marshaler", func(v any) ([]byte, error) {
		quoted := jsonv2.MarshalFunc(func(s string) ([]byte, error) { return []byte(`"~"`), nil })
		return jsonv2.Marshal(v, jsontext.PreserveRawStrings(true), jsonv2.WithMarshalers(quoted))
	}},
}

// checkWire encodes s and the same session the reflect way in each encoding and shape, and fails on any byte that
// differs, or an error one has and the other has not.
func checkWire(t *testing.T, what string, s Session) {
	t.Helper()
	r := reflectSession(s)
	want, wantErr := json.Marshal(&r)
	if got, ok := appendSession(nil, &s); ok != (wantErr == nil) || ok && !bytes.Equal(got, want) {
		t.Fatalf("%s, appendSession (each field of Session needs its line in session_wire.go):\n got %s (%v)\nwant %s (%v)",
			what, got, ok, want, wantErr)
	}
	shapes := []struct {
		name      string
		got, want any
	}{
		{"pointer", &s, &r},
		{"value", s, r},
		{"list", []Session{s, s}, []reflectSession{r, r}},
		{"in a map", map[string]any{"s": s}, map[string]any{"s": r}},
	}
	for _, e := range wireEncodings {
		for _, sh := range shapes {
			got, gotErr := e.enc(sh.got)
			want, wantErr := e.enc(sh.want)
			if (gotErr != nil) != (wantErr != nil) || !bytes.Equal(got, want) {
				t.Fatalf("%s, %s, %s:\n got %s (%v)\nwant %s (%v)", what, e.name, sh.name, got, gotErr, want, wantErr)
			}
		}
	}
}

// The hand-written encoder writes the reflect encoder's bytes: for every field of Session on its own, with each kind
// of value it can hold (empty, nil, escapes, unicode, bad UTF-8, times in other zones and out of range), for all of
// them at once, and for a busy machine's list. A field added to Session without its line in appendSession fails here.
func TestSessionWireMatchesReflect(t *testing.T) {
	if got := sessionLayout(); got != sessionWireLayout {
		t.Fatalf("Session's fields changed: give each new one its line in appendSession, then set sessionWireLayout to %#x", got)
	}
	if got := hierarchyLayout(); got != hierarchyWireLayout {
		t.Fatalf("SessionHierarchy's or SessionChildren's fields changed: give each new one its line in appendSession, "+
			"then set hierarchyWireLayout to %#x", got)
	}
	checkWire(t, "an empty session", Session{})
	ty := reflect.TypeFor[Session]()
	for i := range ty.NumField() {
		f := ty.Field(i)
		for n, v := range wireSamples(t, f.Type) {
			var s Session
			reflect.ValueOf(&s).Elem().Field(i).Set(v)
			checkWire(t, f.Name+" sample "+strconv.Itoa(n), s)
		}
	}
	// The nested values written by hand: every field of each, with each kind of value, in a list of them.
	for _, c := range []struct {
		name   string
		ty     reflect.Type
		layout uint64
		set    func(*Session, reflect.Value)
	}{
		{"backgroundWireLayout", reflect.TypeFor[BackgroundTask](), backgroundWireLayout, func(s *Session, v reflect.Value) {
			s.Background = []BackgroundTask{{}, v.Interface().(BackgroundTask)}
		}},
		{"subagentWireLayout", reflect.TypeFor[Subagent](), subagentWireLayout, func(s *Session, v reflect.Value) {
			s.Subagents = []Subagent{v.Interface().(Subagent), {}}
		}},
		{"workflowToolWireLayout", reflect.TypeFor[WorkflowTool](), workflowToolWireLayout, func(s *Session, v reflect.Value) {
			tool := v.Interface().(WorkflowTool)
			s.Subagents = []Subagent{{LastTool: &tool}}
		}},
	} {
		if got := typeLayout(c.ty); got != c.layout {
			t.Fatalf("%s's fields changed: give each new one its line in session_wire.go, then set %s to %#x", c.ty, c.name, got)
		}
		for i := range c.ty.NumField() {
			for n, v := range append([]reflect.Value{reflect.Zero(c.ty.Field(i).Type)}, wireSamples(t, c.ty.Field(i).Type)...) {
				x := reflect.New(c.ty).Elem()
				x.Field(i).Set(v)
				var s Session
				c.set(&s, x)
				checkWire(t, c.ty.Name()+"."+c.ty.Field(i).Name+" sample "+strconv.Itoa(n), s)
			}
		}
	}
	all := wireFilled(t, ty).Interface().(Session)
	all.UpdatedAt = wireTimes[3]
	checkWire(t, "every field set", all)
	var p struct {
		Sessions []Session `json:"sessions"`
	}
	if err := json.Unmarshal(sessionListJSON(t), &p); err != nil {
		t.Fatal(err)
	}
	for _, s := range p.Sessions {
		checkWire(t, "the listed "+s.SessionID, s)
	}
}

// A subagent's model and tokens (#5030) go out after its latest tool, each only when set, as the reflect encoder writes
// them (TestSessionWireMatchesReflect tries every value).
func TestSubagentWireModelAndTokens(t *testing.T) {
	s := Session{SessionID: "s1", Subagents: []Subagent{
		{ID: "a1", SessionID: "s1@a1", Steps: 12, LastTool: &WorkflowTool{Name: "Read", Summary: "Reading a.go"},
			Model: "claude-haiku-4-5", Tokens: 23456},
		{ID: "a2", SessionID: "s1@a2"},
	}}
	checkWire(t, "a subagent with its model and tokens", s)
	b, ok := appendSession(nil, &s)
	if !ok {
		t.Fatal("not written by hand")
	}
	for _, want := range []string{
		`"last_tool":{"name":"Read","summary":"Reading a.go"},"model":"claude-haiku-4-5","tokens":23456}`,
		`{"id":"a2","session_id":"s1@a2","running":false,"started_at":"0001-01-01T00:00:00Z","updated_at":"0001-01-01T00:00:00Z"}]`,
	} {
		if !bytes.Contains(b, []byte(want)) {
			t.Fatalf("%s\nhas no %s", b, want)
		}
	}
}

// appendWireString quotes every rune, and every byte that is not UTF-8, as encoding/json does.
func TestWireStringMatchesEncodingJSON(t *testing.T) {
	check := func(s string) {
		want, _ := json.Marshal(s)
		if got := appendWireString(nil, s); !bytes.Equal(got, want) {
			t.Fatalf("%q: %s, want %s", s, got, want)
		}
	}
	for r := rune(0); r <= utf8.MaxRune; r++ {
		check("a" + string(r) + "b")
	}
	for c := 0x80; c <= 0xff; c++ {
		check("a" + string([]byte{byte(c)}) + "b")
		check(string([]byte{0xe4, byte(c)}))
		check(string([]byte{0xf0, 0x9f, byte(c)}))
		check(string([]byte{0xed, byte(c), 0x80}))
	}
	for _, s := range wireStrings {
		check(s)
	}
}
