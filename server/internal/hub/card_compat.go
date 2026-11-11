package hub

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// featureCardRing: the client draws a ```persona-card of type ring (#3068). A client declares it in client_hello's
// features, or on a REST request in the X-Persona-Features header (comma-separated); a REST request without the
// header also counts when it matches a connection of the same device, platform and version that declared it.
//
// Every other client gets each ring rewritten on its way out (cardCompatItems): a ring the apps can draw becomes a
// persona-view of the same parts (ringReplacement), which every app has drawn since before #3068; one they cannot
// becomes Markdown (the title in bold, the parts against the goal, a "label: value unit" line per part, the note).
// Nothing is stored: the transcript keeps the ring, and a client that declares the feature later gets it whole.
const featureCardRing = "persona_card_ring"

// featuresHeader is how a REST request declares what it can draw (the same names as client_hello's features).
const featuresHeader = "X-Persona-Features"

// requestWants reports whether a REST request's client declared feature: in its X-Persona-Features header, or on a
// connection of the same device, platform and version (requestHasElicitation's rule).
func (h *Hub) requestWants(r *http.Request, feature string) bool {
	if requestDeclares(r, feature) {
		return true
	}
	who, rc := principalOf(r.Context()), requestClientOf(r.Context())
	if rc.Platform == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		c.mu.Lock()
		matches := c.device == who.ID && c.meta.Platform == rc.Platform && c.meta.Version == rc.Version
		c.mu.Unlock()
		if matches && c.has(feature) {
			return true
		}
	}
	return false
}

// requestDeclares reports whether this request itself declares feature in its X-Persona-Features header.
func requestDeclares(r *http.Request, feature string) bool {
	for _, v := range r.Header.Values(featuresHeader) {
		for _, f := range strings.Split(v, ",") {
			if strings.TrimSpace(f) == feature {
				return true
			}
		}
	}
	return false
}

// cardCompatItems is items as a client gets them: unchanged when it draws rings, else a copy with each assistant
// text's ring cards rewritten (ringCardsDowngraded). The node's slice is shared, so it is never written to.
func cardCompatItems(l i18n.Lang, ring bool, items []proto.Item) []proto.Item {
	if ring {
		return items
	}
	var out []proto.Item
	for i := range items {
		it := items[i]
		if it.Kind != proto.KindText || it.Role != "assistant" {
			continue
		}
		text := ringCardsDowngraded(l, it.Text)
		if text == it.Text {
			continue
		}
		if out == nil {
			out = append([]proto.Item(nil), items...)
		}
		out[i].Text = text
	}
	if out == nil {
		return items
	}
	return out
}

// ringCardsDowngraded is md with each complete ```persona-card of type ring written for a client that does not draw
// rings. Other blocks, and rings inside another code block (examples), stay as they are.
func ringCardsDowngraded(l i18n.Lang, md string) string {
	if !strings.Contains(md, "persona-card") || !strings.Contains(md, "ring") {
		return md
	}
	if !i18n.Valid(l) {
		l = i18n.ZhHans
	}
	blocks := wire.AssistantViewBlocks(md)
	// The apps draw at most three persona-view blocks in a reply (the rest as their fallback): rings past that many,
	// counting the reply's own views, are written as words.
	views := 3
	for _, blk := range blocks {
		if blk.Language == "persona-view" {
			views--
		}
	}
	var b strings.Builder
	at := 0
	for _, blk := range blocks {
		if blk.Language != "persona-card" || !blk.Complete {
			continue
		}
		var c wire.PersonaCard
		if len(blk.Raw) > wire.AssistantViewMost || json.Unmarshal(blk.Raw, &c) != nil || c.Type != wire.PersonaCardKindRing {
			continue
		}
		b.WriteString(md[at:blk.Start])
		b.WriteString(ringReplacement(l, c, views > 0))
		views--
		if strings.HasSuffix(md[blk.Start:blk.End], "\n") {
			b.WriteString("\n")
		}
		at = blk.End
	}
	if at == 0 {
		return md
	}
	b.WriteString(md[at:])
	return b.String()
}

