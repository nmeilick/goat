//go:build go1.18

package sample

import (
	"fmt"
	"strings"
)

const (
	ModeFast Mode = iota
	ModeSlow
)

type Mode int

const defaultMode = ModeFast

type File struct {
	Name string
}

// Stat describes the file.
func (f File) Stat() string {
	return helper(f.Name)
}

func helper(s string) string {
	return fmt.Sprintf("%s (mode %d)", strings.TrimSpace(s), defaultMode)
}

type List[T any] struct {
	items []T
}

func (l List[T]) Get(i int) T {
	return l.items[i]
}

var DefaultName = "sample"
