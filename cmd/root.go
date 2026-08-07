/*
Package cmd wires the goat command tree: argument parsing, root flags,
help, and exit-code mapping. The refactoring engine lives in internal/goat.
*/
package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// AppName is the command's name in help and error output.
const AppName = "goat"

// Build metadata, injected via -ldflags -X (see the Makefile build target).
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// Options carries the root flags that later commands consume.
type Options struct {
	DryRun  bool
	Verbose int
	Quiet   bool
	Color   string
}

func (o *Options) useColor(w io.Writer) bool {
	switch o.Color {
	case "always":
		return true
	case "never":
		return false
	}
	if _, noColor := os.LookupEnv("NO_COLOR"); noColor {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func (o *Options) validate() error {
	switch o.Color {
	case "auto", "always", "never":
		return nil
	}
	return &usageError{fmt.Sprintf("invalid --color value %q (want auto, always or never)", o.Color)}
}

// usageError marks errors caused by bad command-line input; they exit 2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

/*
Execute runs the command tree with args (excluding argv[0]) and returns the
process exit code: 0 on success, 2 for usage errors, 1 for anything else.
*/
func Execute(args []string, stdout, stderr io.Writer) int {
	root := newRootCmd()
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(args)

	if hasHelpFlag(args) {
		return printHelp(root, args)
	}

	runCmd, err := root.ExecuteC()
	if err == nil {
		return 0
	}
	if isUsageError(err) {
		hint := AppName
		if runCmd != nil && runCmd != root {
			hint += " " + runCmd.Name()
		}
		fmt.Fprintf(stderr, "error: %v\nrun '%s --help'\n", err, hint)
		return 2
	}
	fmt.Fprintf(stderr, "error: %v\n", err)
	return 1
}

func newRootCmd() *cobra.Command {
	opts := &Options{}
	root := &cobra.Command{
		Use:   AppName,
		Short: "Move Go declarations between files of a package, with type-checked precision",
		Long: `goat — the Go AST Transformer — moves top-level declarations (functions,
methods, types, constants, variables) between files of the same Go
package and fixes imports on both sides.

Workflow: discover with 'goat symbols <file.go>', preview with
'goat --dry-run move ...', apply with 'goat move ...', undo with
'goat restore'. Every mutation is backed up, so any move can be reverted.
'symbols' is also available under its alias 'ls'.

Root flags go before the command path ('goat -n move ...'), command-local
flags after it.

Exit codes: 0 success, 1 failure (no files touched), 2 usage error.`,
		// Without a RunE, a bare 'goat bogus' would print help with exit 0;
		// a runnable root turns it into a usage error.
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return &usageError{fmt.Sprintf("unknown command %q", args[0])}
		},
		Version:          Version,
		SilenceErrors:    true,
		SilenceUsage:     true,
		TraverseChildren: true,
	}
	root.SetVersionTemplate(versionLine() + "\n")
	root.CompletionOptions.HiddenDefaultCmd = true
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		msg := err.Error()
		// An unknown flag on a subcommand that matches a root flag is a
		// placement error, not an unknown one: root flags go before the
		// command path, so say so.
		if cmd != nil && cmd != root {
			if flag, ok := rootFlagCollision(msg); ok {
				msg = fmt.Sprintf("%s; root flags go before the command path ('goat %s %s ...')", msg, flag, cmd.Name())
			}
		}
		return &usageError{msg}
	})

	f := root.Flags()
	f.BoolVarP(&opts.DryRun, "dry-run", "n", false, "preview changes as a unified diff without writing anything")
	f.CountVarP(&opts.Verbose, "verbose", "v", "print diagnostics to stderr")
	f.BoolVarP(&opts.Quiet, "quiet", "q", false, "suppress diagnostics (never primary output)")
	f.StringVar(&opts.Color, "color", "auto", "colorize output: auto, always or never (respects NO_COLOR)")

	root.PersistentPreRunE = func(_ *cobra.Command, _ []string) error {
		return opts.validate()
	}

	root.AddCommand(newVersionCmd(), newSymbolsCmd(opts), newMoveCmd(opts), newRestoreCmd(opts))
	return root
}

func versionLine() string {
	return fmt.Sprintf("%s %s (commit %s, built %s)", AppName, Version, Commit, Date)
}

/*
hasHelpFlag reports whether the raw arguments ask for help. They are
scanned before flag parsing so that 'goat cmd --bogus --help' still prints
help instead of a parse error (stock Cobra only does this for
missing-argument errors).
*/
func hasHelpFlag(args []string) bool {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			return true
		}
	}
	return false
}

func printHelp(root *cobra.Command, args []string) int {
	var path []string
	for _, a := range args {
		if a == "--" {
			break
		}
		if !strings.HasPrefix(a, "-") {
			path = append(path, a)
		}
	}
	target, _, err := root.Find(path)
	if err != nil || target == nil {
		target = root
	}
	if err := target.Help(); err != nil {
		fmt.Fprintf(root.ErrOrStderr(), "error: %v\n", err)
		return 1
	}
	return 0
}

/*
rootFlagCollision extracts the flag named by a pflag unknown-flag error
and reports whether it is one of the root flags (dry-run, verbose, quiet,
color). Those are only valid before the command path, so an unknown-flag
error naming one of them earns a placement hint.
*/
func rootFlagCollision(msg string) (string, bool) {
	if name, ok := strings.CutPrefix(msg, "unknown flag: --"); ok {
		if i := strings.IndexAny(name, " ='"); i >= 0 {
			name = name[:i]
		}
		switch name {
		case "dry-run", "verbose", "quiet", "color":
			return "--" + name, true
		}
		return "", false
	}
	if rest, ok := strings.CutPrefix(msg, "unknown shorthand flag: '"); ok {
		name, _, _ := strings.Cut(rest, "'")
		switch name {
		case "n", "v", "q":
			return "-" + name, true
		}
	}
	return "", false
}

/*
isUsageError distinguishes bad command-line input (exit 2) from runtime
failures (exit 1).
*/
func isUsageError(err error) bool {
	var ue *usageError
	if errors.As(err, &ue) {
		return true
	}
	// Cobra's unknown-command and extraneous-argument errors use this wording.
	return strings.HasPrefix(err.Error(), "unknown command")
}
