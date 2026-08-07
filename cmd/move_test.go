package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

/*
copyMoveCase copies a rewrite golden case's module (go.mod + in/ tree)
into a temp directory and returns the module root and package directory.
*/
func copyMoveCase(t *testing.T, name string) (root, pkgDir string) {
	t.Helper()
	caseDir, err := filepath.Abs(filepath.Join("..", "internal", "goat", "testdata", "rewrite", name))
	if err != nil {
		t.Fatal(err)
	}
	root = t.TempDir()
	data, err := os.ReadFile(filepath.Join(caseDir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	pkgDir = filepath.Join(root, "pkg")
	copyDir(t, filepath.Join(caseDir, "in"), pkgDir)
	return root, pkgDir
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		s, d := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		if e.IsDir() {
			copyDir(t, s, d)
			continue
		}
		data, err := os.ReadFile(s)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(d, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// wantBytes returns the expected content of one file of a golden case.
func wantBytes(t *testing.T, name, file string) []byte {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "internal", "goat", "testdata", "rewrite", name, "want", file))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

/*
backupRuns lists the entries of the backup store under xdg; a missing
store is an empty list.
*/
func backupRuns(t *testing.T, xdg string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(xdg, "goat", "backups"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestMoveDryRunWritesNothing(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)
	_, pkgDir := copyMoveCase(t, "happy")
	src := filepath.Join(pkgDir, "file.go")
	dst := filepath.Join(pkgDir, "dst.go")
	before, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := run("-n", "move", "Hello,Shout", "-f", src, "-t", dst)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "---") || !strings.Contains(stdout, "+++") {
		t.Errorf("stdout should contain a unified diff with --- and +++ headers:\n%s", stdout)
	}
	after, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("a dry run must not touch the source file")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("a dry run must not create the destination file")
	}
	if runs := backupRuns(t, xdg); len(runs) > 0 {
		t.Errorf("a dry run must not write backups, found %v", runs)
	}
}

func TestMoveDryRunColor(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_, pkgDir := copyMoveCase(t, "happy")
	src := filepath.Join(pkgDir, "file.go")
	dst := filepath.Join(pkgDir, "dst.go")

	// --color=always overrides NO_COLOR.
	t.Setenv("NO_COLOR", "1")
	stdout, stderr, code := run("--color=always", "-n", "move", "Hello,Shout", "-f", src, "-t", dst)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "\x1b[") {
		t.Errorf("--color=always should colorize the diff even with NO_COLOR set:\n%q", stdout)
	}

	stdout, stderr, code = run("--color=never", "-n", "move", "Hello,Shout", "-f", src, "-t", dst)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if strings.Contains(stdout, "\x1b[") {
		t.Errorf("--color=never should not colorize the diff:\n%q", stdout)
	}

	// Auto mode respects NO_COLOR (and a pipe is not a terminal anyway).
	stdout, stderr, code = run("-n", "move", "Hello,Shout", "-f", src, "-t", dst)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if strings.Contains(stdout, "\x1b[") {
		t.Errorf("auto color with NO_COLOR should not colorize the diff:\n%q", stdout)
	}
}

func TestMoveNoSymbolsExit2(t *testing.T) {
	_, stderr, code := run("move", "-f", "a.go", "-t", "b.go")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "<SYMBOL>") {
		t.Errorf("stderr = %q, want it to name the missing <SYMBOL> argument", stderr)
	}
	if !strings.Contains(stderr, "run 'goat move --help'") {
		t.Errorf("stderr = %q, want the usage hint", stderr)
	}
}

func TestMoveMissingFromExit2(t *testing.T) {
	_, stderr, code := run("move", "Sym", "-t", "b.go")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "--from") {
		t.Errorf("stderr = %q, want it to name the missing --from flag", stderr)
	}
}

func TestMoveMissingToExit2(t *testing.T) {
	_, stderr, code := run("move", "Sym", "-f", "a.go")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "--to") {
		t.Errorf("stderr = %q, want it to name the missing --to flag", stderr)
	}
}

func TestMoveSameFileExit2(t *testing.T) {
	_, stderr, code := run("move", "Sym", "-f", "a.go", "-t", "a.go")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "--to must differ from --from") {
		t.Errorf("stderr = %q, want it to say --to must differ from --from", stderr)
	}
}

func TestMoveExtraneousArgExit2(t *testing.T) {
	/*
		Every positional of move is a symbol, so the only extra input cobra
		rejects is an unknown flag; it gets the common error style.
	*/
	_, stderr, code := run("move", "Sym", "-f", "a.go", "-t", "b.go", "--bogus")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, `unknown flag: --bogus`) {
		t.Errorf("stderr = %q, want it to name the bad flag", stderr)
	}
	if !strings.Contains(stderr, "run 'goat move --help'") {
		t.Errorf("stderr = %q, want the usage hint", stderr)
	}
}

