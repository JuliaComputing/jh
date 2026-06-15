package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsJuliaHubURL(t *testing.T) {
	// Isolate HOME so the configured-server branch reads a known config and the
	// real ~/.juliahub never influences the result.
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)
	if err := writeConfigFile("custom-internal.example.org"); err != nil {
		t.Fatalf("failed to seed config: %v", err)
	}

	tests := []struct {
		host string
		want bool
	}{
		{"", false},
		{"juliahub.com", true},
		{"nightly.juliahub.com", true},
		{"my.juliahub.dev", true},             // contains "juliahub"
		{"custom-internal.example.org", true}, // matches configured server
		{"github.com", false},
		{"gitlab.example.org", false},
	}
	for _, tt := range tests {
		if got := isJuliaHubURL(tt.host); got != tt.want {
			t.Errorf("isJuliaHubURL(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestGetClonePath(t *testing.T) {
	if got := getClonePath("/tmp/dest", "proj"); got != "/tmp/dest" {
		t.Errorf("getClonePath with explicit path = %q, want /tmp/dest", got)
	}
	if got := getClonePath("", "proj"); got != "./proj" {
		t.Errorf("getClonePath default = %q, want ./proj", got)
	}
}

func TestFindAvailableFolderName(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(wd) })

	// No collisions yet → first candidate.
	if got := findAvailableFolderName("proj"); got != "./proj-1" {
		t.Fatalf("findAvailableFolderName first = %q, want ./proj-1", got)
	}

	// Occupy proj-1 and proj-2; the next free name is proj-3.
	for _, name := range []string{"proj-1", "proj-2"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	if got := findAvailableFolderName("proj"); got != "./proj-3" {
		t.Errorf("findAvailableFolderName with collisions = %q, want ./proj-3", got)
	}
}

func TestGitCredentialHelperActions(t *testing.T) {
	// store and erase are intentional no-ops.
	if err := gitCredentialHelper("store"); err != nil {
		t.Errorf("gitCredentialHelper(store) = %v, want nil", err)
	}
	if err := gitCredentialHelper("erase"); err != nil {
		t.Errorf("gitCredentialHelper(erase) = %v, want nil", err)
	}
	// An unknown action is an error.
	if err := gitCredentialHelper("bogus"); err == nil {
		t.Error("gitCredentialHelper(bogus) = nil, want error")
	}
}

func TestCheckGitInstalled(t *testing.T) {
	// git is available in the dev/CI environment, so this should succeed.
	if err := checkGitInstalled(); err != nil {
		t.Skipf("git not installed in this environment: %v", err)
	}
}
