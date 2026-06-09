//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// `jh job list` — currently a placeholder in the CLI. This test pins that
// behaviour: it must run cleanly and produce output. If/when the command is
// implemented, tighten this assertion.
func TestJobList(t *testing.T) {
	requireCreds(t)
	res := runJH(t, "job", "list")
	if res.exitCode != 0 {
		skipIfUnsupported(t, res)
		t.Fatalf("job list exited %d\nstderr: %s", res.exitCode, res.stderr)
	}
	if strings.TrimSpace(res.combined()) == "" {
		t.Error("job list produced no output")
	}
}
