package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nmeilick/goat/internal/goat"
)

func newRestoreCmd(opts *Options) *cobra.Command {
	var list, jsonOut bool
	cmd := &cobra.Command{
		Use:   "restore [--list [--json] | <run-id>]",
		Short: "Revert a previous goat run from its backup",
		Long: `restore undoes a previous goat run by copying its backed-up
before-state back over the current files: with a run-id it restores that
run, with no argument the most recent one. Every move is backed up before
its first write, and restore backs up the current state first too, so a
mistaken restore is itself undoable with another restore. --list shows
the backup runs recorded for the current directory, newest first; --dry-run
shows what would change without writing anything.

With --list --json the listing is machine-readable:

  {"backups": [
    {"runId": "2026-08-07T09-41-03", "time": "2026-08-07T09:41:03Z",
     "command": "goat move FileStat --from file.go --to file_access.go",
     "files": ["file.go", "file_access.go"]}
  ]}

Exit codes: 0 success, 1 failure (no files touched), 2 usage error.`,
		Example: `  goat restore --list
  goat restore
  goat restore 2026-08-07T09-41-03`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return &usageError{fmt.Sprintf("unknown command %q for %q", args[1], cmd.CommandPath())}
			}
			if list && len(args) == 1 {
				return &usageError{"--list takes no run-id argument"}
			}
			if jsonOut && !list {
				return &usageError{"--json only applies together with --list"}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			workdir, err := os.Getwd()
			if err != nil {
				return err
			}
			runs, err := goat.List(workdir)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			if list {
				if jsonOut {
					return writeBackupsJSON(out, runs)
				}
				writeBackupsText(out, runs)
				return nil
			}

			runID := ""
			if len(args) == 1 {
				runID = args[0]
				found := false
				for _, r := range runs {
					if r.RunID == runID {
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("no backup run %q for this directory; run 'goat restore --list' to see the available runs", runID)
				}
			} else {
				if len(runs) == 0 {
					return fmt.Errorf("no backups found for %s", workdir)
				}
				runID = runs[0].RunID
			}

			var manifest *goat.Manifest
			for i := range runs {
				if runs[i].RunID == runID {
					manifest = &runs[i]
					break
				}
			}

			if opts.DryRun {
				fmt.Fprintf(out, "would restore backup %s:\n", runID)
				for _, f := range manifest.Files {
					if f.Existed {
						fmt.Fprintf(out, "  restore %s\n", f.Path)
					} else {
						fmt.Fprintf(out, "  remove %s (created by the run)\n", f.Path)
					}
				}
				fmt.Fprintln(out, "the current state would be backed up first")
				return nil
			}

			affected, err := goat.Restore(workdir, runID)
			if err != nil {
				return err
			}
			diag := func(format string, a ...any) {
				if opts.Verbose > 0 && !opts.Quiet {
					fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", a...)
				}
			}
			for _, p := range affected {
				diag("restored %s", p)
			}
			fmt.Fprintf(out, "restored %d files from backup %s\n", len(affected), runID)
			return nil
		},
	}
	cmd.Flags().BoolVar(&list, "list", false, "list the backup runs of the current directory, newest first")
	cmd.Flags().BoolVarP(&jsonOut, "json", "j", false, "print the --list listing as indented JSON")
	return cmd
}

/*
backupRunJSON is the CLI's view of one backup run for
'restore --list --json': plain path strings and the run id, not the
on-disk Manifest shape (modes, existed flags, workdir).
*/
type backupRunJSON struct {
	RunID   string   `json:"runId"`
	Time    string   `json:"time"`
	Command string   `json:"command"`
	Files   []string `json:"files"`
}

func writeBackupsJSON(w io.Writer, runs []goat.Manifest) error {
	doc := struct {
		Backups []backupRunJSON `json:"backups"`
	}{Backups: []backupRunJSON{}}
	for _, r := range runs {
		files := make([]string, 0, len(r.Files))
		for _, f := range r.Files {
			files = append(files, f.Path)
		}
		doc.Backups = append(doc.Backups, backupRunJSON{
			RunID:   r.RunID,
			Time:    r.Time.Format("2006-01-02T15:04:05Z"),
			Command: r.Command,
			Files:   files,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

func writeBackupsText(w io.Writer, runs []goat.Manifest) {
	if len(runs) == 0 {
		fmt.Fprintln(w, "no backups found")
		return
	}
	for _, r := range runs {
		files := make([]string, 0, len(r.Files))
		for _, f := range r.Files {
			files = append(files, f.Path)
		}
		fmt.Fprintf(w, "%s  %s\n  %s\n  files: %s\n",
			r.RunID, r.Time.Format("2006-01-02T15:04:05Z"), r.Command, strings.Join(files, ", "))
	}
}
