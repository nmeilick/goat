package goat

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

/*
Manifest describes one backup run: the state of every file a goat command
touched, recorded before the first mutation. This is the on-disk format,
not the CLI's JSON output: 'restore --list --json' renders a different
shape (files as plain path strings plus a runId field), so cmd must
marshal a dedicated view struct rather than Manifest itself.
*/
type Manifest struct {
	RunID   string       `json:"-"` // the run's directory name, filled by List and Restore
	Time    time.Time    `json:"time"`
	Workdir string       `json:"workdir"` // symlink-resolved absolute path
	Command string       `json:"command"`
	Files   []BackupFile `json:"files"`
}

// BackupFile records the before-state of one touched file.
type BackupFile struct {
	Path    string      `json:"path"`    // relative to Workdir; ".." segments address outside files
	Mode    os.FileMode `json:"mode"`    // permission bits when the file existed
	Existed bool        `json:"existed"` // false: the run created the file, restore deletes it
}

/*
backupsRoot returns the root of the backup store,
$XDG_STATE_HOME/goat/backups (default ~/.local/state/goat/backups).
*/
func backupsRoot() (string, error) {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("locating the state directory: %w", err)
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "goat", "backups"), nil
}

/*
payloadName maps a manifest path to the name of its backup payload inside
the run directory. Paths inside the workdir are stored under their
relative path for readability. Paths outside it contain ".." segments
that would escape the run directory when joined — two runs touching the
same outside file would overwrite each other's backup — so those are
stored under a deterministic hash of the path instead.
*/
func payloadName(rel string) string {
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return rel
	}
	sum := sha256.Sum256([]byte(rel))
	return fmt.Sprintf("outside-%x", sum[:8])
}

/*
Create backs up the before-state of files (paths in any form, resolved
before recording) as a new run under the backup store and returns the
run's id. Files that do not exist yet are recorded as absent so a restore
deletes them. Nothing but the backup store is written; a failure aborts
before the caller mutates anything.
*/
func Create(workdir string, files []string, command string) (runID string, err error) {
	wd, err := resolveFile(workdir)
	if err != nil {
		return "", err
	}

	type snapshot struct {
		rel     string
		mode    os.FileMode
		existed bool
		content []byte
	}
	snaps := make([]snapshot, 0, len(files))
	for _, f := range files {
		abs, err := resolveFile(f)
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(wd, abs)
		if err != nil {
			return "", err
		}
		s := snapshot{rel: rel}
		info, err := os.Stat(abs)
		switch {
		case err == nil:
			s.existed = true
			s.mode = info.Mode().Perm()
			if s.content, err = os.ReadFile(abs); err != nil {
				return "", fmt.Errorf("reading %s: %w", f, err)
			}
		case os.IsNotExist(err):
		default:
			return "", fmt.Errorf("stating %s: %w", f, err)
		}
		snaps = append(snaps, s)
	}

	root, err := backupsRoot()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("creating the backup store: %w", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	runID, runDir, err := newRunDir(root, now)
	if err != nil {
		return "", err
	}
	// A half-written run must not be listed or restored.
	complete := false
	defer func() {
		if !complete {
			os.RemoveAll(runDir)
		}
	}()

	manifest := Manifest{Time: now, Workdir: wd, Command: command}
	for _, s := range snaps {
		if s.existed {
			dst := filepath.Join(runDir, payloadName(s.rel))
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return "", fmt.Errorf("backing up %s: %w", s.rel, err)
			}
			if err := os.WriteFile(dst, s.content, s.mode); err != nil {
				return "", fmt.Errorf("backing up %s: %w", s.rel, err)
			}
		}
		manifest.Files = append(manifest.Files, BackupFile{Path: s.rel, Mode: s.mode, Existed: s.existed})
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(runDir, "manifest.json"), data, 0o644); err != nil {
		return "", fmt.Errorf("writing the backup manifest: %w", err)
	}
	complete = true
	return runID, nil
}

/*
newRunDir creates the run directory for now, suffixing the timestamp id
with -2, -3, … on collision.
*/
func newRunDir(root string, now time.Time) (runID, runDir string, err error) {
	base := now.Format("2006-01-02T15-04-05")
	for i := 1; ; i++ {
		runID = base
		if i > 1 {
			runID = fmt.Sprintf("%s-%d", base, i)
		}
		runDir = filepath.Join(root, runID)
		if err := os.Mkdir(runDir, 0o755); err == nil {
			return runID, runDir, nil
		} else if !os.IsExist(err) {
			return "", "", fmt.Errorf("creating the backup run: %w", err)
		}
	}
}

