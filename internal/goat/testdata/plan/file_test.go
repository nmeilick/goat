package plan

import "testing"

func TestHelper(t *testing.T) {
	if testHelper() == "" {
		t.Fail()
	}
}
