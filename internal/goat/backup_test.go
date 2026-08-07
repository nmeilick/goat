package goat

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// useStateDir points the backup store at a temporary XDG_STATE_HOME.
func useStateDir(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
}

// writeWorkFile creates a file with exactly the given content and mode.
func writeWorkFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// fileState returns a file's content and permission bits.
func fileState(t *testing.T, path string) (string, os.FileMode) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data), info.Mode().Perm()
}

func TestBackupRoundTrip(t *testing.T) {
	useStateDir(t)
	wd := t.TempDir()
	a := filepath.Join(wd, "a.go")
	b := filepath.Join(wd, "b.go")
	writeWorkFile(t, a, "package x\n\nvar A = 1\n", 0o640)

	runID, err := Create(wd, []string{a, b}, "goat move A --from a.go --to b.go")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if runID == "" {
		t.Fatal("Create returned an empty run id")
	}

	// The run mutates: a.go shrinks and changes mode, b.go appears.
	writeWorkFile(t, a, "package x\n", 0o600)
	writeWorkFile(t, b, "package x\n\nvar A = 1\n", 0o644)

	affected, err := Restore(wd, runID)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(affected) != 2 {
		t.Errorf("Restore affected %d paths, want 2", len(affected))
	}

	content, mode := fileState(t, a)
	if content != "package x\n\nvar A = 1\n" {
		t.Errorf("a.go content = %q, want the original", content)
	}
	if mode != 0o640 {
		t.Errorf("a.go mode = %o, want 640", mode)
	}
	if _, err := os.Stat(b); !os.IsNotExist(err) {
		t.Errorf("b.go should be deleted by restore, stat err = %v", err)
	}
}

func TestRestoreUndoable(t *testing.T) {
	useStateDir(t)
	wd := t.TempDir()
	a := filepath.Join(wd, "a.go")
	writeWorkFile(t, a, "v1\n", 0o644)

	runID, err := Create(wd, []string{a}, "goat move A --from a.go --to b.go")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	writeWorkFile(t, a, "v2\n", 0o644)

	if _, err := Restore(wd, runID); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if content, _ := fileState(t, a); content != "v1\n" {
		t.Fatalf("after restore a.go = %q, want v1", content)
	}

	runs, err := List(wd)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("List found %d runs, want 2 (the move and the restore's own backup)", len(runs))
	}
	undo := runs[0] // newest first: the restore's backup of the intermediate state
	if !strings.Contains(undo.Command, runID) {
		t.Errorf("restore backup command = %q, want it to name the restored run %q", undo.Command, runID)
	}
	if _, err := Restore(wd, undo.RunID); err != nil {
		t.Fatalf("second Restore: %v", err)
	}
	if content, _ := fileState(t, a); content != "v2\n" {
		t.Errorf("after undoing the restore a.go = %q, want the intermediate v2", content)
	}
}

func TestListScopedToWorkdir(t *testing.T) {
	useStateDir(t)
	wd1 := t.TempDir()
	wd2 := t.TempDir()
	a := filepath.Join(wd1, "a.go")
	writeWorkFile(t, a, "package x\n", 0o644)

	runID, err := Create(wd1, []string{a}, "goat move A --from a.go --to b.go")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	runs, err := List(wd1)
	if err != nil {
		t.Fatalf("List(wd1): %v", err)
	}
	if len(runs) != 1 || runs[0].RunID != runID {
		t.Fatalf("List(wd1) = %+v, want exactly run %q", runs, runID)
	}

	other, err := List(wd2)
	if err != nil {
		t.Fatalf("List(wd2): %v", err)
	}
	if len(other) != 0 {
		t.Errorf("List(wd2) found %d runs, want none from another workdir", len(other))
	}
	if _, err := Restore(wd2, runID); err == nil {
		t.Error("Restore from the wrong workdir should fail")
	}
}

func TestRestoreRecreatesDeletedFile(t *testing.T) {
	useStateDir(t)
	wd := t.TempDir()
	a := filepath.Join(wd, "a.go")
	writeWorkFile(t, a, "package x\n\nvar A = 1\n", 0o640)

	runID, err := Create(wd, []string{a}, "goat move A --from a.go --to b.go")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}

	if _, err := Restore(wd, runID); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	content, mode := fileState(t, a)
	if content != "package x\n\nvar A = 1\n" || mode != 0o640 {
		t.Errorf("recreated a.go = %q mode %o, want original content and mode 640", content, mode)
	}
}

func TestBackupOutsideWorkdir(t *testing.T) {
	/*
		Two runs in the same workdir both touch the same file outside it:
		each run must keep its own payload (a ".." path joined onto the run
		directory would land both payloads in the same shared location), and
		restoring the older run must return its own before-state.
	*/
	useStateDir(t)
	base := t.TempDir()
	wd := filepath.Join(base, "wd")
	if err := os.Mkdir(wd, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside.go")
	writeWorkFile(t, outside, "v1\n", 0o644)

	run1, err := Create(wd, []string{outside}, "goat move A --from ../outside.go --to x.go")
	if err != nil {
		t.Fatalf("Create run1: %v", err)
	}
	writeWorkFile(t, outside, "v2\n", 0o644)
	if _, err := Create(wd, []string{outside}, "goat move B --from ../outside.go --to y.go"); err != nil {
		t.Fatalf("Create run2: %v", err)
	}

	// The manifest keeps the workdir-relative path with its ".." segment.
	runs, err := List(wd)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var found bool
	for _, r := range runs {
		if r.RunID == run1 {
			found = true
			if len(r.Files) != 1 || r.Files[0].Path != filepath.Join("..", "outside.go") {
				t.Errorf("run1 manifest files = %+v, want the relative path ../outside.go", r.Files)
			}
		}
	}
	if !found {
		t.Fatalf("List does not contain run %q", run1)
	}

	if _, err := Restore(wd, run1); err != nil {
		t.Fatalf("Restore %q: %v", run1, err)
	}
	if content, _ := fileState(t, outside); content != "v1\n" {
		t.Errorf("after restoring the older run, outside.go = %q, want its own before-state v1", content)
	}
}

func TestBackupFailureAborts(t *testing.T) {
	/*
		XDG_STATE_HOME as a regular file makes the store unwritable for any
		user — permission bits would be ignored by root.
	*/
	blocker := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(blocker, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", blocker)

	wd := t.TempDir()
	a := filepath.Join(wd, "a.go")
	writeWorkFile(t, a, "package x\n", 0o640)

	if _, err := Create(wd, []string{a}, "goat move A --from a.go --to b.go"); err == nil {
		t.Fatal("Create should fail when the backup store cannot be created")
	}
	content, mode := fileState(t, a)
	if content != "package x\n" || mode != 0o640 {
		t.Errorf("failed backup touched a.go: content %q mode %o", content, mode)
	}
}

func TestRunIDTiebreak(t *testing.T) {
	/*
		Same-timestamp runs order by their numeric collision suffix; a
		string compare would sort "-10" before "-2".
	*/
	ids := []string{"2026-08-07T01-02-03", "2026-08-07T01-02-03-2", "2026-08-07T01-02-03-10"}
	sort.Slice(ids, func(i, j int) bool { return runIDLess(ids[i], ids[j]) })
	want := []string{"2026-08-07T01-02-03", "2026-08-07T01-02-03-2", "2026-08-07T01-02-03-10"}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("got order %v, want %v", ids, want)
		}
	}
	if !runIDLess("2026-08-07T01-02-03-2", "2026-08-07T01-02-03-10") {
		t.Error("-2 should sort before -10")
	}
}
