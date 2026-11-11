package larkcli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/osproc"
)

const defaultTimeout = 15 * time.Second

const loginTimeout = 10 * time.Minute

// Config controls the executable and profile. It contains no credentials.
type Config struct {
	Bin     string
	Profile string
	Timeout time.Duration
	// InboxPoll and InboxPageLimit are consumed by the built-in larkcli
	// connector. Keeping them here lets tests and embedders construct the
	// adapter and connector together without another configuration object.
	InboxPoll      time.Duration
	InboxPageLimit int
	// Now is injectable for expiry tests; production uses time.Now.
	Now func() time.Time
}

// Runner executes lark-cli with a bounded context and decodes only JSON output.
// It never logs stdout or stderr. The latter is captured solely to classify a
// structured CLI error without retaining its message body in the returned error.
type Runner struct {
	cfg Config

	mu   sync.Mutex
	path string
}

// New creates a runner. An empty Bin means lark-cli on PATH (or in the login
// shell's PATH, matching the L1 assistant wrapper).
func New(cfg Config) *Runner {
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	return &Runner{cfg: cfg}
}

// NewRunner is a compact constructor for callers that already have the three
// configuration values from lark.cli.
func NewRunner(bin, profile string, timeout time.Duration) *Runner {
	return New(Config{Bin: bin, Profile: profile, Timeout: timeout})
}

// Profile returns the configured lark-cli profile name. It contains no
// credentials and is used only to make user-facing commands point at the same
// profile as the runner.
func (r *Runner) Profile() string {
	if r == nil {
		return ""
	}
	return r.cfg.Profile
}

// Run executes a read-only user command and returns its JSON stdout. It is the
// generic escape hatch for adapter additions; typed methods below own the L2
// command shapes.
func (r *Runner) Run(ctx context.Context, args ...string) ([]byte, error) {
	return r.execute(ctx, args, outputJSON)
}

// RunJSON is an explicit alias for Run for callers that want to document the
// output contract at the call site.
func (r *Runner) RunJSON(ctx context.Context, args ...string) ([]byte, error) {
	return r.Run(ctx, args...)
}

// RunAs executes a read or write command with an explicit lark-cli identity.
// Reads in this adapter use user by default; Inbox replies deliberately use bot
// because the default Lark app cannot send as the user.
func (r *Runner) RunAs(ctx context.Context, identity string, args ...string) ([]byte, error) {
	if identity == "" {
		identity = "user"
	}
	return r.executeAs(ctx, args, outputJSON, identity)
}

func (r *Runner) now() time.Time {
	if r != nil && r.cfg.Now != nil {
		return r.cfg.Now()
	}
	return time.Now()
}

// Version returns the executable's semantic version as printed by --version.
func (r *Runner) Version(ctx context.Context) (string, error) {
	out, err := r.execute(ctx, []string{"--version"}, outputVersion)
	if err != nil {
		return "", err
	}
	version := parseVersion(string(out))
	if version == "" {
		return "", cliError(KindOther, errors.New("invalid version output"))
	}
	return version, nil
}

// CheckVersion rejects versions older than the pinned minimum.
func (r *Runner) CheckVersion(ctx context.Context) error {
	got, err := r.Version(ctx)
	if err != nil {
		return err
	}
	if compareVersion(got, MinimumVersion) < 0 {
		return &Error{Kind: KindUnsupportedVersion, Version: got, Minimum: MinimumVersion}
	}
	return nil
}

// AuthStatus reads the user identity without touching credentials or changing
// lark-cli configuration.
func (r *Runner) AuthStatus(ctx context.Context) (AuthStatus, error) {
	out, err := r.execute(ctx, []string{"auth", "status"}, outputAuth)
	if err != nil {
		return AuthStatus{}, err
	}
	var raw authStatusWire
	if err := json.Unmarshal(out, &raw); err != nil {
		return AuthStatus{}, cliError(KindOther, errors.New("invalid auth status JSON"))
	}
	return raw.result(r.now()), nil
}

// ConfigInfo is the public, non-secret part of one lark-cli profile. The CLI
// has emitted both JSON and key/value output across its releases, so callers
// should use AppID and ignore the original presentation.
type ConfigInfo struct {
	AppID string
	Brand string
}

// ConfigShow reads the selected profile's app binding. It never returns the
// app secret or any other credential material.
func (r *Runner) ConfigShow(ctx context.Context) (ConfigInfo, error) {
	out, err := r.execute(ctx, []string{"config", "show"}, outputRaw)
	if err != nil {
		return ConfigInfo{}, err
	}
	return parseConfigInfo(out), nil
}

// InitProfile creates or updates a named profile. The secret is written only
// to stdin; it is never put in argv, an error, or a log.
func (r *Runner) InitProfile(ctx context.Context, name, appID, secret, brand string) error {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(appID) == "" || secret == "" {
		return errors.New("lark-cli profile initialization needs a name, app id and secret")
	}
	args := []string{"config", "init", "--name", name, "--app-id", appID, "--app-secret-stdin", "--brand", brand}
	_, err := r.executeInput(ctx, args, outputRaw, "", []byte(secret))
	return err
}

