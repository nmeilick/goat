package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/*
doMove runs a real move of the happy golden case and returns the case's
package directory and the backup run id it recorded.
*/
func doMove(t *testing.T) (pkgDir, runID string) {
	t.Helper()
	_, pkgDir = copyMoveCase(t, "happy")
	src := filepath.Join(pkgDir, "file.go")
	dst := filepath.Join(pkgDir, "dst.go")
	stdout, stderr, code := run("move", "Hello,Shout", "-f", src, "-t", dst)
	if code != 0 {
		t.Fatalf("move: exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	for _, line := range strings.Split(stdout, "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "backup" {
			return pkgDir, fields[1]
		}
	}
	t.Fatalf("move stdout does not name a backup run:\n%s", stdout)
	return "", ""
}

func TestRestoreListText(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_, runID := doMove(t)

	stdout, stderr, code := run("restore", "--list")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	for _, want := range []string{runID, "goat move", "file.go", "dst.go"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("restore --list does not contain %q:\n%s", want, stdout)
		}
	}
}

func TestRestoreListJSON(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_, runID := doMove(t)

	stdout, stderr, code := run("restore", "--list", "--json")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	type backup struct {
		RunID   string   `json:"runId"`
		Time    string   `json:"time"`
		Command string   `json:"command"`
		Files   []string `json:"files"`
	}
	var doc struct {
		Backups []backup `json:"backups"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not the spec's JSON shape: %v\n%s", err, stdout)
	}
	if len(doc.Backups) != 1 {
		t.Fatalf("backups = %d runs, want 1", len(doc.Backups))
	}
	b := doc.Backups[0]
	if b.RunID != runID {
		t.Errorf("runId = %q, want %q", b.RunID, runID)
	}
	if !strings.HasPrefix(b.Command, "goat move ") {
		t.Errorf("command = %q, want the recorded move command", b.Command)
	}
	files := strings.Join(b.Files, ",")
	if !strings.Contains(files, "file.go") {
		t.Errorf("files = %v, want the moved files' path strings", b.Files)
	}
	if !strings.Contains(files, "dst.go") {
		t.Errorf("files = %v, want the destination listed too", b.Files)
	}
}

func TestRestoreRoundTrip(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_, pkgDir := copyMoveCase(t, "happy")
	src := filepath.Join(pkgDir, "file.go")
	dst := filepath.Join(pkgDir, "dst.go")
	before, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	// A non-default mode proves restore reproduces recorded modes.
	if err := os.Chmod(src, 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, code := run("move", "Hello,Shout", "-f", src, "-t", dst)
	if code != 0 {
		t.Fatal("move failed")
	}

	stdout, stderr, code := run("restore")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "restored 2 files from backup ") {
		t.Errorf("stdout should carry the summary line:\n%s", stdout)
	}

	after, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("the source file is not byte-identical to its before-move state")
	}
	info, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("source mode = %o, want 600", info.Mode().Perm())
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("the destination the move created should be deleted, stat error = %v", err)
	}
}

func TestRestoreDryRunWritesNothing(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)
	pkgDir, runID := doMove(t)
	src := filepath.Join(pkgDir, "file.go")
	dst := filepath.Join(pkgDir, "dst.go")
	srcBefore, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := run("-n", "restore")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "would restore backup "+runID) {
		t.Errorf("stdout should preview the restore:\n%s", stdout)
	}
	srcAfter, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if string(srcAfter) != string(srcBefore) {
		t.Error("a dry run must not touch the source file")
	}
	if _, err := os.Stat(dst); err != nil {
		t.Error("a dry run must not remove the destination file")
	}
	if runs := backupRuns(t, xdg); len(runs) != 1 {
		t.Errorf("a dry run must not write backups, found %d runs", len(runs))
	}
}

func TestRestoreNoBackupsError(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_, stderr, code := run("restore")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "no backups found") {
		t.Errorf("stderr = %q, want it to contain %q", stderr, "no backups found")
	}
}

func TestRestoreBadRunIDError(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	doMove(t)

	_, stderr, code := run("restore", "2020-01-01T00-00-00")
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "2020-01-01T00-00-00") {
		t.Errorf("stderr = %q, want it to name the bad run-id", stderr)
	}
	if !strings.Contains(stderr, "restore --list") {
		t.Errorf("stderr = %q, want it to suggest 'restore --list'", stderr)
	}
}
