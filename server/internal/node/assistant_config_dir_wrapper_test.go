package node

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

// A wrapper launcher that sets its own CLAUDE_CONFIG_DIR only ever opens its own profile. A conversation whose recorded
// profile holds no copy of its transcript (the launcher changed since) used to follow the newest copy into ~/.claude
// without asking the wrapper, then hit the wrapper conflict and refuse every message. With a copy in the wrapper's own
// profile it resumes there; with none, the only copies are where the wrapper cannot go, and the conflict stays.
func TestAssistantResumeWithoutARecordedCopyStaysInTheWrapperProfile(t *testing.T) {
	for _, runtime := range []string{"", proto.AssistantRuntimeAgentSDK} {
		for _, scenario := range []string{"wrapper-holds-older-copy", "wrapper-holds-no-copy", "recorded-wrapper-holds-no-copy"} {
			t.Run("runtime="+runtime+"/"+scenario, func(t *testing.T) {
				f := sdkTest(t)
				n := f.n
				n.home = os.Getenv("HOME")
				n.dir = filepath.Join(n.home, ".persona")
				n.cdirs = newClaudeDirs()
				n.cdirs.scan = func() map[int]procEnv { return nil }
				t.Setenv("CLAUDE_CONFIG_DIR", "")
				os.Unsetenv("CLAUDE_CONFIG_DIR")
				profile := filepath.Join(n.home, ".config", "claude-code", "profiles", "work")
				writeFile(t, filepath.Join(n.home, ".zshrc"), `function work { CLAUDE_CONFIG_DIR=`+shellQuote(profile)+` command claude "$@"; }`+"\n")
				cwd := filepath.Join(n.dir, "assistant")
				id := "a5500000-0000-4000-8000-000000000401"
				wrapperCopy := scenario == "wrapper-holds-older-copy"
				if wrapperCopy {
					writeProfileTranscript(t, profile, cwd, id)
					ageTranscript(t, profile, id)
				}
				writeProfileTranscript(t, filepath.Join(n.home, ".claude"), cwd, id)
				recorded := filepath.Join(n.dir, "profiles", "cc-old") // no copy of the transcript
				if scenario == "recorded-wrapper-holds-no-copy" {
					recorded = profile
				}
				putSessionState(t, n, id, map[string]any{"launcher": "work", "cwd": cwd, "config_dir": recorded})
				params := map[string]any{"launcher": "work", "cwd": cwd, "text": "hello", "session_id": id, "runtime": runtime, "model_fingerprint": "wrapper-no-recorded-copy"}
				_, err := nodeCall(t, n, "assistant.send", params)
				if !wrapperCopy {
					if err == nil || !strings.Contains(err.Error(), "conflicts with the launcher's own profile") {
						t.Fatalf("resume with no copy the wrapper can open: %v, want the wrapper conflict", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("resume refused: %v", err)
				}
				if got := envValue(f.proc(id).cmd.Environ(), "CLAUDE_CONFIG_DIR"); got != profile {
					t.Fatalf("process profile %q, want %q", got, profile)
				}
				if st, _ := n.state.get(id); st.ConfigDir != profile {
					t.Fatalf("recorded profile %q, want %q", st.ConfigDir, profile)
				}
			})
		}
	}
}

// A resume that follows the newest copy into ~/.claude ("default") reads the launcher's default model from the same
// directory the process opens: ~/.claude/settings.json, not the recorded profile's (#2487, #4553).
func TestAssistantResumeIntoTheDefaultProfileReadsItsModelSetting(t *testing.T) {
	for _, runtime := range []string{"", proto.AssistantRuntimeAgentSDK} {
		t.Run("runtime="+runtime, func(t *testing.T) {
			f, real := resumeTest(t)
			n := f.n
			for _, k := range claudeModelVars {
				t.Setenv(k, "")
			}
			managed, managedDirs := claudeManagedSettings, claudeManagedSettingsDirs
			claudeManagedSettings, claudeManagedSettingsDirs = nil, nil
			t.Cleanup(func() { claudeManagedSettings, claudeManagedSettingsDirs = managed, managedDirs })
			id := "a5500000-0000-4000-8000-000000000402"
			cwd := filepath.Join(n.dir, "assistant")
			private := filepath.Join(n.dir, "profiles", "cc-old")
			writeProfileTranscript(t, private, cwd, id)
			ageTranscript(t, private, id)
			writeFile(t, filepath.Join(private, "settings.json"), `{"model":"opus"}`)
			home := filepath.Join(real, ".claude")
			writeProfileTranscript(t, home, cwd, id)
			writeFile(t, filepath.Join(home, "settings.json"), `{"model":"sonnet"}`)
			putSessionState(t, n, id, map[string]any{"launcher": "claude", "cwd": cwd, "config_dir": private})
			params := map[string]any{"launcher": "claude", "cwd": cwd, "text": "hello", "session_id": id, "runtime": runtime, "model_fingerprint": "default-newest-copy"}
			if _, err := nodeCall(t, n, "assistant.send", params); err != nil {
				t.Fatalf("resume failed: %v (child saw %q)", err, f.file("cli-profile"))
			}
			p := f.proc(id)
			if got, set := lookupEnv(p.cmd.Environ(), "CLAUDE_CONFIG_DIR"); set {
				t.Fatalf("process CLAUDE_CONFIG_DIR = %q, want unset (~/.claude)", got)
			}
			model := ""
			if runtime == "" {
				if got := f.file("cli-profile"); got != "unset\n" {
					t.Fatalf("CLI child CLAUDE_CONFIG_DIR = %q, want unset", got)
				}
				if i := indexOf(p.cmd.Args, "--model"); i >= 0 && i+1 < len(p.cmd.Args) {
					model = p.cmd.Args[i+1]
				}
			} else {
				model, _ = f.start(t)["model"].(string)
			}
			if model != "sonnet" {
				t.Fatalf("resumed on --model %q, want ~/.claude/settings.json's %q", model, "sonnet")
			}
			if st, _ := n.state.get(id); st.ConfigDir != "default" {
				t.Fatalf("recorded profile %q, want default", st.ConfigDir)
			}
		})
	}
}
