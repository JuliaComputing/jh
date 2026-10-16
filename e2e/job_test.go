//go:build e2e

package e2e

import (
	"encoding/json"
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

// `jh job result list` against a job name that cannot exist: exercises the real
// GET /api/rest/jobs/{jobname} response shape (an empty "details" list) without
// depending on a job with results on the instance.
func TestJobResultListUnknownJob(t *testing.T) {
	requireCreds(t)
	res := runJH(t, "job", "result", "list", "jr-jhe2enonexistent")
	if res.exitCode == 0 {
		t.Fatalf("job result list for an unknown job exited 0\nstdout: %s", res.stdout)
	}
	skipIfUnsupported(t, res)
	if !strings.Contains(res.stderr, "not found") {
		t.Errorf("want a not-found error, got stderr: %s", res.stderr)
	}
}

// `jh job usage --json` — GET /api/v1/jobs/usage. Skips on instances that
// predate the endpoint.
func TestJobUsageJSON(t *testing.T) {
	requireCreds(t)
	res := runJH(t, "job", "usage", "--json")
	if res.exitCode != 0 {
		if strings.Contains(res.stderr, "does not report compute usage") {
			t.Skipf("instance predates GET /api/v1/jobs/usage: %s", errorLine(res.stderr))
		}
		skipIfUnsupported(t, res)
		t.Fatalf("job usage exited %d\nstderr: %s", res.exitCode, res.stderr)
	}
	var doc map[string]struct {
		Used  *int64 `json:"used"`
		Limit *int64 `json:"limit"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, res.stdout)
	}
	for _, k := range []string{"jobs", "vcpus", "gpus"} {
		u, ok := doc[k]
		if !ok || u.Used == nil || u.Limit == nil {
			t.Errorf("missing %s.used/limit in %s", k, res.stdout)
			continue
		}
		if *u.Used < 0 || *u.Limit < 0 {
			t.Errorf("%s: negative values %d/%d", k, *u.Used, *u.Limit)
		}
	}
}
