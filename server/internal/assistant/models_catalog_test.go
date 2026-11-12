package assistant

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestClosestEarlierGeneration(t *testing.T) {
	cases := []struct {
		id         string
		candidates []string
		want       string
	}{
		{"qwen4-30b", []string{"qwen3-235b", "qwen3-30b"}, "qwen3-30b"},
		{"qwen3.9-27b", []string{"qwen3.8-max", "qwen3.8-27b", "qwen3.7-27b", "qwen3.9-235b"}, "qwen3.8-27b"},
		{"qwen3.7-27b", []string{"qwen3.8-27b"}, ""},
		{"qwen3.9-235b", []string{"qwen3.8-27b", "qwen3-235b"}, "qwen3-235b"},
		{"qwen3.10-27b", []string{"qwen3.9-27b", "qwen3.8-27b"}, "qwen3.9-27b"},
		{"glm-5.10", []string{"glm-5.9", "glm-5.3", "glm-5.11"}, "glm-5.9"},
		{"grok-4.10", []string{"grok-4.9", "grok-4.11"}, "grok-4.9"},
		{"claude-sonnet-6", []string{"claude-sonnet-4-6", "claude-sonnet-5", "claude-opus-5"}, "claude-sonnet-5"},
		{"claude-opus-5-4", []string{"claude-opus-5-5", "claude-opus-5", "claude-opus-4-8"}, "claude-opus-5"},
		{"claude-opus-5-6", []string{"claude-opus-5-5", "claude-opus-5"}, "claude-opus-5-5"},
		{"gpt-5o-mini", []string{"gpt-4o", "gpt-4o-mini", "gpt-4.1"}, "gpt-4o-mini"},
		{"gpt-5o", []string{"gpt-4o-mini", "gpt-4o"}, "gpt-4o"},
		{"qwen3.9-max-preview", []string{"qwen3.8-max", "qwen3.8-max-preview"}, "qwen3.8-max-preview"},
		{"private-70b", []string{"private-7b", "private-8b"}, ""},
		{"gpt-oss-120b", []string{"gpt-oss-20b"}, ""},
		{"mixtral-8x22b", []string{"mixtral-8x7b"}, ""},
		{"private-1.5b", []string{"private-0.5b"}, ""},
		{"private-700m", []string{"private-350m"}, ""},
		{"mistral-7b-instruct", []string{"mistral-7b"}, ""},
		{"claude-opus-5-6-20251109", []string{"claude-opus-5-5"}, "claude-opus-5-5"},
		{"qwen3.9-27b-20260101", []string{"qwen3.8-27b-20250101"}, "qwen3.8-27b-20250101"},
		{"anthropic/claude-opus-5-6", []string{"claude-opus-5-5"}, "claude-opus-5-5"},
		{"Anthropic/Claude-Opus-5-6[1m]", []string{"claude-opus-5-5"}, "claude-opus-5-5"},
		{"qwen/qwen3.9-27b-20260101", []string{"qwen3.8-27b"}, "qwen3.8-27b"},
		{"deepseek/deepseek-v5-pro", []string{"deepseek-v4-pro", "deepseek-v4-flash"}, "deepseek-v4-pro"},
	}
	for _, tc := range cases {
		var gens []catalogGeneration
		for _, id := range tc.candidates {
			key := catalogGenerationKey(id)
			g, ok := parseModelGeneration(key)
			if !ok {
				continue
			}
			gens = append(gens, catalogGeneration{model: proto.ProviderModel{ID: id}, gen: g})
		}
		got, ok := closestGeneration(catalogGenerationKey(tc.id), gens)
		if tc.want == "" {
			if ok {
				t.Errorf("%s: inherited %s, want none", tc.id, got.model.ID)
			}
			continue
		}
		if !ok || got.model.ID != tc.want {
			t.Errorf("%s: got %q ok=%v, want %s", tc.id, got.model.ID, ok, tc.want)
		}
	}
}

