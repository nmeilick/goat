package goat

import (
	"strings"
	"testing"
)

func TestUnifiedDiffNoNewlineMarker(t *testing.T) {
	/*
		old lacks the trailing newline: the shared final line must show as
		-/+ with the GNU diff marker on the old side only.
	*/
	diff := UnifiedDiff("a", "b", []byte("x\ny"), []byte("x\ny\n"), false)
	if diff == "" {
		t.Fatal("a trailing-newline-only change must produce a diff")
	}
	if !strings.Contains(diff, "-y\n\\ No newline at end of file\n") {
		t.Errorf("the removed final line should carry the no-newline marker:\n%s", diff)
	}
	if !strings.Contains(diff, "+y\n") {
		t.Errorf("the added final line should appear without a marker:\n%s", diff)
	}

	// Reversed: the new side lacks the newline.
	diff = UnifiedDiff("a", "b", []byte("x\ny\n"), []byte("x\ny"), false)
	if !strings.Contains(diff, "+y\n\\ No newline at end of file\n") {
		t.Errorf("the added final line should carry the no-newline marker:\n%s", diff)
	}

	// Both sides lack it and are identical: no diff at all.
	if diff := UnifiedDiff("a", "b", []byte("x\ny"), []byte("x\ny"), false); diff != "" {
		t.Errorf("identical contents should produce no diff, got:\n%s", diff)
	}

	/*
		Both sides lack it but differ earlier: the shared final line shows
		as context with a single marker, as GNU diff renders it.
	*/
	diff = UnifiedDiff("a", "b", []byte("u\ny"), []byte("v\ny"), false)
	if !strings.Contains(diff, " y\n\\ No newline at end of file\n") {
		t.Errorf("the shared final line should carry one no-newline marker:\n%s", diff)
	}
	if strings.Count(diff, `\ No newline at end of file`) != 1 {
		t.Errorf("expected exactly one marker, got:\n%s", diff)
	}
}
