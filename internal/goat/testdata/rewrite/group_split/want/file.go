package groups

var (
	// A stays.
	A = 1
	// C stays too.
	C = 3
)

// Use keeps A and C referenced.
func Use() int { return A + C }