func TestCatalogGenerationFallback(t *testing.T) {
	cases := []struct {
		id     string
		ok     bool
		from   string
		name   string
		window int
	}{
		{"claude-opus-5-5", true, "", "Opus 5.5", 1000000},
		{"claude-opus-5-6", true, "claude-opus-5-5", "", 1000000},
		{"claude-opus-6", true, "claude-opus-5-5", "", 1000000},
		{"claude-opus-5-4", true, "claude-opus-5", "", 1000000},
		{"claude-opus-4-4", false, "", "", 0},
		{"claude-sonnet-4-7", true, "claude-sonnet-4-6", "", 1000000},
		{"claude-haiku-4-6", true, "claude-haiku-4-5", "", 200000},
		{"claude-haiku-5-6", true, "claude-haiku-5-5", "", 1000000},
		{"glm-5.4", true, "glm-5.3", "", 1000000},
		{"glm-5.4-flash", true, "glm-5.3-flash", "", 1000000},
		{"glm-5.10", true, "glm-5.3", "", 1000000},
		{"glm-4", false, "", "", 0},
		{"qwen3.9-27b", true, "qwen3.8-27b", "", 991808},
		{"qwen3.10-27b", true, "qwen3.8-27b", "", 991808},
		{"qwen3.7-27b", false, "", "", 0},
		{"qwen3.9-235b", false, "", "", 0},
		{"qwen3.9-max", true, "qwen3.8-max", "", 983616},
		{"qwen3.9-plus", true, "qwen3.7-plus", "", 991808},
		{"qwen3.9-flash", true, "qwen3.8-flash", "", 991808},
		{"qwen3.9-max-preview", false, "", "", 0},
		{"mimo-v2.7-flash", true, "mimo-v2.6-flash", "", 1000000},
		{"mimo-v2.7-pro-ultraspeed", true, "mimo-v2.6-pro-ultraspeed", "", 1000000},
		{"deepseek-v5-flash", true, "deepseek-v4-flash", "", 1048576},
		{"kimi-k3.2", true, "kimi-k3", "", 1048576},
		{"glm-5-3", true, "", "GLM-5.3", 1000000},
		{"claude-opus-5.5", true, "", "Opus 5.5", 1000000},
		{"claude-opus-5.5-20260101", true, "", "Opus 5.5", 1000000},
		{"qwen3-8-27b", true, "", "Qwen3.8 27B", 991808},
		{"qwen3.9.27b", true, "qwen3.8-27b", "", 991808},
		{"qwen3.8.27b", true, "", "Qwen3.8 27B", 991808},
		{"qwen3.9.max", true, "qwen3.8-max", "", 983616},
		{"deepseek-v4.pro", true, "", "DeepSeek V4 Pro", 1048576},
		{"glm-5.3.0", true, "glm-5.3", "", 1000000},
		{"claude-opus-5-6[1m]-20260101", true, "claude-opus-5-5", "", 1000000},
		{"claude-opus-5-5[1m]-20260101", true, "", "Opus 5.5", 1000000},
		{"anthropic/claude-opus-5.5", true, "", "Opus 5.5", 1000000},
		{"vendor/qwen3.9-plus-20260101", true, "qwen3.7-plus", "", 991808},
		{"qwen3.8-max-20260301", true, "", "Qwen 3.8 Max", 983616},
		{"kimi-k2.8-code", true, "kimi-k2.7-code", "", 262144},
		{"seed-2.2-pro", true, "seed-2.1-pro", "", 256000},
		{"mimo-v2.7-pro", true, "mimo-v2.6-pro", "", 1000000},
		{"MiniMax-M2.8", true, "MiniMax-M2.7", "", 204800},
		{"MiniMax-M2.8-highspeed", true, "MiniMax-M2.7-highspeed", "", 204800},
		{"claude-opus-5-6-20251109", true, "claude-opus-5-5", "", 1000000},
		{"claude-opus-5-5-20260101", true, "", "Opus 5.5", 1000000},
		{"anthropic/claude-opus-5-6", true, "claude-opus-5-5", "", 1000000},
		{"Anthropic/Claude-Opus-5-6[1m]", true, "claude-opus-5-5", "", 1000000},
		{"qwen/qwen3.9-27b-20260101", true, "qwen3.8-27b", "", 991808},
		{"deepseek/deepseek-v5-pro", true, "deepseek-v4-pro", "", 1048576},
		{"nobody-knows", false, "", "", 0},
		{"private-70b", false, "", "", 0},
	}
	for _, tc := range cases {
		got, ok := catalogModel(tc.id)
		if ok != tc.ok || got.InheritedFrom != tc.from || got.Name != tc.name || (tc.ok && got.ContextWindow != tc.window) {
			t.Errorf("%s: got %+v ok=%v", tc.id, got, ok)
		}
		if tc.from != "" && got.Name != "" {
			t.Errorf("%s: inherited a name %q", tc.id, got.Name)
		}
	}
}

