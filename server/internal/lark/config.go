// Package lark is hub.json's "lark": how the hub and the assistant run lark-cli, Feishu's official open-platform CLI
// (docs/plans/plan-lark-cli.md). Feishu through a Lark gateway (the user's own web client) is an extension's now
// (docs/plans/plan-extensions.md "Step 3"); its old fields are still read, only to say so.
package lark

import (
	"errors"
	"time"

	"github.com/nwverse/persona/server/internal/lazyre"
)

// sourceName is a lark.prefer value: "cli" or an extension's own source.
var sourceName = lazyre.New(`^[a-z][a-z0-9-]{0,31}$`)

// Config is hub.json's lark.
type Config struct {
	// CLI is lark-cli; nil: not used.
	CLI *CLIConfig `json:"cli,omitempty"`
	// Prefer is the primary source when an extension serves Feishu as well: "cli" (lark-cli), or the extension's own
	// source by the name it gives it (the extension reads and checks that; the hub passes it on). "" leaves it to
	// the extension.
	Prefer string `json:"prefer,omitempty"`

	// Account, BaseURL, Container, Token and NativeOLKToken pointed the hub at a Lark gateway before an extension served
	// it: not used any more (the hub logs a line when they are there), except Account, the gateway account the
	// Feishu items a plugin hands over came to.
	Account        int    `json:"account,omitempty"`
	BaseURL        string `json:"base_url,omitempty"`
	Container      string `json:"container,omitempty"`
	Token          string `json:"token,omitempty"`
	NativeOLKToken string `json:"native_olk_token,omitempty"`
}

// CLIConfig is how the hub and the assistant run lark-cli. Its app and the user's login stay in lark-cli's own store.
type CLIConfig struct {
	Bin     string `json:"bin,omitempty"`     // command name or absolute path; default lark-cli, looked up in a login shell
	Profile string `json:"profile,omitempty"` // lark-cli --profile; "" is its default profile
	// Extensions explicitly allows extensions that declare the "lark-cli" host
	// integration to use its bounded, read-only RPC. It grants no shell access,
	// no writes, and no credentials.
	Extensions bool `json:"extensions,omitempty"`
	// InboxPollSeconds controls the lark-cli Inbox watcher. Values below 60
	// seconds are clamped; zero keeps the two-minute default.
	InboxPollSeconds int `json:"inbox_poll_seconds,omitempty"`
	// InboxPageLimit bounds search pages in one round. Remaining pages are read
	// by later rounds so one busy tenant cannot monopolize the poll.
	InboxPageLimit int `json:"inbox_page_limit,omitempty"`
}

func (c *CLIConfig) InboxPoll() time.Duration {
	if c == nil || c.InboxPollSeconds <= 0 {
		return 0
	}
	return time.Duration(c.InboxPollSeconds) * time.Second
}

// MaxAccount is the largest account number.
const MaxAccount = 999999999

// LegacyGatewayConfigured: the old Lark gateway fields are there (the hub says they are not read any more).
func (cfg Config) LegacyGatewayConfigured() bool { return cfg.Token != "" || cfg.Container != "" }

func (cfg Config) Validate() error {
	if cfg.Account < 0 || cfg.Account > MaxAccount {
		return errors.New("lark.account must be an integer between 1 and 999999999 (or 0 for the primary account)")
	}
	switch {
	case cfg.Prefer == "lark-cli":
		return errors.New(`lark.prefer: lark-cli is "cli"`)
	case cfg.Prefer != "" && !sourceName.MatchString(cfg.Prefer):
		return errors.New(`lark.prefer must be "cli" or the name of the extension's own source (a-z0-9-)`)
	}
	return nil
}
