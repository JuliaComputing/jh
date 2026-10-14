package main

import "testing"

func TestFormatCount(t *testing.T) {
	for n, want := range map[int]string{0: "0", 7: "7", 999: "999", 1000: "1,000", 78428: "78,428", 1234567: "1,234,567", -4200: "-4,200"} {
		if got := formatCount(n); got != want {
			t.Errorf("formatCount(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestSparkline(t *testing.T) {
	if got := sparkline([]int{0, 7, 14}); got != "▁▄█" {
		t.Errorf("sparkline = %q, want ▁▄█", got)
	}
	if got := sparkline([]int{5, 5}); got != "▁▁" {
		t.Errorf("flat sparkline = %q, want ▁▁", got)
	}
	if got := sparkline(nil); got != "" {
		t.Errorf("empty sparkline = %q", got)
	}
}

func TestTruncateAndPad(t *testing.T) {
	if got := truncate("Modélisation", 6); got != "Modél…" {
		t.Errorf("truncate = %q, want Modél…", got)
	}
	if got := truncate("JuMP", 10); got != "JuMP" {
		t.Errorf("truncate short = %q", got)
	}
	if got := pad("é", 3); got != "é  " {
		t.Errorf("pad counts runes: %q", got)
	}
	if got := padLeft("42", 4); got != "  42" {
		t.Errorf("padLeft = %q", got)
	}
}

func TestStylingOffWhenDisabled(t *testing.T) {
	prev := colorEnabled
	colorEnabled = false
	defer func() { colorEnabled = prev }()
	if bold("x") != "x" || dim("y") != "y" {
		t.Error("styling must be a no-op when color is disabled")
	}
	colorEnabled = true
	if bold("x") != "\033[1mx\033[0m" {
		t.Errorf("bold = %q", bold("x"))
	}
}