/*
List returns the manifests of all backup runs recorded for workdir,
newest first. A missing store is an empty list, not an error.
*/
func List(workdir string) ([]Manifest, error) {
	wd, err := resolveFile(workdir)
	if err != nil {
		return nil, err
	}
	root, err := backupsRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the backup store: %w", err)
	}
	var runs []Manifest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := loadManifest(filepath.Join(root, e.Name()))
		if err != nil || m.Workdir != wd {
			continue // not a complete run, or another workdir's history
		}
		m.RunID = e.Name()
		runs = append(runs, *m)
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].Time.Equal(runs[j].Time) {
			return runIDLess(runs[j].RunID, runs[i].RunID)
		}
		return runs[i].Time.After(runs[j].Time)
	})
	return runs, nil
}

/*
runIDLess orders run ids of one timestamp by their numeric collision
suffix: a bare id sorts first, then -2, -3, … -10 (a plain string compare
would put "-10" before "-2").
*/
func runIDLess(a, b string) bool {
	ab, an := splitRunID(a)
	bb, bn := splitRunID(b)
	if ab != bb {
		return ab < bb
	}
	return an < bn
}

/*
splitRunID separates a run id into its timestamp base and numeric suffix
(1 when unsuffixed).
*/
func splitRunID(id string) (string, int) {
	if i := strings.LastIndexByte(id, '-'); i >= 0 {
		if n, err := strconv.Atoi(id[i+1:]); err == nil {
			return id[:i], n
		}
	}
	return id, 1
}

func loadManifest(runDir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(runDir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", runDir, err)
	}
	return &m, nil
}

/*
Restore puts the before-state of run runID back over the current files:
files the run created are deleted, files it changed are rewritten
atomically with their recorded modes, files it deleted are recreated.
Restore is itself a mutation and backs up the current state first, so a
mistaken restore is undoable. It returns the affected paths.
*/
func Restore(workdir, runID string) ([]string, error) {
	if runID == "" || runID == "." || runID == ".." || strings.ContainsAny(runID, `/\`) {
		return nil, fmt.Errorf("invalid backup run id %q", runID)
	}
	wd, err := resolveFile(workdir)
	if err != nil {
		return nil, err
	}
	root, err := backupsRoot()
	if err != nil {
		return nil, err
	}
	runDir := filepath.Join(root, runID)
	m, err := loadManifest(runDir)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("no backup run %q", runID)
	}
	if err != nil {
		return nil, err
	}
	if m.Workdir != wd {
		return nil, fmt.Errorf("backup run %q was recorded for %s, not %s", runID, m.Workdir, wd)
	}

	// Back up the current state before overwriting anything.
	current := make([]string, 0, len(m.Files))
	for _, f := range m.Files {
		current = append(current, filepath.Join(wd, f.Path))
	}
	if _, err := Create(wd, current, "goat restore "+runID); err != nil {
		return nil, err
	}

	affected := make([]string, 0, len(m.Files))
	for _, f := range m.Files {
		target := filepath.Join(wd, f.Path)
		if !f.Existed {
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
				return affected, fmt.Errorf("removing %s: %w", target, err)
			}
			affected = append(affected, target)
			continue
		}
		content, err := os.ReadFile(filepath.Join(runDir, payloadName(f.Path)))
		if err != nil {
			return affected, fmt.Errorf("reading the backup of %s: %w", f.Path, err)
		}
		if err := writeFileAtomic(target, content, f.Mode); err != nil {
			return affected, err
		}
		affected = append(affected, target)
	}
	return affected, nil
}

/*
writeFileAtomic writes content to path atomically and with the given
permission mode: a temp file in the target's directory is written, synced,
chmodded and renamed over the symlink-resolved target. An interrupted
write never leaves a truncated file, the rename does not tighten the
file's mode, and a symlinked path keeps its symlink while the real file
gets the new content.
*/
func writeFileAtomic(path string, content []byte, mode os.FileMode) error {
	resolved, err := resolveFile(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(resolved), "."+filepath.Base(resolved)+".tmp-*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Rename(tmpName, resolved); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	/*
		Flush the directory entry so the rename itself survives a crash.
		Best-effort: some platforms cannot sync directories.
	*/
	if d, err := os.Open(filepath.Dir(resolved)); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}
