//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// Top-level CLI behaviour that needs no credentials.

func TestVersion(t *testing.T) {
	res := runOK(t, "--version")
	if strings.TrimSpace(res.combined()) == "" {
		t.Error("--version produced no output")
	}
}

func TestRootHelp(t *testing.T) {
	res := runOK(t, "--help")
	assertContains(t, res.combined(), "JuliaHub")
	assertContains(t, res.combined(), "Available Commands")
}
