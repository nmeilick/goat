package goat

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

/*
DstFile is the parsed view of the move destination: its top-level
declaration names, build tags, and parse status, used by PlanMove's
refusals. Exists is false when --to names a new file; ParseErr is set
when an existing file does not parse.
*/
type DstFile struct {
	Path      string          // symlink-resolved absolute path of --to
	Exists    bool            // false: --to names a new file
	DeclNames map[string]bool // top-level names already declared, methods as "Type.Method"
	BuildTags []string        // //go:build and legacy "// +build" lines
	ParseErr  error           // non-nil when the existing file does not parse
}

/*
Plan is the computed result of a move: the declarations relocating from
the source file to the destination, in source order, plus non-fatal
warnings the caller should surface (e.g. a generated-code source file).
*/
type Plan struct {
	File     *File    // the indexed source file
	Dst      *DstFile // the parsed destination (Exists false: a new file)
	Decls    []*Decl  // the moving set, in source order
	Warnings []string // non-fatal notices for stderr
}

/*
ParseDst builds the DstFile view of the file at path, resolving symlinks.
A missing file yields a DstFile with Exists false (a new destination);
an unparseable one yields ParseErr set. Non-.go destinations, test files
and cgo files are refused outright (test/cgo is the same scope rule
load.go enforces for --from), before anything is written.
*/
func ParseDst(path string) (*DstFile, error) {
	resolved, err := resolveFile(path)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(resolved, ".go") {
		return nil, fmt.Errorf("%s: the destination must be a .go file", path)
	}
	if strings.HasSuffix(resolved, "_test.go") {
		return nil, fmt.Errorf("%s: test files are not supported", path)
	}
	dst := &DstFile{Path: resolved, DeclNames: map[string]bool{}}
	f, err := parser.ParseFile(token.NewFileSet(), resolved, nil, parser.ParseComments)
	if err != nil {
		if os.IsNotExist(err) {
			return dst, nil
		}
		dst.Exists = true
		dst.ParseErr = err
		return dst, nil
	}
	dst.Exists = true
	for _, imp := range f.Imports {
		if imp.Path.Value == `"C"` {
			return nil, fmt.Errorf("%s: cgo files (import \"C\") are not supported", path)
		}
	}
	dst.BuildTags = buildTags(f)
	for _, node := range f.Decls {
		switch n := node.(type) {
		case *ast.FuncDecl:
			if n.Recv == nil {
				dst.DeclNames[n.Name.Name] = true
			} else {
				dst.DeclNames[recvTypeName(n.Recv.List[0].Type)+"."+n.Name.Name] = true
			}
		case *ast.GenDecl:
			if n.Tok == token.IMPORT {
				continue
			}
			for _, spec := range n.Specs {
				switch s := spec.(type) {
				case *ast.ValueSpec:
					for _, id := range s.Names {
						if id.Name != "_" {
							dst.DeclNames[id.Name] = true
						}
					}
				case *ast.TypeSpec:
					dst.DeclNames[s.Name.Name] = true
				}
			}
		}
	}
	return dst, nil
}