// AuthLogout signs the selected profile out. lark-cli owns the credential
// store; this method only asks it to remove the login.
func (r *Runner) AuthLogout(ctx context.Context) error {
	_, err := r.execute(ctx, []string{"auth", "logout"}, outputRaw)
	return err
}

// LoginInfo is the device-flow link printed by lark-cli auth login.
type LoginInfo struct {
	URL      string
	UserCode string
}

// LoginProcess is a blocking lark-cli auth login process. lark-cli itself
// performs the device-flow polling; Persona only exposes its link and watches
// the process until it exits.
type LoginProcess struct {
	mu     sync.Mutex
	info   LoginInfo
	err    error
	ready  chan struct{}
	done   chan struct{}
	cancel context.CancelFunc
	once   sync.Once
}

// StartLogin starts the connector's least-privilege `auth login --scope`
// request without --no-wait. The command
// has its own ten-minute lifetime so the HTTP request that began the login may
// finish while the process continues in the background.
func (r *Runner) StartLogin(ctx context.Context) (*LoginProcess, error) {
	bin, err := r.binary(ctx)
	if err != nil {
		return nil, err
	}
	loginCtx, cancel := context.WithTimeout(context.Background(), loginTimeout)
	p := &LoginProcess{ready: make(chan struct{}), done: make(chan struct{}), cancel: cancel}
	argv := r.profileArgs([]string{"auth", "login", "--scope", strings.Join(ReadAsMeScopes, " ")})
	cmd := exec.CommandContext(loginCtx, bin, argv...)
	cmd.Env = cliEnv(bin)
	// lark-cli may launch a child process of its own. Killing only the shell
	// wrapper leaves that child holding stdout open, so terminate the whole
	// process group on cancellation or timeout.
	cmd.SysProcAttr = osproc.GroupAttr()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, cliError(KindOther, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, cliError(KindOther, err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, cliError(KindOther, err)
	}
	finished := make(chan struct{})
	go func(pid int) {
		select {
		case <-loginCtx.Done():
			select {
			case <-finished:
				return
			default:
				_ = osproc.StopGroup(pid, true)
				// A child forked while that signal goes out can miss it (macOS) and live on, adopted by launchd, holding
				// stdout open so the login reports done only once it exits: sweep until the group is empty (#4004).
				osproc.SweepGroup(pid, time.Second)
			}
		case <-finished:
		}
	}(cmd.Process.Pid)
	var readers sync.WaitGroup
	read := func(in io.Reader) {
		defer readers.Done()
		s := bufio.NewScanner(io.LimitReader(in, 1<<20))
		for s.Scan() {
			p.line(s.Text())
		}
	}
	readers.Add(2)
	go read(stdout)
	go read(stderr)
	go func() {
		readers.Wait()
		err := cmd.Wait()
		p.mu.Lock()
		if loginCtx.Err() == context.DeadlineExceeded {
			p.err = &Error{Kind: KindTimeout}
		} else if err != nil && loginCtx.Err() == nil {
			p.err = cliError(KindOther, err)
		}
		p.mu.Unlock()
		close(finished)
		cancel()
		close(p.done)
	}()
	// The URL is normally printed immediately. A short independent wait keeps
	// a malformed or very old CLI from making the request hang forever.
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-p.ready:
		return p, nil
	case <-p.done:
		return p, p.startError()
	case <-timer.C:
		p.Cancel()
		return nil, &Error{Kind: KindTimeout}
	}
}

// Profiles reads the named lark-cli profiles without returning secrets. The
// profile list command emits JSON by default, so no output-format flag is
// needed (and older CLI releases reject one).
func (r *Runner) Profiles(ctx context.Context) ([]Profile, error) {
	out, err := r.executeNoProfile(ctx, []string{"profile", "list"}, outputRaw)
	if err != nil {
		return nil, err
	}
	var raw []profileWire
	if err := json.Unmarshal(out, &raw); err != nil {
		var envelope struct {
			Profiles []profileWire `json:"profiles"`
		}
		if err := json.Unmarshal(out, &envelope); err != nil {
			return nil, cliError(KindOther, errors.New("invalid profile list JSON"))
		}
		raw = envelope.Profiles
	}
	profiles := make([]Profile, 0, len(raw))
	for _, p := range raw {
		name := strings.TrimSpace(firstNonEmpty(p.Name, p.NameSnake))
		if name == "" {
			continue
		}
		profiles = append(profiles, Profile{
			Name:        name,
			AppID:       strings.TrimSpace(firstNonEmpty(p.AppID, p.AppIDSnake)),
			Brand:       strings.TrimSpace(p.Brand),
			User:        strings.TrimSpace(firstNonEmpty(p.User, p.UserSnake)),
			TokenStatus: strings.TrimSpace(firstNonEmpty(p.TokenStatus, p.TokenStatusSnake)),
		})
	}
	return profiles, nil
}

