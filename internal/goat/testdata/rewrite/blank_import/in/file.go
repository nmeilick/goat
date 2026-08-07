package blanki

import (
	"fmt"
	_ "net/http/pprof"
)

// Move leaves.
func Move() string {
	return fmt.Sprintf("move")
}

// Stay remains.
func Stay() string {
	return fmt.Sprintf("stay")
}
