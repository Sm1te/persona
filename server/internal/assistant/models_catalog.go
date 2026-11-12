package assistant

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
)

// The built-in model catalog (#2455): what is known of the models gateways commonly serve to Claude Code — the
// context window and the thinking levels. A model a gateway lists (POST /api/v1/launchers/discover) is filled in from
// it. The data is cindy's model registry, the Claude series and the models of the common Chinese gateways
// (THIRD_PARTY_NOTICES.md); the file contains only what that registry states.
// An id the file does not list inherits the context window and thinking levels of the closest earlier generation of
// the same family (the same leading name and the same size or variant suffix). It does not inherit the name.
// Gateway-reported values still win. InheritedFrom names the catalog id that filled a gap.

//go:embed models_catalog.json
var modelCatalogJSON []byte

// modelGeneration is one numbered generation. family is the leading name plus the non-numeric suffix (a size or a
// variant such as "-27b" or "-flash"); version is the number between them, one component per dot or dash.
type modelGeneration struct {
	family  string
	version []int
}

type catalogGeneration struct {
	model proto.ProviderModel
	gen   modelGeneration
}

type catalogData struct {
	byID map[string]proto.ProviderModel
	gens []catalogGeneration
}

var modelCatalog = sync.OnceValue(func() catalogData {
	var doc struct {
		Models []proto.ProviderModel `json:"models"`
	}
	if err := json.Unmarshal(modelCatalogJSON, &doc); err != nil {
		panic("models_catalog.json: " + err.Error())
	}
	out := catalogData{byID: make(map[string]proto.ProviderModel, len(doc.Models))}
	for _, m := range doc.Models {
		key := strings.ToLower(m.ID)
		out.byID[key] = m
		// Index the same key a lookup uses, so a dated or prefixed catalog row is still a source.
		if g, ok := parseModelGeneration(catalogGenerationKey(m.ID)); ok {
			out.gens = append(out.gens, catalogGeneration{model: m, gen: g})
		}
	}
	return out
})

var modelDateSuffix = lazyre.New(`-\d{8}$`)

// normalizeCatalogID is the catalog key of id: lower case, without a trailing "[1m]" and without a vendor prefix
// ("anthropic/…", anything before the last slash).
func normalizeCatalogID(id string) string {
	key := strings.ToLower(strings.TrimSpace(id))
	if i := strings.LastIndex(key, "/"); i >= 0 {
		key = key[i+1:]
	}
	return strings.TrimSuffix(key, "[1m]")
}

// catalogGenerationKey drops a trailing "[1m]" and a trailing date ("-20250929") in either order, so a dated id
// joins the undated family. An exact lookup tries the dated key first.
func catalogGenerationKey(id string) string {
	key := normalizeCatalogID(id)
	for {
		next := strings.TrimSuffix(key, "[1m]")
		next = modelDateSuffix.ReplaceAllString(next, "")
		next = strings.TrimSuffix(next, "[1m]")
		if next == key {
			return key
		}
		key = next
	}
}

// idHas1M reports an unlisted 1M marker, including when a date was appended after it.
func idHas1M(id string) bool {
	key := strings.ToLower(strings.TrimSpace(id))
	if i := strings.LastIndex(key, "/"); i >= 0 {
		key = key[i+1:]
	}
	if strings.HasSuffix(key, "[1m]") {
		return true
	}
	return strings.HasSuffix(modelDateSuffix.ReplaceAllString(key, ""), "[1m]")
}

// catalogModel looks a model id up: as given, then without a vendor prefix ("anthropic/…"), the "[1m]" marker and a
// date suffix ("-20250929"), case-insensitively. The same generation written with the other separator (glm-5-3 and
// glm-5.3) is that catalog entry. Anything else the catalog does not list inherits capabilities from the closest
// earlier generation of the same family. InheritedFrom is set only then, and the name is left empty.
func catalogModel(id string) (proto.ProviderModel, bool) {
	key := normalizeCatalogID(id)
	cat := modelCatalog()
	if m, ok := cat.byID[key]; ok {
		return m, true
	}
	stripped := catalogGenerationKey(id)
	if stripped != key {
		if m, ok := cat.byID[stripped]; ok {
			return m, true
		}
	}
	// glm-5-3 and glm-5.3 are the same generation. Treating the dot form as missing would
	// inherit from an older one (glm-5.2) and drop capabilities the catalog already states.
	if twin, ok := sameGeneration(stripped, cat.gens); ok {
		return twin.model, true
	}
	// 5.3.0 is not the 5.3 row (a gateway window must not be replaced), but it is not 5.2 either.
	if src, ok := zeroExtendedGeneration(stripped, cat.gens); ok {
		return inheritedModel(stripped, src.model), true
	}
	src, ok := closestGeneration(stripped, cat.gens)
	if !ok {
		return proto.ProviderModel{}, false
	}
	return inheritedModel(stripped, src.model), true
}

