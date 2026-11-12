package assistant

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestAgentNameSpinnerDisplayKeepsOneShot(t *testing.T) {
	s, f, calls := namingFixture(t, func(w http.ResponseWriter, r *http.Request) {
		namingReply(w, "Fix PDF export")
	})
	c := f.sessions["server"][0]
	if !s.tryAgentName(context.Background(), "server", c, time.Now()) {
		t.Fatal("first naming was not dispatched")
	}
	for _, title := range []string{
		"Claude Code", " ◐ Claude Code ", "\t◑ Claude Code", "◒ Claude Code", "◓ Claude Code",
		"⠋ Claude Code", "⠙ Claude Code", "⠹ Claude Code", "⠸ Claude Code", "⠼ Claude Code",
		"⠴ Claude Code", "⠦ Claude Code", "⠧ Claude Code", "⠇ Claude Code", "⠏ Claude Code",
	} {
		current := c
		current.Title = title
		if got := s.agentDisplayName("server", current); got != "Fix PDF export" {
			t.Errorf("frame %q hid the saved name: %q", title, got)
		}
		if s.tryAgentName(context.Background(), "server", current, time.Now()) {
			t.Errorf("frame %q dispatched a second naming", title)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("model calls = %d, want 1", calls.Load())
	}
	s.agentNames.mu.Lock()
	record := s.agentNames.records[agentNameKey("server", c.SessionID)]
	s.agentNames.mu.Unlock()
	if record.Changes != 1 {
		t.Fatalf("successful changes = %d, want 1", record.Changes)
	}
	for _, test := range []struct {
		name   string
		mutate func(*proto.Session)
	}{
		{"actual title", func(c *proto.Session) { c.Title = "Fix search" }},
		{"different tool", func(c *proto.Session) { c.Title = "◑ Codex" }},
		{"manual flag", func(c *proto.Session) { c.TitleManual = true }},
		{"manual source", func(c *proto.Session) { c.TitleSource = proto.TitleSourceManual }},
		{"unknown source", func(c *proto.Session) { c.TitleSource = proto.TitleSourceUnknown }},
		{"assistant", func(c *proto.Session) { c.Assistant = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			current := c
			current.Title = "◑ Claude Code"
			test.mutate(&current)
			if got := s.agentDisplayName("server", current); got != "" {
				t.Fatalf("protected native title was overridden: %q", got)
			}
		})
	}
}

func TestAgentNameSpinnerDuringModelResult(t *testing.T) {
	for _, test := range []struct {
		name   string
		title  string
		source proto.TitleSource
		manual bool
		want   string
	}{
		{"next frame", "◑ Claude Code", proto.TitleSourcePrompt, false, "Fix PDF export"},
		{"stopped", "Claude Code", proto.TitleSourcePrompt, false, "Fix PDF export"},
		{"actual title", "Fix search", proto.TitleSourcePrompt, false, ""},
		{"different tool", "◑ Codex", proto.TitleSourcePrompt, false, ""},
		{"native manual", "◑ Claude Code", proto.TitleSourceManual, false, ""},
		{"manual flag", "◑ Claude Code", proto.TitleSourcePrompt, true, ""},
		{"unknown source", "◑ Claude Code", proto.TitleSourceUnknown, false, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			var f *fakeHub
			s, hub, calls := namingFixture(t, func(w http.ResponseWriter, r *http.Request) {
				f.mu.Lock()
				f.sessions["server"][0].Title = test.title
				f.sessions["server"][0].TitleSource = test.source
				f.sessions["server"][0].TitleManual = test.manual
				f.mu.Unlock()
				namingReply(w, "Fix PDF export")
			})
			f = hub
			c := f.sessions["server"][0]
			if !s.tryAgentName(context.Background(), "server", c, time.Now()) {
				t.Fatal("first naming was not dispatched")
			}
			f.mu.Lock()
			current := f.sessions["server"][0]
			f.mu.Unlock()
			if got := s.agentDisplayName("server", current); got != test.want {
				t.Fatalf("late result display = %q, want %q", got, test.want)
			}
			s.agentNames.mu.Lock()
			record := s.agentNames.records[agentNameKey("server", c.SessionID)]
			s.agentNames.mu.Unlock()
			wantChanges := 0
			if test.want != "" {
				wantChanges = 1
				if s.tryAgentName(context.Background(), "server", current, time.Now()) {
					t.Fatal("default dispatched a second naming")
				}
			}
			if record.Changes != wantChanges || calls.Load() != 1 {
				t.Fatalf("changes=%d calls=%d, want changes=%d calls=1", record.Changes, calls.Load(), wantChanges)
			}
		})
	}
}
