// Package testhome keeps a test binary off the developer's real ~/.persona.
//
// Code that finds its state through the home directory (the assistant process a hub test starts, the hub's own
// setup) writes there when a test does not point PERSONA_HOME somewhere else. Run from a developer machine that has a
// hub on it, such a test rewrote the real hub's manager.json and secret.key, and the next run's assistant child read
// the poisoned file and never came up. scripts/test/test-server.sh already runs with a throw-away HOME; a plain
// `go test` did not.
//
// A package whose tests start Persona processes calls Main from its TestMain: the tests run with HOME set to an empty
// directory and PERSONA_HOME unset, and the run fails if anything created <HOME>/.persona. A test that needs state
// sets PERSONA_HOME (t.Setenv) to a directory of its own.
//
// Go tools a test starts (`go list` in the hub's architecture guard) still find the developer's Go env file, module
// cache and build cache: Main pins GOENV, GOPATH, GOMODCACHE and GOCACHE to what they were under the real HOME.
// Without that a child lost settings such as `go env -w GOPROXY=…`, fell back to the default proxy with an empty
// cache and timed out where that proxy is unreachable (#5191).
package testhome

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// isolated names the throw-away home once Main made it: a test binary a test starts again inherits the variable and
// the HOME and PERSONA_HOME its parent's test set, and must not replace them.
const isolated = "PERSONA_TEST_HOME_ISOLATED"

// Active says whether this test binary runs through Main (or is started by one that does), for a test of a package's
// TestMain that needs no database.
func Active() bool { return os.Getenv(isolated) != "" }

// Main runs run (os.Exit(testhome.Main(m.Run)), or around dbtest.Main) with a throw-away HOME and returns its exit
// code, or 1 when the run left <HOME>/.persona behind.
func Main(run func() int) int { return runIn(run, os.Stderr) }

func runIn(run func() int, stderr io.Writer) int {
	if os.Getenv(isolated) != "" {
		return run()
	}
	dir := "" // a short path: the tests make Unix sockets under it
	if _, err := os.Stat("/tmp"); err == nil && runtime.GOOS != "windows" {
		dir = "/tmp"
	}
	home, err := os.MkdirTemp(dir, "persona-test-home-")
	if err != nil {
		fmt.Fprintf(stderr, "testhome: %v\n", err)
		return 1
	}
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	defer os.RemoveAll(home)
	vars := map[string]string{"HOME": home, "USERPROFILE": home, "PERSONA_HOME": "", isolated: home}
	for k, v := range goEnv() { // read under the real HOME, before it changes
		vars[k] = v
	}
	restore := set(vars)
	defer restore()
	code := run()
	if left := leftBehind(home); len(left) > 0 {
		fmt.Fprintf(stderr, "testhome: the tests wrote to the default state directory ~/.persona (%d files, e.g. %s).\n"+
			"A test that needs state sets PERSONA_HOME to a directory of its own (t.Setenv).\n",
			len(left), strings.Join(left[:min(len(left), 5)], ", "))
		if code == 0 {
			code = 1
		}
	}
	return code
}

// goPaths are the Go settings whose defaults come from the home directory.
var goPaths = []string{"GOENV", "GOPATH", "GOMODCACHE", "GOCACHE"}

// goEnv returns goPaths as the go tool resolves them now, leaving out empty ones; nothing when there is no go tool.
// A variable, so a test can stand in for the go tool.
var goEnv = func() map[string]string {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, goBin, append([]string{"env", "-json"}, goPaths...)...).Output()
	if err != nil {
		return nil
	}
	var all map[string]string
	if json.Unmarshal(out, &all) != nil {
		return nil
	}
	pinned := map[string]string{}
	for _, k := range goPaths {
		if v := all[k]; v != "" {
			pinned[k] = v
		}
	}
	return pinned
}

// set changes the variables (an empty value unsets) and returns how to put them back.
func set(vars map[string]string) func() {
	type old struct {
		value string
		had   bool
	}
	saved := map[string]old{}
	for k, v := range vars {
		prev, had := os.LookupEnv(k)
		saved[k] = old{prev, had}
		if v == "" {
			_ = os.Unsetenv(k)
		} else {
			_ = os.Setenv(k, v)
		}
	}
	return func() {
		for k, o := range saved {
			if o.had {
				_ = os.Setenv(k, o.value)
			} else {
				_ = os.Unsetenv(k)
			}
		}
	}
}

// leftBehind lists the files under <home>/.persona, relative to home.
func leftBehind(home string) []string {
	var files []string
	root := filepath.Join(home, ".persona")
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(home, path)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if _, err := os.Lstat(root); err == nil && len(files) == 0 {
		files = append(files, ".persona") // an empty directory is still a write
	}
	return files
}
