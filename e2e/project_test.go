//go:build e2e

package e2e

import (
	"regexp"
	"strings"
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

// TestCloneNotFound runs the `jh clone` owner/name lookup against the live
// server with a project that doesn't exist, so the server must accept the
// lookup's document and filter and the command ends in "not found" before any
// git clone starts.
func TestCloneNotFound(t *testing.T) {
	requireCreds(t)
	res := runJH(t, "clone", "zz-jh-e2e-no-such-user/zz_jh_e2e_no_such_project_0xDEADBEEF", t.TempDir())
	if res.exitCode == 0 {
		t.Fatalf("clone of a nonexistent project succeeded:\n%s", truncate(res.combined()))
	}
	if !strings.Contains(res.combined(), "not found") {
		skipIfUnsupported(t, res)
		t.Fatalf("clone lookup did not end in 'not found':\n%s", truncate(res.combined()))
	}
}
