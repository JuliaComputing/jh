//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `jh scan` — submit a Julia Manifest.toml for a Trivy vulnerability scan, then
// `scan status <uuid>` / `scan results <uuid>`. The static-analysis backend is
// not present on every instance (the endpoint 404s), so the live test skips on
// that gap; when the endpoint is available it exercises submit → status →
// results using --no-wait to avoid a long poll.

const sampleManifest = `julia_version = "1.10.0"
manifest_format = "2.0"
project_hash = "jh-e2e"

[[deps.Example]]
uuid = "7876af07-990d-54b4-ab0e-23690620f79a"
version = "0.5.3"

[[deps.JSON]]
uuid = "682c06a0-de6a-54ab-a142-c8b1cf79cde6"
version = "0.21.4"
`

// scanBackendGap reports whether scan is unavailable on this instance (the
// static-analysis service is not deployed) rather than a CLI defect.
func scanBackendGap(out string) bool {
	low := strings.ToLower(out)
	return strings.Contains(low, "status 404") || isServerError(out) || strings.Contains(low, "not allowed")
}

// TestScanRejectsMissingManifest verifies input validation: a path that does not
// exist is reported cleanly. This is local validation and needs no backend.
func TestScanRejectsMissingManifest(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "Manifest.toml") // never created
	res := runJH(t, "scan", missing)
	if res.exitCode == 0 {
		t.Fatalf("expected scan to fail on a missing manifest, got exit 0:\n%s", res.combined())
	}
	assertContains(t, res.combined(), "cannot read")
}

// TestScanManifestLifecycle submits a manifest scan (--no-wait), then checks its
// status and attempts to fetch results, driving status/results off the
// server-assigned run_uuid.
func TestScanManifestLifecycle(t *testing.T) {
	requireCreds(t)

	manifest := filepath.Join(t.TempDir(), "Manifest.toml")
	if err := os.WriteFile(manifest, []byte(sampleManifest), 0o644); err != nil {
		t.Fatalf("failed to write sample manifest: %v", err)
	}

	// Submit without waiting for the (potentially long) Trivy run.
	sub := runJH(t, "scan", manifest, "--no-wait")
	if sub.exitCode != 0 {
		if scanBackendGap(sub.combined()) {
			t.Skipf("manifest scan unavailable on this instance: %s", errorLine(sub.combined()))
		}
		skipIfUnsupported(t, sub)
		t.Fatalf("scan --no-wait exited %d\nstderr: %s", sub.exitCode, sub.stderr)
	}

	// --no-wait prints the bare run_uuid on stdout.
	runUUID := reUUID.FindString(sub.stdout)
	if runUUID == "" {
		runUUID = reUUID.FindString(sub.combined())
	}
	if runUUID == "" {
		t.Fatalf("no run_uuid in scan output:\n%s", truncate(sub.combined()))
	}

	// status <uuid>
	st := runJH(t, "scan", "status", runUUID)
	if st.exitCode != 0 {
		skipIfUnsupported(t, st)
		t.Fatalf("scan status %s exited %d\nstderr: %s", runUUID, st.exitCode, st.stderr)
	}
	assertContains(t, st.combined(), "Status:")
	assertContains(t, st.combined(), "Tool:")

	// results <uuid> — may not be ready yet for a just-submitted scan, which is
	// fine; only fail if it succeeds but returns nothing.
	rs := runJH(t, "scan", "results", runUUID)
	if rs.exitCode != 0 {
		t.Logf("scan results not yet available for %s (tolerated): %s", runUUID, firstLine(rs.combined()))
		return
	}
	if strings.TrimSpace(rs.combined()) == "" {
		t.Error("scan results exited 0 but produced no output")
	}
}
