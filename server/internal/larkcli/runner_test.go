package larkcli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeConfig(t *testing.T, scenario string) Config {
	t.Helper()
	bin, err := filepath.Abs(filepath.Join("testdata", "fake-lark-cli.sh"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LARKCLI_FAKE_SCENARIO", scenario)
	return Config{Bin: bin, Profile: "example", Timeout: time.Second}
}

func TestRunnerSuccessJSONAndProfile(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "args.log")
	t.Setenv("LARKCLI_FAKE_LOG", logPath)
	r := New(fakeConfig(t, "success"))
	ctx := context.Background()

	auth, err := r.AuthStatus(ctx)
	if err != nil || !auth.LoggedIn || auth.AppID != "cli_example" || auth.UserName != "Example User" || auth.OpenID != "ou_example_user" {
		t.Fatalf("auth = %+v, err = %v", auth, err)
	}
	if got := auth.GrantedScopes; len(got) < 2 || got[0] != "im:chat:read" || got[1] != "im:message:readonly" {
		t.Fatalf("scopes = %v", got)
	}
	chats, err := r.ListChats(ctx, 10)
	if err != nil || len(chats.Chats) != 2 || chats.Chats[0].ChatID != "oc_example_group" {
		t.Fatalf("chats = %+v, err = %v", chats, err)
	}
	atMe, err := r.ListChatsAtMe(ctx, 10)
	if err != nil || len(atMe.Chats) != 1 || atMe.Chats[0].ChatID != "oc_example_group" {
		t.Fatalf("at-me chats = %+v, err = %v", atMe, err)
	}
	messages, err := r.ListMessages(ctx, "oc_example_group", "", "", 10)
	if err != nil || len(messages.Messages) != 2 || messages.Messages[0].MessageID != "om_example_older" {
		t.Fatalf("messages = %+v, err = %v", messages, err)
	}
	search, err := r.SearchMessages(ctx, "example", SearchOptions{})
	if err != nil || len(search.Messages) != 1 || search.Messages[0].ChatName != "Example Group" {
		t.Fatalf("search = %+v, err = %v", search, err)
	}
	if err := r.CheckVersion(ctx); err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(log)
	if !strings.Contains(text, "--profile example") || !strings.Contains(text, "--as user --format json") {
		t.Fatalf("profile or user JSON flags missing from %q", text)
	}
}

func TestSearchLimit(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "search-args.log")
	t.Setenv("LARKCLI_FAKE_LOG", logPath)
	r := New(fakeConfig(t, "success"))
	ctx := context.Background()
	tests := []struct {
		name        string
		kind        string
		limit       int
		command     string
		wantArgs    string
		withoutArgs string
	}{
		{name: "documents requested", kind: "docs", limit: 3, command: "drive +search", wantArgs: " --page-size 3 "},
		{name: "documents default", kind: "docs", command: "drive +search", withoutArgs: " --page-size "},
		{name: "documents maximum", kind: "docs", limit: 50, command: "drive +search", wantArgs: " --page-size 20 "},
		{name: "people requested", kind: "people", limit: 3, command: "contact +search-user", wantArgs: " --page-size 3 "},
		{name: "people default", kind: "people", command: "contact +search-user", withoutArgs: " --page-size "},
		{name: "people maximum", kind: "people", limit: 50, command: "contact +search-user", wantArgs: " --page-size 30 "},
		{name: "chats requested", kind: "chats", limit: 3, command: "im +chat-search", wantArgs: " --page-size 3 "},
		{name: "chats default", kind: "chats", command: "im +chat-search", withoutArgs: " --page-size "},
		{name: "chats maximum", kind: "chats", limit: 100, command: "im +chat-search", wantArgs: " --page-size 50 "},
		{name: "messages requested", kind: "messages", limit: 3, command: "im +messages-search", wantArgs: " --page-size 3 "},
		{name: "messages default", kind: "messages", command: "im +messages-search", wantArgs: " --page-size 20 "},
		{name: "messages maximum", kind: "messages", limit: 100, command: "im +messages-search", wantArgs: " --page-size 50 "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before, err := os.ReadFile(logPath)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if _, err := r.SearchLimit(ctx, tc.kind, "example", tc.limit); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			got := " " + strings.TrimSpace(strings.TrimPrefix(string(after), string(before))) + " "
			if !strings.Contains(got, " "+tc.command+" ") {
				t.Fatalf("search command = %q", got)
			}
			if tc.wantArgs != "" && !strings.Contains(got, tc.wantArgs) {
				t.Fatalf("search args = %q, want %q", got, tc.wantArgs)
			}
			if tc.withoutArgs != "" && strings.Contains(got, tc.withoutArgs) {
				t.Fatalf("default search overrode CLI page size: %q", got)
			}
		})
	}
	before, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Search(ctx, "docs", "example"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimPrefix(string(after), string(before)); strings.Contains(got, "--page-size") {
		t.Fatalf("legacy Search changed CLI defaults: %q", got)
	}
}

