package goat

import (
	"bytes"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

/*
rewriteCase describes one golden directory under testdata/rewrite: which
symbols move from src to dst, and the expected source-file fate.
*/
type rewriteCase struct {
	src     string
	symbols []string
	dst     string
	removed bool // the emptied source is expected to be removed
	kept    bool // the emptied source is expected to be kept for its comments
}

var rewriteCases = map[string]rewriteCase{
	"happy":                 {src: "file.go", symbols: []string{"Hello", "Shout"}, dst: "dst.go"},
	"trailing_comment":      {src: "file.go", symbols: []string{"Move"}, dst: "dst.go"},
	"free_comment":          {src: "file.go", symbols: []string{"Move"}, dst: "dst.go"},
	"group_split":           {src: "file.go", symbols: []string{"B"}, dst: "dst.go"},
	"iota_group":            {src: "file.go", symbols: []string{"Alpha", "Beta", "Gamma"}, dst: "dst.go"},
	"aliased_imports":       {src: "file.go", symbols: []string{"Pick", "ReadKey"}, dst: "dst.go"},
	"embed":                 {src: "file.go", symbols: []string{"greeting"}, dst: "dst.go"},
	"blank_import":          {src: "file.go", symbols: []string{"Move"}, dst: "dst.go"},
	"build_tags":            {src: "tagged.go", symbols: []string{"Tagged"}, dst: "dst.go"},
	"merge_existing":        {src: "file.go", symbols: []string{"Move"}, dst: "dst.go"},
	"merge_aliased":         {src: "file.go", symbols: []string{"Move"}, dst: "dst.go"},
	"group_split_comment":   {src: "file.go", symbols: []string{"B"}, dst: "dst.go"},
	"emptied_blank_import":  {src: "only.go", symbols: []string{"Only"}, dst: "dst.go", kept: true},
	"emptied_source":        {src: "only.go", symbols: []string{"Only"}, dst: "dst.go", removed: true},
	"emptied_with_comments": {src: "file.go", symbols: []string{"Only"}, dst: "dst.go", kept: true},
}

func TestRewriteGolden(t *testing.T) {
	root := testdataPath(t, "rewrite")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		rc, ok := rewriteCases[name]
		if !ok {
			t.Errorf("golden directory %s has no test configuration", name)
			continue
		}
		t.Run(name, func(t *testing.T) { runRewriteCase(t, root, name, rc) })
	}
	for name := range rewriteCases {
		if info, err := os.Stat(filepath.Join(root, name)); err != nil || !info.IsDir() {
			t.Errorf("test configuration %s has no golden directory", name)
		}
	}
}

/*
runRewriteCase copies the case's in/ tree into a temp module, runs the
move through the public pipeline, applies the rewrite, and compares the
resulting tree against want/ byte-for-byte. The result is then loaded
again to prove the moved package still compiles.
*/
func runRewriteCase(t *testing.T, root, name string, rc rewriteCase) {
	caseDir := filepath.Join(root, name)
	work := t.TempDir()
	copyFile(t, filepath.Join(caseDir, "go.mod"), filepath.Join(work, "go.mod"))
	copyTree(t, filepath.Join(caseDir, "in"), filepath.Join(work, "pkg"))
	pkgDir := filepath.Join(work, "pkg")

	rw := computeRewrite(t, pkgDir, rc)
	if rw.SrcRemoved != rc.removed {
		t.Errorf("SrcRemoved = %v, want %v", rw.SrcRemoved, rc.removed)
	}
	if rw.SrcKept != rc.kept {
		t.Errorf("SrcKept = %v, want %v", rw.SrcKept, rc.kept)
	}

	// Apply the pure result: destination first, then the source.
	if err := os.WriteFile(filepath.Join(pkgDir, rc.dst), rw.Dst, 0o644); err != nil {
		t.Fatal(err)
	}
	if rw.SrcRemoved {
		if err := os.Remove(filepath.Join(pkgDir, rc.src)); err != nil {
			t.Fatal(err)
		}
	} else if err := os.WriteFile(filepath.Join(pkgDir, rc.src), rw.Src, 0o644); err != nil {
		t.Fatal(err)
	}

	compareTrees(t, filepath.Join(caseDir, "want"), pkgDir)

	if _, err := LoadFile(filepath.Join(pkgDir, rc.dst)); err != nil {
		t.Errorf("the destination does not compile after the move: %v", err)
	}
	if !rc.removed {
		if _, err := LoadFile(filepath.Join(pkgDir, rc.src)); err != nil {
			t.Errorf("the source does not compile after the move: %v", err)
		}
	}
}

