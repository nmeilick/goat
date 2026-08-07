package aliased

import (
	"math/rand"
)

// Stay uses math/rand too.
func Stay(n int) int {
	return rand.Intn(n)
}
