package goat

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/imports"
)

/*
Rewrite is the computed content of both files of a move, produced purely
in memory: nothing is written to disk. Src is nil when SrcRemoved is set.
*/
type Rewrite struct {
	Src        []byte // new content of the source file
	Dst        []byte // new content of the destination file
	SrcRemoved bool   // no declarations remain: the source file is to be deleted
	SrcKept    bool   // emptied source kept for its non-declaration comments or blank imports
	/*
		SrcKeptBlankImports distinguishes the two SrcKept reasons: true when
		surviving blank imports (not comments) keep the emptied source.
	*/
	SrcKeptBlankImports bool
}

/*
RewritePlan computes the new contents of the source and destination files
for a Plan. Moving declarations are cut as exact byte ranges (doc comment
included, a trailing same-line comment riding along) so untouched code
keeps its formatting; a new destination is seeded from the source's build
tags, package clause and import block. Both sides then pass through
imports.Process, which prunes and re-formats them.
*/
func RewritePlan(p *Plan) (*Rewrite, error) {
	if p.Dst == nil {
		return nil, fmt.Errorf("the plan has no destination")
	}
	src, err := os.ReadFile(p.File.Path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", p.File.Path, err)
	}

	s := &surgery{src: src, fset: p.File.Package.Fset}
	chunks := s.cutMoving(p)

	/*
		A moved //go:embed declaration takes the source's blank embed import
		along; imports.Process never removes blank imports itself.
	*/
	remaining, err := applyEdits(src, s.edits)
	if err != nil {
		return nil, err
	}
	if hasEmbedDirective(chunks) && !bytes.Contains(remaining, []byte("//go:embed")) {
		s.dropBlankEmbed(p.File)
		if remaining, err = applyEdits(src, s.edits); err != nil {
			return nil, err
		}
	}

	moving := map[*Decl]bool{}
	for _, d := range p.Decls {
		moving[d] = true
	}
	emptied := true
	for _, d := range p.File.Decls {
		if !moving[d] {
			emptied = false
			break
		}
	}

	rw := &Rewrite{}
	keptComments := keepsComments(p.File, s.fset, s.edits)
	keptBlankImports := keepsBlankImports(p.File, s.fset, s.edits)
	if emptied && !keptComments && !keptBlankImports {
		rw.SrcRemoved = true
	} else {
		out, err := processImports(p.File.Path, remaining)
		if err != nil {
			return nil, err
		}
		rw.Src = out
		rw.SrcKept = emptied
		rw.SrcKeptBlankImports = emptied && keptBlankImports && !keptComments
	}

	dst, err := s.buildDst(p, chunks)
	if err != nil {
		return nil, err
	}
	rw.Dst, err = processImports(p.Dst.Path, dst)
	if err != nil {
		return nil, err
	}
	return rw, nil
}

// edit is a byte range removed from the source.
type edit struct{ start, end int }

/*
surgery holds the source bytes and the fileset mapping token positions to
offsets in them, and accumulates the cut ranges while chunks are built.
*/
type surgery struct {
	src   []byte
	fset  *token.FileSet
	edits []edit
}

func (s *surgery) off(pos token.Pos) int {
	return s.fset.File(pos).Offset(pos)
}

/*
cutMoving removes every moving declaration from the source and returns
the destination chunks in source order. A parenthesized group whose specs
all move (or an indivisible iota group) moves verbatim; a partially
selected group is split, each moving spec becoming a single-spec
declaration.
*/
func (s *surgery) cutMoving(p *Plan) [][]byte {
	movingSpecs := map[*ast.GenDecl]int{}
	for _, d := range p.Decls {
		if g, ok := d.Node.(*ast.GenDecl); ok && d.Spec != nil {
			movingSpecs[g]++
		}
	}

	var chunks [][]byte
	done := map[*ast.GenDecl]bool{}
	for _, d := range p.Decls {
		switch n := d.Node.(type) {
		case *ast.FuncDecl:
			chunks = append(chunks, s.cutNode(docPos(n.Doc, n.Pos()), n.End()))
		case *ast.GenDecl:
			if done[n] {
				continue
			}
			done[n] = true
			if d.Spec == nil || !n.Lparen.IsValid() || movingSpecs[n] == len(n.Specs) {
				chunks = append(chunks, s.cutNode(docPos(n.Doc, n.Pos()), n.End()))
				continue
			}
			for _, d2 := range p.Decls {
				if d2.Node == n && d2.Spec != nil {
					chunks = append(chunks, s.cutSpec(n, d2.Spec))
				}
			}
		}
	}
	return chunks
}

