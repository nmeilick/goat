package cmd

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/nmeilick/goat/internal/goat"
)

func newSymbolsCmd(opts *Options) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:     "symbols <file.go>",
		Aliases: []string{"ls"},
		Short:   "List a Go file's top-level declarations and who uses them",
		Long: `symbols lists the top-level declarations of one Go file — functions,
methods, types, constants and variables — with kind, line count and usage
hints: which other files of the package reference a declaration, and which
declarations inside the same file do. Run it before a move to see what a
file contains and what is safe to relocate. Test files are refused.

With --json the listing is machine-readable:

  {"file": "file.go", "symbols": [
    {"name": "FileStat", "kind": "func", "lines": 42,
     "usedBy": ["ChangeFileOwner"], "usedElsewhere": false}
  ]}

Exit codes: 0 success, 1 failure (no files touched), 2 usage error.`,
		Example: `  goat symbols file.go
  goat ls --json file.go`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return &usageError{"missing required <file.go> argument"}
			}
			if len(args) > 1 {
				return &usageError{fmt.Sprintf("accepts exactly one <file.go> argument, got %d", len(args))}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			pkg, err := goat.LoadFile(args[0])
			if err != nil {
				return err
			}
			file, err := goat.Index(pkg)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeSymbolsJSON(cmd.OutOrStdout(), file)
			}
			return writeSymbolsTable(cmd.OutOrStdout(), file)
		},
	}
	cmd.Flags().BoolVarP(&jsonOut, "json", "j", false, "print the listing as indented JSON instead of a table")
	return cmd
}

/*
sameFileUsers maps each declaration to the sorted names of the same-file
declarations referencing it.
*/
func sameFileUsers(f *goat.File) map[*goat.Decl][]string {
	users := map[*goat.Decl][]string{}
	for _, d := range f.Decls {
		for _, u := range d.Uses {
			users[u] = append(users[u], d.Name)
		}
	}
	for _, names := range users {
		sort.Strings(names)
	}
	return users
}

/*
usageHint renders the trailing "used by" column: the referencing files
when usage crosses file boundaries, otherwise the exclusive same-file
users in parentheses.
*/
func usageHint(d *goat.Decl, users []string) string {
	if d.UsedElsewhere {
		files := make([]string, 0, len(d.ExternalFiles))
		for name := range d.ExternalFiles {
			files = append(files, name)
		}
		sort.Strings(files)
		return "used by: " + strings.Join(files, " + ")
	}
	if len(users) == 0 {
		return ""
	}
	return fmt.Sprintf("used by: (only %s)", strings.Join(users, ", "))
}

/*
declLines returns the number of source lines a declaration spans, doc
comment included.
*/
func declLines(fset *token.FileSet, d *goat.Decl) int {
	start, end := d.Node.Pos(), d.Node.End()
	if fn, ok := d.Node.(*ast.FuncDecl); ok && fn.Doc != nil {
		start = fn.Doc.Pos()
	}
	if g, ok := d.Node.(*ast.GenDecl); ok && d.Spec == nil && g.Doc != nil {
		start = g.Doc.Pos()
	}
	if d.Spec != nil {
		start, end = d.Spec.Pos(), d.Spec.End()
		switch s := d.Spec.(type) {
		case *ast.ValueSpec:
			if s.Doc != nil {
				start = s.Doc.Pos()
			}
		case *ast.TypeSpec:
			if s.Doc != nil {
				start = s.Doc.Pos()
			}
		}
	}
	return fset.Position(end).Line - fset.Position(start).Line + 1
}

func writeSymbolsTable(w io.Writer, f *goat.File) error {
	users := sameFileUsers(f)
	tw := tabwriter.NewWriter(w, 0, 4, 3, ' ', 0)
	for _, d := range f.Decls {
		line := fmt.Sprintf("%s\t%s\t%d lines", d.Kind, d.Name, declLines(f.Package.Fset, d))
		if hint := usageHint(d, users[d]); hint != "" {
			line += "\t" + hint
		}
		if _, err := fmt.Fprintln(tw, line); err != nil {
			return err
		}
	}
	return tw.Flush()
}

type symbolJSON struct {
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	Lines         int      `json:"lines"`
	UsedBy        []string `json:"usedBy"`
	UsedElsewhere bool     `json:"usedElsewhere"`
}

func writeSymbolsJSON(w io.Writer, f *goat.File) error {
	users := sameFileUsers(f)
	doc := struct {
		File    string       `json:"file"`
		Symbols []symbolJSON `json:"symbols"`
	}{File: filepath.Base(f.Path), Symbols: []symbolJSON{}}
	for _, d := range f.Decls {
		usedBy := users[d]
		if usedBy == nil {
			usedBy = []string{}
		}
		doc.Symbols = append(doc.Symbols, symbolJSON{
			Name:          d.Name,
			Kind:          string(d.Kind),
			Lines:         declLines(f.Package.Fset, d),
			UsedBy:        usedBy,
			UsedElsewhere: d.UsedElsewhere,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}
