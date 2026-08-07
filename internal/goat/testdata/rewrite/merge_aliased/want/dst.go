package mergealias

import (
	cryptorand "crypto/rand"
	"fmt"
)

// Existing was already here.
func Existing() int {
	return 1
}

// Move joins the existing file.
func Move() error {
	b := make([]byte, 4)
	if _, err := cryptorand.Read(b); err != nil {
		return fmt.Errorf("read: %w", err)
	}
	return nil
}