type profileWire struct {
	Name             string `json:"name"`
	NameSnake        string `json:"profile_name"`
	AppID            string `json:"appId"`
	AppIDSnake       string `json:"app_id"`
	Brand            string `json:"brand"`
	User             string `json:"user"`
	UserSnake        string `json:"user_name"`
	TokenStatus      string `json:"tokenStatus"`
	TokenStatusSnake string `json:"token_status"`
}

func (r *Runner) profileArgs(args []string) []string {
	argv := make([]string, 0, len(args)+2)
	if r.cfg.Profile != "" {
		argv = append(argv, "--profile", r.cfg.Profile)
	}
	return append(argv, args...)
}

func (p *LoginProcess) line(line string) {
	if info, ok := parseLoginInfo(line); ok {
		p.mu.Lock()
		if p.info.URL == "" {
			p.info = info
			p.once.Do(func() { close(p.ready) })
		}
		p.mu.Unlock()
	}
}

func (p *LoginProcess) startError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	return errors.New("lark-cli did not provide a device verification link")
}

// Info returns the URL and user code after StartLogin has observed them.
func (p *LoginProcess) Info() LoginInfo {
	if p == nil {
		return LoginInfo{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.info
}

// Poll reports whether the process exited. It never returns command output.
func (p *LoginProcess) Poll() (bool, error) {
	if p == nil {
		return true, errors.New("no lark-cli login process")
	}
	select {
	case <-p.done:
		p.mu.Lock()
		err := p.err
		p.mu.Unlock()
		return true, err
	default:
		return false, nil
	}
}

// Cancel stops the process and is safe to call more than once.
func (p *LoginProcess) Cancel() {
	if p != nil && p.cancel != nil {
		p.cancel()
	}
}

// ListChats calls the recent chat shortcut. It includes both group and direct
// chats and asks the CLI for newest activity first.
func (r *Runner) ListChats(ctx context.Context, limit int) (ChatList, error) {
	if limit <= 0 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}
	out, err := r.execute(ctx, []string{"im", "+chat-list", "--types", "p2p,group", "--sort", "active_time", "--page-size", fmt.Sprint(limit)}, outputJSON)
	if err != nil {
		return ChatList{}, err
	}
	var page ChatList
	if err := decodeData(out, &page); err != nil {
		return ChatList{}, cliError(KindOther, errors.New("invalid chat list JSON"))
	}
	return page, nil
}

// ListChatsAtMe finds chats with a message mentioning the user and normalizes
// the message results to the same chat list shape as ListChats.
func (r *Runner) ListChatsAtMe(ctx context.Context, limit int) (ChatList, error) {
	if limit <= 0 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}
	page, err := r.SearchMessages(ctx, "", SearchOptions{AtMe: true, Limit: limit})
	if err != nil {
		return ChatList{}, err
	}
	return ChatListFromMessages(page.Messages, limit), nil
}

// ChatListFromMessages keeps one row per conversation while preserving the
// first result's activity order.
func ChatListFromMessages(messages []Message, limit int) ChatList {
	seen := map[string]bool{}
	out := ChatList{}
	for _, message := range messages {
		if message.ChatID == "" || seen[message.ChatID] {
			continue
		}
		seen[message.ChatID] = true
		out.Chats = append(out.Chats, Chat{ChatID: message.ChatID, Name: message.ChatName, ChatType: message.ChatType})
		if len(out.Chats) == limit {
			break
		}
	}
	out.Total = len(out.Chats)
	return out
}

// ListMessages reads one chat or thread. The CLI's newest-first response is
// reversed so the connector's historical tool contract remains oldest first.
func (r *Runner) ListMessages(ctx context.Context, chatID, thread, before string, limit int) (MessagePage, error) {
	if limit <= 0 {
		limit = 40
	}
	if limit > 50 {
		limit = 50
	}
	args := []string{"im"}
	if thread != "" {
		args = append(args, "+threads-messages-list", "--thread", thread)
	} else {
		if chatID == "" {
			return MessagePage{}, errors.New("chat_id is required")
		}
		args = append(args, "+chat-messages-list", "--chat-id", chatID)
	}
	args = append(args, "--order", "desc", "--page-size", fmt.Sprint(limit), "--no-reactions")
	if before != "" {
		args = append(args, "--page-token", before)
	}
	out, err := r.execute(ctx, args, outputJSON)
	if err != nil {
		return MessagePage{}, err
	}
	var page MessagePage
	if err := decodeData(out, &page); err != nil {
		return MessagePage{}, cliError(KindOther, errors.New("invalid message list JSON"))
	}
	for i, j := 0, len(page.Messages)-1; i < j; i, j = i+1, j-1 {
		page.Messages[i], page.Messages[j] = page.Messages[j], page.Messages[i]
	}
	return page, nil
}

