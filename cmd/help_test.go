package cmd

import (
	"strings"
	"testing"
)

/*
TestHelpContracts pins the durable bits of the help text: the semantics
and shapes a user or agent relies on. It never snapshots prose.
*/
func TestHelpContracts(t *testing.T) {
	help := func(args ...string) string {
		t.Helper()
		stdout, stderr, code := run(append(args, "--help")...)
		if code != 0 {
			t.Fatalf("%v --help: exit code = %d, want 0 (stderr: %s)", args, code, stderr)
		}
		return stdout
	}
	contains := func(name, text string, wants ...string) {
		t.Helper()
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Errorf("%s help does not contain %q:\n%s", name, want, text)
			}
		}
	}

	contains("move", help("move"),
		"referenced only by the moving set", // the --with-deps exclusivity rule
		"--dry-run move",                    // a dry-run example
		"'File*'",                           // a wildcard example
		"'!FileModify'",                     // an exclusion example
		"Type.Method",
	)
	contains("symbols", help("symbols"),
		`"symbols"`, // the --json shape
		"Test files are refused",
	)
	contains("restore", help("restore"),
		`"backups"`, // the --list --json shape
		`"runId"`,
	)
	contains("root", help(),
		"'ls'", // the symbols alias
		"Root flags go before the command path",
	)
}
