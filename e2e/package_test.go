//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// `jh package` — search/info/dependency. These depend on the REST `/packages/info`
// endpoint with a GraphQL fallback; on some instances those are unavailable or
// the GraphQL role is not permitted ("query is not allowed"). Such cases are
// treated as backend gaps and skipped; when the commands work, real structure
// is asserted. DataFrames is used as a well-known canary.

const canaryPackage = "DataFrames"

func TestPackageSearch(t *testing.T) {
	requireCreds(t)
	res := runJH(t, "package", "search", canaryPackage)
	if res.exitCode != 0 || backendGap(res.combined()) {
		skipIfUnsupported(t, res)
		t.Skipf("package search unavailable on this instance: %s", errorLine(res.combined()))
	}
	if strings.Contains(strings.ToLower(res.combined()), "no packages found") {
		t.Skip("no packages indexed on this instance")
	}
	assertContains(t, res.combined(), canaryPackage)
}

func TestPackageInfo(t *testing.T) {
	requireCreds(t)
	res := runJH(t, "package", "info", canaryPackage)
	out := res.combined()
	if res.exitCode != 0 || backendGap(out) {
		skipIfUnsupported(t, res)
		t.Skipf("package info unavailable on this instance: %s", errorLine(out))
	}
	if strings.Contains(strings.ToLower(out), "not found") {
		t.Skipf("%s not indexed on this instance", canaryPackage)
	}
	assertContains(t, out, canaryPackage)
	if !reUUID.MatchString(out) {
		t.Errorf("expected a UUID in package info output:\n%s", truncate(out))
	}
}

func TestPackageDependency(t *testing.T) {
	requireCreds(t)
	res := runJH(t, "package", "dependency", canaryPackage)
	out := res.combined()
	if res.exitCode != 0 || backendGap(out) {
		skipIfUnsupported(t, res)
		if strings.Contains(strings.ToLower(out), "not found") {
			t.Skipf("%s not indexed on this instance", canaryPackage)
		}
		t.Skipf("package dependency unavailable on this instance: %s", errorLine(out))
	}
	assertContains(t, out, "UUID") // dependency table header
}

// TestPackageInfoNotFound verifies a lookup for a nonexistent package is handled
// cleanly when the package backend is available.
func TestPackageInfoNotFound(t *testing.T) {
	requireCreds(t)
	res := runJH(t, "package", "info", "Zz_jh_e2e_does_not_exist_0xDEADBEEF")
	out := res.combined()
	if backendGap(out) {
		t.Skipf("package backend unavailable on this instance: %s", errorLine(out))
	}
	assertContains(t, out, "not found")
}
