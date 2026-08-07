package goat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// planSource loads and indexes a file of testdata/plan.
func planSource(t *testing.T, name string) *File {
	t.Helper()
	pkg, err := LoadFile(testdataPath(t, "plan", name))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	f, err := Index(pkg)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	return f
}

/*
newFileDst returns the DstFile for a not-yet-existing destination in the
source file's own directory.
*/
func newFileDst(t *testing.T, f *File) *DstFile {
	t.Helper()
	dst, err := ParseDst(filepath.Join(filepath.Dir(f.Path), "out.go"))
	if err != nil {
		t.Fatalf("ParseDst: %v", err)
	}
	if dst.Exists {
		t.Fatal("out.go should not exist")
	}
	return dst
}

/*
copyPlanPkg copies testdata/plan into a temp dir — required for tests
that create destination files next to the source, since checked-in
testdata must never be mutated. Returns the temp package directory.
*/
func copyPlanPkg(t *testing.T) string {
	t.Helper()
	src := testdataPath(t, "plan")
	dst := t.TempDir()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

// planSourceIn loads and indexes a file of a copied plan package.
func planSourceIn(t *testing.T, dir, name string) *File {
	t.Helper()
	pkg, err := LoadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	f, err := Index(pkg)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	return f
}

// writeDst writes content as name inside dir and returns its DstFile.
func writeDst(t *testing.T, dir, name, content string) *DstFile {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	dst, err := ParseDst(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("ParseDst: %v", err)
	}
	return dst
}

func planNames(p *Plan) []string {
	var names []string
	for _, d := range p.Decls {
		names = append(names, d.Name)
	}
	return names
}

func hasName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func TestRefuseInit(t *testing.T) {
	f := planSource(t, "file.go")
	_, err := PlanMove(f, []string{"init"}, false, newFileDst(t, f))
	if err == nil || !strings.Contains(err.Error(), "init") {
		t.Fatalf("expected an init refusal, got %v", err)
	}
}

func TestRefusePartialIota(t *testing.T) {
	f := planSource(t, "file.go")
	_, err := PlanMove(f, []string{"Alpha"}, false, newFileDst(t, f))
	if err == nil || !strings.Contains(err.Error(), "iota") {
		t.Fatalf("expected a partial-iota refusal, got %v", err)
	}
}

func TestWildcardPartialIota(t *testing.T) {
	// Alpha* matches only one member of the Alpha/Beta iota group.
	f := planSource(t, "file.go")
	_, err := PlanMove(f, []string{"Alpha*"}, false, newFileDst(t, f))
	if err == nil || !strings.Contains(err.Error(), "iota") {
		t.Fatalf("expected a partial-iota refusal, got %v", err)
	}
}

func TestRefusePartialConstRepeat(t *testing.T) {
	// ConstB implicitly repeats ConstA's expression: whole block or nothing.
	f := planSource(t, "file.go")
	_, err := PlanMove(f, []string{"ConstA"}, false, newFileDst(t, f))
	if err == nil || !strings.Contains(err.Error(), "ConstA") {
		t.Fatalf("expected a const-repeat refusal naming ConstA, got %v", err)
	}
	if !strings.Contains(err.Error(), "whole block") {
		t.Errorf("error %q should state the whole-block remedy", err)
	}
}

func TestRefuseDuplicateInDst(t *testing.T) {
	dir := copyPlanPkg(t)
	f := planSourceIn(t, dir, "file.go")
	dst := writeDst(t, dir, "dst.go", "package plan\n\nfunc Top() string {\n\treturn \"\"\n}\n")
	_, err := PlanMove(f, []string{"Top"}, false, dst)
	if err == nil || !strings.Contains(err.Error(), "already declares") {
		t.Fatalf("expected a duplicate-declaration refusal, got %v", err)
	}
	if !strings.Contains(err.Error(), "Top") {
		t.Errorf("error %q should name the duplicate symbol", err)
	}
}

func TestRefuseOutsideDir(t *testing.T) {
	f := planSource(t, "file.go")
	dst, err := ParseDst(filepath.Join(t.TempDir(), "dst.go"))
	if err != nil {
		t.Fatalf("ParseDst: %v", err)
	}
	_, err = PlanMove(f, []string{"Top"}, false, dst)
	if err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("expected an outside-directory refusal, got %v", err)
	}
}