func TestCatalogExactEntriesDoNotInherit(t *testing.T) {
	for id, want := range modelCatalog().byID {
		for _, q := range []string{id, "vendor/" + id, strings.ToUpper(id), id + "[1m]", id + "-20260101"} {
			got, ok := catalogModel(q)
			if !ok || got.InheritedFrom != "" || got.ID != want.ID || got.Name != want.Name ||
				got.ContextWindow != want.ContextWindow || got.DefaultEffort != want.DefaultEffort ||
				!slices.Equal(got.Efforts, want.Efforts) {
				t.Errorf("%s via %q: %+v ok=%v", id, q, got, ok)
			}
		}
	}
}

func TestCatalogPricesAreExactOnly(t *testing.T) {
	exact, ok := catalogModel("claude-opus-5-5")
	if !ok || exact.Price == nil || exact.Price.Input != 4 || exact.Price.Output != 20 || exact.Price.CacheRead == nil || *exact.Price.CacheRead != 0.2 {
		t.Fatalf("exact catalog price: %+v", exact)
	}
	inherited, ok := catalogModel("claude-opus-5-6")
	if !ok || inherited.InheritedFrom == "" || inherited.Price != nil {
		t.Fatalf("a different model must not inherit a price: %+v", inherited)
	}
	if got := enrichModel(proto.ProviderModel{ID: "claude-opus-5-6"}); got.Price != nil {
		t.Fatalf("enrichment inherited a price: %+v", got)
	}
	own := &proto.ModelPrice{Input: 9, Output: 99}
	if got := enrichModel(proto.ProviderModel{ID: "claude-opus-5-5", Price: own}); got.Price == own || got.Price == nil || got.Price.Output != 20 {
		t.Fatalf("a non-catalog price survived or replaced the exact reference price: %+v", got)
	}
	if got := enrichModel(proto.ProviderModel{ID: "private-model", Price: own}); got.Price != nil {
		t.Fatalf("an unknown model kept a non-catalog price: %+v", got)
	}
}