// SearchMessages searches the user's messages. It deliberately disables
// reaction enrichment: reactions are an optional second permission and are not
// needed by the read-tool contract.
func (r *Runner) SearchMessages(ctx context.Context, query string, opts SearchOptions) (MessagePage, error) {
	args := []string{"im", "+messages-search"}
	if query != "" {
		args = append(args, "--query", query)
	}
	for _, pair := range [][2]string{{"--chat-id", opts.ChatID}, {"--sender", opts.Sender}, {"--chat-type", opts.ChatType}, {"--start", opts.Start}, {"--end", opts.End}} {
		if pair[1] != "" {
			args = append(args, pair[0], pair[1])
		}
	}
	if opts.AtMe {
		args = append(args, "--is-at-me")
	}
	args = append(args, "--page-size", fmt.Sprint(opts.normalizedLimit()), "--no-reactions")
	if opts.PageToken != "" {
		args = append(args, "--page-token", opts.PageToken)
	}
	out, err := r.execute(ctx, args, outputJSON)
	if err != nil {
		return MessagePage{}, err
	}
	var page MessagePage
	if err := decodeData(out, &page); err != nil {
		return MessagePage{}, cliError(KindOther, errors.New("invalid message search JSON"))
	}
	return page, nil
}

// InspectDocument resolves a document or wiki link to the title and the
// underlying Drive metadata. It is deliberately read-only and always runs as
// the lark-cli user identity.
// OwnedDocument is one document the signed-in user owns, from `drive +search --mine`.
type OwnedDocument struct {
	URL     string
	Token   string
	Type    string
	Title   string
	Owner   string
	Updated time.Time
	Opened  time.Time
}

// OwnedDocuments lists documents the user owns, most recently edited first, one page of at most 20 (`drive +search
// --mine --sort edit_time`). next is the token for the following page, empty at the end.
func (r *Runner) OwnedDocuments(ctx context.Context, pageToken string) (docs []OwnedDocument, next string, err error) {
	// Only types whose comments lark-cli can list: folders and mind notes would only fail every read.
	args := []string{"drive", "+search", "--mine", "--sort", "edit_time", "--page-size", "20", "--doc-types", commentableDocTypes}
	if pageToken != "" {
		args = append(args, "--page-token", pageToken)
	}
	out, err := r.execute(ctx, args, outputJSON)
	if err != nil {
		return nil, "", err
	}
	var raw struct {
		HasMore   bool   `json:"has_more"`
		PageToken string `json:"page_token"`
		Results   []struct {
			Title string `json:"title_highlighted"`
			Meta  struct {
				URL      string `json:"url"`
				Token    string `json:"token"`
				DocTypes string `json:"doc_types"`
				Owner    string `json:"owner_id"`
				Updated  int64  `json:"update_time"`
				Opened   int64  `json:"last_open_time"`
			} `json:"result_meta"`
		} `json:"results"`
	}
	if err := decodeData(out, &raw); err != nil {
		return nil, "", cliError(KindOther, errors.New("invalid document search JSON"))
	}
	for _, res := range raw.Results {
		if res.Meta.URL == "" {
			continue
		}
		d := OwnedDocument{URL: res.Meta.URL, Token: res.Meta.Token, Type: strings.ToLower(res.Meta.DocTypes), Title: stripHighlight(res.Title), Owner: res.Meta.Owner}
		if res.Meta.Updated > 0 {
			d.Updated = time.Unix(res.Meta.Updated, 0).UTC()
		}
		if res.Meta.Opened > 0 {
			d.Opened = time.Unix(res.Meta.Opened, 0).UTC()
		}
		docs = append(docs, d)
	}
	if raw.HasMore {
		next = raw.PageToken
	}
	return docs, next, nil
}

// commentableDocTypes are the drive search types `drive +list-comments` accepts (lark-cli 1.0.99).
const commentableDocTypes = "doc,docx,sheet,bitable,file,slides,wiki"

// stripHighlight drops the search's <em> marks from a title.
func stripHighlight(s string) string {
	return strings.TrimSpace(strings.NewReplacer("<em>", "", "</em>", "", "<b>", "", "</b>", "").Replace(s))
}

// DocumentOwner returns the open_id of a document's owner (`drive metas batch_query`). token and docType are the
// underlying document's, as InspectDocument returns them (a wiki link is unwrapped there).
func (r *Runner) DocumentOwner(ctx context.Context, token, docType string) (string, error) {
	if strings.TrimSpace(token) == "" || strings.TrimSpace(docType) == "" {
		return "", errors.New("document token and type are required")
	}
	body, err := json.Marshal(map[string]any{"request_docs": []map[string]string{{"doc_token": token, "doc_type": docType}}})
	if err != nil {
		return "", err
	}
	out, err := r.execute(ctx, []string{"drive", "metas", "batch_query", "--user-id-type", "open_id", "--data", string(body)}, outputJSON)
	if err != nil {
		return "", err
	}
	var raw struct {
		Metas []struct {
			Token string `json:"doc_token"`
			Owner string `json:"owner_id"`
		} `json:"metas"`
		Failed []struct {
			Code int `json:"code"`
		} `json:"failed_list"`
	}
	if err := decodeData(out, &raw); err != nil {
		return "", cliError(KindOther, errors.New("invalid document metadata JSON"))
	}
	for _, meta := range raw.Metas {
		if meta.Owner != "" {
			return meta.Owner, nil
		}
	}
	if len(raw.Failed) > 0 {
		return "", &Error{Kind: KindMissingPermission}
	}
	return "", nil
}

