package main

import "testing"

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		current, latest string
		want            int
	}{
		{"v1.2.3", "v1.2.3", 0},  // equal, with v prefix
		{"1.2.3", "1.2.3", 0},    // equal, without prefix
		{"v1.2.3", "v1.2.4", -1}, // older
		{"v1.3.0", "v1.2.9", 1},  // newer
		{"dev", "v1.0.0", -1},    // dev is always older
		{"v1.0.0", "v1.0.0", 0},  // prefix stripped on both sides
	}
	for _, tt := range tests {
		if got := compareVersions(tt.current, tt.latest); got != tt.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tt.current, tt.latest, got, tt.want)
		}
	}
}