func TestEnrichModelGeneration(t *testing.T) {
	got := enrichModel(proto.ProviderModel{ID: "claude-opus-5-6"})
	if got.Name != "" || got.InheritedFrom != "claude-opus-5-5" || got.ContextWindow != 1000000 || got.DefaultEffort != "medium" || len(got.Efforts) != 5 {
		t.Fatalf("bare inherit: %+v", got)
	}
	got.Efforts[0] = "mutated"
	again := enrichModel(proto.ProviderModel{ID: "claude-opus-5-6"})
	exact := enrichModel(proto.ProviderModel{ID: "claude-opus-5-5"})
	if again.Efforts[0] == "mutated" || exact.Efforts[0] == "mutated" || exact.Name != "Opus 5.5" || exact.InheritedFrom != "" {
		t.Fatalf("aliased or exact changed: again=%+v exact=%+v", again, exact)
	}

	// What the gateway reported wins, including when that leaves nothing to inherit.
	full := enrichModel(proto.ProviderModel{
		ID: "glm-5.4", Name: "Gateway", ContextWindow: 111,
		Efforts: []string{"low"}, DefaultEffort: "low",
	})
	if full.Name != "Gateway" || full.ContextWindow != 111 || full.DefaultEffort != "low" || len(full.Efforts) != 1 || full.InheritedFrom != "" {
		t.Fatalf("gateway wins: %+v", full)
	}

	partial := enrichModel(proto.ProviderModel{ID: "glm-5.4", Name: "Gateway", ContextWindow: 111})
	if partial.Name != "Gateway" || partial.ContextWindow != 111 || partial.InheritedFrom != "glm-5.3" || partial.DefaultEffort != "high" || len(partial.Efforts) != 3 {
		t.Fatalf("partial: %+v", partial)
	}

	// An unlisted [1m] id is 1M even when the previous generation's window is smaller.
	// haiku-4-5 states no efforts, so nothing is inherited and inherited_from stays empty.
	marked := enrichModel(proto.ProviderModel{ID: "claude-haiku-4-6[1m]"})
	if marked.ContextWindow != 1000000 || marked.InheritedFrom != "" || marked.Name != "" {
		t.Fatalf("[1m] ahead of inherited window: %+v", marked)
	}
	// Efforts still come from the previous generation; the marker only owns the window.
	markedOpus := enrichModel(proto.ProviderModel{ID: "claude-opus-5-6[1m]"})
	if markedOpus.ContextWindow != 1000000 || markedOpus.InheritedFrom != "claude-opus-5-5" || markedOpus.DefaultEffort != "medium" || len(markedOpus.Efforts) != 5 {
		t.Fatalf("[1m] opus: %+v", markedOpus)
	}
	// [1m] on a listed id keeps that entry, including a window that is not 1M.
	listed := enrichModel(proto.ProviderModel{ID: "claude-haiku-4-5[1m]"})
	if listed.ContextWindow != 200000 || listed.Name != "Haiku 4.5" || listed.InheritedFrom != "" {
		t.Fatalf("listed [1m]: %+v", listed)
	}
	if m := enrichModel(proto.ProviderModel{ID: "own[1m]"}); m.ContextWindow != 1000000 || m.InheritedFrom != "" {
		t.Fatalf("unknown [1m]: %+v", m)
	}
	// The other version separator is the catalog entry, not an inheritance from glm-5.2 (which has no "low").
	dashed := enrichModel(proto.ProviderModel{ID: "glm-5-3"})
	if dashed.Name != "GLM-5.3" || dashed.InheritedFrom != "" || !slices.Contains(dashed.Efforts, "low") {
		t.Fatalf("same generation: %+v", dashed)
	}
}