// docPos returns the doc comment's start when present, else pos.
func docPos(doc *ast.CommentGroup, pos token.Pos) token.Pos {
	if doc != nil {
		return doc.Pos()
	}
	return pos
}

// specDoc returns the doc comment of a const/var/type spec.
func specDoc(spec ast.Spec) *ast.CommentGroup {
	switch s := spec.(type) {
	case *ast.ValueSpec:
		return s.Doc
	case *ast.TypeSpec:
		return s.Doc
	}
	return nil
}

/*
cutNode removes a whole declaration (doc comment through its end) and
returns its verbatim chunk for the destination.
*/
func (s *surgery) cutNode(pos, end token.Pos) []byte {
	start, stop, chunk := s.rangeFor(s.off(pos), s.off(end))
	s.edits = append(s.edits, edit{start, stop})
	return chunk
}

/*
cutSpec removes one spec from inside a parenthesized group and builds its
destination chunk as a stand-alone single-spec declaration. The cut range
follows the same trailing-comment rule as whole declarations, so a
same-line comment travels with the spec.
*/
func (s *surgery) cutSpec(g *ast.GenDecl, spec ast.Spec) []byte {
	start, stop, chunk := s.rangeFor(s.off(docPos(specDoc(spec), spec.Pos())), s.off(spec.End()))
	s.edits = append(s.edits, edit{start, stop})

	var b bytes.Buffer
	if doc := bytes.TrimRight(chunk[:s.off(spec.Pos())-start], " \t\r\n"); len(doc) > 0 {
		b.Write(doc)
		b.WriteByte('\n')
	}
	b.WriteString(g.Tok.String())
	b.WriteByte(' ')
	b.Write(bytes.TrimSpace(chunk[s.off(spec.Pos())-start:]))
	return b.Bytes()
}

/*
rangeFor computes the cut range for a node spanning [start, end): start
backs up over line-leading whitespace, end extends through the end of its
line — but only when the rest of the line is whitespace plus at most a
comment, so a declaration sharing the closing line is never swallowed —
and one trailing blank line is consumed. The returned chunk is the cut
text without its trailing blank space.
*/
func (s *surgery) rangeFor(start, end int) (cutStart, cutEnd int, chunk []byte) {
	ls := start
	for ls > 0 && s.src[ls-1] != '\n' {
		ls--
	}
	if len(bytes.Trim(s.src[ls:start], " \t")) == 0 {
		start = ls
	}

	ext := end
	if s.lineRestIsComment(end) {
		i := end
		for i < len(s.src) && s.src[i] != '\n' {
			i++
		}
		ext = i
		if ext < len(s.src) {
			ext++
		}
	}

	chunk = bytes.TrimRight(s.src[start:ext], " \t\r\n")
	cutStart, cutEnd = start, ext
	if ext > end {
		/*
			The declaration ended on its own line: consume one blank line
			after it so the source keeps no stray gap.
		*/
		j := ext
		for j < len(s.src) && (s.src[j] == ' ' || s.src[j] == '\t') {
			j++
		}
		if j < len(s.src) && s.src[j] == '\n' {
			cutEnd = j + 1
		}
	}
	return cutStart, cutEnd, chunk
}

/*
lineRestIsComment reports whether the bytes from `from` to the end of the
line are whitespace plus at most one comment.
*/
func (s *surgery) lineRestIsComment(from int) bool {
	i := from
	for i < len(s.src) && s.src[i] != '\n' {
		i++
	}
	rest := strings.Trim(string(s.src[from:i]), " \t\r")
	switch {
	case rest == "":
		return true
	case strings.HasPrefix(rest, "//"):
		return true
	case strings.HasPrefix(rest, "/*") && strings.HasSuffix(rest, "*/") &&
		!strings.Contains(rest[2:len(rest)-2], "*/"):
		return true
	}
	return false
}