/*
PlanMove computes the move of the named symbols from file to dst: it
parses the selection (comma-separated segments, "*" globs against the
file's declaration names, "!" exclusions applied after expansion),
resolves unknown names with close-match suggestions, optionally grows the
selection to its exclusive dependency closure, and enforces the safety
refusals — any failure aborts before anything is written. A nil dst
means the destination view is unknown and its refusals are skipped;
callers resolving --to should always build one with ParseDst.
*/
func PlanMove(file *File, symbols []string, withDeps bool, dst *DstFile) (*Plan, error) {
	includes, excludes := splitSelection(symbols)
	if len(includes) == 0 {
		return nil, fmt.Errorf("no symbols given to move")
	}

	selected := map[*Decl]bool{}
	matched := map[*Decl]map[string]bool{} // member names each decl was selected by
	selectDecl := func(d *Decl, name string) {
		selected[d] = true
		if matched[d] == nil {
			matched[d] = map[string]bool{}
		}
		matched[d][name] = true
	}

	for _, inc := range includes {
		if strings.Contains(inc, "*") {
			if err := expandWildcard(file, inc, selectDecl); err != nil {
				return nil, err
			}
			continue
		}
		d := file.ByName(inc)
		if d == nil {
			return nil, unknownSymbolError(file, inc)
		}
		selectDecl(d, inc)
	}

	for _, exc := range excludes {
		d := file.ByName(exc)
		if d == nil {
			return nil, unknownSymbolError(file, exc)
		}
		if !selected[d] {
			continue
		}
		if isIotaGroup(d) {
			return nil, fmt.Errorf("cannot exclude %s: part of an iota const block (move the whole block or none of it)", exc)
		}
		if vs, ok := d.Spec.(*ast.ValueSpec); ok && len(vs.Names) > 1 {
			return nil, fmt.Errorf("cannot exclude %s: %s %s is a multi-name spec and moves as a unit (exclude neither or select neither)", exc, d.Kind, d.Name)
		}
		delete(selected, d)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("nothing left to move: the exclusions emptied the selection")
	}

	/*
		A partially selected iota group refuses, whether selected literally
		or by a wildcard matching only some members: splitting would change
		the constants' values.
	*/
	for _, d := range file.Decls {
		names, ok := matched[d]
		if !ok || !selected[d] || !isIotaGroup(d) {
			continue
		}
		for _, member := range definedNames(d) {
			if !names[member] {
				return nil, fmt.Errorf("cannot move %s: part of an iota const block (move the whole block or none of it)", firstMatchedName(matched[d]))
			}
		}
	}

	if withDeps {
		addExclusiveClosure(file, selected)
	}

	var moving []*Decl
	for _, d := range file.Decls {
		if selected[d] {
			moving = append(moving, d)
		}
	}

	for _, d := range moving {
		if d.Kind == FuncDecl && d.Name == "init" {
			return nil, fmt.Errorf("cannot move init: init() functions never move")
		}
		/*
			A const group with expression-less specs (const ( a = 1; b ) —
			b repeats a's expression) moves only as a whole block: splitting
			would drop the repeated expression or leave an invalid group.
		*/
		if isRepeatConstMember(d) {
			for _, sib := range file.Decls {
				if sib != d && sib.Node == d.Node && !selected[sib] {
					return nil, fmt.Errorf("cannot move %s: part of a const block with implicit repeated expressions (move the whole block or none of it)", d.Name)
				}
			}
		}
	}

	/*
		A partially selected group whose specs share one line (var ( a = 1;
		b = 2 )) cannot be split textually — the cut ranges would overlap.
		Refuse with the remedy instead of dying inside imports.Process.
	*/
	for _, d := range moving {
		g, ok := d.Node.(*ast.GenDecl)
		if !ok || d.Spec == nil || !g.Lparen.IsValid() {
			continue
		}
		partial := false
		for _, sib := range file.Decls {
			if sib.Node == d.Node && !selected[sib] {
				partial = true
				break
			}
		}
		if !partial {
			continue
		}
		fset := file.Package.Fset
		line := fset.Position(d.Spec.Pos()).Line
		for _, sib := range g.Specs {
			if sib == d.Spec {
				continue
			}
			if fset.Position(sib.Pos()).Line == line || fset.Position(sib.End()).Line == line {
				return nil, fmt.Errorf("cannot move %s: its group's specs share a line; reformat the file first (one spec per line)", d.Name)
			}
		}
	}

	if dst != nil {
		if err := checkDestination(file, dst, moving); err != nil {
			return nil, err
		}
	}

	plan := &Plan{File: file, Dst: dst, Decls: moving}
	if file.Generated {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s is marked as generated code; regeneration will discard edits", filepath.Base(file.Path)))
	}
	return plan, nil
}

/*
checkDestination enforces the refusals that involve the destination
file: same file as the source, outside the source directory, not
parseable, build-tag mismatch, and duplicate declarations.
*/
func checkDestination(file *File, dst *DstFile, moving []*Decl) error {
	if dst.Path == file.Path {
		return fmt.Errorf("--to must differ from --from (both are %s)", file.Path)
	}
	if filepath.Dir(dst.Path) != filepath.Dir(file.Path) {
		return fmt.Errorf("cannot move to %s: the destination must be in the source file's directory %s", dst.Path, filepath.Dir(file.Path))
	}
	if dst.ParseErr != nil {
		return fmt.Errorf("destination %s does not parse: %v", dst.Path, dst.ParseErr)
	}
	if dst.Exists && !sameTags(file.BuildTags, dst.BuildTags) {
		return fmt.Errorf("cannot move to %s: build tags differ from the source file (move to a new file instead)", dst.Path)
	}
	for _, d := range moving {
		for _, name := range definedNames(d) {
			if dst.DeclNames[name] {
				return fmt.Errorf("cannot move %s: %s already declares it", name, filepath.Base(dst.Path))
			}
		}
	}
	return nil
}

/*
splitSelection splits the symbol arguments into includes and excludes:
each argument may be a comma-separated list, and segments starting with
"!" are exclusions. Empty segments and duplicates are dropped.
*/
func splitSelection(symbols []string) (includes, excludes []string) {
	seenInc := map[string]bool{}
	seenExc := map[string]bool{}
	for _, arg := range symbols {
		for _, seg := range strings.Split(arg, ",") {
			seg = strings.TrimSpace(seg)
			if seg == "" || seg == "!" {
				continue
			}
			if strings.HasPrefix(seg, "!") {
				if name := seg[1:]; !seenExc[name] {
					seenExc[name] = true
					excludes = append(excludes, name)
				}
			} else if !seenInc[seg] {
				seenInc[seg] = true
				includes = append(includes, seg)
			}
		}
	}
	return includes, excludes
}

