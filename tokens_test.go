package main

import (
	"strings"
	"testing"
)

func TestFormatTokenDate(t *testing.T) {
	// A well-formed RFC3339 timestamp is reformatted into the human layout.
	got := formatTokenDate("2025-01-02T03:04:05Z")
	if !strings.Contains(got, "2025") || !strings.Contains(got, "Jan") {
		t.Errorf("formatTokenDate reformat = %q, want a Jan 2025 date", got)
	}

	// Unparseable input is returned verbatim.
	for _, in := range []string{"", "not-a-date", "2025/01/02"} {
		if got := formatTokenDate(in); got != in {
			t.Errorf("formatTokenDate(%q) = %q, want passthrough", in, got)
		}
	}
}
