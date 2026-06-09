//go:build e2e

package e2e

import (
	"regexp"
	"strings"
	"testing"
)

// `jh user` — info (self, GraphQL) and list (public_users, GraphQL).

// TestUserInfo verifies the GraphQL-backed `user info` returns the logged-in
// user's real details, matching the token identity.
func TestUserInfo(t *testing.T) {
	requireCreds(t)
	res := runJH(t, "user", "info")
	if res.exitCode != 0 {
		skipIfUnsupported(t, res)
		t.Fatalf("user info exited %d\nstderr: %s", res.exitCode, res.stderr)
	}
	out := res.combined()

	assertContains(t, out, "User Information")
	if !regexp.MustCompile(`(?mi)^ID:\s*\d+`).MatchString(out) {
		t.Errorf("expected a numeric 'ID:' line:\n%s", truncate(out))
	}
	assertContains(t, out, "Username:")
	assertContains(t, out, "Roles:")
	if expectedEmail != "" {
		assertContains(t, out, expectedEmail)
	}
}

// TestUserList verifies `user list` returns users in the "Name (username)" form.
func TestUserList(t *testing.T) {
	requireCreds(t)
	res := runJH(t, "user", "list")
	if res.exitCode != 0 {
		skipIfUnsupported(t, res)
		t.Fatalf("user list exited %d\nstderr: %s", res.exitCode, res.stderr)
	}
	// At least one "Name (username)" entry should be present.
	if !regexp.MustCompile(`\(\S+\)`).MatchString(res.combined()) {
		t.Errorf("user list output not in 'Name (username)' form:\n%s", truncate(res.combined()))
	}
}

// TestIdentityConsistentAcrossCommands cross-checks that `auth status` and
// `user info` agree on the logged-in email (and that it matches the token).
func TestIdentityConsistentAcrossCommands(t *testing.T) {
	requireCreds(t)
	status := runOK(t, "auth", "status").combined()
	info := runOK(t, "user", "info").combined()

	email := firstField(status, "Email")
	if email == "" {
		t.Skip("auth status did not report an email to cross-check")
	}
	assertContains(t, info, email)
	if expectedEmail != "" && !strings.EqualFold(email, expectedEmail) {
		t.Errorf("auth status email %q does not match token email %q", email, expectedEmail)
	}
}
