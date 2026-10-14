package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

// colorEnabled is true when stdout is a terminal, NO_COLOR is unset and TERM
// is not "dumb"; piped or redirected output stays plain text.
var colorEnabled = detectColor()

func detectColor() bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok || os.Getenv("TERM") == "dumb" {
		return false
	}
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func ansi(code, s string) string {
	if !colorEnabled || s == "" {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func bold(s string) string { return ansi("1", s) }
func dim(s string) string  { return ansi("2", s) }

// formatCount renders n with comma thousands separators: 78428 → "78,428".
func formatCount(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

var sparkBlocks = []rune("▁▂▃▄▅▆▇█")

// sparkline maps values onto eight block heights between their minimum and
// maximum; a flat series renders as the lowest block.
func sparkline(values []int) string {
	if len(values) == 0 {
		return ""
	}
	lo, hi := values[0], values[0]
	for _, v := range values {
		lo, hi = min(lo, v), max(hi, v)
	}
	var b strings.Builder
	for _, v := range values {
		i := 0
		if hi > lo {
			i = (v - lo) * (len(sparkBlocks) - 1) / (hi - lo)
		}
		b.WriteRune(sparkBlocks[i])
	}
	return b.String()
}

// truncate shortens s to at most width runes, ending in "…" when cut.
func truncate(s string, width int) string {
	if utf8.RuneCountInString(s) <= width {
		return s
	}
	if width <= 1 {
		return "…"
	}
	return string([]rune(s)[:width-1]) + "…"
}

// pad left-aligns s in width runes; ANSI styling is applied after padding so
// escape codes never count towards the width.
func pad(s string, width int) string {
	if n := utf8.RuneCountInString(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

func padLeft(s string, width int) string {
	if n := utf8.RuneCountInString(s); n < width {
		return strings.Repeat(" ", width-n) + s
	}
	return s
}

const fieldLabelWidth = 14

// printField prints an indented, aligned "label  value" line; empty values
// are skipped.
func printField(label, value string) {
	if value == "" {
		return
	}
	fmt.Printf("  %s%s\n", dim(pad(label, fieldLabelWidth)), value)
}
