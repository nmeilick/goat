/*
goat — the Go AST Transformer: moves top-level declarations between files
of a Go package with type-checked precision.
*/
package main

import (
	"os"

	"github.com/nmeilick/goat/cmd"
)

func main() {
	os.Exit(cmd.Execute(os.Args[1:], os.Stdout, os.Stderr))
}
