//go:build e2e

package e2e

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// Shared assertion, output-parsing, and skip helpers used across the suite.

// --- assertions ---

// assertContains fails the test if haystack does not contain needle (case-insensitive).
func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(strings.ToLower(haystack), strings.ToLower(needle)) {
		t.Errorf("output does not contain %q\n---\n%s\n---", needle, truncate(haystack))
	}
}

// --- output formatting ---

// truncate caps long output for test logs (e.g. a multi-thousand-row listing).
func truncate(s string) string {
	const max = 2000
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("\n... [truncated %d bytes]", len(s)-max)
}

// firstLine returns the first non-empty, trimmed line of s.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// errorLine returns the first line that looks like an error/status message,
// falling back to firstLine — clearer skip/fail diagnostics than the first
// (often progress) line of output.
func errorLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		l := strings.TrimSpace(line)
		ll := strings.ToLower(l)
		if strings.Contains(ll, "fail") || strings.Contains(ll, "error") ||
			strings.Contains(ll, "status ") || strings.Contains(ll, "not found") ||
			strings.Contains(ll, "not allowed") {
			return l
		}
	}
	return firstLine(s)
}

// --- output parsing (drives list -> first-entry -> detail chaining) ---

var (
	reIDLine       = regexp.MustCompile(`(?m)^ID:\s*([0-9a-fA-F-]{36})`)
	reRegistryLine = regexp.MustCompile(`(?m)^(\S+)\s+\(([0-9a-fA-F-]{36})\)`)
	reParen        = regexp.MustCompile(`\(([^)\s]+)\)`)
	reUUID         = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
)

// firstID returns the first UUID on an "ID: <uuid>" line (e.g. dataset/project list).
func firstID(out string) string {
	if m := reIDLine.FindStringSubmatch(out); len(m) == 2 {
		return m[1]
	}
	return ""
}

// firstField returns the value of the first "<field>: <value>" line (case-insensitive).
func firstField(out, field string) string {
	re := regexp.MustCompile(`(?mi)^` + regexp.QuoteMeta(field) + `:\s*(.+)$`)
	if m := re.FindStringSubmatch(out); len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// firstRegistryName returns the name from the first "Name (uuid)" line of `registry list`.
func firstRegistryName(out string) string {
	if m := reRegistryLine.FindStringSubmatch(out); len(m) >= 2 {
		return m[1]
	}
	return ""
}

// firstParenValue returns the first parenthesized token, e.g. the username from a
// `user list` line "Display Name (username)".
func firstParenValue(out string) string {
	if m := reParen.FindStringSubmatch(out); len(m) == 2 {
		return m[1]
	}
	return ""
}

// --- backend-gap detection (skip vs fail) ---

// isServerError reports whether output looks like an upstream HTTP 5xx error.
func isServerError(out string) bool {
	low := strings.ToLower(out)
	for _, s := range []string{"status 500", "status 502", "status 503", "status 504", "internal server error"} {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}

// backendGap reports whether output indicates the relevant service/endpoint is
// unavailable on this instance (absent endpoint, disallowed query, or 5xx) —
// i.e. a reason to skip rather than fail. Used by the package and scan tests,
// whose backends are not present on every instance.
func backendGap(out string) bool {
	low := strings.ToLower(out)
	if isServerError(out) {
		return true
	}
	for _, s := range []string{"status 404", "not allowed", "graphql errors"} {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}

// unsupportedMarkers are substrings indicating a command is unavailable or
// unauthorized on the instance (rather than a CLI defect): missing permissions,
// absent endpoints, disallowed queries, or transient backend errors.
var unsupportedMarkers = []string{
	"permission", "forbidden", "unauthorized", "not allowed",
	"status 401", "status 403", "status 404",
	"status 500", "status 502", "status 503", "status 504", "internal server error",
	"context deadline exceeded", "timeout",
}

// skipIfUnsupported skips the test when the result indicates the command is not
// supported/authorized on this instance. It deliberately does NOT match CLI-side
// defects (e.g. JSON unmarshal errors), so those still fail.
func skipIfUnsupported(t *testing.T, res result) {
	t.Helper()
	low := strings.ToLower(res.combined())
	for _, m := range unsupportedMarkers {
		if strings.Contains(low, m) {
			t.Skipf("command unavailable/unauthorized on this instance (matched %q): %s", m, errorLine(res.combined()))
		}
	}
}