func TestRootFlagPlacement(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_, pkgDir := copyMoveCase(t, "happy")
	src := filepath.Join(pkgDir, "file.go")
	dst := filepath.Join(pkgDir, "dst.go")

	// Root flags before the command path work.
	stdout, stderr, code := run("-n", "move", "Hello", "-f", src, "-t", dst)
	if code != 0 {
		t.Fatalf("goat -n move: exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "+++") {
		t.Errorf("goat -n move: stdout should show the dry-run diff:\n%s", stdout)
	}

	/*
		A root flag after the command path is an unknown flag, with a note
		that root flags go before the command path.
	*/
	_, stderr, code = run("move", "Hello", "-f", src, "-t", dst, "-n")
	if code != 2 {
		t.Fatalf("goat move ... -n: exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "unknown shorthand flag") {
		t.Errorf("stderr = %q, want it to name the unknown flag", stderr)
	}
	if !strings.Contains(stderr, "root flags go before the command path") {
		t.Errorf("stderr = %q, want the root-flag placement note", stderr)
	}
}

func TestVerboseQuiet(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	_, pkgDir := copyMoveCase(t, "happy")
	stdout, stderr, code := run("-v", "move", "Hello,Shout",
		"-f", filepath.Join(pkgDir, "file.go"), "-t", filepath.Join(pkgDir, "dst.go"))
	if code != 0 {
		t.Fatalf("-v: exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if stderr == "" {
		t.Error("-v should print diagnostics to stderr during a move")
	}
	if !strings.Contains(stdout, "moved 2 declarations:") {
		t.Errorf("-v: stdout should carry the summary line:\n%s", stdout)
	}

	_, pkgDir = copyMoveCase(t, "happy")
	stdout, stderr, code = run("-q", "move", "Hello,Shout",
		"-f", filepath.Join(pkgDir, "file.go"), "-t", filepath.Join(pkgDir, "dst.go"))
	if code != 0 {
		t.Fatalf("-q: exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if stderr != "" {
		t.Errorf("-q should suppress diagnostics, stderr = %q", stderr)
	}
	if !strings.Contains(stdout, "moved 2 declarations:") {
		t.Errorf("-q: primary output must be unaffected:\n%s", stdout)
	}
}

func TestMoveEndToEnd(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)
	root, pkgDir := copyMoveCase(t, "happy")
	src := filepath.Join(pkgDir, "file.go")
	dst := filepath.Join(pkgDir, "dst.go")

	stdout, stderr, code := run("move", "Hello,Shout", "-f", src, "-t", dst)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "moved 2 declarations:") {
		t.Errorf("stdout should carry the summary line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Hello, Shout") {
		t.Errorf("the summary should name the moved symbols:\n%s", stdout)
	}

	for _, f := range []string{"file.go", "dst.go"} {
		got, err := os.ReadFile(filepath.Join(pkgDir, f))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(wantBytes(t, "happy", f)) {
			t.Errorf("%s does not match the golden output:\n--- got ---\n%s\n--- want ---\n%s", f, got, wantBytes(t, "happy", f))
		}
	}

	// The summary's second line names the backup run; it must exist.
	var runID string
	for _, line := range strings.Split(stdout, "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "backup" {
			runID = fields[1]
		}
	}
	if runID == "" {
		t.Fatalf("stdout does not name a backup run:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(xdg, "goat", "backups", runID, "manifest.json")); err != nil {
		t.Errorf("backup run %s has no manifest: %v", runID, err)
	}

	/*
		The moved testdata module is stdlib-only, so building it needs no
		network or module downloads.
	*/
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	build := exec.Command("go", "build", "./...")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./... in the moved module: %v\n%s", err, out)
	}
}

func TestMoveEmptiedSourceSummary(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	t.Run("removed", func(t *testing.T) {
		_, pkgDir := copyMoveCase(t, "emptied_source")
		src := filepath.Join(pkgDir, "only.go")
		stdout, stderr, code := run("move", "Only", "-f", src, "-t", filepath.Join(pkgDir, "dst.go"))
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
		}
		if !strings.Contains(stdout, "moved 1 declaration:") {
			t.Errorf("stdout should carry the summary line:\n%s", stdout)
		}
		if !strings.Contains(stdout, "source file removed") {
			t.Errorf("the summary should note the source file's removal:\n%s", stdout)
		}
		if _, err := os.Stat(src); !os.IsNotExist(err) {
			t.Errorf("the emptied source should be removed, stat error = %v", err)
		}
	})

	t.Run("kept", func(t *testing.T) {
		_, pkgDir := copyMoveCase(t, "emptied_with_comments")
		src := filepath.Join(pkgDir, "file.go")
		stdout, stderr, code := run("move", "Only", "-f", src, "-t", filepath.Join(pkgDir, "dst.go"))
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
		}
		if !strings.Contains(stdout, "source file kept with comments only") {
			t.Errorf("the summary should note the comments-kept source:\n%s", stdout)
		}
		if _, err := os.Stat(src); err != nil {
			t.Errorf("the comments-carrying source should be kept: %v", err)
		}
	})
}

