package extension

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
)

// A git repository as a market (docs/extensions/extensions.md "Extension market"): the hub keeps a shallow clone in its data directory and
// reads the index and the packages from it, with its machine's own credentials (its SSH key) — for private hosts
// with no way to read one file with a token.

const gitFor = 3 * time.Minute

var (
	scpLike = lazyre.New(`^[A-Za-z0-9][A-Za-z0-9._-]*@[A-Za-z0-9][A-Za-z0-9.-]*:[A-Za-z0-9._~][A-Za-z0-9._/~-]*$`)
	gitRef  = lazyre.New(`^[A-Za-z0-9._/-]{1,200}$`)
)

// parseGit reads git:<url>[#ref], or a bare git@host:path / ssh:// URL.
func parseGit(s string) (MarketSpec, bool, error) {
	rest, explicit := strings.CutPrefix(s, "git:")
	if !explicit && !scpLike.MatchString(strings.SplitN(s, "#", 2)[0]) && !strings.HasPrefix(s, "ssh://") {
		return MarketSpec{}, false, nil
	}
	u, ref, _ := strings.Cut(rest, "#")
	if ref != "" && (!gitRef.MatchString(ref) || strings.Contains(ref, "..") || strings.HasPrefix(ref, "-")) {
		return MarketSpec{}, true, errors.New("分支名不对")
	}
	switch {
	case scpLike.MatchString(u):
	case strings.HasPrefix(u, "ssh://") || strings.HasPrefix(u, "https://"):
		if strings.ContainsAny(u, " \t\n") {
			return MarketSpec{}, true, errors.New("git 地址不对")
		}
	default:
		return MarketSpec{}, true, errors.New("git 市场写成 git:git@host:path.git、git:ssh://… 或 git:https://…（可以带 #分支）")
	}
	return MarketSpec{Kind: "git", URL: u, Ref: ref}, true, nil
}

// checkout is where the market's clone is.
func (m *Market) checkout() string { return filepath.Join(m.Dir, MarketID(m.Spec)) }

func (m *Market) git(ctx context.Context, dir string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, gitFor)
	defer cancel()
	// Only ssh and https: no local paths, no ext:: helpers. No prompts: a market without access fails, never waits.
	full := append([]string{"-c", "protocol.allow=never", "-c", "protocol.ssh.allow=always", "-c", "protocol.https.allow=always",
		"-c", "core.hooksPath=/dev/null"}, args...)
	if m.allowLocal {
		full = append([]string{"-c", "protocol.file.allow=always"}, full...)
	}
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=/bin/false")
	if m.Token != "" {
		u, err := url.Parse(m.Spec.URL)
		if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(m.Token, "\r\n") {
			return i18n.Err("extension-market_security.git_token")
		}
		if err := gitReadsConfigEnv(ctx); err != nil {
			return err
		}
		// Environment config avoids argv, clone URLs, and persisted .git/config secrets.
		header := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+m.Token))
		cmd.Env = append(cmd.Env, "GIT_CONFIG_COUNT=3", "GIT_CONFIG_KEY_0=http."+m.Spec.URL+".extraHeader", "GIT_CONFIG_VALUE_0="+header,
			"GIT_CONFIG_KEY_1=http.followRedirects", "GIT_CONFIG_VALUE_1=false", "GIT_CONFIG_KEY_2=credential.helper", "GIT_CONFIG_VALUE_2=")
	}
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes -o ConnectTimeout=20")
	}
	inOwnGroup(cmd)
	out, err := cmd.CombinedOutput()
	err = endGroup(cmd, err)
	if errors.Is(err, exec.ErrNotFound) { // no output to show: say what is missing (the Docker image has no git)
		return i18n.Err("extension-market_git.git_missing")
	}
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if m.Token != "" {
			msg = i18n.T(i18n.Default, "extension-market_security.request")
		}
		if ctx.Err() != nil {
			msg = i18n.T(i18n.Default, "extension-status.git_timeout")
		}
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = strings.TrimSpace(msg[i+1:])
		}
		return fmt.Errorf("git %s：%s", args[0], msg)
	}
	return nil
}

// gitVersion reads "git version 2.30.2" (also "… (Apple Git-146)", "….windows.1").
var gitVersion = lazyre.New(`^git version (\d+)\.(\d+)`)

// gitVersionOutput runs `git version`; a variable so tests can stand in for an older git.
var gitVersionOutput = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "git", "version").Output()
}

// gitReadsConfigEnv refuses a token clone on a git older than 2.31. A token's header, the refused redirects and the
// cleared credential helper reach git only through GIT_CONFIG_COUNT, which an older git ignores without a word: the
// clone would then run without them and fail with a misleading request error.
func gitReadsConfigEnv(ctx context.Context) error {
	out, err := gitVersionOutput(ctx)
	if errors.Is(err, exec.ErrNotFound) {
		return i18n.Err("extension-market_git.git_missing")
	}
	if err != nil {
		return fmt.Errorf("git version: %w", err)
	}
	m := gitVersion.FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		return fmt.Errorf("git version: unexpected output %q", strings.TrimSpace(string(out)))
	}
	major, _ := strconv.Atoi(m[1]) // digits: the pattern matched them
	minor, _ := strconv.Atoi(m[2])
	if major < 2 || major == 2 && minor < 31 {
		return i18n.Err("extension-market_git.git_too_old", i18n.Args{"version": m[1] + "." + m[2]})
	}
	return nil
}

// syncGit brings the clone to the branch's latest commit (cloning it the first time).
func (m *Market) syncGit(ctx context.Context) error {
	if m.Dir == "" {
		return errors.New("这个 hub 没有地方放 git 市场")
	}
	dir := m.checkout()
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := os.MkdirAll(m.Dir, 0o700); err != nil {
			return err
		}
		_ = os.RemoveAll(dir)
		args := []string{"clone", "--depth", "1", "--no-tags", "--single-branch"}
		if m.Spec.Ref != "" {
			args = append(args, "--branch", m.Spec.Ref)
		}
		if err := m.git(ctx, m.Dir, append(args, "--", m.Spec.URL, dir)...); err != nil {
			_ = os.RemoveAll(dir)
			return err
		}
		return nil
	}
	ref := m.Spec.Ref
	if ref == "" {
		ref = "HEAD"
	}
	if err := m.git(ctx, dir, "fetch", "--depth", "1", "--no-tags", "--", m.Spec.URL, ref); err != nil {
		return err
	}
	return m.git(ctx, dir, "reset", "--hard", "--quiet", "FETCH_HEAD")
}

// gitFile copies a file of the clone (a path inside it, not through a link out of it) into w.
func (m *Market) gitFile(rel string, w io.Writer, limit int64) error {
	clean := path.Clean("/" + rel)[1:]
	if clean == "" || clean != rel || strings.HasPrefix(clean, ".git/") {
		return errors.New("包的相对路径不能跳出市场")
	}
	root, err := filepath.EvalSymlinks(m.checkout())
	if err != nil {
		return err
	}
	p, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(clean)))
	if err != nil {
		return fmt.Errorf("仓库里没有 %s：%w", clean, os.ErrNotExist)
	}
	if r, err := filepath.Rel(root, p); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return errors.New("包的相对路径不能跳出市场")
	}
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(w, io.LimitReader(f, limit+1))
	if err == nil && n > limit {
		err = fmt.Errorf("大于 %d 字节", limit)
	}
	return err
}

// RemoveCheckout deletes a git market's clone (the market was removed).
func (m *Market) RemoveCheckout() {
	if m.Spec.Kind == "git" && m.Dir != "" {
		_ = os.RemoveAll(m.checkout())
	}
}
