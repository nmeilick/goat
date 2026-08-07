package embedcase

import (
	"fmt"
)

// Greet prints the greeting.
func Greet() string {
	return fmt.Sprintf("greeting: %s", greeting)
}

// Stay remains.
func Stay() string {
	return fmt.Sprintf("stay")
}