func (r *Runner) InspectDocument(ctx context.Context, link string) (Document, error) {
	if strings.TrimSpace(link) == "" {
		return Document{}, errors.New("document URL is required")
	}
	out, err := r.execute(ctx, []string{"drive", "+inspect", "--url", link}, outputJSON)
	if err != nil {
		return Document{}, err
	}
	var raw struct {
		Document
		Canonical string `json:"canonical_url"`
		FileToken string `json:"file_token"`
		FileType  string `json:"file_type"`
		ObjToken  string `json:"obj_token"`
		ObjType   string `json:"obj_type"`
		Metas     []struct {
			URL   string `json:"url"`
			Token string `json:"doc_token"`
			Type  string `json:"doc_type"`
			Title string `json:"title"`
		} `json:"metas"`
		Failed []struct {
			Code int `json:"code"`
		} `json:"failed_list"`
	}
	if err := decodeData(out, &raw); err != nil {
		return Document{}, cliError(KindOther, errors.New("invalid document metadata JSON"))
	}
	if len(raw.Failed) > 0 && len(raw.Metas) == 0 {
		return Document{}, &Error{Kind: KindMissingPermission}
	}
	d := raw.Document
	if d.URL == "" {
		d.URL = link
	}
	if d.CanonicalURL == "" {
		d.CanonicalURL = raw.Canonical
	}
	if d.Token == "" {
		d.Token = raw.FileToken
	}
	if d.Token == "" {
		d.Token = raw.ObjToken
	}
	if d.Type == "" {
		d.Type = raw.FileType
	}
	if d.Type == "" {
		d.Type = raw.ObjType
	}
	if len(raw.Metas) > 0 {
		meta := raw.Metas[0]
		if d.URL == "" {
			d.URL = meta.URL
		}
		if d.CanonicalURL == "" {
			d.CanonicalURL = meta.URL
		}
		if d.Token == "" {
			d.Token = meta.Token
		}
		if d.Type == "" {
			d.Type = meta.Type
		}
		if d.Title == "" {
			d.Title = meta.Title
		}
	}
	return d, nil
}

// ListComments lists unresolved comments on a document. The returned page
// token is an opaque value for the next page in the same poll.
func (r *Runner) ListComments(ctx context.Context, link, pageToken string, limit int) (CommentPage, error) {
	if strings.TrimSpace(link) == "" {
		return CommentPage{}, errors.New("document URL is required")
	}
	args := []string{"drive", "+list-comments", "--url", link, "--solved-status", "false", "--page-size", fmt.Sprint(pageLimit(limit))}
	if pageToken != "" {
		args = append(args, "--page-token", pageToken)
	}
	out, err := r.execute(ctx, args, outputJSON)
	if err != nil {
		return CommentPage{}, err
	}
	var raw struct {
		Items     []Comment `json:"items"`
		Comments  []Comment `json:"comments"`
		HasMore   bool      `json:"has_more,omitempty"`
		PageToken string    `json:"page_token,omitempty"`
	}
	if err := decodeData(out, &raw); err != nil {
		return CommentPage{}, cliError(KindOther, errors.New("invalid document comments JSON"))
	}
	if len(raw.Items) == 0 {
		raw.Items = raw.Comments
	}
	return CommentPage{Comments: raw.Items, HasMore: raw.HasMore, PageToken: raw.PageToken}, nil
}

// ListReplies lists one comment's root body and replies. The first page's
// first item is the root body according to lark-cli's documented contract.
func (r *Runner) ListReplies(ctx context.Context, link, commentID, pageToken string, limit int) (ReplyPage, error) {
	if strings.TrimSpace(link) == "" || strings.TrimSpace(commentID) == "" {
		return ReplyPage{}, errors.New("document URL and comment id are required")
	}
	args := []string{"drive", "+list-replies", "--url", link, "--comment-id", commentID, "--page-size", fmt.Sprint(pageLimit(limit))}
	if pageToken != "" {
		args = append(args, "--page-token", pageToken)
	}
	out, err := r.execute(ctx, args, outputJSON)
	if err != nil {
		return ReplyPage{}, err
	}
	var raw struct {
		Items     []Reply `json:"items"`
		Replies   []Reply `json:"replies"`
		HasMore   bool    `json:"has_more,omitempty"`
		PageToken string  `json:"page_token,omitempty"`
	}
	if err := decodeData(out, &raw); err != nil {
		return ReplyPage{}, cliError(KindOther, errors.New("invalid document replies JSON"))
	}
	if len(raw.Items) == 0 {
		raw.Items = raw.Replies
	}
	return ReplyPage{Replies: raw.Items, HasMore: raw.HasMore, PageToken: raw.PageToken}, nil
}

// ListDocumentComments is a descriptive alias used by the connector.
func (r *Runner) ListDocumentComments(ctx context.Context, link, pageToken string, limit int) (CommentPage, error) {
	return r.ListComments(ctx, link, pageToken, limit)
}

