package merge

import "fmt"

// Move joins the existing file.
func Move() string {
	return fmt.Sprintf("moved")
}

// Stay remains.
func Stay() string {
	return fmt.Sprintf("stay")
}
