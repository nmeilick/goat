package goat

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/*
setupMoveCase copies the rewrite happy case's module (go.mod + in/ tree)
into a temp directory and returns the package directory.
*/
func setupMoveCase(t *testing.T) string {
	t.Helper()
	caseDir := testdataPath(t, "rewrite", "happy")
	work := t.TempDir()
	copyFile(t, filepath.Join(caseDir, "go.mod"), filepath.Join(work, "go.mod"))
	copyTree(t, filepath.Join(caseDir, "in"), filepath.Join(work, "pkg"))
	return filepath.Join(work, "pkg")
}

func TestMoveRollback(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	pkgDir := setupMoveCase(t)
	src := filepath.Join(pkgDir, "file.go")
	dst := filepath.Join(pkgDir, "dst.go")
	before, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}

	command := "goat move Hello,Shout --from file.go --to dst.go"
	_, err = Move(MoveOptions{
		From:    src,
		To:      dst,
		Symbols: []string{"Hello", "Shout"},
		Command: command,
		Verify:  func(string) error { return fmt.Errorf("injected verification failure") },
	})
	if err == nil {
		t.Fatal("expected the injected verification failure to abort the move")
	}
	if !strings.Contains(err.Error(), "injected verification failure") {
		t.Errorf("error %q should report the verification failure", err)
	}
	if !strings.Contains(err.Error(), "backup") {
		t.Errorf("error %q should mention the restored backup", err)
	}

	after, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("the source file was not restored byte-for-byte:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("the destination should be deleted by the rollback, stat error = %v", err)
	}

	// The rollback keeps the move's backup run in the store.
	workdir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	runs, err := List(workdir)
	if err != nil {
		t.Fatal(err)
	}
	kept := false
	for _, r := range runs {
		if r.Command == command {
			kept = true
		}
	}
	if !kept {
		t.Errorf("the move's backup run should be kept after the rollback, runs = %v", runs)
	}
}

func TestMoveEmptyDestination(t *testing.T) {
	/*
		A zero-byte --to breaks the package load with a parser dump naming
		no file; the engine refuses it plainly before loading anything.
	*/
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	pkgDir := setupMoveCase(t)
	dst := filepath.Join(pkgDir, "empty.go")
	if err := os.WriteFile(dst, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Move(MoveOptions{
		From:    filepath.Join(pkgDir, "file.go"),
		To:      dst,
		Symbols: []string{"Hello"},
	})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected an empty-destination refusal, got %v", err)
	}
}