func (r *Runner) ListWikiSpaces(ctx context.Context, pageToken string, limit int) (WikiSpaceList, error) {
	args := []string{"wiki", "spaces", "list", "--page-size", fmt.Sprint(wikiPageLimit(limit))}
	if pageToken != "" {
		args = append(args, "--page-token", pageToken)
	}
	out, err := r.execute(ctx, args, outputJSON)
	if err != nil {
		return WikiSpaceList{}, err
	}
	var raw struct {
		Items     []WikiSpace `json:"items"`
		Spaces    []WikiSpace `json:"spaces"`
		HasMore   bool        `json:"has_more,omitempty"`
		PageToken string      `json:"page_token,omitempty"`
	}
	if err := decodeData(out, &raw); err != nil {
		return WikiSpaceList{}, cliError(KindOther, errors.New("invalid wiki spaces JSON"))
	}
	if len(raw.Items) == 0 {
		raw.Items = raw.Spaces
	}
	return WikiSpaceList{Spaces: raw.Items, HasMore: raw.HasMore, PageToken: raw.PageToken}, nil
}

func (r *Runner) ListWikiNodes(ctx context.Context, spaceID, parentToken, pageToken string, limit int) (WikiNodeList, error) {
	if strings.TrimSpace(spaceID) == "" {
		return WikiNodeList{}, errors.New("wiki space id is required")
	}
	args := []string{"wiki", "nodes", "list", "--space-id", spaceID, "--page-size", fmt.Sprint(wikiPageLimit(limit))}
	if parentToken != "" {
		args = append(args, "--parent-node-token", parentToken)
	}
	if pageToken != "" {
		args = append(args, "--page-token", pageToken)
	}
	out, err := r.execute(ctx, args, outputJSON)
	if err != nil {
		return WikiNodeList{}, err
	}
	var raw struct {
		Items     []WikiNode `json:"items"`
		Nodes     []WikiNode `json:"nodes"`
		HasMore   bool       `json:"has_more,omitempty"`
		PageToken string     `json:"page_token,omitempty"`
	}
	if err := decodeData(out, &raw); err != nil {
		return WikiNodeList{}, cliError(KindOther, errors.New("invalid wiki nodes JSON"))
	}
	if len(raw.Items) == 0 {
		raw.Items = raw.Nodes
	}
	return WikiNodeList{Nodes: raw.Items, HasMore: raw.HasMore, PageToken: raw.PageToken}, nil
}

func pageLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 100 {
		return 100
	}
	return limit
}

func wikiPageLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 50 {
		return 50
	}
	return limit
}

// Search runs the lark_search kind mappings that are safe and read-only in L2.
// Message search is typed; the other supported kinds are returned as the CLI's
// normalized JSON data because their schemas belong to later connector steps.
func (r *Runner) Search(ctx context.Context, kind, query string) (json.RawMessage, error) {
	return r.SearchLimit(ctx, kind, query, 0)
}

// SearchLimit bounds the requested search page. A nonpositive limit preserves
// the CLI's default page size; message search retains its existing default.
func (r *Runner) SearchLimit(ctx context.Context, kind, query string, limit int) (json.RawMessage, error) {
	var args []string
	maximum := 0
	switch kind {
	case "", "messages":
		page, err := r.SearchMessages(ctx, query, SearchOptions{Limit: limit})
		if err != nil {
			return nil, err
		}
		return json.Marshal(page)
	case "chats":
		args = []string{"im", "+chat-search", "--query", query}
		maximum = 50
	case "docs":
		args = []string{"drive", "+search", "--query", query}
		maximum = 20
	case "people":
		args = []string{"contact", "+search-user", "--query", query}
		maximum = 30
	default:
		return nil, fmt.Errorf("unknown search kind %q", kind)
	}
	if limit > 0 {
		if limit > maximum {
			limit = maximum
		}
		args = append(args, "--page-size", fmt.Sprint(limit))
	}
	return r.rawSearch(ctx, args)
}

// SendText sends a plain text message as the bot identity. The idempotency key
// is optional and is passed through to lark-cli when supplied.
func (r *Runner) SendText(ctx context.Context, chatID, text, idempotencyKey string) (json.RawMessage, error) {
	if strings.TrimSpace(chatID) == "" {
		return nil, errors.New("chat_id is required")
	}
	if text == "" {
		return nil, errors.New("text is required")
	}
	args := []string{"im", "+messages-send", "--chat-id", chatID, "--text", text}
	if idempotencyKey != "" {
		args = append(args, "--idempotency-key", idempotencyKey)
	}
	return r.RunAs(ctx, "bot", args...)
}

// ReplyText replies to a known message as the bot identity. The caller may ask
// for a topic reply; normal Inbox replies keep the source message's topic.
func (r *Runner) ReplyText(ctx context.Context, messageID, text, idempotencyKey string, inThread bool) (json.RawMessage, error) {
	if strings.TrimSpace(messageID) == "" {
		return nil, errors.New("message_id is required")
	}
	if text == "" {
		return nil, errors.New("text is required")
	}
	args := []string{"im", "+messages-reply", "--message-id", messageID, "--text", text}
	if idempotencyKey != "" {
		args = append(args, "--idempotency-key", idempotencyKey)
	}
	if inThread {
		args = append(args, "--reply-in-thread")
	}
	return r.RunAs(ctx, "bot", args...)
}

