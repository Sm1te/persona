package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/assistantwire"
	"github.com/nwverse/persona/server/internal/proto"
)

const catalogPath = "/api/v1/launchers/catalog"

func catalogModels(t *testing.T, mux http.Handler, body string) []proto.ProviderModel {
	t.Helper()
	code, raw := launcherCall(t, mux, "POST", catalogPath, body)
	if code != http.StatusOK {
		t.Fatalf("%s: %d %s", body, code, raw)
	}
	var out struct {
		Models []proto.ProviderModel `json:"models"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	return out.Models
}

func catalogIDs(ids ...string) string {
	b, _ := json.Marshal(map[string]any{"ids": ids})
	return string(b)
}

var opus55 = proto.ProviderModel{Name: "Opus 5.5", ContextWindow: 1000000,
	Efforts: []string{"low", "medium", "high", "xhigh", "max"}, DefaultEffort: "medium",
	Price: &proto.ModelPrice{Input: 4, Output: 20, CacheRead: ptr(0.2)}}

// A typed id gets the catalog row discovery would give it, under the id exactly as typed (trimmed): any case, a
// vendor prefix, the [1m] marker or a date suffix.
func TestCatalogLookupForms(t *testing.T) {
	_, mux := launcherTestService(t)
	for _, id := range []string{
		"claude-opus-5-5",
		"Claude-Opus-5-5",
		"anthropic/claude-opus-5-5",
		"claude-opus-5-5[1m]",
		"claude-opus-5-5-20260101",
		"Anthropic/Claude-Opus-5-5[1m]",
	} {
		got := catalogModels(t, mux, catalogIDs("  "+id+"\t"))
		want := opus55
		want.ID = id
		if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
			t.Errorf("%s: %+v, want %+v", id, got, want)
		}
	}
}

// An unlisted newer generation takes the closest earlier one's window and thinking levels, not its name, and says
// which row it took them from, as discovery does.
func TestCatalogLookupInheritsAGeneration(t *testing.T) {
	_, mux := launcherTestService(t)
	got := catalogModels(t, mux, catalogIDs("glm-5.4"))
	want := proto.ProviderModel{ID: "glm-5.4", ContextWindow: 1000000, Efforts: []string{"low", "high", "max"},
		DefaultEffort: "high", InheritedFrom: "glm-5.3"}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("%+v, want %+v", got, want)
	}
	if !reflect.DeepEqual(got[0], enrichModel(proto.ProviderModel{ID: "glm-5.4"})) {
		t.Fatalf("not discovery's lookup: %+v", got[0])
	}
	_, raw := launcherCall(t, mux, "POST", catalogPath, catalogIDs("glm-5.4"))
	if !strings.Contains(raw, `"inherited_from":"glm-5.3"`) || strings.Contains(raw, `"name"`) {
		t.Fatalf("wire shape: %s", raw)
	}
}

// Unknown ids are left out; the rest keep the request's order, each once; empty ids are skipped.
func TestCatalogLookupOrderAndDedupe(t *testing.T) {
	_, mux := launcherTestService(t)
	got := catalogModels(t, mux, catalogIDs("my-own-model", "glm-5.4", " claude-opus-5-5 ", "", "  ", "claude-opus-5-5", "nope", "glm-5.4"))
	var ids []string
	for _, m := range got {
		ids = append(ids, m.ID)
	}
	if !reflect.DeepEqual(ids, []string{"glm-5.4", "claude-opus-5-5"}) {
		t.Fatalf("ids %v", ids)
	}
	if got := catalogModels(t, mux, catalogIDs("my-own-model", "nope")); len(got) != 0 {
		t.Fatalf("unknown ids answered: %+v", got)
	}
}

// Nothing to look up, or nothing known, is an empty list, never null.
func TestCatalogLookupEmptyIsAList(t *testing.T) {
	_, mux := launcherTestService(t)
	for _, body := range []string{`{"ids":[]}`, `{}`, `{"ids":null}`, `{"ids":["","  "]}`, `{"ids":["my-own-model"]}`} {
		code, raw := launcherCall(t, mux, "POST", catalogPath, body)
		if code != http.StatusOK || strings.TrimSpace(raw) != `{"models":[]}` {
			t.Errorf("%s: %d %s", body, code, raw)
		}
	}
}

// More than 100 ids, an id over 200 bytes after trimming, malformed JSON or ids that are not strings: 400 with a
// message in the request's language.
func TestCatalogLookupRejects(t *testing.T) {
	s, mux := launcherTestService(t)
	many := make([]string, catalogLookupMaxIDs)
	for i := range many {
		many[i] = "claude-opus-5-5"
	}
	if got := catalogModels(t, mux, catalogIDs(many...)); len(got) != 1 {
		t.Fatalf("%d ids: %+v", len(many), got)
	}
	long := strings.Repeat("a", catalogLookupMaxIDLen)
	if got := catalogModels(t, mux, catalogIDs("  "+long+"  ")); len(got) != 0 {
		t.Fatalf("a %d-byte id: %+v", len(long), got)
	}
	for name, body := range map[string]string{
		"too many":   catalogIDs(append(many, "glm-5.4")...),
		"too long":   catalogIDs("glm-5.4", long+"b"),
		"bad json":   `{"ids":[`,
		"not a list": `{"ids":"glm-5.4"}`,
		"not text":   `{"ids":[1]}`,
		"top list":   `["glm-5.4"]`,
	} {
		if code, raw := launcherCall(t, mux, "POST", catalogPath, body); code != http.StatusBadRequest || !strings.Contains(raw, `"error"`) {
			t.Errorf("%s: %d %s", name, code, raw)
		}
	}
	for _, c := range []struct{ lang, body, want string }{
		{"en", catalogIDs(append(many, "x")...), "Look up at most 100 model IDs at a time"},
		{"en", catalogIDs(long + "b"), "A model ID is at most 200 bytes"},
		{"en", `{"ids":"x"}`, "Malformed request: ids must be a list of model IDs"},
		{"ja", catalogIDs(long + "b"), "モデル ID は 200 バイトまでです"},
		{"", catalogIDs(append(many, "x")...), "一次最多查 100 个模型 ID"},
	} {
		h := map[string][]string{}
		if c.lang != "" {
			h["Accept-Language"] = []string{c.lang}
		}
		res, err := serveHTTP(context.Background(), s, assistantwire.HTTPRequest{Method: "POST", Path: catalogPath, Header: h, Body: []byte(c.body)}, s.lang)
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(res.Body, &out)
		if res.Status != http.StatusBadRequest || out.Error != c.want {
			t.Errorf("%q: %d %s, want %q", c.lang, res.Status, res.Body, c.want)
		}
	}
}
