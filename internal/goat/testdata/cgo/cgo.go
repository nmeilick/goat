package cgo

// #include <stdlib.h>
import "C"

func Random() int {
	return int(C.rand())
}
