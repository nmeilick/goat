package iotag

const (
	// Alpha is first.
	Alpha = iota
	Beta
	Gamma
)

// Use references the group from the source.
func Use() int { return Alpha }