func TestRefuseUnparseableDst(t *testing.T) {
	dir := copyPlanPkg(t)
	f := planSourceIn(t, dir, "file.go")
	dst := writeDst(t, dir, "dst.go", "package plan\n\nfunc broken(\n")
	_, err := PlanMove(f, []string{"Top"}, false, dst)
	if err == nil || !strings.Contains(err.Error(), "does not parse") {
		t.Fatalf("expected an unparseable-destination refusal, got %v", err)
	}
}

func TestRefuseBuildTagMismatch(t *testing.T) {
	dir := copyPlanPkg(t)
	f := planSourceIn(t, dir, "tagged.go")
	dst := writeDst(t, dir, "dst.go", "package plan\n")
	_, err := PlanMove(f, []string{"Tagged"}, false, dst)
	if err == nil || !strings.Contains(err.Error(), "build tags") {
		t.Fatalf("expected a build-tag mismatch refusal, got %v", err)
	}
}

func TestCommaAndSpaceSelection(t *testing.T) {
	f := planSource(t, "file.go")
	p, err := PlanMove(f, []string{"Top,mid", "leaf"}, false, newFileDst(t, f))
	if err != nil {
		t.Fatalf("PlanMove: %v", err)
	}
	names := planNames(p)
	if len(names) != 3 || !hasName(names, "Top") || !hasName(names, "mid") || !hasName(names, "leaf") {
		t.Errorf("moving set = %v, want [Top mid leaf]", names)
	}
}

func TestUnknownSymbolSuggests(t *testing.T) {
	f := planSource(t, "file.go")
	_, err := PlanMove(f, []string{"FileM"}, false, newFileDst(t, f))
	if err == nil || !strings.Contains(err.Error(), `unknown symbol "FileM"`) {
		t.Fatalf("expected an unknown-symbol error, got %v", err)
	}
	if !strings.Contains(err.Error(), "FileMode") {
		t.Errorf("error %q should suggest FileMode", err)
	}
}

func TestBareMethodSuggestsQualified(t *testing.T) {
	f := planSource(t, "file.go")
	_, err := PlanMove(f, []string{"Stat"}, false, newFileDst(t, f))
	if err == nil || !strings.Contains(err.Error(), "File.Stat") {
		t.Fatalf("expected a bare-method error naming File.Stat, got %v", err)
	}
	if !strings.Contains(err.Error(), "Dir.Stat") {
		t.Errorf("error %q should also name Dir.Stat", err)
	}
}

func TestMethodDisambiguation(t *testing.T) {
	f := planSource(t, "file.go")

	p, err := PlanMove(f, []string{"File.Stat"}, false, newFileDst(t, f))
	if err != nil {
		t.Fatalf("PlanMove File.Stat: %v", err)
	}
	if names := planNames(p); len(names) != 1 || names[0] != "File.Stat" {
		t.Errorf("moving File.Stat = %v, want [File.Stat]", names)
	}

	p, err = PlanMove(f, []string{"Dir.Stat"}, false, newFileDst(t, f))
	if err != nil {
		t.Fatalf("PlanMove Dir.Stat: %v", err)
	}
	if names := planNames(p); len(names) != 1 || names[0] != "Dir.Stat" {
		t.Errorf("moving Dir.Stat = %v, want [Dir.Stat]", names)
	}
}

func TestMultiNameSpecMovesTogether(t *testing.T) {
	f := planSource(t, "file.go")
	p, err := PlanMove(f, []string{"MultiA"}, false, newFileDst(t, f))
	if err != nil {
		t.Fatalf("PlanMove: %v", err)
	}
	names := planNames(p)
	if len(names) != 1 || names[0] != "MultiA, MultiB" {
		t.Errorf("moving set = %v, want the whole spec [MultiA, MultiB]", names)
	}
}

