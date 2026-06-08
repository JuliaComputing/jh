//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// `jh vuln` — scans a package for advisories via the vulnerabilities + docs
// version endpoints. These are data-dependent (the advisory DB / docs build can
// lag), so the test asserts a recognizable report when it runs and skips on
// data/backend gaps.
func TestVulnScan(t *testing.T) {
	requireCreds(t)
	res := runJH(t, "vuln", "MbedTLS_jll")
	low := strings.ToLower(res.combined())

	if res.exitCode != 0 {
		if strings.Contains(low, "not found") || strings.Contains(low, "no versions") || isServerError(res.combined()) {
			t.Skipf("MbedTLS_jll not resolvable on this instance: %s", firstLine(res.combined()))
		}
		skipIfUnsupported(t, res)
		t.Fatalf("vuln exited %d\nstderr: %s", res.exitCode, res.stderr)
	}

	if !strings.Contains(low, "advisory") && !strings.Contains(low, "affected") && !strings.Contains(low, "no ") {
		t.Errorf("vuln output not a recognizable advisory report:\n%s", truncate(res.combined()))
	}
}