// ringReplacement is a ring as an older client draws it: a ```persona-view block, or Markdown when the apps could not
// draw the ring either (no trailing line break).
//
// Not a stacked_bar: an older app labels a stacked_bar's big number "Total" whatever it is, adds the rest into the bar
// and writes the title again under it, so a day's goal read as what was eaten (1,950 for 1,770) and "remaining" as one
// more part. Every app that draws persona-card also draws persona-view (one section of metric, chart, row and text),
// where the hub writes each line itself: what the parts add up to against the goal ("Eaten 1,770 / Goal 1,950 kcal"),
// what is left or over on its own in the metric's grey detail and never in the bar, the title once.
func ringReplacement(l i18n.Lang, c wire.PersonaCard, asView bool) string {
	r := ringSummaryOf(l, c)
	words := ringWords(l, c, r)
	if !asView || c.View() == nil {
		return words
	}
	// An older app reads a row without a title, and so the whole view, as broken; a bar of nothing says nothing.
	sum := 0.0
	for _, p := range r.parts {
		if strings.TrimSpace(p.Label) == "" {
			return words
		}
		sum += p.Value
	}
	if sum <= 0 {
		return words
	}
	var items []wire.Component
	if c.Subtitle != "" {
		items = append(items, wire.Component{Type: wire.ComponentText, Text: c.Subtitle, Style: wire.TextStyleSecondary})
	}
	items = append(items, wire.Component{Type: wire.ComponentMetric, Label: r.label, Value: r.headline, Detail: r.left})
	// An older app draws at most four series: past four parts, the first three and "other".
	shown := r.parts
	if len(shown) > 4 {
		other := 0.0
		for _, p := range shown[3:] {
			other += p.Value
		}
		shown = append(append([]wire.CardPart{}, shown[:3]...), wire.CardPart{Label: tt(l, "card_other"), Value: other})
	}
	var series []wire.ChartSeries
	for i, p := range shown {
		s := wire.ChartSeries{Name: p.Label, Points: []wire.ChartPoint{{Label: r.bar, Value: p.Value, Display: r.amount(p.Value)}}}
		if len(r.parts) > 4 && i == len(shown)-1 {
			s.Tone = wire.ToneNeutral
		}
		series = append(series, s)
	}
	items = append(items, wire.Component{Type: wire.ComponentChart, Kind: wire.ChartKindBar, Stacked: true, Unit: r.unit,
		Detail: c.Note, Series: series})
	for _, p := range r.parts {
		items = append(items, wire.Component{Type: wire.ComponentRow, Title: p.Label, Subtitle: p.Note, Value: r.amount(p.Value)})
	}
	v := wire.View{Sections: []wire.Section{{Title: c.Title, Items: items}}, Fallback: words}
	raw, err := json.Marshal(v)
	if err != nil {
		return words
	}
	return "```persona-view\n" + string(raw) + "\n```"
}

// ringSummary is what a downgraded ring says about its parts and its total.
type ringSummary struct {
	parts    []wire.CardPart
	unit     string
	label    string // the metric's label
	headline string // the metric's value: "Eaten 1,770 / Goal 1,950 kcal", or the parts' sum without a goal
	left     string // "Remaining 180 kcal" / "Over 150 kcal", empty when on the goal or without one
	bar      string // the one bar's label: what the parts are ("Eaten"), never the title again
	goal     bool   // the ring has a total above 0: the headline is the parts against it
}

func (r ringSummary) amount(v float64) string {
	if r.unit == "" {
		return wire.Number(v)
	}
	return wire.Number(v) + " " + r.unit
}

// ringSummaryOf: the parts (segments and rest) add up to what was done; total, when there is one, is the goal.
func ringSummaryOf(l i18n.Lang, c wire.PersonaCard) ringSummary {
	r := ringSummary{parts: append([]wire.CardPart{}, c.Segments...), unit: c.Unit}
	if c.Rest != nil {
		r.parts = append(r.parts, *c.Rest)
	}
	sum := 0.0
	for _, p := range r.parts {
		sum += p.Value
	}
	if c.Total != nil && c.Total.Unit != "" {
		r.unit = c.Total.Unit
	}
	done := "card_done"
	if ringIsFood(r.unit) {
		done = "card_eaten"
	}
	r.bar = tt(l, done)
	// A goal of nothing is no goal.
	if c.Total == nil || c.Total.Value <= 0 {
		r.label, r.headline = tt(l, "card_total"), r.amount(sum)
		return r
	}
	r.goal, r.label = true, tt(l, "card_progress")
	r.headline = tt(l, "card_of_goal", i18n.Args{"done": r.bar, "value": wire.Number(sum), "goal": wire.Number(c.Total.Value)})
	if r.unit != "" {
		r.headline += " " + r.unit
	}
	switch goal := c.Total.Value; {
	case wire.Number(sum) == wire.Number(goal):
	case sum < goal:
		r.left = tt(l, "card_remaining") + " " + r.amount(goal-sum)
	default:
		r.left = tt(l, "card_over") + " " + r.amount(sum-goal)
	}
	return r
}

// ringIsFood: a unit of food energy, where what the parts add up to was eaten.
func ringIsFood(unit string) bool {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "kcal", "cal", "kj", "千卡", "大卡", "卡", "卡路里", "千焦": // i18n-ignore: unit names the assistant writes, matched, never shown
		return true
	}
	return false
}

// ringWords is the ring as Markdown: the title in bold, the subtitle, the headline, a line per part, what is left or
// over, the note.
func ringWords(l i18n.Lang, c wire.PersonaCard, r ringSummary) string {
	var lines []string
	if c.Title != "" {
		lines = append(lines, "**"+c.Title+"**")
	}
	if c.Subtitle != "" {
		lines = append(lines, c.Subtitle)
	}
	if r.goal {
		lines = append(lines, "- "+r.headline)
	} else {
		lines = append(lines, "- "+r.label+": "+r.headline)
	}
	for _, p := range r.parts {
		line := "- " + p.Label + ": " + r.amount(p.Value)
		if p.Note != "" {
			line += " (" + p.Note + ")"
		}
		lines = append(lines, line)
	}
	if r.left != "" {
		lines = append(lines, "- "+r.left)
	}
	if c.Note != "" {
		lines = append(lines, "", c.Note)
	}
	return strings.Join(lines, "\n")
}