// inheritedModel copies capabilities only. The name stays empty; price and routing are not catalog fields.
func inheritedModel(id string, src proto.ProviderModel) proto.ProviderModel {
	return proto.ProviderModel{
		ID:            id,
		ContextWindow: src.ContextWindow,
		Efforts:       append([]string(nil), src.Efforts...),
		DefaultEffort: src.DefaultEffort,
		InheritedFrom: src.ID,
	}
}

// sameGeneration is the catalog entry of this family whose version compares equal (5.3 and 5-3).
// A parameter size has no generation, so it cannot land here.
func sameGeneration(key string, gens []catalogGeneration) (catalogGeneration, bool) {
	want, ok := parseModelGeneration(key)
	if !ok {
		return catalogGeneration{}, false
	}
	best := -1
	for i := range gens {
		// 5.3 and 5-3 match. 5.3.0 does not: a trailing zero is a longer version, not this row.
		if gens[i].gen.family != want.family || !sameGenerationVersion(gens[i].gen.version, want.version) {
			continue
		}
		if best < 0 || gens[i].model.ID < gens[best].model.ID {
			best = i
		}
	}
	if best < 0 {
		return catalogGeneration{}, false
	}
	return gens[best], true
}

// zeroExtendedGeneration is a shorter catalog version that compares equal only because the query adds trailing
// zeros (5.3 for 5.3.0). It fills gaps; it is not an exact row, so a gateway window and name stay.
func zeroExtendedGeneration(key string, gens []catalogGeneration) (catalogGeneration, bool) {
	want, ok := parseModelGeneration(key)
	if !ok {
		return catalogGeneration{}, false
	}
	best := -1
	for i := range gens {
		v := gens[i].gen.version
		if gens[i].gen.family != want.family || len(v) >= len(want.version) || compareVersion(v, want.version) != 0 {
			continue
		}
		if best < 0 || len(v) > len(gens[best].gen.version) ||
			(len(v) == len(gens[best].gen.version) && gens[i].model.ID < gens[best].model.ID) {
			best = i
		}
	}
	if best < 0 {
		return catalogGeneration{}, false
	}
	return gens[best], true
}

// closestGeneration is the newest catalog generation strictly older than key in the same family.
func closestGeneration(key string, gens []catalogGeneration) (catalogGeneration, bool) {
	want, ok := parseModelGeneration(key)
	if !ok {
		return catalogGeneration{}, false
	}
	best := -1
	for i := range gens {
		if gens[i].gen.family != want.family || compareVersion(gens[i].gen.version, want.version) >= 0 {
			continue
		}
		if best < 0 || compareVersion(gens[i].gen.version, gens[best].gen.version) > 0 ||
			(compareVersion(gens[i].gen.version, gens[best].gen.version) == 0 && gens[i].model.ID < gens[best].model.ID) {
			best = i
		}
	}
	if best < 0 {
		return catalogGeneration{}, false
	}
	return gens[best], true
}

// enrichModel fills capabilities the gateway did not say from the catalog; what it said about them wins. Price is
// different: only an exact catalog entry supplies the reference price, so an external value never survives enrichment.
// A name is filled only from an exact entry (a listed id, or the same generation written with the other separator).
// InheritedFrom is set only when a missing capability was copied from an earlier generation. An unlisted "[1m]" id is
// a 1M window, ahead of an inherited window; an exact entry keeps the window the catalog states.
func enrichModel(m proto.ProviderModel) proto.ProviderModel {
	m.Price = nil
	if c, ok := catalogModel(m.ID); ok {
		if m.Name == "" && c.InheritedFrom == "" {
			m.Name = c.Name
		}
		if c.InheritedFrom == "" {
			m.Price = c.Price
		}
		window := c.ContextWindow
		if c.InheritedFrom != "" && idHas1M(m.ID) {
			window = 0
		}
		filled := false
		if m.ContextWindow == 0 && window != 0 {
			m.ContextWindow = window
			filled = true
		}
		if len(m.Efforts) == 0 && len(c.Efforts) > 0 {
			m.Efforts = append([]string(nil), c.Efforts...)
			filled = true
		}
		if m.DefaultEffort == "" && c.DefaultEffort != "" {
			m.DefaultEffort = c.DefaultEffort
			filled = true
		}
		if filled && c.InheritedFrom != "" {
			m.InheritedFrom = c.InheritedFrom
		}
	}
	if m.ContextWindow == 0 && idHas1M(m.ID) {
		m.ContextWindow = 1000000
	}
	return m
}

