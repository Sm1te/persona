package extension

import (
	"bytes"
	"context"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nwverse/persona/server/internal/i18n"
)

func TestPrivateHTTPSGitMarketTokenAndPackage(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	if err := gitReadsConfigEnv(context.Background()); err != nil {
		t.Skipf("this machine's git cannot carry a token (TestGitTokenNeedsGit231 covers the refusal): %v", err)
	}
	work, root := t.TempDir(), t.TempDir()
	run(t, work, "init", "-q", "-b", "main")
	blob := []byte("archive fixture")
	if err := os.WriteFile(filepath.Join(work, "package.tar.gz"), blob, 0o600); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"market_version":1,"name":"Private git","extensions":[]}`)
	if err := os.WriteFile(filepath.Join(work, IndexFile), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	run(t, work, "add", "-A")
	run(t, work, "commit", "-q", "-m", "fixture")
	run(t, root, "clone", "-q", "--bare", work, filepath.Join(root, "market.git"))
	var authenticated, leaked atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "x-access-token" || password != "fixture-secret" {
			w.WriteHeader(401)
			return
		}
		authenticated.Add(1)
		if !strings.HasPrefix(r.URL.Path, "/market.git/") {
			leaked.Add(1)
			w.WriteHeader(403)
			return
		}
		cmd := exec.CommandContext(r.Context(), "git", "http-backend")
		cmd.Env = append(os.Environ(), "GIT_PROJECT_ROOT="+root, "GIT_HTTP_EXPORT_ALL=1", "REQUEST_METHOD="+r.Method, "PATH_INFO="+r.URL.Path, "QUERY_STRING="+r.URL.RawQuery, "CONTENT_TYPE="+r.Header.Get("Content-Type"), "CONTENT_LENGTH="+strconv.FormatInt(max(r.ContentLength, 0), 10))
		cmd.Stdin = io.LimitReader(r.Body, 4<<20)
		out, err := cmd.Output()
		if err != nil {
			t.Error("git backend failed")
			w.WriteHeader(500)
			return
		}
		header, body, found := bytes.Cut(out, []byte("\r\n\r\n"))
		if !found {
			t.Error("git backend response missing headers")
			w.WriteHeader(500)
			return
		}
		status := 200
		for _, line := range strings.Split(string(header), "\r\n") {
			key, value, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			if key == "Status" {
				status, _ = strconv.Atoi(strings.Fields(value)[0])
			} else {
				w.Header().Add(key, strings.TrimSpace(value))
			}
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	defer server.Close()
	cert := filepath.Join(t.TempDir(), "server-ca.pem")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_SSL_CAINFO", cert)
	m := &Market{Spec: MarketSpec{Kind: "git", URL: server.URL + "/market.git"}, Token: "fixture-secret", Dir: t.TempDir()}
	if _, err := m.FetchSigned(context.Background()); err != nil {
		t.Fatal(err)
	}
	pkg := &MarketPackage{URL: "package.tar.gz", SHA256: ociSum(blob), Size: int64(len(blob))}
	if _, _, err := m.Download(context.Background(), pkg, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	// Fetch again uses the original URL, not credentials embedded in origin config.
	if _, err := m.FetchSigned(context.Background()); err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(filepath.Join(m.checkout(), ".git", "config"))
	if err != nil || bytes.Contains(config, []byte("fixture-secret")) {
		t.Fatal("git credential persisted to checkout")
	}
	if leaked.Load() != 0 || authenticated.Load() == 0 {
		t.Fatal("git credential binding failed")
	}
	if m.SharesTokenWith(MarketSpec{Kind: "https", URL: server.URL + "/catalog"}) {
		t.Fatal("git token was shared with non-clone source")
	}
}

// A token clone refuses a git that would ignore GIT_CONFIG_COUNT (before 2.31) instead of cloning without the token,
// the refused redirects and the cleared credential helper.
func TestGitTokenNeedsGit231(t *testing.T) {
	was := gitVersionOutput
	t.Cleanup(func() { gitVersionOutput = was })
	for _, tc := range []struct {
		out     string
		err     error
		wantKey string
	}{
		{out: "git version 2.30.2\n", wantKey: "extension-market_git.git_too_old"},
		{out: "git version 1.9.5\n", wantKey: "extension-market_git.git_too_old"},
		{out: "git version 2.31.0\n"},
		{out: "git version 2.39.3 (Apple Git-146)\n"},
		{out: "git version 2.45.1.windows.1\n"},
		{out: "git version 3.0.0\n"},
		{err: exec.ErrNotFound, wantKey: "extension-market_git.git_missing"},
	} {
		gitVersionOutput = func(context.Context) ([]byte, error) { return []byte(tc.out), tc.err }
		err := gitReadsConfigEnv(context.Background())
		if tc.wantKey == "" {
			if err != nil {
				t.Errorf("%q: %v", tc.out, err)
			}
			continue
		}
		var msg *i18n.Message
		if !errors.As(err, &msg) || msg.Key != tc.wantKey {
			t.Errorf("%q %v: got %v, want %s", tc.out, tc.err, err, tc.wantKey)
		}
	}
	// The clone never starts: no git runs with the token after the version check refuses it.
	gitVersionOutput = func(context.Context) ([]byte, error) { return []byte("git version 2.30.2\n"), nil }
	m := &Market{Spec: MarketSpec{Kind: "git", URL: "https://git.example.com/market.git"}, Token: "fixture-secret", Dir: t.TempDir()}
	if _, err := m.FetchSigned(context.Background()); err == nil || !strings.Contains(err.Error(), "2.30") {
		t.Fatalf("token clone on git 2.30: %v", err)
	}
	if entries, _ := os.ReadDir(m.Dir); len(entries) != 0 {
		t.Fatalf("a clone was started: %v", entries)
	}
}
