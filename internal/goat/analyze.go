package goat

import (
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DeclKind classifies a top-level declaration.
type DeclKind string

const (
	FuncDecl   DeclKind = "func"
	MethodDecl DeclKind = "method"
	TypeDecl   DeclKind = "type"
	ConstDecl  DeclKind = "const"
	VarDecl    DeclKind = "var"
)

/*
Decl is one movable top-level declaration of the indexed file. A
parenthesized iota const group is a single indivisible Decl whose Name
joins all member names; other GenDecl groups index one Decl per spec.
*/
type Decl struct {
	Name          string // display name; methods are "Type.Method"
	Kind          DeclKind
	Node          ast.Decl        // *ast.FuncDecl or the enclosing *ast.GenDecl
	Spec          ast.Spec        // the spec within a GenDecl; nil for whole groups and funcs
	Uses          []*Decl         // same-file declarations this declaration references
	UsedElsewhere bool            // referenced from another file of the package
	ExternalFiles map[string]bool // names of the other package files referencing it
}

/*
File is the indexed view of one Go source file: its declarations, the
reference graph between them, and file-level metadata.
*/
type File struct {
	Package     *Package
	Path        string
	PackageName string
	Imports     []*ast.ImportSpec
	BuildTags   []string // //go:build and legacy "// +build" lines found in the file
	Generated   bool     // carries a "// Code generated ... DO NOT EDIT." marker
	Decls       []*Decl
	byName      map[string]*Decl
}

/*
ByName returns the declaration defining name, or nil. Methods live under
their receiver-qualified "Type.Method" name; blank "_" names are skipped.
*/
func (f *File) ByName(name string) *Decl {
	return f.byName[name]
}

/*
Suggest returns declaration names that contain name (or that name
contains), case-insensitively — close-match hints for error messages.
*/
func (f *File) Suggest(name string) []string {
	needle := strings.ToLower(name)
	var out []string
	for n := range f.byName {
		cand := strings.ToLower(n)
		if strings.Contains(cand, needle) || strings.Contains(needle, cand) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

/*
Index builds the declaration index and reference graph for the target
file of a Package returned by LoadFile. External usage is computed across
every package variant, so references from test files count.
*/
func Index(pkg *Package) (*File, error) {
	info := pkg.Plain.TypesInfo
	file := &File{
		Package:     pkg,
		Path:        pkg.Path,
		PackageName: pkg.Plain.Name,
		Imports:     pkg.File.Imports,
		BuildTags:   buildTags(pkg.File),
		Generated:   isGenerated(pkg.File),
		byName:      map[string]*Decl{},
	}
	defPos := map[token.Pos]*Decl{} // definition position -> owning decl

	for _, node := range pkg.File.Decls {
		switch n := node.(type) {
		case *ast.FuncDecl:
			d := &Decl{Kind: FuncDecl, Node: n, ExternalFiles: map[string]bool{}}
			if n.Recv == nil {
				d.Name = n.Name.Name
			} else {
				d.Kind = MethodDecl
				d.Name = recvTypeName(n.Recv.List[0].Type) + "." + n.Name.Name
			}
			file.addDecl(defPos, info, d, []*ast.Ident{n.Name})
		case *ast.GenDecl:
			if n.Tok == token.IMPORT {
				continue // imports are metadata, not movable declarations
			}
			indexGenDecl(file, defPos, info, n)
		}
	}

	buildUsesEdges(file, defPos, info)
	buildExternalRefs(pkg, file, defPos)
	return file, nil
}

/*
addDecl registers d, mapping each defining identifier's name (and, for
methods, only the receiver-qualified name) to it.
*/
func (f *File) addDecl(defPos map[token.Pos]*Decl, info *types.Info, d *Decl, idents []*ast.Ident) {
	f.Decls = append(f.Decls, d)
	for _, id := range idents {
		if obj := info.Defs[id]; obj != nil {
			defPos[obj.Pos()] = d
		}
		if id.Name == "_" {
			continue
		}
		key := id.Name
		if d.Kind == MethodDecl {
			key = d.Name
		}
		f.byName[key] = d
	}
}

/*
indexGenDecl indexes a const/var/type declaration: as one indivisible
Decl when it is a parenthesized group containing iota, otherwise one Decl
per spec (a multi-name spec such as "var a, b" is a single Decl).
*/
func indexGenDecl(f *File, defPos map[token.Pos]*Decl, info *types.Info, n *ast.GenDecl) {
	kind := map[token.Token]DeclKind{
		token.CONST: ConstDecl,
		token.VAR:   VarDecl,
		token.TYPE:  TypeDecl,
	}[n.Tok]

	if n.Tok == token.CONST && n.Lparen.IsValid() && len(n.Specs) > 1 && groupUsesIota(info, n) {
		d := &Decl{Kind: kind, Node: n, ExternalFiles: map[string]bool{}}
		var idents []*ast.Ident
		for _, spec := range n.Specs {
			vs := spec.(*ast.ValueSpec)
			idents = append(idents, vs.Names...)
		}
		names := make([]string, 0, len(idents))
		for _, id := range idents {
			names = append(names, id.Name)
		}
		d.Name = strings.Join(names, ", ")
		f.addDecl(defPos, info, d, idents)
		return
	}

	for _, spec := range n.Specs {
		d := &Decl{Kind: kind, Node: n, Spec: spec, ExternalFiles: map[string]bool{}}
		var idents []*ast.Ident
		switch s := spec.(type) {
		case *ast.ValueSpec:
			idents = s.Names
		case *ast.TypeSpec:
			idents = []*ast.Ident{s.Name}
		}
		names := make([]string, 0, len(idents))
		for _, id := range idents {
			names = append(names, id.Name)
		}
		d.Name = strings.Join(names, ", ")
		f.addDecl(defPos, info, d, idents)
	}
}

/*
groupUsesIota reports whether any spec of the group references the
predeclared iota.
*/
func groupUsesIota(info *types.Info, n *ast.GenDecl) bool {
	iota := types.Universe.Lookup("iota")
	found := false
	for _, spec := range n.Specs {
		ast.Inspect(spec, func(node ast.Node) bool {
			if id, ok := node.(*ast.Ident); ok && info.Uses[id] == iota {
				found = true
				return false
			}
			return !found
		})
	}
	return found
}

/*
buildUsesEdges fills each declaration's Uses list with the same-file
declarations it references, per types.Info. A method is inspected whole,
receiver included: the receiver type counts as a use of the type, so the
edge is method→type — the self-edge guard keeps it from ever being
method→method. This is what lets --with-deps pull a type that only a
moving method uses.
*/
func buildUsesEdges(f *File, defPos map[token.Pos]*Decl, info *types.Info) {
	for _, d := range f.Decls {
		var roots []ast.Node
		switch {
		case d.Spec != nil:
			roots = []ast.Node{d.Spec}
		default:
			roots = []ast.Node{d.Node}
		}
		seen := map[*Decl]bool{}
		for _, root := range roots {
			ast.Inspect(root, func(node ast.Node) bool {
				id, ok := node.(*ast.Ident)
				if !ok {
					return true
				}
				if obj := info.Uses[id]; obj != nil {
					if target := defPos[obj.Pos()]; target != nil && target != d && !seen[target] {
						seen[target] = true
						d.Uses = append(d.Uses, target)
					}
				}
				return true
			})
		}
	}
}

/*
buildExternalRefs records, per declaration, the names of other package
files referencing it — scanning every variant so test files count — and
derives UsedElsewhere. Objects are matched across variants by definition
position in the shared fileset.
*/
func buildExternalRefs(pkg *Package, f *File, defPos map[token.Pos]*Decl) {
	for _, v := range pkg.Variants {
		if v.TypesInfo == nil {
			continue
		}
		for id, obj := range v.TypesInfo.Uses {
			d := defPos[obj.Pos()]
			if d == nil {
				continue
			}
			useFile := pkg.Fset.Position(id.Pos()).Filename
			if useFile == "" || useFile == pkg.Path {
				continue
			}
			d.ExternalFiles[filepath.Base(useFile)] = true
		}
	}
	for _, d := range f.Decls {
		d.UsedElsewhere = len(d.ExternalFiles) > 0
	}
}

/*
recvTypeName returns the base type name of a method receiver expression,
stripping pointer and generic instantiation syntax: List[T].Get indexes
as List.Get.
*/
func recvTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.StarExpr:
		return recvTypeName(e.X)
	case *ast.IndexExpr: // List[T]
		return recvTypeName(e.X)
	case *ast.IndexListExpr: // Pair[K, V]
		return recvTypeName(e.X)
	case *ast.Ident:
		return e.Name
	}
	return ""
}

/*
buildTags returns the build-constraint lines found in the file's
comments: //go:build lines and legacy "// +build" lines (both are copied
to a new destination file by a move). Only comment groups before the
package clause are considered — constraints cannot appear later, so a
doc comment mentioning //go:build in prose is never mistaken for one.
*/
func buildTags(f *ast.File) []string {
	var tags []string
	for _, cg := range f.Comments {
		if cg.Pos() > f.Package {
			break
		}
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, "//go:build") || strings.HasPrefix(c.Text, "// +build") {
				tags = append(tags, c.Text)
			}
		}
	}
	return tags
}

var generatedMarker = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

/*
isGenerated reports whether the file carries the standard generated-code
marker (https://go.dev/s/generatedcode).
*/
func isGenerated(f *ast.File) bool {
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if generatedMarker.MatchString(c.Text) {
				return true
			}
		}
	}
	return false
}
