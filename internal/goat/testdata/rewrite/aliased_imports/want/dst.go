package aliased

import (
	cryptorand "crypto/rand"
	"math/rand"
)

// Pick uses math/rand.
func Pick(n int) int {
	return rand.Intn(n)
}

// ReadKey uses crypto/rand.
func ReadKey(b []byte) error {
	_, err := cryptorand.Read(b)
	return err
}
