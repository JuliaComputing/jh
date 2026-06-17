//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// CLI-surface tests that need no credentials: subcommand help wiring and
// argument validation. These run even when the suite has no login, so they give
// baseline coverage of the command tree and cobra arg handling on every CI run.

// TestSubcommandHelp verifies each command group renders help listing its
// documented subcommands.
func TestSubcommandHelp(t *testing.T) {
	cases := []struct {
		args     []string
		expected []string
	}{
		{[]string{"auth", "--help"}, []string{"login", "status", "env"}},
		{[]string{"dataset", "--help"}, []string{"list", "download", "upload", "status"}},
		{[]string{"registry", "--help"}, []string{"list", "config", "permission", "registrator"}},
		{[]string{"package", "--help"}, []string{"search", "info", "dependency"}},
		{[]string{"admin", "--help"}, []string{"user", "token", "group", "credential", "landing-page"}},
		{[]string{"admin", "credential", "--help"}, []string{"list", "add", "update", "delete"}},
		{[]string{"scan", "--help"}, []string{"status", "results"}},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			out := runOK(t, tc.args...).combined()
			for _, want := range tc.expected {
				assertContains(t, out, want)
			}
		})
	}
}

// TestArgValidation verifies commands that require exactly one positional argument
// reject a missing argument cleanly (cobra validation, before any network call).
func TestArgValidation(t *testing.T) {
	cases := [][]string{
		{"vuln"},
		{"package", "info"},
		{"package", "dependency"},
		{"registry", "config"},
		{"scan", "status"},
		{"scan", "results"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			res := runJH(t, args...)
			if res.exitCode == 0 {
				t.Fatalf("expected non-zero exit for missing arg, got 0:\n%s", truncate(res.combined()))
			}
			// Cobra prints an arg error and the usage block.
			if !strings.Contains(strings.ToLower(res.combined()), "arg") &&
				!strings.Contains(res.combined(), "Usage:") {
				t.Errorf("expected an arg/usage error message:\n%s", truncate(res.combined()))
			}
		})
	}
}
