//go:build e2e

package e2e

import (
	"regexp"
	"testing"
)

// `jh project list` — GraphQL. Renders "Found N project(s)" or "No projects
// found". Tolerant of GraphQL timeouts (skipIfUnsupported).
func TestProjectList(t *testing.T) {
	requireCreds(t)
	res := runJH(t, "project", "list")
	if res.exitCode != 0 {
		skipIfUnsupported(t, res)
		t.Fatalf("project list exited %d\nstderr: %s", res.exitCode, res.stderr)
	}
	if !regexp.MustCompile(`(?i)Found \d+ project|No projects found`).MatchString(res.combined()) {
		t.Errorf("project list output not a recognizable listing:\n%s", truncate(res.combined()))
	}
}

// TestProjectListByUser exercises the --user filter. The flag requires a
// username, which we take from the first `user list` entry.
func TestProjectListByUser(t *testing.T) {
	requireCreds(t)
	users := runJH(t, "user", "list")
	if users.exitCode != 0 {
		skipIfUnsupported(t, users)
		t.Skip("user list unavailable, cannot resolve a username to filter by")
	}
	username := firstParenValue(users.combined())
	if username == "" {
		t.Skip("could not resolve a username from `user list`")
	}

	res := runJH(t, "project", "list", "--user", username)
	if res.exitCode != 0 {
		skipIfUnsupported(t, res)
		t.Fatalf("project list --user %s exited %d\nstderr: %s", username, res.exitCode, res.stderr)
	}
	if !regexp.MustCompile(`(?i)Found \d+ project|No projects found`).MatchString(res.combined()) {
		t.Errorf("project list --user output not a recognizable listing:\n%s", truncate(res.combined()))
	}
}
