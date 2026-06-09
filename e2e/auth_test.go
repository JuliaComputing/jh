//go:build e2e

package e2e

import (
	"regexp"
	"testing"
)

// `jh auth` — status and env. These are deterministic on any instance: the CLI
// must report the same server and identity the credentials represent.

// TestAuthStatus verifies the credentials authenticate and that status reports a
// valid token for the expected server and user.
func TestAuthStatus(t *testing.T) {
	requireCreds(t)
	out := runOK(t, "auth", "status").combined()

	assertContains(t, out, "Token Status: Valid")
	assertContains(t, out, "Subject:")
	assertContains(t, out, "Issuer:")
	if expectedServer != "" {
		assertContains(t, out, "Server: "+expectedServer)
	}
	if expectedEmail != "" {
		assertContains(t, out, expectedEmail)
	}
}

// TestAuthEnv verifies `auth env` emits the shell-integration variables, with a
// real id token and the correct host.
func TestAuthEnv(t *testing.T) {
	requireCreds(t)
	out := runOK(t, "auth", "env").combined()

	assertContains(t, out, "JULIAHUB_HOST=")
	assertContains(t, out, "JULIAHUB_ID_TOKEN=")
	if expectedServer != "" {
		assertContains(t, out, "JULIAHUB_HOST="+expectedServer)
	}
	// The id token should be a JWT (three dot-separated segments).
	if !regexp.MustCompile(`JULIAHUB_ID_TOKEN=[\w-]+\.[\w-]+\.[\w-]+`).MatchString(out) {
		t.Errorf("JULIAHUB_ID_TOKEN does not look like a JWT:\n%s", truncate(out))
	}
}
