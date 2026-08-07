//go:build go1.18

package tagged

import "fmt"

// Tagged moves to a new tagged file.
func Tagged() string {
	return fmt.Sprintf("tagged")
}

// Stay remains.
func Stay() string {
	return fmt.Sprintf("stay")
}
