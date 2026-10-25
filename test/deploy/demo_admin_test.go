// Package deploy tests the scripts that set up the machine of qoqi.dev,
// with what they call stood in for.
package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// run runs demo-admin.sh with qoqi as a script that fails when told to.
func run(t *testing.T, dir string, fails bool, started bool) (string, error) {
	t.Helper()
	state := filepath.Join(dir, "state")
	os.MkdirAll(state, 0o700)
	if started {
		os.WriteFile(filepath.Join(state, "daemon.json"), []byte("{}"), 0o600)
	} else {
		os.Remove(filepath.Join(state, "daemon.json"))
	}
	fake := filepath.Join(dir, "qoqi")
	code := "cat > " + filepath.Join(dir, "given") + "\n"
	if fails {
		code = "exit 1\n"
	}
	os.WriteFile(fake, []byte("#!/bin/sh\n"+code), 0o700)
	cmd := exec.Command("sh", "../../deploy/server/demo-admin.sh")
	cmd.Env = append(os.Environ(), "KEPT="+filepath.Join(dir, "kept"), "STATE="+state, "QOQI="+fake, "WAIT=1")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// The password said to be given is the one the daemon took: a run where
// it did not take it fails, keeps none, and the next run gives one.
func TestTheDemoKeepsAPasswordOnlyOnceGiven(t *testing.T) {
	dir := t.TempDir()
	kept := filepath.Join(dir, "kept")

	if _, err := run(t, dir, true, true); err == nil {
		t.Fatal("the password refused, the script succeeded: Caddy would start with admin, admin")
	}
	if _, err := os.Stat(kept); err == nil {
		t.Fatal("a password is kept that the daemon never took")
	}

	if out, err := run(t, dir, false, false); err == nil || !strings.Contains(out, "did not start") {
		t.Fatalf("the daemon not started: %v %s", err, out)
	}

	if out, err := run(t, dir, false, true); err != nil {
		t.Fatalf("the password given: %v %s", err, out)
	}
	given, _ := os.ReadFile(filepath.Join(dir, "given"))
	saved, _ := os.ReadFile(kept)
	if len(saved) < 16 || string(given) != string(saved) {
		t.Errorf("kept %q, given %q", saved, given)
	}
	if info, err := os.Stat(kept); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the password is readable by others: %v", info.Mode())
	}

	// Given once, it is not given again
	os.Remove(filepath.Join(dir, "given"))
	if _, err := run(t, dir, true, true); err != nil {
		t.Errorf("run again: %v", err)
	}
}