/*
expandWildcard selects every declaration whose indexed name matches the
glob pattern (receiver-qualified method names included), reporting each
matched member name. A pattern matching nothing is an error.
*/
func expandWildcard(file *File, pattern string, selectDecl func(*Decl, string)) error {
	if _, err := path.Match(pattern, ""); err != nil {
		return fmt.Errorf("invalid pattern %q: %v", pattern, err)
	}
	matchedAny := false
	for _, name := range sortedDeclNames(file) {
		if ok, _ := path.Match(pattern, name); ok {
			matchedAny = true
			selectDecl(file.byName[name], name)
		}
	}
	if !matchedAny {
		return fmt.Errorf("pattern %q matches no declaration in %s", pattern, filepath.Base(file.Path))
	}
	return nil
}

/*
unknownSymbolError builds the error for a name the file does not declare:
a bare method name points at the receiver-qualified form, anything else
lists close matches.
*/
func unknownSymbolError(file *File, name string) error {
	if !strings.Contains(name, ".") {
		var qualified []string
		for _, key := range sortedDeclNames(file) {
			if strings.HasSuffix(key, "."+name) {
				qualified = append(qualified, key)
			}
		}
		if len(qualified) > 0 {
			return fmt.Errorf("%s is a method; use the receiver-qualified form: %s", name, strings.Join(qualified, ", "))
		}
	}
	if s := file.Suggest(name); len(s) > 0 {
		return fmt.Errorf("unknown symbol %q in %s (closest matches: %s)", name, filepath.Base(file.Path), strings.Join(s, ", "))
	}
	return fmt.Errorf("unknown symbol %q in %s", name, filepath.Base(file.Path))
}

/*
addExclusiveClosure grows selected to a fixpoint with the exclusive
dependency closure: any declaration referenced only by symbols already in
the set. Exclusivity spans the whole package — a declaration referenced
from another file (UsedElsewhere, test files included) stays put — and a
declaration with no referencers at all is never pulled: unreferenced dead
code must not travel with a move.
*/
func addExclusiveClosure(file *File, selected map[*Decl]bool) {
	referencers := map[*Decl][]*Decl{}
	for _, d := range file.Decls {
		for _, u := range d.Uses {
			referencers[u] = append(referencers[u], d)
		}
	}
	for {
		grown := false
		for _, cand := range file.Decls {
			if selected[cand] || cand.UsedElsewhere {
				continue
			}
			refs := referencers[cand]
			if len(refs) == 0 {
				continue
			}
			exclusive := true
			for _, r := range refs {
				if !selected[r] {
					exclusive = false
					break
				}
			}
			if exclusive {
				selected[cand] = true
				grown = true
			}
		}
		if !grown {
			return
		}
	}
}

/*
definedNames returns the top-level names a declaration defines, in the
form ByName and DstFile.DeclNames use: receiver-qualified for methods,
every member for multi-name specs and indivisible groups.
*/
func definedNames(d *Decl) []string {
	if d.Kind == FuncDecl || d.Kind == MethodDecl {
		return []string{d.Name}
	}
	var names []string
	addSpec := func(spec ast.Spec) {
		switch s := spec.(type) {
		case *ast.ValueSpec:
			for _, id := range s.Names {
				names = append(names, id.Name)
			}
		case *ast.TypeSpec:
			names = append(names, s.Name.Name)
		}
	}
	if d.Spec != nil {
		addSpec(d.Spec)
		return names
	}
	if g, ok := d.Node.(*ast.GenDecl); ok {
		for _, spec := range g.Specs {
			addSpec(spec)
		}
	}
	return names
}

/*
isIotaGroup reports whether d is an indivisible iota const group, which
analyze indexes as a single Decl with Spec nil.
*/
func isIotaGroup(d *Decl) bool {
	if d.Spec != nil || d.Kind != ConstDecl {
		return false
	}
	g, ok := d.Node.(*ast.GenDecl)
	return ok && g.Lparen.IsValid() && len(g.Specs) > 1
}

/*
isRepeatConstMember reports whether d is one spec of a parenthesized
const group containing expression-less specs, whose members implicitly
repeat the previous expression and therefore move only as a whole block.
*/
func isRepeatConstMember(d *Decl) bool {
	if d.Spec == nil || d.Kind != ConstDecl {
		return false
	}
	g, ok := d.Node.(*ast.GenDecl)
	if !ok || !g.Lparen.IsValid() || len(g.Specs) < 2 {
		return false
	}
	for _, spec := range g.Specs {
		if vs, ok := spec.(*ast.ValueSpec); ok && len(vs.Values) == 0 {
			return true
		}
	}
	return false
}

/*
sortedDeclNames returns the file's index keys in sorted order, for
deterministic wildcard expansion and error messages.
*/
func sortedDeclNames(file *File) []string {
	names := make([]string, 0, len(file.byName))
	for name := range file.byName {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

/*
firstMatchedName returns the smallest name a decl was selected by, for
deterministic refusal messages.
*/
func firstMatchedName(names map[string]bool) string {
	list := make([]string, 0, len(names))
	for name := range names {
		list = append(list, name)
	}
	sort.Strings(list)
	return list[0]
}

/*
sameTags reports whether two build-tag line lists hold the same lines,
ignoring order.
*/
func sameTags(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]string(nil), a...)
	bs := append([]string(nil), b...)
	sort.Strings(as)
	sort.Strings(bs)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}
