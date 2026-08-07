package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nmeilick/goat/internal/goat"
)

func newMoveCmd(opts *Options) *cobra.Command {
	var from, to string
	var withDeps bool
	cmd := &cobra.Command{
		Use:   "move <SYMBOL...> --from <file.go> --to <file.go>",
		Short: "Move top-level declarations from one Go file to another in the same package",
		Long: `move relocates top-level declarations — functions, methods, types,
constants and variables — from one Go file to another file in the same
package and fixes imports on both sides. It is the primitive for
splitting a large file into scoped files. Every move is backed up
first; 'goat restore' reverts it.

Symbols are the names 'goat symbols' lists; methods are
receiver-qualified (Type.Method) because unrelated types can share
method names. Each argument may be a comma-separated list. An argument
containing '*' is a glob over the file's declarations; an argument
starting with '!' excludes what it names after all inclusions expand —
quote it against shell history expansion.

--with-deps also moves the exclusive dependency closure: declarations
referenced only by the moving set, transitively. Anything used from
another file or a test file stays put.

Exit codes: 0 success, 1 failure (no files touched), 2 usage error.`,
		Example: `  goat move FileStat --from file.go --to file_access.go
  goat --dry-run move 'File*' '!FileModify' -f file.go -t file_access.go
  goat move ChangeFileOwner --with-deps -f file.go -t file_access.go`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return &usageError{"missing required <SYMBOL> argument"}
			}
			if !hasSymbol(args) {
				return &usageError{"no symbols given to move"}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if from == "" {
				return &usageError{"required flag --from not set"}
			}
			if to == "" {
				return &usageError{"required flag --to not set"}
			}
			if samePath(from, to) {
				return &usageError{fmt.Sprintf("--to must differ from --from (both are %s)", from)}
			}

			stderr := cmd.ErrOrStderr()
			diag := func(format string, a ...any) {
				if opts.Verbose > 0 && !opts.Quiet {
					fmt.Fprintf(stderr, format+"\n", a...)
				}
			}

			record := append([]string{AppName, "move"}, args...)
			record = append(record, "--from", from, "--to", to)
			if withDeps {
				record = append(record, "--with-deps")
			}
			command := quoteArgs(record)

			diag("loading %s", from)
			res, err := goat.Move(goat.MoveOptions{
				From:     from,
				To:       to,
				Symbols:  args,
				WithDeps: withDeps,
				DryRun:   opts.DryRun,
				Color:    opts.useColor(cmd.OutOrStdout()),
				Command:  command,
			})
			if err != nil {
				return err
			}
			if !opts.Quiet {
				for _, w := range res.Warnings {
					fmt.Fprintf(stderr, "warning: %s\n", w)
				}
			}
			if opts.DryRun {
				fmt.Fprint(cmd.OutOrStdout(), res.Diff)
				return nil
			}
			diag("backup run %s recorded", res.BackupRunID)
			diag("post-move verification passed")

			out := cmd.OutOrStdout()
			noun := "declarations"
			if len(res.Moved) == 1 {
				noun = "declaration"
			}
			fmt.Fprintf(out, "moved %d %s: %s → %s (%s)", len(res.Moved), noun, from, to, strings.Join(res.Moved, ", "))
			switch {
			case res.SrcRemoved:
				fmt.Fprint(out, "; source file removed")
				if res.SymlinkRemoved != "" {
					fmt.Fprintf(out, " (symlink %s removed too)", res.SymlinkRemoved)
				}
			case res.SrcKept && res.SrcKeptBlankImports:
				fmt.Fprint(out, "; source file kept (blank imports remain)")
			case res.SrcKept:
				fmt.Fprint(out, "; source file kept with comments only")
			}
			fmt.Fprintln(out)
			fmt.Fprintf(out, "backup %s (undo with 'goat restore')\n", res.BackupRunID)
			return nil
		},
	}
	cmd.Flags().StringVarP(&from, "from", "f", "", "source file the symbols move out of (required)")
	cmd.Flags().StringVarP(&to, "to", "t", "", "destination file in the same directory, new or existing (required)")
	cmd.Flags().BoolVar(&withDeps, "with-deps", false, "also move the exclusive dependency closure of the symbols")
	return cmd
}

/*
quoteArgs renders a command line for the backup record and restore
listings. Words made of shell-safe characters stay bare; anything else
gets POSIX quoting: single quotes by preference, double quotes when the
word contains a single quote, and backslash-escaped single quotes when
both kinds appear.
*/
func quoteArgs(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = quoteArg(a)
	}
	return strings.Join(q, " ")
}

func quoteArg(a string) string {
	switch {
	case a == "":
		return "''"
	case strings.Trim(a, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-./:=,@%+") == "":
		return a
	case !strings.Contains(a, "'"):
		return "'" + a + "'"
	case !strings.ContainsAny(a, "\"$`\\!"):
		return `"` + a + `"`
	default:
		return "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
}

/*
hasSymbol reports whether the symbol arguments contain any non-empty
comma-separated segment. An empty selection (goat move ,, ...) is a usage
error checked before the package loads; exclusion-only selections pass
through to the engine's plan-level refusal.
*/
func hasSymbol(args []string) bool {
	for _, arg := range args {
		for _, seg := range strings.Split(arg, ",") {
			if seg = strings.TrimSpace(seg); seg != "" && seg != "!" {
				return true
			}
		}
	}
	return false
}

func samePath(a, b string) bool {
	if a == b {
		return true
	}
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return false
	}
	if aa == bb {
		return true
	}
	ra, errA := filepath.EvalSymlinks(aa)
	rb, errB := filepath.EvalSymlinks(bb)
	return errA == nil && errB == nil && ra == rb
}