func TestMovePreservesSymlink(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_, pkgDir := copyMoveCase(t, "happy")
	real := filepath.Join(pkgDir, "file.go")
	/*
		The symlink lives outside the package directory: a symlinked .go file
		inside it would be compiled as a second file.
	*/
	link := filepath.Join(t.TempDir(), "link.go")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := run("move", "Hello,Shout", "-f", link, "-t", filepath.Join(pkgDir, "dst.go"))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "moved 2 declarations:") {
		t.Errorf("stdout should carry the summary line:\n%s", stdout)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("the path should still be a symlink after the move")
	}
	got, err := os.ReadFile(real)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(wantBytes(t, "happy", "file.go")) {
		t.Errorf("the real file does not have the new content:\n--- got ---\n%s\n--- want ---\n%s", got, wantBytes(t, "happy", "file.go"))
	}
}

func TestMoveSameFileSymlinkExit2(t *testing.T) {
	/*
		--to naming the same file as --from through a symlink is the exit-2
		usage error, not a plan-level refusal.
	*/
	dir := t.TempDir()
	real := filepath.Join(dir, "real.go")
	if err := os.WriteFile(real, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.go")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := run("move", "Sym", "-f", link, "-t", real)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stderr, "--to must differ from --from") {
		t.Errorf("stderr = %q, want it to say --to must differ from --from", stderr)
	}
}

func TestMoveEmptySelectionExit2(t *testing.T) {
	/*
		A selection that is empty after comma-splitting is a usage error,
		checked before the package is loaded.
	*/
	_, stderr, code := run("move", ",,", "-f", "a.go", "-t", "b.go")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stderr, "no symbols given to move") {
		t.Errorf("stderr = %q, want it to say no symbols were given", stderr)
	}
	if !strings.Contains(stderr, "run 'goat move --help'") {
		t.Errorf("stderr = %q, want the usage hint", stderr)
	}
}

func TestMoveRemovesDanglingSymlink(t *testing.T) {
	/*
		Moving every declaration out of a source reached through a symlink
		removes the emptied file and the now-dangling link.
	*/
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_, pkgDir := copyMoveCase(t, "emptied_source")
	real := filepath.Join(pkgDir, "only.go")
	link := filepath.Join(t.TempDir(), "link.go")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := run("move", "Only", "-f", link, "-t", filepath.Join(pkgDir, "dst.go"))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if _, err := os.Stat(real); !os.IsNotExist(err) {
		t.Errorf("the emptied source should be removed, stat error = %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("the dangling symlink should be removed too, lstat error = %v", err)
	}
	if !strings.Contains(stdout, "symlink") {
		t.Errorf("the summary should mention the removed symlink:\n%s", stdout)
	}
}

func TestMoveBlankImportSourceSummary(t *testing.T) {
	/*
		A source emptied of declarations but still holding a blank import is
		kept; the summary must say blank imports remain, not "comments only".
	*/
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module blankkept\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "file.go")
	if err := os.WriteFile(src, []byte(`package blankkept

import (
	"fmt"
	_ "net/http/pprof"
)

// Only moves away.
func Only() string { return fmt.Sprint("only") }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := run("move", "Only", "-f", src, "-t", filepath.Join(root, "dst.go"))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "source file kept (blank imports remain)") {
		t.Errorf("the summary should note the surviving blank imports:\n%s", stdout)
	}
	if strings.Contains(stdout, "comments only") {
		t.Errorf("no comments survived; the summary must not claim they did:\n%s", stdout)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("the blank-import-carrying source should be kept: %v", err)
	}
}

func TestQuoteArgs(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain.go", "plain.go"},
		{"-f", "-f"},
		{"File*", "'File*'"},
		{"!FileModify", "'!FileModify'"},
		{"a b.go", "'a b.go'"},
		{"it's.go", `"it's.go"`},
		{"x$HOME", "'x$HOME'"},
		{"~user", "'~user'"},
		{"#hash", "'#hash'"},
		{"", "''"},
		{"line\nbreak", "'line\nbreak'"},
		{"a'b\"c", `'a'\''b"c'`},
	}
	for _, c := range cases {
		if got := quoteArg(c.in); got != c.want {
			t.Errorf("quoteArg(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := quoteArgs([]string{"goat", "move", "File*", "--from", "file.go"}); got != `goat move 'File*' --from file.go` {
		t.Errorf("quoteArgs joined = %q", got)
	}
}
