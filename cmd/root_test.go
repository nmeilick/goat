package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func run(args ...string) (stdout, stderr string, code int) {
	var out, errBuf bytes.Buffer
	code = Execute(args, &out, &errBuf)
	return out.String(), errBuf.String(), code
}

func TestVersionCommand(t *testing.T) {
	stdout, stderr, code := run("version")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if want := "goat dev (commit unknown, built unknown)"; !strings.Contains(stdout, want) {
		t.Errorf("stdout = %q, want it to contain %q", stdout, want)
	}
}

func TestVersionFlag(t *testing.T) {
	stdout, stderr, code := run("--version")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if want := "goat dev (commit unknown, built unknown)"; !strings.Contains(stdout, want) {
		t.Errorf("stdout = %q, want it to contain %q", stdout, want)
	}
}

func TestUnknownCommandExit2(t *testing.T) {
	_, stderr, code := run("bogus")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, `"bogus"`) {
		t.Errorf("stderr = %q, want it to name the bad command", stderr)
	}
}

func TestUnknownFlagExit2(t *testing.T) {
	_, stderr, code := run("--bogus")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "--bogus") {
		t.Errorf("stderr = %q, want it to name the bad flag", stderr)
	}
}

func TestRootHelpShowsWorkflow(t *testing.T) {
	stdout, stderr, code := run("--help")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	for _, want := range []string{"symbols", "move", "restore", "--dry-run"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("root help does not contain %q", want)
		}
	}
}
