// Package tzguard holds a test only: the hub runs in UTC and its user does not, so server code reads times in the
// user's zone (hub.userLocation, the knowledge base's Store.Zone), never in the machine's own.
package tzguard

import (
	"bufio"
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Where the machine's own zone is right, and why.
var allowed = map[string]string{
	"internal/node/":                        "a node runs on the user's own computer, in their zone (uploads by day)",
	"internal/hub/usertz.go":                "userLocation / userTZName: the hub's zone only while the user's is unknown",
	"internal/extmgr/usertz.go":             "userLocation: the hub's zone only while the user's is unknown (as hub/usertz.go)",
	"internal/assistant/zone.go":            "location: the hub's zone only while the user's is unknown (as userLocation)",
	"internal/kb/zone.go":                   "the archive's zone before it is set, and files written before zones were recorded",
	"internal/devices/metrics.go":           "Usage: the machine's zone only without Options.Location (the manager passes the user's, assistant/main.go)",
	"internal/connector/larkcli/clizone.go": "cliLocation: lark-cli prints wall-clock times in the zone of the machine running it, the hub's",
	"internal/fleetsoak/":                   "a developer's soak command: its log clock and output directory name are for the person at that terminal",
}

var (
	// .Local(), time.Local, and the clock read straight off time.Now() (its zone is the machine's).
	localZone = regexp.MustCompile(`\.Local\(\)|\btime\.Local\b|time\.Now\(\)\.(Format|Hour|Minute|Day|Weekday|YearDay|Month|Year|Date|Clock)\b`)
)

func TestNoMachineTimeZone(t *testing.T) {
	root := serverRoot(t)
	checked := 0
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !isSource(p) {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		for prefix := range allowed {
			if strings.HasPrefix(rel, prefix) {
				return nil
			}
		}
		checked++
		hits, err := machineZoneLines(p)
		for _, h := range hits {
			t.Errorf("%s:%d: the machine's time zone (the hub runs in UTC): read times in the user's, "+
				"hub.userLocation() / kb Store.Zone(), or add the file to `allowed` with the reason:\n\t%s",
				rel, h.line, h.text)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 50 {
		t.Fatalf("only %d files checked: is the walk at server/?", checked)
	}
}

// TestAllowedStillNeeded keeps `allowed` honest: an entry that no longer reads the machine's zone only hides its files
// from TestNoMachineTimeZone, so a new time.Local there would pass unnoticed.
func TestAllowedStillNeeded(t *testing.T) {
	root := serverRoot(t)
	for _, entry := range slices.Sorted(maps.Keys(allowed)) {
		hits := 0
		path := filepath.Join(root, filepath.FromSlash(entry))
		err := filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !isSource(p) {
				return err
			}
			found, err := machineZoneLines(p)
			hits += len(found)
			return err
		})
		switch {
		case err != nil:
			t.Errorf("allowed[%q]: %v: delete the entry if its code is gone", entry, err)
		case hits == 0:
			t.Errorf("allowed[%q] no longer reads the machine's time zone: "+
				"delete the stale entry so its files are scanned again", entry)
		}
	}
}

// serverRoot is the server/ directory the guard walks.
func serverRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// isSource reports whether the guard reads p: Go code that is not a test.
func isSource(p string) bool {
	return strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go")
}

// zoneHit is one line that reads the machine's time zone.
type zoneHit struct {
	line int
	text string
}

// machineZoneLines returns the lines of a Go file that read the machine's time zone, skipping comments and calls known
// to return bool.
func machineZoneLines(path string) ([]zoneHit, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var siblings [][]byte
	if bytes.Contains(source, []byte(".Local()")) {
		entries, err := os.ReadDir(filepath.Dir(path))
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			other := filepath.Join(filepath.Dir(path), e.Name())
			if other == path || e.IsDir() || !isSource(other) {
				continue
			}
			text, err := os.ReadFile(other)
			if err != nil {
				return nil, err
			}
			siblings = append(siblings, text)
		}
	}
	var hits []zoneHit
	sc := bufio.NewScanner(bytes.NewReader(maskBooleanLocalCalls(source, siblings...)))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for n := 1; sc.Scan(); n++ {
		code, _, _ := strings.Cut(sc.Text(), "//") // comments may name it
		if localZone.MatchString(code) {
			hits = append(hits, zoneHit{n, strings.TrimSpace(sc.Text())})
		}
	}
	return hits, sc.Err()
}
