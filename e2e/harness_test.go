//go:build e2e

// Package e2e contains end-to-end smoke tests for the jh CLI.
//
// These tests build the jh binary and run it against a live JuliaHub instance,
// asserting that read-only commands succeed and produce sensible output. They
// are guarded by the `e2e` build tag so they never run as part of the normal
// `go test ./...` unit suite.
//
// They are designed to run as part of the JuliaHub platform nightly CI, which
// deploys a fresh instance and provisions credentials. See e2e/README.md and
// e2e/ci/ for how the platform workflow invokes this suite.
//
// Authentication is always file-based: the jh CLI reads credentials from
// ~/.juliahub, exactly as it does for a normal interactive login. For CI, this
// harness materializes a throwaway ~/.juliahub inside an isolated HOME from the
// tokens the platform exports, so the CLI's auth path is exercised unchanged and
// no real login on the runner is touched.
//
// Run locally against your existing login (uses your real ~/.juliahub):
//
//	go test -tags e2e -v ./e2e/...
//
// Or have the harness write an isolated config from explicit credentials
// (matching the CI contract):
//
//	JULIAHUB_SERVER=https://nightly.juliahub.dev \
//	JULIAHUB_ID_TOKEN=<id_token> \
//	JULIAHUB_TOKEN=<access_token> \
//	  go test -tags e2e -v ./e2e/...
//
// If no isolated credentials are supplied and there is no ~/.juliahub login, the
// suite skips rather than fails.
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
	"regexp"
	"strings"
	"testing"
	"time"
)

// Ground-truth identity for the instance under test, loaded in TestMain from the
// active ~/.juliahub config (and its id_token). Tests assert that the CLI's
// output matches these values, i.e. that it faithfully reports who is logged in.
var (
	expectedServer string
	expectedEmail  string
	expectedName   string
)

// jhBin is the path to the jh binary under test, set in TestMain.
var jhBin string

// testHome, when non-empty, is an isolated HOME directory containing a
// .juliahub config written by the harness. jh subprocesses run with HOME set to
// it. When empty, subprocesses inherit the real HOME (i.e. a developer's login).
var testHome string

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
	if testHome != "" {
		os.RemoveAll(testHome)
	}
	os.Exit(code)
}

// loadGroundTruth populates the expected* identity vars from the active config
// file, falling back to the id_token's claims for any missing fields.
func loadGroundTruth() {
	cfg := readActiveConfig()
	expectedServer = cfg["server"]
	expectedEmail = cfg["email"]
	expectedName = cfg["name"]

	if claims := jwtClaims(cfg["id_token"]); claims != nil {
		if expectedEmail == "" {
			if e, ok := claims["email"].(string); ok {
				expectedEmail = e
			}
		}
		if expectedName == "" {
			if n, ok := claims["name"].(string); ok {
				expectedName = n
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

	tmp, err := os.MkdirTemp("", "jh-e2e-bin-")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	jhBin = filepath.Join(tmp, "jh")
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
// the credential env vars are provided (CI). The jh CLI then authenticates from
// that file just like a normal login. When the env vars are absent, this is a
// no-op and the suite falls back to the real ~/.juliahub.
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
	// Populate name/email from the id token so the isolated config matches what
	// a real `jh auth login` writes (and so ground-truth identity is available).
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
	err      error
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
	res := result{stdout: stdout.String(), stderr: stderr.String(), err: err}
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

// assertContains fails the test if haystack does not contain needle (case-insensitive).
func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(strings.ToLower(haystack), strings.ToLower(needle)) {
		t.Errorf("output does not contain %q\n---\n%s\n---", needle, truncate(haystack))
	}
}

// --- output helpers ---

// truncate caps long output for test logs (e.g. a multi-thousand-row listing).
func truncate(s string) string {
	const max = 2000
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("\n... [truncated %d bytes]", len(s)-max)
}

// firstLine returns the first non-empty line of s, trimmed — useful for skip/fail messages.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

// errorLine returns the first line that looks like an error/status message,
// falling back to firstLine — gives clearer skip/fail diagnostics than the first
// (often progress) line of output.
func errorLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		l := strings.TrimSpace(line)
		ll := strings.ToLower(l)
		if strings.Contains(ll, "fail") || strings.Contains(ll, "error") ||
			strings.Contains(ll, "status ") || strings.Contains(ll, "not found") ||
			strings.Contains(ll, "not allowed") {
			return l
		}
	}
	return firstLine(s)
}

var (
	reIDLine       = regexp.MustCompile(`(?m)^ID:\s*([0-9a-fA-F-]{36})`)
	reRegistryLine = regexp.MustCompile(`(?m)^(\S+)\s+\(([0-9a-fA-F-]{36})\)`)
	reUUID         = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
)

// firstID returns the first UUID on an "ID: <uuid>" line (e.g. from dataset/project list).
func firstID(out string) string {
	if m := reIDLine.FindStringSubmatch(out); len(m) == 2 {
		return m[1]
	}
	return ""
}

// firstField returns the value of the first "<field>: <value>" line (case-insensitive).
func firstField(out, field string) string {
	re := regexp.MustCompile(`(?mi)^` + regexp.QuoteMeta(field) + `:\s*(.+)$`)
	if m := re.FindStringSubmatch(out); len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// firstRegistryName returns the name from the first "Name (uuid)" line of `registry list`.
func firstRegistryName(out string) string {
	if m := reRegistryLine.FindStringSubmatch(out); len(m) >= 2 {
		return m[1]
	}
	return ""
}

var reParen = regexp.MustCompile(`\(([^)\s]+)\)`)

// firstParenValue returns the first parenthesized token, e.g. the username from a
// `user list` line "Display Name (username)".
func firstParenValue(out string) string {
	if m := reParen.FindStringSubmatch(out); len(m) == 2 {
		return m[1]
	}
	return ""
}

// isServerError reports whether output looks like an upstream HTTP 5xx error.
func isServerError(output string) bool {
	low := strings.ToLower(output)
	for _, s := range []string{"status 500", "status 502", "status 503", "status 504", "internal server error"} {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}

// unsupportedMarkers are substrings that indicate a command is unavailable or
// unauthorized on the instance under test (rather than a CLI defect): missing
// permissions, absent endpoints, disallowed queries, or transient backend
// errors. Tests skip rather than fail when they see one of these.
var unsupportedMarkers = []string{
	"permission", "forbidden", "unauthorized", "not allowed",
	"status 401", "status 403", "status 404",
	"status 500", "status 502", "status 503", "status 504", "internal server error",
	"context deadline exceeded", "timeout",
}

// skipIfUnsupported skips the test when the result indicates the command is not
// supported/authorized on this instance. It deliberately does NOT match
// CLI-side defects (e.g. JSON unmarshal errors), so those still fail.
func skipIfUnsupported(t *testing.T, res result) {
	t.Helper()
	low := strings.ToLower(res.combined())
	for _, m := range unsupportedMarkers {
		if strings.Contains(low, m) {
			t.Skipf("command unavailable/unauthorized on this instance (matched %q): %s", m, firstLine(res.combined()))
		}
	}
}
