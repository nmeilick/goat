package sample

import "testing"

func TestHelper(t *testing.T) {
	if helper("x") == "" {
		t.Fail()
	}
}
