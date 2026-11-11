package node

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The clipboard goes through pbcopy and pbpaste (fakes here: a file stands for the clipboard).
func TestMacClipboard(t *testing.T) {
	dir := t.TempDir()
	board := filepath.Join(dir, "board")
	copyBin, pasteBin := filepath.Join(dir, "pbcopy"), filepath.Join(dir, "pbpaste")
	_ = os.WriteFile(copyBin, []byte("#!/bin/sh\ncat > "+board+"\n"), 0o755)
	_ = os.WriteFile(pasteBin, []byte("#!/bin/sh\ncat "+board+"\n"), 0o755)
	oldCopy, oldPaste, oldOS := pbcopyPath, pbpastePath, installOS
	pbcopyPath, pbpastePath, installOS = copyBin, pasteBin, "darwin"
	t.Cleanup(func() { pbcopyPath, pbpastePath, installOS = oldCopy, oldPaste, oldOS })

	if _, err := macClipboard(context.Background(), json.RawMessage(`{"set": "从手机来的 · ok"}`)); err != nil {
		t.Fatal(err)
	}
	got, err := macClipboard(context.Background(), json.RawMessage(`{}`))
	if err != nil || got.(map[string]any)["text"] != "从手机来的 · ok" {
		t.Fatalf("%v %v", got, err)
	}
	installOS = "linux"
	if _, err := macClipboard(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("only a Mac")
	}
}

// pbcopy and pbpaste run in UTF-8 whatever the node's locale (#3075): under launchd there is none, and they took the
// text as MacRoman. The fakes write down the locale they were given.
func TestMacClipboardRunsInUTF8(t *testing.T) {
	dir := t.TempDir()
	seen := filepath.Join(dir, "seen")
	copyBin, pasteBin := filepath.Join(dir, "pbcopy"), filepath.Join(dir, "pbpaste")
	script := "#!/bin/sh\ncat > /dev/null\necho \"$LANG $LC_ALL $LC_CTYPE\" >> " + seen + "\n"
	_ = os.WriteFile(copyBin, []byte(script), 0o755)
	_ = os.WriteFile(pasteBin, []byte(script), 0o755)
	oldCopy, oldPaste, oldOS := pbcopyPath, pbpastePath, installOS
	pbcopyPath, pbpastePath, installOS = copyBin, pasteBin, "darwin"
	t.Cleanup(func() { pbcopyPath, pbpastePath, installOS = oldCopy, oldPaste, oldOS })
	t.Setenv("LANG", "C")
	t.Setenv("LC_ALL", "C")

	if _, err := macClipboard(context.Background(), json.RawMessage(`{"set": "中文"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := macClipboard(context.Background(), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(seen)
	want := "en_US.UTF-8 en_US.UTF-8 en_US.UTF-8\n"
	if string(got) != want+want {
		t.Fatalf("pbcopy and pbpaste saw %q", got)
	}
}

// The real pbcopy and pbpaste in a launchd agent's environment (no LANG; the login's text encoding, MacRoman): what
// lands on this Mac's clipboard, read as any app reads it (in UTF-8), is the text sent, Chinese, emoji and a shell's
// special characters included, and reading it back gives the same. pbcopy and pbpaste in MacRoman would round-trip
// the bytes while every other app saw ‰∏≠Êñá. PERSONA_REAL_CLIPBOARD=1 on a Mac: it uses this Mac's clipboard, and
// puts back the text that was on it.
func TestMacClipboardRealRoundTrip(t *testing.T) {
	if runtime.GOOS != "darwin" || os.Getenv("PERSONA_REAL_CLIPBOARD") != "1" {
		t.Skip("PERSONA_REAL_CLIPBOARD=1 on a Mac")
	}
	utf8 := append(os.Environ(), "LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8")
	oldOS := installOS
	installOS = "darwin"
	t.Cleanup(func() { installOS = oldOS })
	read := exec.Command(pbpastePath)
	read.Env = utf8
	before, _ := read.Output()
	t.Cleanup(func() {
		c := exec.Command(pbcopyPath)
		c.Env = utf8
		c.Stdin = strings.NewReader(string(before))
		_ = c.Run()
	})
	for _, k := range []string{"LANG", "LC_ALL", "LC_CTYPE"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	t.Setenv("__CF_USER_TEXT_ENCODING", "0x1F5:0x0:0x0")
	text := "中文 · emoji 😀 · café\n$ \" ' \\ ` ! & | ;\ttab\r\nend"
	if _, err := macClipboard(context.Background(), mustJSON(map[string]any{"set": text})); err != nil {
		t.Fatal(err)
	}
	other := exec.Command(pbpastePath)
	other.Env = utf8
	if seen, err := other.Output(); err != nil || string(seen) != text {
		t.Fatalf("the Mac's clipboard has %q (%v)", seen, err)
	}
	got, err := macClipboard(context.Background(), json.RawMessage(`{}`))
	if err != nil || got.(map[string]any)["text"] != text {
		t.Fatalf("%q %v", got, err)
	}
}