func (r *Runner) rawSearch(ctx context.Context, args []string) (json.RawMessage, error) {
	out, err := r.execute(ctx, args, outputJSON)
	if err != nil {
		return nil, err
	}
	data, err := unwrapData(out)
	if err != nil {
		return nil, cliError(KindOther, errors.New("invalid search JSON"))
	}
	return data, nil
}

type outputMode uint8

const (
	outputVersion outputMode = iota
	outputAuth
	outputJSON
	outputRaw
)

type commandResult struct {
	stdout []byte
	stderr []byte
}

func (r *Runner) execute(ctx context.Context, args []string, mode outputMode) ([]byte, error) {
	identity := ""
	if mode == outputJSON {
		identity = "user"
	}
	return r.executeAs(ctx, args, mode, identity)
}

func (r *Runner) executeAs(ctx context.Context, args []string, mode outputMode, identity string) ([]byte, error) {
	return r.executeInputProfile(ctx, args, mode, identity, nil, true)
}

func (r *Runner) executeNoProfile(ctx context.Context, args []string, mode outputMode) ([]byte, error) {
	return r.executeInputProfile(ctx, args, mode, "", nil, false)
}

func (r *Runner) executeInput(ctx context.Context, args []string, mode outputMode, identity string, input []byte) ([]byte, error) {
	return r.executeInputProfile(ctx, args, mode, identity, input, true)
}

func (r *Runner) executeInputProfile(ctx context.Context, args []string, mode outputMode, identity string, input []byte, includeProfile bool) ([]byte, error) {
	bin, err := r.binary(ctx)
	if err != nil {
		return nil, err
	}
	argv := args
	if includeProfile {
		argv = r.profileArgs(args)
	}
	switch mode {
	case outputAuth:
		argv = append(argv, "--json")
	case outputJSON:
		if identity == "" {
			identity = "user"
		}
		argv = append(argv, "--as", identity, "--format", "json")
	}

	timeout := r.cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, argv...)
	cmd.Env = cliEnv(bin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	err = cmd.Run()
	result := commandResult{stdout: stdout.Bytes(), stderr: stderr.Bytes()}
	if cctx.Err() != nil {
		return nil, &Error{Kind: KindTimeout, cause: cctx.Err()}
	}
	if err != nil {
		if classified := classifyFailure(result); classified != nil {
			return nil, classified
		}
		return nil, cliError(KindOther, err)
	}
	return result.stdout, nil
}

func parseConfigInfo(raw []byte) ConfigInfo {
	var value any
	if json.Unmarshal(raw, &value) == nil {
		var out ConfigInfo
		findConfigFields(value, &out)
		if out.AppID != "" {
			return out
		}
	}
	return ConfigInfo{AppID: firstRegex(raw, configAppIDPattern), Brand: firstRegex(raw, configBrandPattern)}
}

func findConfigFields(value any, out *ConfigInfo) {
	switch v := value.(type) {
	case map[string]any:
		for key, value := range v {
			switch strings.ToLower(strings.ReplaceAll(key, "_", "")) {
			case "appid":
				if s, ok := value.(string); ok && out.AppID == "" {
					out.AppID = strings.TrimSpace(s)
				}
			case "brand":
				if s, ok := value.(string); ok && out.Brand == "" {
					out.Brand = strings.TrimSpace(s)
				}
			}
			findConfigFields(value, out)
		}
	case []any:
		for _, item := range v {
			findConfigFields(item, out)
		}
	}
}

var (
	configAppIDPattern = lazyre.New(`(?i)["']?app[_-]?id["']?\s*[:=]\s*["']?([A-Za-z0-9_.-]+)`)
	configBrandPattern = lazyre.New(`(?i)["']?brand["']?\s*[:=]\s*["']?([A-Za-z0-9_-]+)`)
	loginURLPattern    = lazyre.New(`https://[^\s"'<>]+`)
)

func firstRegex(raw []byte, re *lazyre.Regexp) string {
	match := re.FindSubmatch(raw)
	if len(match) == 2 {
		return strings.TrimRight(string(match[1]), ".,;)")
	}
	return ""
}

func parseLoginInfo(line string) (LoginInfo, bool) {
	for _, candidate := range loginURLPattern.FindAllString(line, -1) {
		candidate = strings.TrimRight(candidate, ".,;)")
		u, err := url.Parse(candidate)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			continue
		}
		code := u.Query().Get("user_code")
		if code == "" {
			continue
		}
		return LoginInfo{URL: candidate, UserCode: code}, true
	}
	return LoginInfo{}, false
}

