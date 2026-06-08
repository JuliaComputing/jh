//go:build e2e

// Package e2e contains end-to-end tests for the jh CLI. They build the jh binary
// and run its read/GET commands (plus a manifest-scan submission) against a live
// JuliaHub instance, asserting real behaviour. They are guarded by the `e2e`
// build tag, so `go test ./...` never runs them.
//
// They are designed to run as part of the JuliaHub platform nightly CI. See
// README.md for the layout and the "Running in CI" section.
//
// Authentication is always file-based: the jh CLI reads credentials from
// ~/.juliahub, exactly as after a normal interactive login. For CI, this harness
// materializes a throwaway ~/.juliahub inside an isolated HOME from tokens passed
// via env vars, so the CLI's auth path is exercised unchanged and no real login
// on the runner is touched.
//
// Run locally against your existing login (uses your real ~/.juliahub):
//
//	go test -tags e2e -v ./e2e/...        # or: make e2e
//
// Or write an isolated config from explicit credentials (the CI contract):
//
//	JULIAHUB_SERVER=https://nightly.juliahub.dev \
//	JULIAHUB_ID_TOKEN=<id_token> \
//	JULIAHUB_TOKEN=<access_token> \
//	  go test -tags e2e -v ./e2e/...
//
// With no isolated credentials and no ~/.juliahub login, the suite skips rather
// than fails.
package e2e

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Ground-truth identity for the instance under test, loaded in TestMain from the
// active ~/.juliahub (and its id_token). Tests assert the CLI reports these
// values, i.e. that it faithfully reflects who is logged in.
var (
	expectedServer string
	expectedEmail  string
)

var (
	// jhBin is the path to the jh binary under test, set in TestMain.
	jhBin string
	// binDir is the temp dir holding a freshly built binary, removed on exit
	// (empty when JH_BIN supplies a prebuilt binary).
	binDir string
	// testHome, when non-empty, is an isolated HOME holding a .juliahub written
	// by the harness; jh subprocesses run with HOME set to it. When empty,
	// subprocesses inherit the real HOME (a developer's login).
	testHome string
)

// commandTimeout bounds any single jh invocation so a hung network call cannot
// stall the whole suite.
const commandTimeout = 90 * time.Second

func TestMain(m *testing.M) {
	if err := buildBinary(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := setupIsolatedConfig(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	loadGroundTruth()

	code := m.Run()

	if binDir != "" {
		os.RemoveAll(binDir)
	}
	if testHome != "" {
		os.RemoveAll(testHome)
	}
	os.Exit(code)
}

// buildBinary locates or builds the jh binary under test.
func buildBinary() error {
	// Allow CI to supply a prebuilt binary; otherwise build one from the repo
	// root (the parent directory of this package).
	if bin := os.Getenv("JH_BIN"); bin != "" {
		abs, err := filepath.Abs(bin)
		if err != nil {
			return fmt.Errorf("JH_BIN is not a valid path: %w", err)
		}
		jhBin = abs
		return nil
	}

	dir, err := os.MkdirTemp("", "jh-e2e-bin-")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	binDir = dir
	jhBin = filepath.Join(dir, "jh")
	build := exec.Command("go", "build", "-o", jhBin, ".")
	build.Dir = ".." // repo root, where package main lives
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("failed to build jh binary: %w", err)
	}
	return nil
}

// setupIsolatedConfig writes a throwaway ~/.juliahub into an isolated HOME when
// credential env vars are provided (CI). The jh CLI then authenticates from that
// file just like a normal login. When the env vars are absent this is a no-op and
// the suite falls back to the real ~/.juliahub.
func setupIsolatedConfig() error {
	server := normalizeHost(os.Getenv("JULIAHUB_SERVER"))
	idToken := os.Getenv("JULIAHUB_ID_TOKEN")
	accessToken := os.Getenv("JULIAHUB_TOKEN")

	// Either token can stand in for the other if only one is provided.
	if idToken == "" {
		idToken = accessToken
	}
	if accessToken == "" {
		accessToken = idToken
	}
	if idToken == "" {
		return nil // no isolated credentials; use the real ~/.juliahub
	}
	if server == "" {
		return fmt.Errorf("JULIAHUB_SERVER must be set when supplying credentials")
	}

	dir, err := os.MkdirTemp("", "jh-e2e-home-")
	if err != nil {
		return fmt.Errorf("failed to create isolated HOME: %w", err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "server=%s\n", server)
	fmt.Fprintf(&b, "access_token=%s\n", accessToken)
	fmt.Fprintf(&b, "token_type=Bearer\n")
	if rt := os.Getenv("JULIAHUB_REFRESH_TOKEN"); rt != "" {
		fmt.Fprintf(&b, "refresh_token=%s\n", rt)
	}
	fmt.Fprintf(&b, "id_token=%s\n", idToken)
	// Populate name/email from the id token so the isolated config matches what a
	// real `jh auth login` writes (and so ground-truth identity is available).
	if claims := jwtClaims(idToken); claims != nil {
		if n, ok := claims["name"].(string); ok && n != "" {
			fmt.Fprintf(&b, "name=%s\n", n)
		}
		if e, ok := claims["email"].(string); ok && e != "" {
			fmt.Fprintf(&b, "email=%s\n", e)
		}
	}

	configPath := filepath.Join(dir, ".juliahub")
	if err := os.WriteFile(configPath, []byte(b.String()), 0600); err != nil {
		os.RemoveAll(dir)
		return fmt.Errorf("failed to write %s: %w", configPath, err)
	}

	testHome = dir
	return nil
}

// loadGroundTruth populates the expected* identity vars from the active config,
// falling back to the id_token's claims for any missing fields.
func loadGroundTruth() {
	cfg := readActiveConfig()
	expectedServer = cfg["server"]
	expectedEmail = cfg["email"]

	if claims := jwtClaims(cfg["id_token"]); claims != nil {
		if expectedEmail == "" {
			if e, ok := claims["email"].(string); ok {
				expectedEmail = e
			}
		}
		if expectedServer == "" {
			if iss, ok := claims["iss"].(string); ok {
				expectedServer = normalizeHost(strings.TrimSuffix(strings.TrimSuffix(iss, "/dex"), "/"))
			}
		}
	}
}

// activeConfigPath returns the ~/.juliahub the CLI will read: the isolated one
// when set, otherwise the one in the real HOME.
func activeConfigPath() string {
	if testHome != "" {
		return filepath.Join(testHome, ".juliahub")
	}
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".juliahub")
}

// readActiveConfig parses the active ~/.juliahub into a key=value map.
func readActiveConfig() map[string]string {
	m := map[string]string{}
	path := activeConfigPath()
	if path == "" {
		return m
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return m
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, "="); i > 0 {
			m[line[:i]] = line[i+1:]
		}
	}
	return m
}