func TestEnrichDiscoveredModelWindows(t *testing.T) {
	// An exact catalog window replaces a server-reported one.
	exact := enrichDiscoveredModel(proto.ProviderModel{ID: "claude-opus-4-8", ContextWindow: 123, Name: "From server"})
	if exact.ContextWindow != 1000000 || exact.Name != "From server" || exact.InheritedFrom != "" || exact.DefaultEffort != "medium" {
		t.Fatalf("exact window: %+v", exact)
	}
	// A server-reported window wins over one inherited from the previous generation.
	reported := enrichDiscoveredModel(proto.ProviderModel{ID: "claude-opus-5-6", ContextWindow: 123})
	if reported.ContextWindow != 123 || reported.InheritedFrom != "claude-opus-5-5" || reported.DefaultEffort != "medium" || reported.Name != "" {
		t.Fatalf("reported over inherited: %+v", reported)
	}
	// With no server window, the previous generation fills it.
	gap := enrichDiscoveredModel(proto.ProviderModel{ID: "claude-opus-5-6"})
	if gap.ContextWindow != 1000000 || gap.InheritedFrom != "claude-opus-5-5" {
		t.Fatalf("inherited gap: %+v", gap)
	}
	// A server-reported window beats [1m], which beats an inherited window.
	reportedMark := enrichDiscoveredModel(proto.ProviderModel{ID: "claude-haiku-4-6[1m]", ContextWindow: 123})
	if reportedMark.ContextWindow != 123 || reportedMark.InheritedFrom != "" {
		t.Fatalf("server over [1m]: %+v", reportedMark)
	}
	marked := enrichDiscoveredModel(proto.ProviderModel{ID: "claude-opus-5-6[1m]"})
	if marked.ContextWindow != 1000000 || marked.InheritedFrom != "claude-opus-5-5" {
		t.Fatalf("discovered [1m]: %+v", marked)
	}
	reportedOpus := enrichDiscoveredModel(proto.ProviderModel{ID: "claude-opus-5-6[1m]", ContextWindow: 123})
	if reportedOpus.ContextWindow != 123 || reportedOpus.InheritedFrom != "claude-opus-5-5" || reportedOpus.DefaultEffort != "medium" {
		t.Fatalf("server window keeps inherited efforts: %+v", reportedOpus)
	}
}

func TestTrailingZeroDoesNotReplaceGatewayWindow(t *testing.T) {
	got := enrichDiscoveredModel(proto.ProviderModel{ID: "glm-5.3.0", Name: "Gateway", ContextWindow: 128000})
	if got.Name != "Gateway" || got.ContextWindow != 128000 || got.InheritedFrom != "glm-5.3" || !slices.Contains(got.Efforts, "low") {
		t.Fatalf("%+v", got)
	}
}

func TestDiscoveredExactCatalogWindowWins(t *testing.T) {
	for id, want := range modelCatalog().byID {
		if want.ContextWindow == 0 {
			continue
		}
		got := enrichDiscoveredModel(proto.ProviderModel{ID: id, Name: "server", ContextWindow: 123})
		if got.ContextWindow != want.ContextWindow || got.InheritedFrom != "" || got.Name != "server" {
			t.Errorf("%s: %+v", id, got)
		}
		marked := enrichDiscoveredModel(proto.ProviderModel{ID: id + "[1m]", ContextWindow: 123})
		if marked.ContextWindow != want.ContextWindow || marked.InheritedFrom != "" {
			t.Errorf("%s[1m]: %+v", id, marked)
		}
	}
}

func TestDatedCatalogRowIsAGenerationSource(t *testing.T) {
	var gens []catalogGeneration
	m := proto.ProviderModel{ID: "claude-opus-5-5-20250929", Name: "Opus 5.5", ContextWindow: 1000000, Efforts: []string{"low"}, DefaultEffort: "low"}
	g, ok := parseModelGeneration(catalogGenerationKey(m.ID))
	if !ok {
		t.Fatal("dated row has no generation")
	}
	gens = append(gens, catalogGeneration{model: m, gen: g})
	got, ok := closestGeneration("claude-opus-5-6", gens)
	if !ok || got.model.ID != m.ID {
		t.Fatalf("dated source: %+v %v", got.model, ok)
	}
}

func TestNoTwoCatalogEntriesShareAGeneration(t *testing.T) {
	seen := map[string]string{}
	for _, g := range modelCatalog().gens {
		key := g.gen.family + "#" + versionKey(g.gen.version)
		if prev, ok := seen[key]; ok {
			t.Errorf("%s and %s are the same generation", prev, g.model.ID)
		}
		seen[key] = g.model.ID
	}
}

func versionKey(v []int) string {
	var b strings.Builder
	for i, n := range v {
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(strconv.Itoa(n))
	}
	return b.String()
}