/*
applyEdits returns src without the edited ranges. Overlapping or
out-of-bounds ranges are a programming error and reported rather than
panicking or corrupting the output.
*/
func applyEdits(src []byte, edits []edit) ([]byte, error) {
	sort.Slice(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	var b bytes.Buffer
	last := 0
	for _, e := range edits {
		if e.start < last || e.end < e.start || e.end > len(src) {
			return nil, fmt.Errorf("invalid edit range [%d, %d) of %d bytes", e.start, e.end, len(src))
		}
		b.Write(src[last:e.start])
		last = e.end
	}
	b.Write(src[last:])
	return b.Bytes(), nil
}

// hasEmbedDirective reports whether any chunk carries a //go:embed directive.
func hasEmbedDirective(chunks [][]byte) bool {
	for _, c := range chunks {
		if bytes.Contains(c, []byte("//go:embed")) {
			return true
		}
	}
	return false
}

/*
dropBlankEmbed removes the source's blank embed import: the declaration
it served moved away, and imports.Process never removes blank imports on
its own. Callers have already confirmed no //go:embed directive remains.
*/
func (s *surgery) dropBlankEmbed(f *File) {
	for _, node := range f.Package.File.Decls {
		g, ok := node.(*ast.GenDecl)
		if !ok || g.Tok != token.IMPORT {
			continue
		}
		for _, spec := range g.Specs {
			imp := spec.(*ast.ImportSpec)
			if imp.Name == nil || imp.Name.Name != "_" || imp.Path.Value != `"embed"` {
				continue
			}
			if len(g.Specs) == 1 {
				s.cutNode(docPos(g.Doc, g.Pos()), g.End())
			} else {
				s.cutNode(docPos(imp.Doc, imp.Pos()), imp.End())
			}
			return
		}
	}
}

/*
keepsComments reports whether the emptied source still contains
non-declaration comments outside the cut ranges (license header, package
doc, free-standing comments) and must therefore be kept. Build-tag lines
alone do not keep a file.
*/
func keepsComments(f *File, fset *token.FileSet, edits []edit) bool {
	for _, cg := range f.Package.File.Comments {
		start := fset.File(cg.Pos()).Offset(cg.Pos())
		end := fset.File(cg.End()).Offset(cg.End())
		cut := false
		for _, e := range edits {
			if start >= e.start && end <= e.end {
				cut = true
				break
			}
		}
		if cut {
			continue
		}
		for _, c := range cg.List {
			if !strings.HasPrefix(c.Text, "//go:build") && !strings.HasPrefix(c.Text, "// +build") {
				return true
			}
		}
	}
	return false
}

/*
keepsBlankImports reports whether the emptied source still contains blank
imports outside the cut ranges (a moved //go:embed declaration takes the
blank embed import along). Blank imports stay in the source, so such a
file is kept: package clause and blank imports, everything else pruned.
*/
func keepsBlankImports(f *File, fset *token.FileSet, edits []edit) bool {
	for _, imp := range f.Imports {
		if imp.Name == nil || imp.Name.Name != "_" {
			continue
		}
		start := fset.File(imp.Pos()).Offset(imp.Pos())
		end := fset.File(imp.End()).Offset(imp.End())
		cut := false
		for _, e := range edits {
			if start >= e.start && end <= e.end {
				cut = true
				break
			}
		}
		if !cut {
			return true
		}
	}
	return false
}

/*
buildDst assembles the destination content: a new file is seeded from the
source's build tags, package clause and import block; an existing file
gets the chunks appended at its end.
*/
func (s *surgery) buildDst(p *Plan, chunks [][]byte) ([]byte, error) {
	if p.Dst.Exists {
		return s.mergeDst(p, chunks)
	}

	var b bytes.Buffer
	for _, tag := range p.File.BuildTags {
		b.WriteString(tag)
		b.WriteByte('\n')
	}
	if len(p.File.BuildTags) > 0 {
		b.WriteByte('\n')
	}
	b.WriteString("package ")
	b.WriteString(p.File.PackageName)
	b.WriteString("\n")

	seed, err := s.seedImports(p.File, chunks)
	if err != nil {
		return nil, err
	}
	if len(seed) > 0 {
		b.WriteString("\nimport (\n")
		for _, spec := range seed {
			b.WriteByte('\t')
			b.WriteString(spec.text)
			b.WriteByte('\n')
		}
		b.WriteString(")\n")
	}
	for _, c := range chunks {
		b.WriteByte('\n')
		b.Write(c)
		b.WriteByte('\n')
	}
	return b.Bytes(), nil
}

/*
importSeed is one import spec copied from the source's import block: its
verbatim source text (a trailing same-line comment included) and its
quoted path.
*/
type importSeed struct {
	text string
	path string
}

/*
seedImports returns the import specs for seeding a destination, copied
from the source's import block — never inferred from scratch, so the
moved code resolves the same import paths the source used (an aliased
crypto/rand must not come back as math/rand). Blank imports stay in the
source, except that a moved //go:embed declaration brings the source's
embed import along.
*/
func (s *surgery) seedImports(f *File, chunks [][]byte) ([]importSeed, error) {
	var seed []importSeed
	var blankEmbed *importSeed
	hasEmbed := false
	for _, imp := range f.Imports {
		blank := imp.Name != nil && imp.Name.Name == "_"
		if imp.Path.Value == `"embed"` {
			if blank {
				spec := s.importSpecSeed(imp)
				blankEmbed = &spec
			} else {
				hasEmbed = true
			}
		}
		if blank {
			continue
		}
		seed = append(seed, s.importSpecSeed(imp))
	}
	if hasEmbedDirective(chunks) && !hasEmbed {
		if blankEmbed == nil {
			return nil, fmt.Errorf("a moved declaration uses //go:embed but %s does not import embed", filepath.Base(f.Path))
		}
		seed = append(seed, *blankEmbed)
	}
	return seed, nil
}

/*
importSpecSeed copies one import spec's source text, extending the range
through the end of the line when the remainder is whitespace plus a
comment — the same rule declaration cuts follow — so a trailing comment
travels with the spec.
*/
func (s *surgery) importSpecSeed(imp *ast.ImportSpec) importSeed {
	end := s.off(imp.End())
	if s.lineRestIsComment(end) {
		for end < len(s.src) && s.src[end] != '\n' {
			end++
		}
	}
	return importSeed{
		text: string(bytes.TrimSpace(s.src[s.off(imp.Pos()):end])),
		path: imp.Path.Value,
	}
}

/*
mergeDst appends the chunks at the end of an existing destination file,
first seeding the destination's import block with the source's import
specs the destination lacks — the same seeding a new file gets, so an
aliased import keeps its original path instead of being inferred.
*/
func (s *surgery) mergeDst(p *Plan, chunks [][]byte) ([]byte, error) {
	existing, err := os.ReadFile(p.Dst.Path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", p.Dst.Path, err)
	}
	seed, err := s.seedImports(p.File, chunks)
	if err != nil {
		return nil, err
	}
	content, err := addImportSpecs(existing, seed)
	if err != nil {
		return nil, fmt.Errorf("seeding imports in %s: %w", p.Dst.Path, err)
	}
	var b bytes.Buffer
	b.Write(bytes.TrimRight(content, " \t\r\n"))
	b.WriteByte('\n')
	for _, c := range chunks {
		b.WriteByte('\n')
		b.Write(c)
		b.WriteByte('\n')
	}
	return b.Bytes(), nil
}

/*
addImportSpecs inserts the import specs the file lacks (matched by path)
into its import section: into the parenthesized block when present, by
widening a single-line import into a block, or as a new block after the
package clause. imports.Process prunes the seeds the moved code does not
use and formats the result.
*/
func addImportSpecs(content []byte, seeds []importSeed) ([]byte, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "destination.go", content, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	var group, single *ast.GenDecl
	for _, decl := range f.Decls {
		g, ok := decl.(*ast.GenDecl)
		if !ok || g.Tok != token.IMPORT {
			continue
		}
		for _, spec := range g.Specs {
			have[spec.(*ast.ImportSpec).Path.Value] = true
		}
		if g.Lparen.IsValid() {
			if group == nil {
				group = g
			}
		} else if single == nil {
			single = g
		}
	}
	var missing []string
	for _, seed := range seeds {
		if !have[seed.path] {
			missing = append(missing, seed.text)
		}
	}
	if len(missing) == 0 {
		return content, nil
	}

	specLines := func(b *bytes.Buffer, specs []string) {
		for _, text := range specs {
			b.WriteByte('\t')
			b.WriteString(text)
			b.WriteByte('\n')
		}
	}
	var b bytes.Buffer
	switch {
	case group != nil:
		rparen := fset.File(group.Rparen).Offset(group.Rparen)
		b.Write(content[:rparen])
		specLines(&b, missing)
		b.Write(content[rparen:])
	case single != nil:
		off := func(pos token.Pos) int { return fset.File(pos).Offset(pos) }
		b.Write(content[:off(single.Pos())])
		b.WriteString("import (\n")
		for _, spec := range single.Specs {
			specLines(&b, []string{string(bytes.TrimSpace(content[off(spec.Pos()):off(spec.End())]))})
		}
		specLines(&b, missing)
		b.WriteByte(')')
		b.Write(content[off(single.End()):])
	default:
		end := fset.File(f.Name.End()).Offset(f.Name.End())
		for end < len(content) && content[end] != '\n' {
			end++
		}
		b.Write(content[:end])
		b.WriteString("\n\nimport (\n")
		specLines(&b, missing)
		b.WriteByte(')')
		b.Write(content[end:])
	}
	return b.Bytes(), nil
}

// processImports prunes and repairs the file's imports and gofmts it.
func processImports(path string, content []byte) ([]byte, error) {
	out, err := imports.Process(path, content, &imports.Options{Comments: true, TabIndent: true, TabWidth: 8})
	if err != nil {
		return nil, fmt.Errorf("fixing imports in %s: %w", filepath.Base(path), err)
	}
	return out, nil
}
