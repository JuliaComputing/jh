//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// `jh group list` — GraphQL groups query. Groups may be empty on an instance, so
// a clean run with either group names or "No groups found" is acceptable.
func TestGroupList(t *testing.T) {
	requireCreds(t)
	res := runJH(t, "group", "list")
	if res.exitCode != 0 {
		skipIfUnsupported(t, res)
		t.Fatalf("group list exited %d\nstderr: %s", res.exitCode, res.stderr)
	}
	if strings.TrimSpace(res.combined()) == "" {
		t.Error("group list produced no output")
	}
}
