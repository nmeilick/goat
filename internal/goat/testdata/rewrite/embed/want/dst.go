package embedcase

import (
	_ "embed"
)

//go:embed hello.txt
var greeting string