func TestRunnerWritesUseBotIdentity(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "args.log")
	t.Setenv("LARKCLI_FAKE_LOG", logPath)
	r := New(fakeConfig(t, "inbox"))
	if _, err := r.SendText(context.Background(), "oc_example_dm", "hello", "send-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReplyText(context.Background(), "om_example_direct", "reply", "send-2", true); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, "--as bot --format json") || !strings.Contains(got, "+messages-send --chat-id oc_example_dm --text hello --idempotency-key send-1") ||
		!strings.Contains(got, "+messages-reply --message-id om_example_direct --text reply --idempotency-key send-2 --reply-in-thread") {
		t.Fatalf("write args = %q", got)
	}
}

func TestManagedProfileConfigAndSecretStdin(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "init.log")
	t.Setenv("LARKCLI_FAKE_INIT_LOG", logPath)
	r := New(fakeConfig(t, "config_missing"))
	got, err := r.ConfigShow(context.Background())
	if err != nil || got.AppID != "" {
		t.Fatalf("missing config = %+v, err = %v", got, err)
	}
	if err := r.InitProfile(context.Background(), "persona", "cli_bot", "secret-value", "feishu"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !strings.Contains(text, "--app-id cli_bot") || !strings.Contains(text, "stdin=secret-value") {
		t.Fatalf("init record = %q", text)
	}
	argv := strings.SplitN(text, "|", 2)[0]
	if strings.Contains(argv, "secret-value") {
		t.Fatalf("secret entered argv: %q", text)
	}
}

func TestBlockingLoginPrintsLinkAndPollsProcess(t *testing.T) {
	release := filepath.Join(t.TempDir(), "release")
	t.Setenv("LARKCLI_FAKE_LOGIN_RELEASE", release)
	argsLog := filepath.Join(t.TempDir(), "login.args")
	t.Setenv("LARKCLI_FAKE_LOG", argsLog)
	r := New(fakeConfig(t, "login_wait"))
	p, err := r.StartLogin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	info := p.Info()
	if info.UserCode != "ABCD-1234" || !strings.Contains(info.URL, "accounts.feishu.cn") {
		t.Fatalf("login info = %+v", info)
	}
	args, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "auth login --scope ") || strings.Contains(string(args), "--domain all") || strings.Contains(string(args), "--no-wait") {
		t.Fatalf("login args = %q", args)
	}
	for _, scope := range ReadAsMeScopes {
		if !strings.Contains(string(args), scope) {
			t.Fatalf("login args do not contain scope %q: %q", scope, args)
		}
	}
	if done, err := p.Poll(); done || err != nil {
		t.Fatalf("pending poll = %v, %v", done, err)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
		done, err := p.Poll()
		if !done || err != nil {
			t.Fatalf("finished login poll = %v, %v", done, err)
		}
	case <-time.After(time.Second):
		p.Cancel()
		t.Fatal("login process did not finish")
	}
}

func TestProfilesAreNonSecretSummaries(t *testing.T) {
	profiles, err := New(fakeConfig(t, "success")).Profiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 3 || profiles[0].Name != "example" || profiles[0].AppID != "cli_example" || profiles[0].User != "Example User" || profiles[0].TokenStatus != "valid" {
		t.Fatalf("profiles = %+v", profiles)
	}
	if profiles[1].Name != "other" || profiles[1].User != "" {
		t.Fatalf("signed-out profile = %+v", profiles[1])
	}
}

func TestBlockingLoginCancellation(t *testing.T) {
	t.Setenv("LARKCLI_FAKE_SCENARIO", "login_wait")
	r := New(fakeConfig(t, "login_wait"))
	p, err := r.StartLogin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p.Cancel()
	select {
	case <-p.done:
		if done, err := p.Poll(); !done || err != nil {
			t.Fatalf("cancelled login poll = %v, %v", done, err)
		}
	case <-time.After(time.Second):
		t.Fatal("login process did not cancel")
	}
}

func TestAuthStatusStates(t *testing.T) {
	for _, tc := range []struct {
		name     string
		scenario string
		loggedIn bool
		expired  bool
	}{
		{"not logged in", "not_logged_in", false, false},
		{"expired", "expired", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth, err := New(fakeConfig(t, tc.scenario)).AuthStatus(context.Background())
			if err != nil || auth.LoggedIn != tc.loggedIn || auth.Expired != tc.expired {
				t.Fatalf("auth = %+v, err = %v", auth, err)
			}
		})
	}
}

