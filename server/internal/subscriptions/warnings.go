package subscriptions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/nwverse/persona/sdk/go/wire"
)

// A dry run's warnings (DryRun.Warnings): what a recipe says that passes every check but will not do what it seems
// to. Decoding a recipe drops a key the format does not know, so "author" or "url" in fields used to pass with that
// column silently empty; and a field whose path finds nothing in any item pulled is empty for every item.

// RecipeKeys are the keys a recipe may have, and FieldKeys those of its fields, as the recipe format names them (the
// generated wire types, so they follow the schema).
var (
	RecipeKeys = jsonKeys(reflect.TypeFor[wire.ConnectorSubscriptionRecipe]())
	FieldKeys  = jsonKeys(reflect.TypeFor[wire.ConnectorSubscriptionFields]())
)

func jsonKeys(t reflect.Type) []string {
	var out []string
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out = append(out, name)
		}
	}
	return out
}

// UnknownKeys are the keys of a recipe's JSON the format does not know: at its top level by name, those of its fields
// as "fields.<name>", sorted. Not an object (or no fields object): only what can be read.
func UnknownKeys(raw json.RawMessage) []string {
	var top map[string]json.RawMessage
	if len(bytes.TrimSpace(raw)) == 0 || json.Unmarshal(raw, &top) != nil {
		return nil
	}
	var out []string
	for k := range top {
		if !slices.Contains(RecipeKeys, k) {
			out = append(out, k)
		}
	}
	var fields map[string]json.RawMessage
	if f, ok := top["fields"]; ok && json.Unmarshal(f, &fields) == nil {
		for k := range fields {
			if !slices.Contains(FieldKeys, k) {
				out = append(out, "fields."+k)
			}
		}
	}
	sort.Strings(out)
	return out
}

// UnmarshalJSON reads a dry run's request and notes the keys of its recipe that decoding drops (Unknown).
func (r *DryRunRequest) UnmarshalJSON(b []byte) error {
	type plain DryRunRequest
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	var raw struct {
		Recipe json.RawMessage `json:"recipe"`
	}
	_ = json.Unmarshal(b, &raw)
	*r = DryRunRequest(p)
	r.Unknown = UnknownKeys(raw.Recipe)
	return nil
}

// unknownWarnings are the warnings for keys the format does not know.
func unknownWarnings(keys []string) []Warning {
	out := []Warning{}
	for _, k := range keys {
		msg := fmt.Sprintf("The recipe format has no key %q, so it does nothing.", k)
		if name, ok := strings.CutPrefix(k, "fields."); ok {
			msg = fmt.Sprintf("fields has no key %q, so nothing is read into it; the keys are %s.", name, strings.Join(FieldKeys, ", "))
		}
		out = append(out, Warning{Code: WarnUnknownKey, Key: k, Message: msg})
	}
	return out
}

// itemProblems are the mapped fields one item has no readable value for, in the recipe format's order.
func itemProblems(m mapped, f *wire.ConnectorSubscriptionFields) []FieldProblem {
	out := []FieldProblem{}
	if f == nil {
		return out
	}
	for _, c := range []struct {
		key, path string
		has, bad  bool
	}{
		{"id", f.ID, m.idFound, false},
		{"thread", f.Thread, m.item.Thread != "", false},
		{"title", f.Title, m.hasTitle, false},
		{"sender", f.Sender, m.item.Sender != "", false},
		{"time", f.Time, m.rawTime != "", m.timeBad},
		{"body", f.Body, m.item.Body != "", false},
		{"link", f.Link, m.item.Link != "", false},
		{"due_date", f.DueDate, m.rawDue != "", m.dueBad},
	} {
		switch {
		case c.path == "":
		case c.bad:
			out = append(out, FieldProblem{Key: "fields." + c.key, Code: FieldBadTime})
		case !c.has:
			out = append(out, FieldProblem{Key: "fields." + c.key, Code: FieldEmpty})
		}
	}
	return out
}

// emptyFieldWarnings are the fields the recipe maps whose path finds a value in none of the items pulled. The title
// is left to the fields check (no_title), which fails the dry run.
func emptyFieldWarnings(f *wire.ConnectorSubscriptionFields, items []mapped) []Warning {
	out := []Warning{}
	if f == nil || len(items) == 0 {
		return out
	}
	for _, c := range []struct {
		key, path string
		has       func(m mapped) bool
	}{
		{"id", f.ID, func(m mapped) bool { return m.idFound }},
		{"thread", f.Thread, func(m mapped) bool { return m.item.Thread != "" }},
		{"sender", f.Sender, func(m mapped) bool { return m.item.Sender != "" }},
		{"time", f.Time, func(m mapped) bool { return m.rawTime != "" }},
		{"body", f.Body, func(m mapped) bool { return m.item.Body != "" }},
		{"link", f.Link, func(m mapped) bool { return m.item.Link != "" }},
		{"due_date", f.DueDate, func(m mapped) bool { return m.rawDue != "" }},
	} {
		if c.path == "" || slices.ContainsFunc(items, c.has) {
			continue
		}
		out = append(out, Warning{Code: WarnEmptyField, Key: "fields." + c.key, Path: c.path,
			Message: fmt.Sprintf("%s finds nothing in any of the %d items, so %s is empty for each.", c.path, len(items), c.key)})
	}
	return out
}
