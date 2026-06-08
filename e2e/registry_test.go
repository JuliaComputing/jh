//go:build e2e

package e2e

import (
	"regexp"
	"strings"
	"testing"
)

// `jh registry` — list, plus config/registrator/permission driven off a real
// registry name taken from the list (preferring General when present).

// pickRegistry returns a registry name to drive the entity-specific commands,
// or "" if the instance has none.
func pickRegistry(t *testing.T) string {
	t.Helper()
	list := runOK(t, "registry", "list").combined()
	if regexp.MustCompile(`(?m)^General\s`).MatchString(list) {
		return "General"
	}
	return firstRegistryName(list)
}

// TestRegistryList verifies the listing renders.
func TestRegistryList(t *testing.T) {
	requireCreds(t)
	res := runJH(t, "registry", "list")
	if res.exitCode != 0 {
		skipIfUnsupported(t, res)
		t.Fatalf("registry list exited %d\nstderr: %s", res.exitCode, res.stderr)
	}
	if !regexp.MustCompile(`(?i)Found \d+ registr|No registries found`).MatchString(res.combined()) {
		t.Errorf("registry list output not a recognizable listing:\n%s", truncate(res.combined()))
	}
}

// TestRegistryConfigFirst takes a registry from the list and fetches its config,
// cross-validating the list and config endpoints.
func TestRegistryConfigFirst(t *testing.T) {
	requireCreds(t)
	name := pickRegistry(t)
	if name == "" {
		t.Skip("no registries on this instance")
	}

	res := runJH(t, "registry", "config", name)
	if res.exitCode != 0 {
		skipIfUnsupported(t, res)
		t.Fatalf("registry config %s exited %d\nstderr: %s", name, res.exitCode, res.stderr)
	}
	out := res.combined()
	assertContains(t, out, `"name"`)
	assertContains(t, out, name)
}

// TestRegistryRegistratorFirst fetches the registrator config for a registry.
// A registry with no registrator configured returns 404, which is tolerated.
func TestRegistryRegistratorFirst(t *testing.T) {
	requireCreds(t)
	name := pickRegistry(t)
	if name == "" {
		t.Skip("no registries on this instance")
	}

	res := runJH(t, "registry", "registrator", name)
	if res.exitCode != 0 {
		skipIfUnsupported(t, res)
		if strings.Contains(strings.ToLower(res.combined()), "not found") {
			t.Skipf("no registrator configured for %s", name)
		}
		t.Fatalf("registry registrator %s exited %d\nstderr: %s", name, res.exitCode, res.stderr)
	}
	// The response is the registrator JSON config.
	assertContains(t, res.combined(), "{")
}

// TestRegistryPermissionListFirst lists permissions for a registry.
func TestRegistryPermissionListFirst(t *testing.T) {
	requireCreds(t)
	name := pickRegistry(t)
	if name == "" {
		t.Skip("no registries on this instance")
	}

	res := runJH(t, "registry", "permission", "list", name)
	if res.exitCode != 0 {
		skipIfUnsupported(t, res)
		t.Fatalf("registry permission list %s exited %d\nstderr: %s", name, res.exitCode, res.stderr)
	}
	if strings.TrimSpace(res.combined()) == "" {
		t.Error("registry permission list produced no output")
	}
}