func TestAuthRefreshLifecycleFixtures(t *testing.T) {
	now := time.Date(2026, 11, 10, 6, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	for _, tc := range []struct {
		name       string
		expectedIn bool
		expectedEx bool
		status     string
	}{
		{name: "needs refresh", expectedIn: true, status: "needs_refresh"},
		{name: "ready", expectedIn: true, status: "valid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scenario := "needs_refresh"
			if tc.name == "ready" {
				scenario = "ready"
			}
			cfg := fakeConfig(t, scenario)
			cfg.Now = func() time.Time { return now }
			auth, err := New(cfg).AuthStatus(context.Background())
			if err != nil || auth.LoggedIn != tc.expectedIn || auth.Expired != tc.expectedEx || auth.TokenStatus != tc.status {
				t.Fatalf("auth = %+v, err = %v", auth, err)
			}
			if !auth.ExpiresAt.Before(now) {
				t.Fatalf("fixture access token should already need refresh: %s", auth.ExpiresAt)
			}
			if days := auth.DaysUntilExpiry(now); days != 7 {
				t.Fatalf("refresh expiry reminder days = %d", days)
			}
		})
	}
}

func TestErrorCategories(t *testing.T) {
	for _, tc := range []struct {
		name     string
		scenario string
		want     ErrorKind
		call     func(*Runner) error
	}{
		{"missing scope", "missing_scope", KindMissingPermission, func(r *Runner) error {
			_, err := r.SearchMessages(context.Background(), "example", SearchOptions{})
			return err
		}},
		{"rate limited", "rate_limited", KindRateLimited, func(r *Runner) error {
			_, err := r.SearchMessages(context.Background(), "example", SearchOptions{})
			return err
		}},
		{"non-zero", "nonzero", KindOther, func(r *Runner) error {
			_, err := r.SearchMessages(context.Background(), "example", SearchOptions{})
			return err
		}},
		{"timeout", "timeout", KindTimeout, func(r *Runner) error {
			r.cfg.Timeout = 20 * time.Millisecond
			_, err := r.SearchMessages(context.Background(), "example", SearchOptions{})
			return err
		}},
		{"old version", "old", KindUnsupportedVersion, func(r *Runner) error { return r.CheckVersion(context.Background()) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call(New(fakeConfig(t, tc.scenario)))
			if KindOf(err) != tc.want {
				t.Fatalf("kind = %q, err = %v", KindOf(err), err)
			}
			if tc.want == KindMissingPermission {
				var e *Error
				if !errors.As(err, &e) || len(e.MissingScopes) != 1 || e.MissingScopes[0] != "im:chat:read" {
					t.Fatalf("missing scopes = %+v", err)
				}
			}
		})
	}
}

func TestStderrIsNotReturned(t *testing.T) {
	_, err := New(fakeConfig(t, "nonzero")).SearchMessages(context.Background(), "example", SearchOptions{})
	if strings.Contains(err.Error(), "message-body-example") {
		t.Fatalf("raw stderr leaked: %v", err)
	}
}

func TestNotInstalled(t *testing.T) {
	r := New(Config{Bin: filepath.Join(t.TempDir(), "missing-lark-cli")})
	if _, err := r.Version(context.Background()); KindOf(err) != KindNotInstalled {
		t.Fatalf("kind = %q, err = %v", KindOf(err), err)
	}
}

func TestContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New(fakeConfig(t, "timeout")).SearchMessages(ctx, "example", SearchOptions{})
	if KindOf(err) != KindTimeout || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, kind = %q", err, KindOf(err))
	}
}

func TestRecordedFixtures(t *testing.T) {
	for _, name := range []string{"auth_status.json", "auth_needs_refresh.json", "auth_ready.json", "auth_not_logged_in.json", "auth_expired.json", "auth_missing_scope.json", "chats.json", "messages.json", "search.json", "error_missing_scope.json", "error_rate_limited.json"} {
		raw, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(name, "auth_") && name != "error_missing_scope.json" {
			var v authStatusWire
			if err := jsonUnmarshal(raw, &v); err != nil {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if name == "chats.json" {
			var v ChatList
			if err := decodeData(raw, &v); err != nil || len(v.Chats) == 0 {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if name == "error_missing_scope.json" || name == "error_rate_limited.json" {
			want := KindMissingPermission
			if name == "error_rate_limited.json" {
				want = KindRateLimited
			}
			if got := classifyFailure(commandResult{stdout: raw}); got == nil || got.Kind != want {
				t.Errorf("%s: got %#v", name, got)
			}
			continue
		}
		var v MessagePage
		if err := decodeData(raw, &v); err != nil || len(v.Messages) == 0 {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Kept as a tiny indirection so the fixture contract test cannot accidentally
// accept a non-JSON sample through a helper with a different decoder.
func jsonUnmarshal(raw []byte, out any) error { return json.Unmarshal(raw, out) }
