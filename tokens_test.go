package main

import (
	"strings"
	"testing"
)

func TestFormatTimestamp(t *testing.T) {
	// A well-formed RFC3339 timestamp is reformatted into the human layout.
	got := formatTimestamp("2025-01-02T03:04:05Z")
	if !strings.Contains(got, "2025") || !strings.Contains(got, "Jan") {
		t.Errorf("formatTimestamp reformat = %q, want a Jan 2025 date", got)
	}

	// Unparseable input is returned verbatim.
	for _, in := range []string{"", "not-a-date", "2025/01/02"} {
		if got := formatTimestamp(in); got != in {
			t.Errorf("formatTimestamp(%q) = %q, want passthrough", in, got)
		}
	}
}
