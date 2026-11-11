package node

// The Mac's clipboard for the app's VNC (#1343): RFB's clipboard carries only Latin-1 and says nothing once the Mac has
// the text, so the app's pastes go through here, pbcopy and pbpaste, which return once it is there. Text only.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

var pbcopyPath, pbpastePath = "/usr/bin/pbcopy", "/usr/bin/pbpaste"

// clipboardEnv is pbcopy's and pbpaste's environment: the node's, in UTF-8. They take the text's encoding from the
// locale, and the resident node runs under launchd with none, where they read and write MacRoman: 中文 pasted on the Mac
// as ‰∏≠Êñá (#3075). pbcopy looks at LANG before LC_ALL, so all three are set (the last of a name wins).
func clipboardEnv() []string {
	return append(os.Environ(), "LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8", "LC_CTYPE=en_US.UTF-8")
}

// clipboardMost is the most text that goes either way.
const clipboardMost = 1 << 20

// macClipboard is computer.clipboard: {"set": text} puts text on the clipboard; {} reads it ({"text": …}).
func macClipboard(ctx context.Context, params json.RawMessage) (any, error) {
	if installOS != "darwin" {
		return nil, errors.New("只有 Mac 有这个剪贴板")
	}
	var p struct {
		Set *string `json:"set"`
	}
	_ = json.Unmarshal(params, &p)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if p.Set != nil {
		if len(*p.Set) > clipboardMost {
			return nil, errors.New("太长了")
		}
		cmd := exec.CommandContext(ctx, pbcopyPath)
		cmd.Env = clipboardEnv()
		cmd.Stdin = strings.NewReader(*p.Set)
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, errors.New("没放进剪贴板：" + strings.TrimSpace(string(out)))
		}
		return map[string]any{"ok": true}, nil
	}
	paste := exec.CommandContext(ctx, pbpastePath)
	paste.Env = clipboardEnv()
	out, err := paste.Output()
	if err != nil {
		return nil, errors.New("读不了剪贴板")
	}
	if len(out) > clipboardMost {
		out = out[:clipboardMost]
	}
	return map[string]any{"text": string(out)}, nil
}
