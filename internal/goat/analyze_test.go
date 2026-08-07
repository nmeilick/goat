package goat

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// indexSample loads and indexes testdata/sample/file.go.
func indexSample(t *testing.T) *File {
	t.Helper()
	pkg, err := LoadFile(testdataPath(t, "sample", "file.go"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	f, err := Index(pkg)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	return f
}

func TestIndexKinds(t *testing.T) {
	f := indexSample(t)

	wantKinds := map[string]DeclKind{
		"helper":      FuncDecl,
		"File.Stat":   MethodDecl,
		"List.Get":    MethodDecl,
		"File":        TypeDecl,
		"Mode":        TypeDecl,
		"defaultMode": ConstDecl,
		"DefaultName": VarDecl,
	}
	for name, kind := range wantKinds {
		d := f.ByName(name)
		if d == nil {
			t.Errorf("no declaration indexed under %q", name)
			continue
		}
		if d.Kind != kind {
			t.Errorf("%s: kind = %q, want %q", name, d.Kind, kind)
		}
	}

	iota := f.ByName("ModeFast")
	if iota == nil {
		t.Fatal("iota group not indexed under ModeFast")
	}
	if iota.Kind != ConstDecl {
		t.Errorf("iota group kind = %q, want %q", iota.Kind, ConstDecl)
	}
	count := 0
	for _, d := range f.Decls {
		if d == iota {
			count++
		}
	}
	if count != 1 {
		t.Errorf("iota group appears %d times in Decls, want 1", count)
	}

	if got := len(f.Decls); got != 9 {
		t.Errorf("len(Decls) = %d, want 9", got)
	}
}

func TestIotaGroupIndivisible(t *testing.T) {
	f := indexSample(t)
	fast := f.ByName("ModeFast")
	slow := f.ByName("ModeSlow")
	if fast == nil || slow == nil {
		t.Fatalf("iota members not indexed: ModeFast=%v ModeSlow=%v", fast != nil, slow != nil)
	}
	if fast != slow {
		t.Error("ModeFast and ModeSlow map to different decls; iota group must be one decl")
	}
	if !strings.Contains(fast.Name, "ModeFast") || !strings.Contains(fast.Name, "ModeSlow") {
		t.Errorf("iota group name %q should join both member names", fast.Name)
	}
}

func TestGraphEdges(t *testing.T) {
	f := indexSample(t)

	stat := f.ByName("File.Stat")
	helper := f.ByName("helper")
	defaultMode := f.ByName("defaultMode")
	if stat == nil || helper == nil || defaultMode == nil {
		t.Fatal("File.Stat, helper and defaultMode must all be indexed")
	}

	if !usesDecl(stat, helper) {
		t.Errorf("File.Stat should use helper, Uses = %v", declNames(stat.Uses))
	}
	if !usesDecl(helper, defaultMode) {
		t.Errorf("helper should use defaultMode, Uses = %v", declNames(helper.Uses))
	}
}

func TestMethodReceiverEdge(t *testing.T) {
	/*
		A method's receiver type counts as a use of the type (a method→type
		edge), so --with-deps can pull a type only a moving method uses.
	*/
	f := indexSample(t)

	stat := f.ByName("File.Stat")
	fileType := f.ByName("File")
	get := f.ByName("List.Get")
	list := f.ByName("List")
	if stat == nil || fileType == nil || get == nil || list == nil {
		t.Fatal("File.Stat, File, List.Get and List must all be indexed")
	}

	if !usesDecl(stat, fileType) {
		t.Errorf("File.Stat should use its receiver type File, Uses = %v", declNames(stat.Uses))
	}
	if !usesDecl(get, list) {
		t.Errorf("List.Get should use its receiver type List, Uses = %v", declNames(get.Uses))
	}
	for _, d := range f.Decls {
		if usesDecl(d, d) {
			t.Errorf("%s must not use itself", d.Name)
		}
		if d.Kind == MethodDecl {
			for _, u := range d.Uses {
				if u.Kind == MethodDecl {
					t.Errorf("%s uses method %s; receiver edges must be method→type only", d.Name, u.Name)
				}
			}
		}
	}
}

func TestUsedElsewhere(t *testing.T) {
	f := indexSample(t)

	fileType := f.ByName("File")
	if !fileType.UsedElsewhere {
		t.Error("File should be used elsewhere (other.go)")
	}
	if !fileType.ExternalFiles["other.go"] {
		t.Errorf("File external refs = %v, want other.go", fileType.ExternalFiles)
	}

	helper := f.ByName("helper")
	if !helper.UsedElsewhere {
		t.Error("helper should be used elsewhere (file_test.go)")
	}
	if !helper.ExternalFiles["file_test.go"] {
		t.Errorf("helper external refs = %v, want file_test.go", helper.ExternalFiles)
	}

	defaultMode := f.ByName("defaultMode")
	if defaultMode.UsedElsewhere {
		t.Errorf("defaultMode is file-local, external refs = %v, want none", defaultMode.ExternalFiles)
	}
}

func TestMetadata(t *testing.T) {
	f := indexSample(t)

	if f.PackageName != "sample" {
		t.Errorf("package name = %q, want %q", f.PackageName, "sample")
	}

	foundTag := false
	for _, tag := range f.BuildTags {
		if tag == "//go:build go1.18" {
			foundTag = true
		}
	}
	if !foundTag {
		t.Errorf("build tags = %v, want a //go:build go1.18 line", f.BuildTags)
	}

	var imports []string
	for _, imp := range f.Imports {
		imports = append(imports, imp.Path.Value)
	}
	if len(imports) != 2 || imports[0] != `"fmt"` || imports[1] != `"strings"` {
		t.Errorf("imports = %v, want [\"fmt\" \"strings\"]", imports)
	}
}

func TestGenericReceiver(t *testing.T) {
	f := indexSample(t)

	d := f.ByName("List.Get")
	if d == nil {
		t.Fatal("List[T].Get should index as List.Get")
	}
	if d.Kind != MethodDecl {
		t.Errorf("List.Get kind = %q, want %q", d.Kind, MethodDecl)
	}
	if f.ByName("List[T].Get") != nil {
		t.Error("List[T].Get should not be an index key")
	}
}

func usesDecl(d, target *Decl) bool {
	for _, u := range d.Uses {
		if u == target {
			return true
		}
	}
	return false
}

func declNames(decls []*Decl) []string {
	var names []string
	for _, d := range decls {
		names = append(names, d.Name)
	}
	return names
}

func TestBuildTagsOnlyBeforePackageClause(t *testing.T) {
	/*
		A doc comment after the package clause mentioning //go:build in
		prose is not a constraint and must not be collected.
	*/
	src := `//go:build go1.18

package p

// Doc explains the //go:build directive syntax.
func Doc() {}
`
	f, err := parser.ParseFile(token.NewFileSet(), "p.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	tags := buildTags(f)
	if len(tags) != 1 || tags[0] != "//go:build go1.18" {
		t.Errorf("buildTags = %v, want only [//go:build go1.18]", tags)
	}
}
