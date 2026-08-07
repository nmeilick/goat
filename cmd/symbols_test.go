package cmd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

/*
sampleFile returns the absolute path of a file inside the engine's
sample testdata package.
*/
func sampleFile(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "internal", "goat", "testdata", "sample", name))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestSymbolsTable(t *testing.T) {
	stdout, stderr, code := run("symbols", sampleFile(t, "file.go"))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	for _, want := range []string{"method", "File.Stat", "helper", "other.go", "file_test.go"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("table does not contain %q:\n%s", want, stdout)
		}
	}
}

func TestSymbolsJSON(t *testing.T) {
	stdout, stderr, code := run("symbols", "--json", sampleFile(t, "file.go"))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	type symbol struct {
		Name          string   `json:"name"`
		Kind          string   `json:"kind"`
		Lines         int      `json:"lines"`
		UsedBy        []string `json:"usedBy"`
		UsedElsewhere bool     `json:"usedElsewhere"`
	}
	var doc struct {
		File    string   `json:"file"`
		Symbols []symbol `json:"symbols"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not the spec's JSON shape: %v\n%s", err, stdout)
	}
	if doc.File != "file.go" {
		t.Errorf("file = %q, want %q", doc.File, "file.go")
	}
	var stat *symbol
	for i := range doc.Symbols {
		if doc.Symbols[i].Name == "File.Stat" {
			stat = &doc.Symbols[i]
		}
	}
	if stat == nil {
		t.Fatal("no File.Stat entry in the JSON symbols list")
	}
	if stat.Kind != "method" {
		t.Errorf("File.Stat kind = %q, want %q", stat.Kind, "method")
	}
	if stat.Lines < 1 {
		t.Errorf("File.Stat lines = %d, want a positive count", stat.Lines)
	}
}

func TestSymbolsAlias(t *testing.T) {
	stdout, stderr, code := run("ls", sampleFile(t, "file.go"))
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "File.Stat") {
		t.Errorf("ls output does not list File.Stat:\n%s", stdout)
	}
}

func TestSymbolsNoArgExit2(t *testing.T) {
	_, stderr, code := run("symbols")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "<file.go>") {
		t.Errorf("stderr = %q, want it to name the missing <file.go> argument", stderr)
	}
	if !strings.Contains(stderr, "run 'goat symbols --help'") {
		t.Errorf("stderr = %q, want the usage hint", stderr)
	}
}

func TestSymbolsExtraneousArgExit2(t *testing.T) {
	_, stderr, code := run("symbols", "a.go", "b.go")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "accepts exactly one <file.go> argument, got 2") {
		t.Errorf("stderr = %q, want it to explain the single-file arity", stderr)
	}
	if !strings.Contains(stderr, "run 'goat symbols --help'") {
		t.Errorf("stderr = %q, want the usage hint", stderr)
	}
}

func TestSymbolsTestFileRefusal(t *testing.T) {
	_, stderr, code := run("symbols", sampleFile(t, "file_test.go"))
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "test files") {
		t.Errorf("stderr = %q, want it to contain %q", stderr, "test files")
	}
}

func TestHelpIgnoresParseErrors(t *testing.T) {
	for _, args := range [][]string{
		{"symbols", "--help"},
		{"symbols", "--bogus", "--help"},
	} {
		stdout, stderr, code := run(args...)
		if code != 0 {
			t.Errorf("%v: exit code = %d, want 0 (stderr: %s)", args, code, stderr)
		}
		if !strings.Contains(stdout, "Usage:") || !strings.Contains(stdout, "symbols") {
			t.Errorf("%v: stdout is not the symbols help:\n%s", args, stdout)
		}
	}
}