// jwtClaims decodes the payload of a JWT into a claims map (best-effort).
func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	payload := parts[1]
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}
	b, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(b, &claims) != nil {
		return nil
	}
	return claims
}

// normalizeHost strips any scheme and trailing slash from a server value.
func normalizeHost(server string) string {
	server = strings.TrimSpace(server)
	server = strings.TrimPrefix(server, "https://")
	server = strings.TrimPrefix(server, "http://")
	return strings.TrimSuffix(server, "/")
}

// requireCreds skips the test unless usable credentials are available: either an
// isolated config written by the harness, or an on-disk login in the real HOME.
func requireCreds(t *testing.T) {
	t.Helper()
	if testHome != "" {
		return
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		if _, err := os.Stat(filepath.Join(home, ".juliahub")); err == nil {
			return
		}
	}
	t.Skip("no credentials: set JULIAHUB_SERVER + JULIAHUB_ID_TOKEN/JULIAHUB_TOKEN, or log in with `jh auth login`")
}

// result holds the outcome of a single jh invocation.
type result struct {
	stdout   string
	stderr   string
	exitCode int
}

// combined returns stdout and stderr concatenated, for convenience in assertions.
func (r result) combined() string { return r.stdout + r.stderr }

// runJH executes `jh <args...>` and returns the captured output. The server and
// credentials come entirely from ~/.juliahub (the isolated config when set,
// otherwise the real login). It does not fail the test on a non-zero exit;
// callers assert on the result.
func runJH(t *testing.T, args ...string) result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, jhBin, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = os.Environ()
	if testHome != "" {
		cmd.Env = append(cmd.Env, "HOME="+testHome)
	}

	err := cmd.Run()
	res := result{stdout: stdout.String(), stderr: stderr.String()}
	if ee, ok := err.(*exec.ExitError); ok {
		res.exitCode = ee.ExitCode()
	} else if err != nil {
		res.exitCode = -1
	}

	t.Logf("$ jh %s\n[exit %d]\nstdout:\n%s\nstderr:\n%s",
		strings.Join(args, " "), res.exitCode, truncate(res.stdout), truncate(res.stderr))

	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("jh %s timed out after %s", strings.Join(args, " "), commandTimeout)
	}
	return res
}

// runOK runs a command and fails the test unless it exits 0.
func runOK(t *testing.T, args ...string) result {
	t.Helper()
	res := runJH(t, args...)
	if res.exitCode != 0 {
		t.Fatalf("jh %s exited %d, want 0\nstderr: %s", strings.Join(args, " "), res.exitCode, res.stderr)
	}
	return res
}
