package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/identity"
	"github.com/nwverse/persona/server/internal/managerconf"
)

// NorthStarCommand is `personad assistant north-star [--date YYYY-MM-DD] [--split RFC3339] [--tz Area/City] [--brief lang]`: the
// north-star readout (north_star.go) against the built-in manager's database, read-only, without a running manager and
// without migrating anything: a baseline from a build that never recorded it. Both databases are opened with read-only
// transactions. On top of what GET /api/v1/assistant/north-star reads, it counts the app messages the hub's client_ops
// kept from before recording began.
func NorthStarCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("assistant north-star", flag.ContinueOnError)
	flags.SetOutput(stderr)
	date := flags.String("date", "", "the day, YYYY-MM-DD in the time zone (default: today, up to now)")
	split := flags.String("split", "", "an RFC 3339 time: availability before and from it (a deployment)")
	tz := flags.String("tz", "", "the time zone (default: the assistant's setting, else this machine's)")
	brief := flags.String("brief", "", "print the briefing's opening lines in this language (en, zh-Hans, ja) instead of JSON: today until now against yesterday until the same time")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("assistant north-star takes no arguments")
	}
	cfg, err := readHubConfig()
	if err != nil {
		return err
	}
	if cfg.DatabaseURL == "" {
		return errors.New("no database_url in hub.json (or PERSONA_DATABASE_URL)")
	}
	own := managerconf.ReadOwnDatabase(identity.HomeDir())
	mine := cfg.DatabaseURL
	switch {
	case own.URL != "":
		mine = own.URL
	case own.Name != "":
		if mine, err = db.WithDatabase(cfg.DatabaseURL, own.Name); err != nil {
			return err
		}
	}
	manager, err := attachReadOnly(ctx, mine, db.ManagerSet)
	if err != nil {
		return fmt.Errorf("the manager's database: %w", err)
	}
	defer manager.Close()
	var hub *db.DB
	if mine != cfg.DatabaseURL {
		if hub, err = attachReadOnly(ctx, cfg.DatabaseURL, db.HubSet); err != nil {
			return fmt.Errorf("the hub's database: %w", err)
		}
		defer hub.Close()
	} else {
		hub = manager // a manager that never moved keeps the hub's tables beside its own
	}
	now := time.Now()
	ns, err := northStarCommand(ctx, manager, hub, *date, *split, *tz, now)
	if err != nil {
		return err
	}
	if *brief != "" {
		b := briefNorthStar{Today: ns, todayOK: true}
		loc, _ := time.LoadLocation(ns.TimeZone) // the readout's own zone: it loaded
		prev, prevUntil := yesterdayUntilNow(ns.Until, loc)
		if b.Yesterday, err = northStarCommand(ctx, manager, hub, prev.Format(time.DateOnly), "", ns.TimeZone, prevUntil); err == nil {
			b.yesterdayOK = true
		}
		for _, line := range b.lines(i18n.Lang(*brief)) {
			fmt.Fprintln(stdout, line)
		}
		return nil
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(ns)
}

func northStarCommand(ctx context.Context, manager, hub *db.DB, date, split, tz string, now time.Time) (NorthStar, error) {
	var stored struct {
		Timezone string `json:"timezone"`
	}
	if _, err := manager.Setting(ctx, settingsKey, &stored); err != nil {
		return NorthStar{}, err
	}
	loc := zoneOf(stored.Timezone)
	if tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return NorthStar{}, err
		}
		loc = l
	}
	since, until, at, err := northStarWindow(date, split, now, loc)
	if err != nil {
		return NorthStar{}, err
	}
	roles, err := rolesNow(ctx, manager)
	if err != nil {
		return NorthStar{}, err
	}
	sent, err := hub.ClientMessages(ctx, since, until)
	if err != nil {
		return NorthStar{}, err
	}
	var extra []nsAttempt
	for _, m := range sent {
		role := roles.of(m.SessionID)
		if role == "" || m.State == "pending" {
			continue
		}
		a := nsAttempt{At: m.At, SessionID: m.SessionID, Role: role, Op: "resume", Origin: originAppOps, Outcome: outcomeUnknown}
		switch m.State {
		case "done":
			a.Outcome = outcomeOK
		case "failed":
			a.Outcome, a.Reason = outcomeFailed, sendReason(m.Code, m.Detail)
		case "unknown":
			if m.Code != "" { // the answer was an error the phone never collected: it failed
				a.Outcome, a.Reason = outcomeFailed, sendReason(m.Code, m.Detail)
			}
		}
		extra = append(extra, a)
	}
	return readNorthStar(ctx, manager, northStarQuery{Since: since, Until: until, Split: at, Loc: loc, Extra: extra})
}

// attachReadOnly attaches to the database at raw with every transaction read-only (Postgres refuses any write).
func attachReadOnly(ctx context.Context, raw string, sets db.Sets) (*db.DB, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("not a postgres:// URL")
	}
	q := u.Query()
	q.Set("default_transaction_read_only", "on")
	u.RawQuery = q.Encode()
	return db.Attach(ctx, u.String(), sets)
}