func TestExclusionInsideMultiNameSpec(t *testing.T) {
	f := planSource(t, "file.go")
	_, err := PlanMove(f, []string{"MultiA", "!MultiB"}, false, newFileDst(t, f))
	if err == nil || !strings.Contains(err.Error(), "MultiB") {
		t.Fatalf("expected a multi-name-spec exclusion error naming MultiB, got %v", err)
	}
	if !strings.Contains(err.Error(), "moves as a unit") {
		t.Errorf("error %q should state the spec moves as a unit", err)
	}
}

func TestWildcardSelection(t *testing.T) {
	f := planSource(t, "file.go")
	p, err := PlanMove(f, []string{"File*"}, false, newFileDst(t, f))
	if err != nil {
		t.Fatalf("PlanMove: %v", err)
	}
	names := planNames(p)
	for _, want := range []string{"File", "File.Stat", "FileModify", "FileMode"} {
		if !hasName(names, want) {
			t.Errorf("File* expansion %v is missing %s", names, want)
		}
	}
	if len(names) != 4 {
		t.Errorf("File* expansion = %v, want exactly 4 declarations", names)
	}
}

func TestWildcardExclusion(t *testing.T) {
	f := planSource(t, "file.go")
	p, err := PlanMove(f, []string{"File*", "!FileModify"}, false, newFileDst(t, f))
	if err != nil {
		t.Fatalf("PlanMove: %v", err)
	}
	names := planNames(p)
	for _, want := range []string{"File", "File.Stat", "FileMode"} {
		if !hasName(names, want) {
			t.Errorf("moving set %v is missing %s", names, want)
		}
	}
	if hasName(names, "FileModify") {
		t.Errorf("FileModify should have been excluded, moving set = %v", names)
	}
}

func TestWildcardMatchesNothing(t *testing.T) {
	f := planSource(t, "file.go")
	_, err := PlanMove(f, []string{"Nope*"}, false, newFileDst(t, f))
	if err == nil || !strings.Contains(err.Error(), "Nope*") {
		t.Fatalf("expected an error naming the pattern, got %v", err)
	}
}

func TestExclusionEmptiesSelection(t *testing.T) {
	f := planSource(t, "file.go")
	_, err := PlanMove(f, []string{"Top", "!Top"}, false, newFileDst(t, f))
	if err == nil || !strings.Contains(err.Error(), "nothing left to move") {
		t.Fatalf("expected a nothing-left-to-move error, got %v", err)
	}
}

func TestGeneratedWarning(t *testing.T) {
	f := planSource(t, "gen.go")
	p, err := PlanMove(f, []string{"GenHelper"}, false, newFileDst(t, f))
	if err != nil {
		t.Fatalf("PlanMove: %v", err)
	}
	if len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "generated") {
		t.Errorf("warnings = %v, want one generated-code warning", p.Warnings)
	}
	if names := planNames(p); len(names) != 1 || names[0] != "GenHelper" {
		t.Errorf("moving set = %v, want [GenHelper]", names)
	}
}

// closurePlan moves Top --with-deps from testdata/plan/file.go.
func closurePlan(t *testing.T) *Plan {
	t.Helper()
	f := planSource(t, "file.go")
	p, err := PlanMove(f, []string{"Top"}, true, newFileDst(t, f))
	if err != nil {
		t.Fatalf("PlanMove: %v", err)
	}
	return p
}

func TestClosureTransitive(t *testing.T) {
	p := closurePlan(t)
	names := planNames(p)
	for _, want := range []string{"Top", "mid", "leaf"} {
		if !hasName(names, want) {
			t.Errorf("closure %v is missing %s", names, want)
		}
	}
	if hasName(names, "deadCode") {
		t.Errorf("unreferenced deadCode must never be pulled, closure = %v", names)
	}
}

