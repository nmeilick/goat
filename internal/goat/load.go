/*
Package goat is the engine behind the goat CLI: it loads Go packages and
moves top-level declarations between files of the same package.
*/
package goat

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Package is the loaded view of the package containing the target file.
type Package struct {
	Path     string              // symlink-resolved absolute path of the target file
	Fset     *token.FileSet      // fileset shared by all variants
	Plain    *packages.Package   // the non-test variant containing the file
	Variants []*packages.Package // every loaded variant, test variants included
	File     *ast.File           // the target file's syntax tree, from Plain
}

/*
resolveFile returns the symlink-resolved absolute path of a file that may
not exist yet: the parent directory is resolved and the base name kept.
It is the one path-resolution helper of the package, shared by loading,
backup and (later) the not-yet-existing --to destination.
*/
func resolveFile(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", path, err)
	}
	return filepath.Join(dir, filepath.Base(abs)), nil
}

/*
LoadFile resolves path and loads its containing package with full type
information, test variants included. It refuses test files, cgo files,
packages that do not compile, and files excluded by build constraints.
*/
func LoadFile(path string) (*Package, error) {
	resolved, err := resolveFile(path)
	if err != nil {
		return nil, err
	}

	if strings.HasSuffix(resolved, "_test.go") {
		return nil, fmt.Errorf("%s: test files are not supported", path)
	}

	/*
		Refuse cgo before compiling anything: the import "C" preamble does
		not follow normal rules, and checking from the import list alone
		needs no C toolchain.
	*/
	cgo, err := importsC(resolved)
	if err != nil {
		return nil, err
	}
	if cgo {
		return nil, fmt.Errorf("%s: cgo files (import \"C\") are not supported", path)
	}

	fset := token.NewFileSet()
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo |
			packages.NeedDeps | packages.NeedImports,
		Dir:   filepath.Dir(resolved),
		Fset:  fset,
		Tests: true,
	}
	variants, err := packages.Load(cfg, ".")
	if err != nil {
		return nil, fmt.Errorf("loading package for %s: %w", path, err)
	}

	var msgs []string
	seen := map[string]bool{}
	for _, p := range variants {
		for _, e := range p.Errors {
			if !seen[e.Error()] {
				seen[e.Error()] = true
				msgs = append(msgs, e.Error())
			}
		}
	}
	if len(msgs) > 0 {
		return nil, fmt.Errorf("package does not compile:\n%s", strings.Join(msgs, "\n"))
	}

	/*
		The plain variant is the one with ID == PkgPath whose syntax actually
		contains the target file. A package's test binary also has ID ==
		PkgPath (and Name "main"), but its syntax is the generated testmain
		and never contains the target file; a real main package's name must
		not disqualify it.
	*/
	var plain *packages.Package
	var file *ast.File
	for _, p := range variants {
		if p.ID != p.PkgPath {
			continue
		}
		if f := findFile(fset, p, resolved); f != nil {
			plain, file = p, f
			break
		}
	}
	if plain == nil {
		return nil, fmt.Errorf("%s is excluded from the package by build constraints under the current GOOS/GOARCH/tags (check with 'GOOS=... go build')", path)
	}

	return &Package{Path: resolved, Fset: fset, Plain: plain, Variants: variants, File: file}, nil
}

// importsC reports whether the file at path imports "C".
func importsC(path string) (bool, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return false, fmt.Errorf("parsing %s: %w", path, err)
	}
	for _, imp := range f.Imports {
		if imp.Path.Value == `"C"` {
			return true, nil
		}
	}
	return false, nil
}

/*
findFile returns the syntax tree of path within pkg, or nil if the file is
not part of the package under the current build constraints.
*/
func findFile(fset *token.FileSet, pkg *packages.Package, path string) *ast.File {
	for _, f := range pkg.Syntax {
		if fset.Position(f.Pos()).Filename == path {
			return f
		}
	}
	return nil
}
