package merge

import (
	"fmt"
	"strings"
)

// Existing was already here.
func Existing() string {
	return strings.TrimSpace(" x ")
}

// Move joins the existing file.
func Move() string {
	return fmt.Sprintf("moved")
}