func TestClosureStopsAtSharedHelper(t *testing.T) {
	p := closurePlan(t)
	if names := planNames(p); hasName(names, "sharedHelper") {
		t.Errorf("sharedHelper is used by other.go and must stay, closure = %v", names)
	}
}

func TestClosureStopsAtTestUsedHelper(t *testing.T) {
	p := closurePlan(t)
	if names := planNames(p); hasName(names, "testHelper") {
		t.Errorf("testHelper is used by file_test.go and must stay, closure = %v", names)
	}
}

func TestTypeStaysWhenMethodMoves(t *testing.T) {
	f := planSource(t, "file.go")
	p, err := PlanMove(f, []string{"File.Stat"}, true, newFileDst(t, f))
	if err != nil {
		t.Fatalf("PlanMove: %v", err)
	}
	names := planNames(p)
	if !hasName(names, "File.Stat") {
		t.Errorf("moving set %v is missing File.Stat", names)
	}
	if hasName(names, "File") {
		t.Errorf("File is used by other.go and must stay when its method moves, moving set = %v", names)
	}
}

func TestClosurePullsExclusiveType(t *testing.T) {
	p := closurePlan(t)
	if names := planNames(p); !hasName(names, "config") {
		t.Errorf("config is used only by moved code and must move with it, closure = %v", names)
	}
}

func TestWholeIotaGroupMoves(t *testing.T) {
	f := planSource(t, "file.go")
	p, err := PlanMove(f, []string{"Alpha", "Beta"}, false, newFileDst(t, f))
	if err != nil {
		t.Fatalf("PlanMove: %v", err)
	}
	names := planNames(p)
	if len(names) != 1 || names[0] != "Alpha, Beta" {
		t.Errorf("moving set = %v, want the intact group [Alpha, Beta]", names)
	}
}

func TestRefuseTestFileDst(t *testing.T) {
	/*
		--to naming a _test.go file refuses before anything is written,
		the same scope rule load.go enforces for --from.
	*/
	_, err := ParseDst(testdataPath(t, "plan", "file_test.go"))
	if err == nil || !strings.Contains(err.Error(), "test files") {
		t.Fatalf("expected a test-file refusal, got %v", err)
	}
}

func TestRefuseCgoDst(t *testing.T) {
	// --to naming a file with import "C" refuses early, not post-write.
	_, err := ParseDst(testdataPath(t, "cgo", "cgo.go"))
	if err == nil || !strings.Contains(err.Error(), "cgo") {
		t.Fatalf("expected a cgo refusal, got %v", err)
	}
}

func TestRefuseSingleLineGroupSplit(t *testing.T) {
	/*
		var ( OneA = 1; OneB = 2 ) cannot be split textually: the specs
		share one line. Checked-in testdata must stay gofmt-clean, so the
		one-line group is written into the temp copy.
	*/
	dir := copyPlanPkg(t)
	content := "package plan\n\nvar ( OneA = 1; OneB = 2 )\n"
	if err := os.WriteFile(filepath.Join(dir, "oneline.go"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	f := planSourceIn(t, dir, "oneline.go")

	_, err := PlanMove(f, []string{"OneA"}, false, newFileDst(t, f))
	if err == nil || !strings.Contains(err.Error(), "share a line") {
		t.Fatalf("expected a single-line-group refusal, got %v", err)
	}

	// Selecting the whole group moves it verbatim: no split, no refusal.
	if _, err := PlanMove(f, []string{"OneA", "OneB"}, false, newFileDst(t, f)); err != nil {
		t.Fatalf("moving the whole group should not refuse, got %v", err)
	}
}

func TestRefuseNonGoDst(t *testing.T) {
	/*
		--to naming a non-.go file refuses plainly instead of dying in
		post-write verification with a build-constraint message.
	*/
	_, err := ParseDst(testdataPath(t, "plan", "output.txt"))
	if err == nil || !strings.Contains(err.Error(), ".go file") {
		t.Fatalf("expected a .go-only refusal, got %v", err)
	}
}
