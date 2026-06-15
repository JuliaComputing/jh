//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// Verbose and flag variants of read-only commands. These drive the --verbose /
// flag branches that the default-output tests don't reach. They reuse the same
// skip-on-backend-gap policy: a command unavailable or unauthorized on the
// instance is skipped, not failed.

// runVerbose runs a read command and asserts it produced output, skipping on the
// usual backend gaps. It is a thin wrapper to keep the variants below compact.
func runVerbose(t *testing.T, args ...string) {
	t.Helper()
	requireCreds(t)
	res := runJH(t, args...)
	if res.exitCode != 0 {
		skipIfUnsupported(t, res)
		t.Fatalf("jh %s exited %d\nstderr: %s", strings.Join(args, " "), res.exitCode, res.stderr)
	}
	if strings.TrimSpace(res.combined()) == "" {
		t.Errorf("jh %s --verbose produced no output", strings.Join(args, " "))
	}
}

// Note: only the *admin* `user list` has a --verbose flag; the GraphQL
// `user list` does not, so it is intentionally not covered here.
func TestRegistryListVerbose(t *testing.T)   { runVerbose(t, "registry", "list", "--verbose") }
func TestAdminUserListVerbose(t *testing.T)  { runVerbose(t, "admin", "user", "list", "--verbose") }
func TestAdminTokenListVerbose(t *testing.T) { runVerbose(t, "admin", "token", "list", "--verbose") }
func TestAdminCredListVerbose(t *testing.T) {
	runVerbose(t, "admin", "credential", "list", "--verbose")
}
func TestPackageSearchVerbose(t *testing.T) {
	runVerbose(t, "package", "search", "DataFrames", "--verbose")
}

// TestVulnFlags exercises the --all and --verbose advisory-rendering branches on
// the canary package. Data-dependent, so it skips on resolution/backend gaps.
func TestVulnFlags(t *testing.T) {
	requireCreds(t)
	for _, args := range [][]string{
		{"vuln", "MbedTLS_jll", "--all"},
		{"vuln", "MbedTLS_jll", "--all", "--verbose"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			res := runJH(t, args...)
			low := strings.ToLower(res.combined())
			if res.exitCode != 0 {
				if strings.Contains(low, "not found") || strings.Contains(low, "no versions") || isServerError(res.combined()) {
					t.Skipf("MbedTLS_jll not resolvable on this instance: %s", firstLine(res.combined()))
				}
				skipIfUnsupported(t, res)
				t.Fatalf("jh %s exited %d\nstderr: %s", strings.Join(args, " "), res.exitCode, res.stderr)
			}
			if !strings.Contains(low, "advisory") && !strings.Contains(low, "affected") && !strings.Contains(low, "no ") {
				t.Errorf("vuln output not a recognizable advisory report:\n%s", truncate(res.combined()))
			}
		})
	}
}
