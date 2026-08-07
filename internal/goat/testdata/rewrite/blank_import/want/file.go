package blanki

import (
	"fmt"
	_ "net/http/pprof"
)

// Stay remains.
func Stay() string {
	return fmt.Sprintf("stay")
}
