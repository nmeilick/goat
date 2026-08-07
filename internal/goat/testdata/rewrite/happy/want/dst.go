package happy

import (
	"fmt"
	"strings"
)

// Hello greets the world.
func Hello() string {
	return fmt.Sprintf("hello %s", "world")
}

// Shout uppercases s.
func Shout(s string) string {
	return strings.ToUpper(s)
}
