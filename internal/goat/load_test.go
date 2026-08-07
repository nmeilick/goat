package goat

import (
	"go/ast"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testdataPath returns the absolute path of a file inside testdata.
func testdataPath(t *testing.T, elems ...string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join(append([]string{"testdata"}, elems...)...))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestLoadGood(t *testing.T) {
	pkg, err := LoadFile(testdataPath(t, "sample", "file.go"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if pkg.Fset == nil || pkg.Plain == nil || pkg.File == nil {
		t.Fatal("expected fileset, plain package and file AST to be set")
	}
	if pkg.Plain.Name != "sample" {
		t.Errorf("plain package name = %q, want %q", pkg.Plain.Name, "sample")
	}
	if base := filepath.Base(pkg.Path); base != "file.go" {
		t.Errorf("resolved path = %q, want file.go", base)
	}

	foundTestVariant := false
	for _, v := range pkg.Variants {
		if v == pkg.Plain {
			continue
		}
		for _, f := range v.GoFiles {
			if filepath.Base(f) == "file_test.go" {
				foundTestVariant = true
			}
		}
	}
	if !foundTestVariant {
		t.Error("no package variant contains file_test.go; Tests: true variants missing")
	}
}

func TestLoadMainPackage(t *testing.T) {
	/*
		A real main package has Name "main"; the variant containing the
		target file must still be selected over the generated testmain
		binary variant (also Name "main" with ID == PkgPath).
	*/
	pkg, err := LoadFile(testdataPath(t, "mainpkg", "main.go"))
	if err != nil {
		t.Fatalf("LoadFile of a main package: %v", err)
	}
	if pkg.Plain.Name != "main" {
		t.Errorf("plain package name = %q, want %q", pkg.Plain.Name, "main")
	}
	if base := filepath.Base(pkg.Path); base != "main.go" {
		t.Errorf("resolved path = %q, want main.go", base)
	}
	if pkg.File.Name.Name != "main" {
		t.Errorf("file AST package name = %q, want %q", pkg.File.Name.Name, "main")
	}
	foundMain := false
	for _, d := range pkg.File.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "main" {
			foundMain = true
		}
	}
	if !foundMain {
		t.Error("the file AST should be main.go and declare func main")
	}
}

func TestLoadBroken(t *testing.T) {
	_, err := LoadFile(testdataPath(t, "broken", "broken.go"))
	if err == nil {
		t.Fatal("expected an error for a package that does not compile")
	}
	if !strings.Contains(err.Error(), "does not compile") {
		t.Errorf("error %q should contain %q", err, "does not compile")
	}
	if !strings.Contains(err.Error(), "broken.go") {
		t.Errorf("error %q should include the compiler errors", err)
	}
}

func TestLoadTestFile(t *testing.T) {
	_, err := LoadFile(testdataPath(t, "sample", "file_test.go"))
	if err == nil {
		t.Fatal("expected an error for a test file")
	}
	if !strings.Contains(err.Error(), "test files") {
		t.Errorf("error %q should contain %q", err, "test files")
	}
}

func TestLoadCgo(t *testing.T) {
	_, err := LoadFile(testdataPath(t, "cgo", "cgo.go"))
	if err == nil {
		t.Fatal("expected an error for a cgo file")
	}
	if !strings.Contains(err.Error(), "cgo") {
		t.Errorf("error %q should contain %q", err, "cgo")
	}
}

func TestLoadBuildExcluded(t *testing.T) {
	_, err := LoadFile(testdataPath(t, "excluded", "excluded.go"))
	if err == nil {
		t.Fatal("expected an error for a build-excluded file")
	}
	if !strings.Contains(err.Error(), "build constraints") {
		t.Errorf("error %q should contain %q", err, "build constraints")
	}
}

func TestLoadSymlink(t *testing.T) {
	/*
		The symlink lives outside the package directory: a symlinked .go
		file inside the directory would be compiled as a second file.
	*/
	real := testdataPath(t, "symlink", "real.go")
	link := filepath.Join(t.TempDir(), "link.go")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	pkg, err := LoadFile(link)
	if err != nil {
		t.Fatalf("LoadFile through symlink: %v", err)
	}
	if pkg.Path != real {
		t.Errorf("resolved path = %q, want the real file %q", pkg.Path, real)
	}
	if pkg.Plain.Name != "symlink" {
		t.Errorf("plain package name = %q, want %q", pkg.Plain.Name, "symlink")
	}
}
