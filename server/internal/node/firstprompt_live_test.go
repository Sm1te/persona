//go:build herdrlive

package node

import (
	"bufio"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/herdr"
	"github.com/nwverse/persona/server/internal/host"
	"github.com/nwverse/persona/server/internal/screen"
)

// TestLiveFirstPrompt opens only test Agents on an explicitly named, isolated herdr server. No hub/node service,
// Persona hooks or user configuration is changed. The launcher supplies authentication from its login shell.
// HERDR_SESSION=persona-firstprompt-<unique> HERDR_SOCKET_PATH=<that session's socket>
// PERSONA_LIVE_FIRST_PROMPT=1 PERSONA_LIVE_LAUNCHER=<launcher> go test -tags herdrlive -run '^TestLiveFirstPrompt$' ./internal/node -v
// PERSONA_LIVE_ARTIFACTS optionally keeps redacted terminal captures and a verification receipt outside the repo.
func TestLiveFirstPrompt(t *testing.T) {
	if os.Getenv("PERSONA_LIVE_FIRST_PROMPT") != "1" {
		t.Skip("PERSONA_LIVE_FIRST_PROMPT=1 not set")
	}
	session, socket := os.Getenv("HERDR_SESSION"), os.Getenv("HERDR_SOCKET_PATH")
	if !strings.HasPrefix(session, "persona-firstprompt-") || filepath.Base(filepath.Dir(socket)) != session {
		t.Fatal("requires an isolated persona-firstprompt-* herdr session and its explicit socket")
	}
	launcher := os.Getenv("PERSONA_LIVE_LAUNCHER")
	if launcher == "" {
		t.Fatal("PERSONA_LIVE_LAUNCHER is required")
	}
	c := herdr.New()
	for _, pasteOnly := range []bool{false, true} {
		name := "normal"
		if pasteOnly {
			name = "swallowed-enter"
		}
		t.Run(name, func(t *testing.T) {
			n, home := testNode(t)
			n.dir = filepath.Join(home, ".persona")
			n.metas = map[string]quickMeta{}
			n.bgScans = map[string]*bgScan{}
			n.logger = log.New(os.Stdout, "", 0)
			cwd := mkdir(t, filepath.Join(home, "project"))
			config := mkdir(t, filepath.Join(home, "claude-test"))
			// Only this throwaway profile skips the onboarding wizard. Folder trust still goes through waitReady.
			if err := os.WriteFile(filepath.Join(config, ".claude.json"), []byte(`{"hasCompletedOnboarding":true,"theme":"dark"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			created, err := c.WorkspaceCreate(ctx, cwd, "first-prompt-test")
			if err != nil {
				t.Fatal(err)
			}
			pane, id := created.RootPane.ID, newUUID()
			h := &firstPromptLiveHost{herdrHost: &herdrHost{c: c}, pasteOnly: pasteOnly}
			n.hosts = host.NewRegistry(h)
			n.paths[id] = filepath.Join(config, "projects", nonAlnum.ReplaceAllString(cwd, "-"), id+".jsonl")
			// Isolated profile/settings: no production hooks or MCPs, and no settings/default writes to the real profile.
			cmd := "CLAUDE_CONFIG_DIR=" + shellQuote(config) + " HERDR_AGENT=claude " + shellQuote(launcher) +
				" --session-id " + id + " --name First-prompt-test --setting-sources '' --strict-mcp-config --mcp-config '{\"mcpServers\":{}}'" +
				" --settings '{\"disableAllHooks\":true}'"
			if err := c.PaneRun(ctx, pane, cmd); err != nil {
				t.Fatal(err)
			}
			a, err := n.waitReady(ctx, pane, nil)
			if err != nil && strings.Contains(err.Error(), "Bypass Permissions confirmation") {
				// Some launchers request bypass. Accept its first-run confirmation only in this disposable profile,
				// never in the user's configuration; the synthetic task explicitly forbids tools and file changes.
				if err = n.sendKeys(ctx, pane, "down", "enter"); err == nil {
					a, err = n.waitReady(ctx, pane, nil)
				}
			}
			if err != nil {
				dctx, done := context.WithTimeout(context.Background(), 5*time.Second)
				defer done()
				shown, _ := c.PaneRead(dctx, pane, "visible", 60)
				shown = strings.ReplaceAll(shown, home, "<test home>")
				t.Fatalf("wait for the test Agent: %v\n%s", err, shown)
			}
			img := filepath.Join(cwd, "test-image.png")
			writeFirstPromptImage(t, img)
			prompt := "This is a disposable first-prompt delivery test. Do not run tools or change files.\n" +
				strings.Repeat("Synthetic multiline context for image paste verification.\n", 26) +
				"Reply with FIRST_PROMPT_IMAGE_OK and say whether an image is attached.\n\n<attached_images>\n" + img + "\n</attached_images>"
			if err := n.firstPrompt(ctx, pane, id, prompt, a); err != nil {
				t.Fatal(err)
			}
			n.firstWait.Wait()
			if lost, ok := n.lostFirst.get(id); ok {
				t.Fatalf("first prompt not delivered: %s", lost.why)
			}
			var counts firstPromptLiveCounts
			waitWithin(t, time.Minute, "test Agent reply", func() bool {
				counts = countFirstPromptLive(n.paths[id])
				return counts.Replies > 0
			})
			if counts.Prompts != 1 || counts.Images != 1 || h.pastes != 1 {
				t.Fatalf("duplicate/missing delivery: counts %+v, pastes %d", counts, h.pastes)
			}
			if pasteOnly && h.enters < 2 {
				t.Fatal("swallowed Enter was not retried")
			}
			shown, err := c.PaneReadANSI(ctx, pane, "visible", 60)
			if err != nil {
				t.Fatal(err)
			}
			if in := screen.ClaudeInput(shown); !in.Box || in.Draft {
				t.Fatal("test Agent input box did not clear after submission")
			}
			if artifacts := os.Getenv("PERSONA_LIVE_ARTIFACTS"); artifacts != "" {
				// The capture shows synthetic data only. Redact temporary/user paths before keeping or publishing it.
				redact := func(s string) string {
					s = strings.ReplaceAll(s, home, "<test home>")
					if userHome, err := os.UserHomeDir(); err == nil {
						s = strings.ReplaceAll(s, userHome, "<user home>")
					}
					return s
				}
				for suffix, capture := range map[string]string{"before.ansi": h.before, "after.ansi": shown} {
					if capture != "" {
						if err := os.WriteFile(filepath.Join(artifacts, name+"-"+suffix), []byte(redact(capture)), 0o600); err != nil {
							t.Fatal(err)
						}
					}
				}
				receipt, _ := json.Marshal(map[string]any{"case": name, "pastes": h.pastes, "recovery_enters": h.enters, "recorded": counts, "input_empty": true})
				if err := os.WriteFile(filepath.Join(artifacts, name+"-receipt.json"), receipt, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Logf("LIVE %s: pastes=%d recovery_enters=%d user_prompts=%d images=%d replies=%d input_empty=true", name, h.pastes, h.enters, counts.Prompts, counts.Images, counts.Replies)
		})
	}
}

// Keep the real host/terminal; inject only a missing initial submit and one swallowed recovery Enter.
// This makes the race reproducible without claiming the normal run happened to reproduce it.
type firstPromptLiveHost struct {
	*herdrHost
	pasteOnly      bool
	pastes, enters int
	before         string
}

func (h *firstPromptLiveHost) Prompt(ctx context.Context, pane host.PaneRef, text string) error {
	h.pastes++
	if h.pasteOnly {
		return h.herdrHost.SendText(ctx, pane, "\x1b[200~"+text+"\x1b[201~")
	}
	return h.herdrHost.Prompt(ctx, pane, text)
}

func (h *firstPromptLiveHost) SendKeys(ctx context.Context, pane host.PaneRef, keys ...string) error {
	if len(keys) == 1 && keys[0] == "enter" && h.pastes > 0 {
		h.enters++
		if h.before == "" {
			h.before, _ = h.c.PaneReadANSI(ctx, pane.ID, "visible", 60)
		}
		if h.pasteOnly && h.enters == 1 {
			return nil
		}
	}
	return h.herdrHost.SendKeys(ctx, pane, keys...)
}

func writeFirstPromptImage(t *testing.T, path string) {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 768, 768))
	x := uint32(1)
	for i := 0; i < len(im.Pix); i += 4 {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		im.Pix[i], im.Pix[i+1], im.Pix[i+2], im.Pix[i+3] = byte(x), byte(x>>8), byte(x>>16), 255
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, im); err != nil {
		t.Fatal(err)
	}
}

type firstPromptLiveCounts struct{ Prompts, Images, Replies int }

func countFirstPromptLive(path string) (counts firstPromptLiveCounts) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 4096), 16<<20) // image prompts are a single megabytes-long JSON line
	for s.Scan() {
		var e struct {
			Type    string `json:"type"`
			Message struct {
				Content []struct{ Type, Text string } `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(s.Bytes(), &e) != nil {
			continue
		}
		for _, block := range e.Message.Content {
			if e.Type == "user" && block.Type == "image" {
				counts.Images++
			}
			if e.Type == "user" && strings.Contains(block.Text, "disposable first-prompt delivery test") {
				counts.Prompts++
			}
			if e.Type == "assistant" && strings.Contains(block.Text, "FIRST_PROMPT_IMAGE_OK") {
				counts.Replies++
			}
		}
	}
	return
}
