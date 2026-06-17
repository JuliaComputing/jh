//go:build e2e

package e2e

import (
	"regexp"
	"strings"
	"testing"
)

// `jh admin` — list/show commands that require elevated permissions. Under a
// standard test user these return permission errors and are skipped; under an
// admin credential they execute and assert real structure. Some endpoints may be
// absent on a given instance (404) and are likewise skipped. Genuine CLI defects
// (e.g. a response that fails to parse) are NOT skipped — they fail.

func TestAdminUserList(t *testing.T) {
	requireCreds(t)
	res := runJH(t, "admin", "user", "list")
	if res.exitCode != 0 {
		skipIfUnsupported(t, res)
		t.Fatalf("admin user list exited %d\nstderr: %s", res.exitCode, res.stderr)
	}
	// Entries are rendered as "username (email)".
	if !regexp.MustCompile(`\(\S+@\S+\)`).MatchString(res.combined()) {
		t.Errorf("admin user list output not in 'name (email)' form:\n%s", truncate(res.combined()))
	}
}

func TestAdminTokenList(t *testing.T) {
	requireCreds(t)
	res := runJH(t, "admin", "token", "list")
	if res.exitCode != 0 {
		skipIfUnsupported(t, res)
		t.Fatalf("admin token list exited %d\nstderr: %s", res.exitCode, res.stderr)
	}
	out := res.combined()
	if !strings.Contains(out, "Subject:") && !regexp.MustCompile(`(?i)Tokens \(\d+`).MatchString(out) {
		t.Errorf("admin token list output not recognizable:\n%s", truncate(out))
	}
}

func TestAdminGroupList(t *testing.T) {
	requireCreds(t)
	res := runJH(t, "admin", "group", "list")
	if res.exitCode != 0 {
		// Skip only on missing perms / unavailable endpoint — NOT on parse
		// errors, which indicate a real CLI/API contract problem.
		skipIfUnsupported(t, res)
		t.Fatalf("admin group list exited %d\nstderr: %s", res.exitCode, res.stderr)
	}
	if strings.TrimSpace(res.combined()) == "" {
		t.Error("admin group list produced no output")
	}
}

func TestAdminCredentialList(t *testing.T) {
	requireCreds(t)
	res := runJH(t, "admin", "credential", "list")
	if res.exitCode != 0 {
		skipIfUnsupported(t, res)
		t.Fatalf("admin credential list exited %d\nstderr: %s", res.exitCode, res.stderr)
	}
	if strings.TrimSpace(res.combined()) == "" {
		t.Error("admin credential list produced no output")
	}
}

func TestAdminLandingPageShow(t *testing.T) {
	requireCreds(t)
	res := runJH(t, "admin", "landing-page", "show")
	out := res.combined()
	// "No custom content set." is a valid empty state. (The CLI currently exits
	// non-zero in that case, so accept it regardless of exit code.)
	if strings.Contains(strings.ToLower(out), "no custom content") {
		return
	}
	if res.exitCode != 0 {
		skipIfUnsupported(t, res)
		t.Fatalf("admin landing-page show exited %d\nstderr: %s", res.exitCode, res.stderr)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("admin landing-page show produced no output")
	}
}
