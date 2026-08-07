package goat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

/*
MoveOptions describes one move run. Verify is the post-write compilation
check; nil uses the default, which reloads the package through the
destination file. Tests inject a failing Verify to exercise the rollback.
*/
type MoveOptions struct {
	From     string   // --from path, as given on the command line
	To       string   // --to path, as given on the command line
	Symbols  []string // the positional selection
	WithDeps bool     // move the exclusive dependency closure too
	DryRun   bool     // compute and report, write nothing
	Color    bool     // colorize the dry-run diff
	Command  string   // command line recorded in the backup manifest
	Verify   func(dstPath string) error
}

/*
Result reports what a move did (or, for a dry run, would do), carrying
everything the CLI needs to print.
*/
type Result struct {
	From       string   // --from as given on the command line
	To         string   // --to as given on the command line
	Moved      []string // display names of the moved declarations, in source order
	Warnings   []string // non-fatal notices for stderr
	SrcRemoved bool     // the emptied source file was removed
	SrcKept    bool     // the emptied source was kept for its comments or blank imports
	/*
		SrcKeptBlankImports is true when blank imports (not comments) are the
		reason the emptied source was kept.
	*/
	SrcKeptBlankImports bool
	/*
		SymlinkRemoved names a --from symlink removed along with the emptied
		source it pointed at; empty when --from was a plain path.
	*/
	SymlinkRemoved string
	BackupRunID    string // the backup run taken before writing; empty on dry runs
	Diff           string // the unified diff of both files; dry runs only
}

/*
Move relocates the selected declarations from --from to --to following
the safety pipeline: the whole transformation is planned in memory, a
backup is taken before the first write, the destination is written
atomically before the source is touched, the package is verified to still
compile, and any failure after the first write rolls back from the
backup.
*/
func Move(opts MoveOptions) (*Result, error) {
	/*
		A zero-byte destination breaks the package load with a parser dump
		naming no file; refuse it plainly before loading.
	*/
	if info, err := os.Stat(opts.To); err == nil && info.Size() == 0 {
		return nil, fmt.Errorf("%s: the destination file is empty; remove it or give it a package clause", opts.To)
	}
	pkg, err := LoadFile(opts.From)
	if err != nil {
		return nil, err
	}
	file, err := Index(pkg)
	if err != nil {
		return nil, err
	}
	dst, err := ParseDst(opts.To)
	if err != nil {
		return nil, err
	}
	plan, err := PlanMove(file, opts.Symbols, opts.WithDeps, dst)
	if err != nil {
		return nil, err
	}
	rw, err := RewritePlan(plan)
	if err != nil {
		return nil, err
	}

	res := &Result{
		From:                opts.From,
		To:                  opts.To,
		Warnings:            plan.Warnings,
		SrcRemoved:          rw.SrcRemoved,
		SrcKept:             rw.SrcKept,
		SrcKeptBlankImports: rw.SrcKeptBlankImports,
	}
	for _, d := range plan.Decls {
		res.Moved = append(res.Moved, d.Name)
	}

	if opts.DryRun {
		diff, err := moveDiff(file, dst, rw, opts.Color)
		if err != nil {
			return nil, err
		}
		res.Diff = diff
		return res, nil
	}

	workdir, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	/*
		The backup must be in place before the first write; a failed backup
		aborts the run.
	*/
	runID, err := Create(workdir, []string{file.Path, dst.Path}, opts.Command)
	if err != nil {
		return nil, err
	}
	res.BackupRunID = runID

	dstMode := os.FileMode(0o644)
	if info, err := os.Stat(dst.Path); err == nil {
		dstMode = info.Mode().Perm()
	}
	if err := writeFileAtomic(dst.Path, rw.Dst, dstMode); err != nil {
		return nil, err // nothing written yet
	}

	/*
		Anything failing from here on leaves a written destination behind, so
		the run rolls back from the backup; the backup itself is kept.
	*/
	rollback := func(cause error) error {
		if _, rerr := Restore(workdir, runID); rerr != nil {
			return fmt.Errorf("%v; automatic rollback from backup %s failed too: %v", cause, runID, rerr)
		}
		return fmt.Errorf("%v; the files were restored automatically from backup %s (the backup is kept in the store)", cause, runID)
	}

	srcInfo, err := os.Stat(file.Path)
	if err != nil {
		return nil, rollback(fmt.Errorf("stating %s: %w", opts.From, err))
	}
	if rw.SrcRemoved {
		/*
			A --from given through a symlink dangles once the emptied file
			is gone; identify it before the removal (a dangling link no
			longer resolves) and remove it too.
		*/
		link := sourceSymlink(opts.From, file.Path)
		if err := os.Remove(file.Path); err != nil {
			return nil, rollback(fmt.Errorf("removing the emptied source %s: %w", opts.From, err))
		}
		if link != "" {
			if err := os.Remove(link); err != nil {
				return nil, rollback(fmt.Errorf("removing the dangling symlink %s: %w", link, err))
			}
			res.SymlinkRemoved = link
		}
	} else if err := writeFileAtomic(file.Path, rw.Src, srcInfo.Mode().Perm()); err != nil {
		return nil, rollback(err)
	}

	verify := opts.Verify
	if verify == nil {
		verify = verifyCompiles
	}
	if err := verify(dst.Path); err != nil {
		return nil, rollback(fmt.Errorf("verification after writing failed: %w", err))
	}
	return res, nil
}

/*
sourceSymlink returns the path of from when it is a symlink to
realPath — the link to remove along with an emptied source — or "".
*/
func sourceSymlink(from, realPath string) string {
	abs, err := filepath.Abs(from)
	if err != nil {
		return ""
	}
	info, err := os.Lstat(abs)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil || resolved != realPath {
		return ""
	}
	return abs
}

/*
verifyCompiles is the default post-write check: reloading the package
through the destination type-checks both files of the move.
*/
func verifyCompiles(dstPath string) error {
	_, err := LoadFile(dstPath)
	return err
}

/*
moveDiff renders the pending changes of both files as unified diffs for a
dry run, destination first (the write order). Nothing is written.
*/
func moveDiff(file *File, dst *DstFile, rw *Rewrite, color bool) (string, error) {
	oldSrc, err := os.ReadFile(file.Path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", file.Path, err)
	}
	var oldDst []byte
	dstOldName := "/dev/null"
	if dst.Exists {
		if oldDst, err = os.ReadFile(dst.Path); err != nil {
			return "", fmt.Errorf("reading %s: %w", dst.Path, err)
		}
		dstOldName = filepath.Base(dst.Path)
	}
	var out strings.Builder
	out.WriteString(UnifiedDiff(dstOldName, filepath.Base(dst.Path), oldDst, rw.Dst, color))
	if rw.SrcRemoved {
		out.WriteString(UnifiedDiff(filepath.Base(file.Path), "/dev/null", oldSrc, nil, color))
	} else {
		out.WriteString(UnifiedDiff(filepath.Base(file.Path), filepath.Base(file.Path), oldSrc, rw.Src, color))
	}
	return out.String(), nil
}
