//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// `jh dataset` — list, and the entity-specific status/download driven off the
// first dataset returned by list.

// TestDatasetList verifies the listing renders (whether empty or not).
func TestDatasetList(t *testing.T) {
	requireCreds(t)
	res := runJH(t, "dataset", "list")
	if res.exitCode != 0 {
		skipIfUnsupported(t, res)
		t.Fatalf("dataset list exited %d\nstderr: %s", res.exitCode, res.stderr)
	}
	if !regexp.MustCompile(`(?i)Found \d+ dataset|No datasets found`).MatchString(res.combined()) {
		t.Errorf("dataset list output not a recognizable listing:\n%s", truncate(res.combined()))
	}
}

// TestDatasetStatusFirst lists datasets, takes the first, and checks its status
// resolves a version and a download URL.
func TestDatasetStatusFirst(t *testing.T) {
	requireCreds(t)
	list := runOK(t, "dataset", "list").combined()
	id := firstID(list)
	if id == "" {
		t.Skip("no datasets on this instance to inspect")
	}

	res := runJH(t, "dataset", "status", id)
	if res.exitCode != 0 {
		skipIfUnsupported(t, res)
		t.Fatalf("dataset status %s exited %d\nstderr: %s", id, res.exitCode, res.stderr)
	}
	out := res.combined()
	assertContains(t, out, "Dataset:")
	assertContains(t, out, "Version:")
	if !regexp.MustCompile(`(?i)Download URL:\s*https?://`).MatchString(out) {
		t.Errorf("expected a presigned Download URL in status output:\n%s", truncate(out))
	}
}

// TestDatasetDownloadFirst lists datasets, takes the first, downloads it to a
// temp path, and asserts the file was written and is non-empty.
func TestDatasetDownloadFirst(t *testing.T) {
	requireCreds(t)
	list := runOK(t, "dataset", "list").combined()
	id := firstID(list)
	if id == "" {
		t.Skip("no datasets on this instance to download")
	}

	dest := filepath.Join(t.TempDir(), "dataset.bin")
	res := runJH(t, "dataset", "download", id, dest)
	if res.exitCode != 0 {
		skipIfUnsupported(t, res)
		t.Fatalf("dataset download %s exited %d\nstderr: %s", id, res.exitCode, res.stderr)
	}

	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("expected downloaded file at %s: %v", dest, err)
	}
	if info.Size() == 0 {
		t.Errorf("downloaded dataset file is empty: %s", dest)
	}
}