// parseModelGeneration reads a generation out of an already normalized id. A parameter size (7b, 0.5b, 8x22b) is not
// a generation. A size that follows a generation number (qwen4-30b, qwen3.8-27b) stays in the family suffix.
func parseModelGeneration(id string) (modelGeneration, bool) {
	if parameterSize(id) {
		return modelGeneration{}, false
	}
	i := strings.IndexAny(id, "0123456789")
	if i <= 0 || !familyPrefix(id[:i]) {
		return modelGeneration{}, false
	}
	version, rest, ok := readGenerationVersion(id[i:])
	if !ok {
		return modelGeneration{}, false
	}
	// qwen3.8.27b and qwen3.9.max use a dot where the catalog writes a dash.
	if strings.HasPrefix(rest, ".") {
		rest = "-" + rest[1:]
	}
	return modelGeneration{family: id[:i] + "|" + rest, version: version}, true
}

// parameterSize reports whether id's first number is a parameter size rather than a generation.
func parameterSize(id string) bool {
	i := strings.IndexAny(id, "0123456789")
	if i <= 0 || !familyPrefix(id[:i]) {
		return false
	}
	j := readDottedNumber(id, i)
	if j == i {
		return false
	}
	if j < len(id) && id[j] == 'x' {
		if k := readDottedNumber(id, j+1); k > j+1 {
			j = k
		}
	}
	if j >= len(id) || !sizeUnit(id[j]) {
		return false
	}
	j++
	if j == len(id) {
		return true
	}
	switch id[j] {
	case '-', '_', '/', '.', ':':
		return true
	default:
		return false
	}
}

func familyPrefix(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && c != '/' && c != '-' {
			return false
		}
	}
	return true
}

func sizeUnit(c byte) bool {
	return c == 'k' || c == 'm' || c == 'b' || c == 't'
}

// readDottedNumber reads digits with an optional fractional part. It returns i when s[i] is not a digit.
func readDottedNumber(s string, i int) int {
	j := readDigits(s, i)
	if j == i {
		return i
	}
	if j < len(s) && s[j] == '.' {
		if k := readDigits(s, j+1); k > j+1 {
			return k
		}
	}
	return j
}

func readDigits(s string, i int) int {
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return i
}

// readGenerationVersion reads the leading version. Each component is one to three digits. A component after the
// first counts only when the next character is '.', '-' or the end, so a date (-20250929) or a size (-27b) stays in
// the suffix instead of becoming another component.
func readGenerationVersion(s string) ([]int, string, bool) {
	n, k, ok := versionComponent(s, false)
	if !ok {
		return nil, s, false
	}
	version := []int{n}
	rest := s[k:]
	for len(rest) > 0 && (rest[0] == '.' || rest[0] == '-') {
		n, k, ok = versionComponent(rest[1:], true)
		if !ok {
			break
		}
		version = append(version, n)
		rest = rest[1+k:]
	}
	return version, rest, true
}

// versionComponent reads one to three digits. bounded also requires the following character to be '.', '-' or the end.
func versionComponent(s string, bounded bool) (n, k int, ok bool) {
	if s == "" || s[0] < '0' || s[0] > '9' {
		return 0, 0, false
	}
	for k < len(s) && k < 3 && s[k] >= '0' && s[k] <= '9' {
		n = n*10 + int(s[k]-'0')
		k++
	}
	if k == 0 {
		return 0, 0, false
	}
	if bounded && k < len(s) && s[k] != '.' && s[k] != '-' {
		return 0, 0, false
	}
	return n, k, true
}

// sameVersion is equal component for component. A shorter version is not the same row as one with a trailing 0.
func sameGenerationVersion(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// compareVersion compares generation numbers numerically, missing components as 0, so 5.10 is newer than 5.9 and 5
// is older than 5.1. Padding is only for order; sameGeneration does not treat 5.3.0 as 5.3.
func compareVersion(a, b []int) int {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		av, bv := 0, 0
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return 0
}
