package transcript

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// The model reads supervision as user input, but it must not consume the chat's
// two-user-question page budget. Compaction changes context, not visible history.
func TestPagePromptProtectsRealUserProvenance(t *testing.T) {
	supervision := prompts.RenderSupervise(prompts.Supervise{Entries: []prompts.SuperviseEntry{{ID: "demo-task", Title: "Verify a test change", Kind: "review"}}})
	base := proto.Item{Role: "user", Kind: proto.KindText, Text: supervision}
	for _, tc := range []struct {
		name string
		edit func(*proto.Item)
		want bool
	}{
		{"automatic supervision", func(*proto.Item) {}, false},
		{"user quotes supervision", func(it *proto.Item) { it.ClientMsgID = "client-demo" }, true},
		{"session quotes supervision", func(it *proto.Item) { it.From = "demo-session" }, true},
		{"ordinary message", func(it *proto.Item) { it.Text = "Please review this task" }, true},
		{"queued input", func(it *proto.Item) { it.Queued = true }, false},
		{"wake", func(it *proto.Item) { it.Wake = true; it.Text = "A background result" }, false},
		{"explicit injection", func(it *proto.Item) {
			it.Text = "Synthetic supervision"
			it.Injected = &proto.Injected{Kind: proto.InjectedSystem, State: "supervise"}
		}, false},
		{"tool result", func(it *proto.Item) { it.Kind = proto.KindToolResult }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			it := base
			tc.edit(&it)
			if got := pagePrompt(it); got != tc.want {
				t.Fatalf("pagePrompt=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestPageTurnsKeepsUserQuestionsAcrossSupervisionAndCompaction(t *testing.T) {
	defer func(w int64) { windowBytes = w }(windowBytes)
	var lines strings.Builder
	longest := 0
	appendLine := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		longest = max(longest, len(b)+1)
		lines.Write(b)
		lines.WriteByte('\n')
	}
	message := func(id, role, text string) {
		appendLine(map[string]any{"type": role, "uuid": id, "message": map[string]any{"role": role, "content": text}})
	}
	supervision := prompts.RenderSupervise(prompts.Supervise{Entries: []prompts.SuperviseEntry{{ID: "demo-task", Title: "Verify a test change", Kind: "review"}}})
	for question := 1; question <= 3; question++ {
		message(fmt.Sprintf("user-%d", question), "user", fmt.Sprintf("Question %d", question))
		message(fmt.Sprintf("answer-%d", question), "assistant", "An answer")
		if question < 3 {
			appendLine(map[string]any{"type": "system", "uuid": fmt.Sprintf("compact-%d", question), "subtype": "compact_boundary", "compactMetadata": map[string]any{"trigger": "auto", "preTokens": 170000}})
			appendLine(map[string]any{"type": "user", "uuid": fmt.Sprintf("summary-%d", question), "isCompactSummary": true, "message": map[string]any{"role": "user", "content": "Synthetic continuation summary"}})
		}
		for n := 0; n < 6; n++ {
			message(fmt.Sprintf("supervision-%d-%d", question, n), "user", supervision)
			message(fmt.Sprintf("review-%d-%d", question, n), "assistant", "Review recorded")
		}
	}
	// The small window follows the fixture, not a fixed size: the supervision prompt grows with its template (#5063
	// made one longer than a fixed 8 KiB window). Three of the longest lines give a page budget (3 windows) that holds
	// the six supervision rounds after the last question, and the file stays over two windows, so the read is windowed.
	small := int64(3 * longest)
	if size := int64(lines.Len()); size <= 2*small {
		t.Fatalf("fixture of %d bytes is too short for a windowed read with a %d-byte window", size, small)
	}
	for _, tc := range []struct {
		name   string
		window int64
	}{{"full", 1 << 20}, {"windowed", small}} {
		t.Run(tc.name, func(t *testing.T) {
			window := tc.window
			windowBytes = window
			path := filepath.Join(t.TempDir(), "conversation.jsonl")
			if err := os.WriteFile(path, []byte(lines.String()), 0o600); err != nil {
				t.Fatal(err)
			}
			f := NewFile(path)
			if _, err := f.Refresh(); err != nil {
				t.Fatal(err)
			}
			latest, more, _ := f.PageTurns(0, 2, 2000)
			questions := func(items []proto.Item) []string {
				var out []string
				for _, it := range items {
					if it.Role == "user" && it.Kind == proto.KindText && strings.HasPrefix(it.Text, "Question ") {
						out = append(out, it.Text)
					}
				}
				return out
			}
			got := questions(latest)
			if len(got) == 0 || got[len(got)-1] != "Question 3" || !more {
				t.Fatalf("latest page questions=%v, has_more=%v", got, more)
			}
			if window == 1<<20 && !reflect.DeepEqual(got, []string{"Question 2", "Question 3"}) {
				t.Fatalf("full read's latest two questions=%v", got)
			}
			all := append([]proto.Item(nil), latest...)
			for pages := 0; more; pages++ {
				if pages >= 20 || len(all) == 0 {
					t.Fatal("older history did not advance")
				}
				before := all[0].Seq
				older, hasMore, _ := f.PageTurns(before, 2, 2000)
				if hasMore && (len(older) == 0 || older[0].Seq >= before) {
					t.Fatal("budgeted page has no advancing cursor")
				}
				all, more = append(older, all...), hasMore
			}
			if got := questions(FoldWork(all)); !reflect.DeepEqual(got, []string{"Question 1", "Question 2", "Question 3"}) || more {
				t.Fatalf("folded complete history questions=%v, has_more=%v", got, more)
			}
			compactions, supervisionCount := 0, 0
			for _, it := range all {
				if it.Compact != nil {
					compactions++
				}
				if prompts.ParseInjected(it.Text) != nil {
					supervisionCount++
				}
			}
			if compactions != 2 || supervisionCount != 18 {
				t.Fatalf("history lost notices: compactions=%d supervision=%d", compactions, supervisionCount)
			}
		})
	}
}