// computeRewrite runs LoadFile → Index → ParseDst → PlanMove → RewritePlan.
func computeRewrite(t *testing.T, pkgDir string, rc rewriteCase) *Rewrite {
	t.Helper()
	pkg, err := LoadFile(filepath.Join(pkgDir, rc.src))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	f, err := Index(pkg)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	dst, err := ParseDst(filepath.Join(pkgDir, rc.dst))
	if err != nil {
		t.Fatalf("ParseDst: %v", err)
	}
	plan, err := PlanMove(f, rc.symbols, false, dst)
	if err != nil {
		t.Fatalf("PlanMove: %v", err)
	}
	rw, err := RewritePlan(plan)
	if err != nil {
		t.Fatalf("RewritePlan: %v", err)
	}
	return rw
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			copyTree(t, filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()))
			continue
		}
		copyFile(t, filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()))
	}
}

// treeFiles returns the relative paths of all files under dir.
func treeFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	return files
}

/*
compareTrees asserts that want and got contain the same files with the
same bytes.
*/
func compareTrees(t *testing.T, want, got string) {
	t.Helper()
	wantFiles := treeFiles(t, want)
	gotFiles := treeFiles(t, got)
	if len(wantFiles) != len(gotFiles) {
		t.Errorf("file sets differ: want %v, got %v", wantFiles, gotFiles)
	}
	for _, rel := range wantFiles {
		wantData, err := os.ReadFile(filepath.Join(want, rel))
		if err != nil {
			t.Fatal(err)
		}
		gotData, err := os.ReadFile(filepath.Join(got, rel))
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		if !bytes.Equal(gotData, wantData) {
			t.Errorf("%s differs:\n--- got ---\n%s\n--- want ---\n%s", rel, gotData, wantData)
		}
	}
}

func TestSeedImportsKeepsTrailingComment(t *testing.T) {
	/*
		A trailing same-line comment on an import spec travels into the
		seeded destination import block, like declaration trailing comments.
	*/
	src := []byte("package p\n\nimport (\n\t\"fmt\" // formatting\n\t\"strings\"\n)\n\nfunc f() {}\n")
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "p.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	s := &surgery{src: src, fset: fset}
	seed, err := s.seedImports(&File{Path: "p.go", Imports: parsed.Imports}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(seed) != 2 {
		t.Fatalf("seed = %v, want 2 specs", seed)
	}
	if seed[0].text != `"fmt" // formatting` {
		t.Errorf("seed[0] = %q, want the trailing comment kept", seed[0].text)
	}
	if seed[1].text != `"strings"` {
		t.Errorf(`seed[1] = %q, want "strings"`, seed[1].text)
	}
}

func TestApplyEditsOverlapGuard(t *testing.T) {
	src := []byte("hello world")
	if _, err := applyEdits(src, []edit{{0, 5}, {6, 11}}); err != nil {
		t.Errorf("disjoint edits should apply, got %v", err)
	}
	if _, err := applyEdits(src, []edit{{0, 6}, {4, 11}}); err == nil {
		t.Error("overlapping edits should be an error, not a panic")
	}
	if _, err := applyEdits(src, []edit{{0, 100}}); err == nil {
		t.Error("an out-of-bounds edit should be an error, not a panic")
	}
}