func (r *Runner) binary(ctx context.Context) (string, error) {
	r.mu.Lock()
	if r.path != "" {
		path := r.path
		r.mu.Unlock()
		return path, nil
	}
	r.mu.Unlock()
	bin := r.cfg.Bin
	if bin == "" {
		bin = "lark-cli"
	}
	if filepath.IsAbs(bin) {
		if _, err := os.Stat(bin); err != nil {
			return "", cliError(KindNotInstalled, err)
		}
		r.mu.Lock()
		r.path = bin
		r.mu.Unlock()
		return bin, nil
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		// npm global installs are often visible only in an interactive login shell.
		lookupCtx, cancel := context.WithTimeout(ctx, minDuration(r.cfg.Timeout, 5*time.Second))
		defer cancel()
		cmd := exec.CommandContext(lookupCtx, loginShell(), "-lc", "command -v "+shellQuote(bin))
		out, _ := cmd.Output()
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) > 0 && filepath.IsAbs(strings.TrimSpace(lines[len(lines)-1])) {
			path = strings.TrimSpace(lines[len(lines)-1])
		}
	}
	if path == "" {
		return "", cliError(KindNotInstalled, err)
	}
	r.mu.Lock()
	r.path = path
	r.mu.Unlock()
	return path, nil
}

func cliEnv(bin string) []string {
	return append(os.Environ(), "PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func loginShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/sh"
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

func minDuration(a, b time.Duration) time.Duration {
	if a <= 0 || a > b {
		return b
	}
	return a
}

var versionPattern = lazyre.New(`(?i)(?:lark-cli\s+version\s+|version\s+)([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?)`)

func parseVersion(s string) string {
	match := versionPattern.FindStringSubmatch(s)
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

func compareVersion(a, b string) int {
	parse := func(s string) [3]int {
		var v [3]int
		base := strings.SplitN(s, "-", 2)[0]
		parts := strings.Split(base, ".")
		for i := 0; i < len(parts) && i < len(v); i++ {
			_, _ = fmt.Sscan(parts[i], &v[i])
		}
		return v
	}
	va, vb := parse(a), parse(b)
	for i := range va {
		if va[i] < vb[i] {
			return -1
		}
		if va[i] > vb[i] {
			return 1
		}
	}
	return 0
}

func decodeData(raw []byte, out any) error {
	data, err := unwrapData(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func unwrapData(raw []byte) (json.RawMessage, error) {
	var env map[string]json.RawMessage
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	if data, ok := env["data"]; ok && len(data) > 0 && string(data) != "null" {
		return data, nil
	}
	return raw, nil
}

func classifyFailure(result commandResult) *Error {
	for _, raw := range [][]byte{result.stdout, result.stderr} {
		if len(raw) == 0 {
			continue
		}
		var env struct {
			Error struct {
				Type           string   `json:"type"`
				Subtype        string   `json:"subtype"`
				Message        string   `json:"message"`
				MissingScopes  []string `json:"missing_scopes"`
				MissingScopes2 []string `json:"missingScopes"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &env) != nil || env.Error.Type == "" && env.Error.Subtype == "" {
			continue
		}
		sub := strings.ToLower(env.Error.Subtype)
		typ := strings.ToLower(env.Error.Type)
		scopes := append(env.Error.MissingScopes, env.Error.MissingScopes2...)
		if len(scopes) == 0 {
			scopes = scopesFromMessage(env.Error.Message)
		}
		switch {
		case sub == "missing_scope" || sub == "permission_denied" || sub == "forbidden" || sub == "not_found" || sub == "resource_not_found" || sub == "document_not_found" || typ == "permission":
			return &Error{Kind: KindMissingPermission, MissingScopes: scopes}
		case sub == "rate_limited" || sub == "too_many_requests" || typ == "rate_limit":
			return &Error{Kind: KindRateLimited}
		case sub == "token_expired" || sub == "expired" || sub == "not_logged_in" || sub == "unauthorized" || typ == "authentication":
			if sub == "token_expired" || sub == "expired" {
				return cliError(KindExpired, nil)
			}
			return cliError(KindNotLoggedIn, nil)
		}
	}
	text := strings.ToLower(string(result.stdout) + "\n" + string(result.stderr))
	switch {
	case strings.Contains(text, "missing scope") || strings.Contains(text, "missing_scope"):
		return &Error{Kind: KindMissingPermission, MissingScopes: scopesFromMessage(text)}
	case strings.Contains(text, "rate limit") || strings.Contains(text, "too many requests"):
		return &Error{Kind: KindRateLimited}
	case strings.Contains(text, "token expired") || strings.Contains(text, "expired"):
		return &Error{Kind: KindExpired}
	case strings.Contains(text, "not found") || strings.Contains(text, "does not exist"):
		return &Error{Kind: KindMissingPermission}
	case strings.Contains(text, "not logged in") || strings.Contains(text, "not signed in"):
		return &Error{Kind: KindNotLoggedIn}
	}
	return nil
}

func scopesFromMessage(message string) []string {
	lower := strings.ToLower(message)
	const marker = "missing required scope(s):"
	index := strings.Index(lower, marker)
	if index < 0 {
		return nil
	}
	value := message[index+len(marker):]
	if end := strings.IndexAny(value, "\r\n"); end >= 0 {
		value = value[:end]
	}
	if end := strings.IndexAny(value, "\"}"); end >= 0 {
		value = value[:end]
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
	return uniqueStrings(parts)
}
