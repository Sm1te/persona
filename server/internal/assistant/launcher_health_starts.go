package assistant

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

// The app's own starts count for launcher health too (#1642, docs/server/launcher-health.md): the hub says how one
// the node ran before answering went (StartEnded), and one answered at once ("New Agent" with async: the node starts
// it after) is followed in its machine's session list, from starting to started or failed.

// startFollowFor is how long a start seen starting is followed, and a failed one remembered as counted: longer than a
// start takes (node's startWait) and than the node lists a failed one (startFailedFor).
const startFollowFor = 20 * time.Minute

// startWatch is a start answered at once that the manager saw starting.
type startWatch struct {
	launcher string
	seen     time.Time
}

// countedStart is a failed start already counted, as kept with the records: when, by this manager's clock.
type countedStart struct {
	Machine string    `json:"machine"`
	Session string    `json:"session"`
	At      time.Time `json:"at"`
}

// StartEnded: an app's start the node ran before answering went as r says (the hub's POST /api/v1/sessions).
func (s *Service) StartEnded(ctx context.Context, r StartEnded) {
	launcher := cmp.Or(r.Launcher, "claude")
	if r.OK {
		s.recordStartSuccess(ctx, r.MachineID, launcher)
		return
	}
	s.recordStartFailure(ctx, r.MachineID, launcher, &rpc.Error{Code: r.Code, Message: r.Error, Data: r.Data})
}

// followStarts follows d's starts answered at once: one that was starting and is listed now without an error worked,
// one listed with start_error failed, counted once while the node lists it. Which ones were counted is kept with the
// records, so a restart does not count them again (a start that worked since would be marked broken anew); the
// failure's updated_at is not compared with anything, being the node's clock.
func (s *Service) followStarts(ctx context.Context, d DeviceSessions) {
	type failure struct {
		launcher string
		err      error
	}
	var worked []string
	var failed []failure
	now := time.Now()
	// started: whether each followed start's last row says it started (neither starting nor failed). Only followed
	// ones are looked up below, and the last row of a session id is the one that left it followed or not, so the
	// rest of the list is not copied.
	var started map[string]bool
	b := &s.health
	b.mu.Lock()
	if b.starting == nil {
		b.starting = map[[2]string]startWatch{}
	}
	for _, sess := range d.Sessions {
		key := [2]string{d.MachineID, sess.SessionID}
		switch {
		case sess.Starting && sess.PendingSwitch != nil:
			// A running Agent moving to another launcher (node/launcher_switch.go), listed as starting meanwhile on the
			// launcher it moves to: not followed. The row listed after it may run on either launcher (back on its own
			// one when the new one did not come up: a start of the own launcher that worked, but not of the one this
			// row names), so it says nothing of the new launcher's starts; the switch's outcome goes to the message
			// that carried it out.
		case sess.Starting:
			if _, ok := b.starting[key]; !ok {
				b.starting[key] = startWatch{launcher: cmp.Or(sess.Launcher, "claude"), seen: now}
			}
		case sess.StartError != "":
			delete(b.starting, key)
			if len(failed) == 0 {
				s.healthLoadLocked(ctx) // what was counted before a restart
			}
			if b.counted == nil {
				b.counted = map[[2]string]time.Time{}
			}
			if _, done := b.counted[key]; !done {
				b.counted[key] = now
				failed = append(failed, failure{cmp.Or(sess.Launcher, "claude"), sessionStartErr(sess)})
			}
		}
		if _, followed := b.starting[key]; followed {
			if started == nil {
				started = map[string]bool{}
			}
			started[sess.SessionID] = !sess.Starting && sess.StartError == ""
		}
	}
	for key, w := range b.starting {
		if key[0] != d.MachineID {
			continue
		}
		clean, listed := started[key[1]]
		switch {
		case listed && clean:
			worked = append(worked, w.launcher)
			delete(b.starting, key)
		case now.Sub(w.seen) > startFollowFor:
			delete(b.starting, key)
		}
	}
	for key, at := range b.counted {
		if now.Sub(at) > startFollowFor {
			delete(b.counted, key)
		}
	}
	if len(failed) > 0 {
		s.healthSaveLocked(ctx) // counted, even when the failure says nothing of the launcher (recordStartFailure)
	}
	b.mu.Unlock()
	for _, l := range worked {
		s.recordStartSuccess(ctx, d.MachineID, l)
	}
	for _, f := range failed {
		s.recordStartFailure(ctx, d.MachineID, f.launcher, f.err)
	}
}

// sessionStartErr is a failed start's error as the node answered it: coded when it was a refusal (node/starting.go).
func sessionStartErr(sess proto.Session) error {
	e := &rpc.Error{Code: sess.StartErrorCode, Message: sess.StartError}
	if len(sess.StartErrorArgs) > 0 {
		data := make(map[string]any, len(sess.StartErrorArgs))
		for k, v := range sess.StartErrorArgs {
			data[k] = v
		}
		e.Data, _ = json.Marshal(data)
	}
	return e
}

// codedStartRefusal is err's code and text details when it is a coded refusal the hub words itself (apierr.Sentence): a
// failed task's error_code and error_args. Stalled starts also keep their code, with the node's existing message.
func codedStartRefusal(err error) (string, map[string]string) {
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != proto.CodeAgentStartStalled && apierr.Sentence(i18n.Default, re.Code, nil) == "" {
		return "", nil
	}
	var data map[string]any
	_ = json.Unmarshal(re.Data, &data)
	args := map[string]string{}
	for k, v := range data {
		if str, ok := v.(string); ok && k != "delivered" {
			args[k] = str
		}
	}
	if len(args) == 0 {
		args = nil
	}
	return re.Code, args
}

// startFailureText is why an agent did not start on machine, for the task it was for: the hub's sentence in the
// user's language for a coded refusal (a launcher the new terminal tab cannot run, #1642), else err's own text.
func (s *Service) startFailureText(ctx context.Context, machine string, err error) string {
	code, args := codedStartRefusal(err)
	if code == "" {
		return err.Error()
	}
	return startErrorText(s.lang(ctx), s.machineName(machine), proto.Session{StartError: err.Error(), StartErrorCode: code, StartErrorArgs: args})
}
