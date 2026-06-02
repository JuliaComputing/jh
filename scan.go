package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Endpoints proxied by websrvr at /api/v0/static_analysis/ to the StaticAnalysis
// service, which then registers them under /static_analysis/. See
// websrvr/nginx/nginx.conf.template and services/StaticAnalysis/src/API.jl.
const staticAnalysisPathPrefix = "/api/v0/static_analysis"

// ManifestScanStatus mirrors the inline schema returned by
// GET /static_analysis/manifest/scan/status/{run_uuid}.
type ManifestScanStatus struct {
	Status                     string  `json:"status"`
	CreatedAt                  string  `json:"created_at"`
	FinishedAt                 *string `json:"finished_at"`
	JhubToolID                 string  `json:"jhub_tool_id"`
	JhubToolRulesetFingerprint *string `json:"jhub_tool_ruleset_fingerprint"`
	FailureReason              *string `json:"failure_reason"`
}

// ScanInputs is the set of files staged for upload in a single scan request.
type ScanInputs struct {
	ManifestPath string
	ManifestBody string
	ProjectPath  string // empty if no Project.toml is being sent
	ProjectBody  string
}

// manifestNameRe matches the manifest file names Julia/Pkg recognizes:
// Manifest.toml and JuliaManifest.toml, plus the version-specific variants
// Manifest-v1.11.toml / JuliaManifest-v1.11.toml.
var manifestNameRe = regexp.MustCompile(`^(Julia)?Manifest(-v(\d+)\.(\d+))?\.toml$`)

// projectNames are the project file names Pkg recognizes, in precedence order
// (JuliaProject.toml is preferred over Project.toml).
var projectNames = []string{"JuliaProject.toml", "Project.toml"}

// manifestCandidate is a recognized manifest file found in a directory, with
// the parsed pieces used to order candidates by Pkg's resolution precedence.
type manifestCandidate struct {
	path        string
	name        string
	juliaPrefix bool
	versioned   bool
	major       int
	minor       int
}

// findManifestCandidates returns the manifest files in dir that Julia/Pkg
// would recognize, ordered by resolution precedence (most-preferred first):
// version-specific before unversioned, higher version before lower, and the
// JuliaManifest* spelling before plain Manifest* at the same rank.
func findManifestCandidates(dir string) ([]manifestCandidate, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("cannot read directory %q: %w", dir, err)
	}
	var cands []manifestCandidate
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := manifestNameRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		c := manifestCandidate{
			path:        filepath.Join(dir, e.Name()),
			name:        e.Name(),
			juliaPrefix: m[1] != "",
			versioned:   m[2] != "",
		}
		if c.versioned {
			c.major, _ = strconv.Atoi(m[3])
			c.minor, _ = strconv.Atoi(m[4])
		}
		cands = append(cands, c)
	}
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.versioned != b.versioned {
			return a.versioned // versioned manifests take precedence
		}
		if a.versioned && b.versioned {
			if a.major != b.major {
				return a.major > b.major // newest version first
			}
			if a.minor != b.minor {
				return a.minor > b.minor
			}
		}
		if a.juliaPrefix != b.juliaPrefix {
			return a.juliaPrefix // JuliaManifest* before Manifest*
		}
		return a.name < b.name
	})
	return cands, nil
}

// chooseManifest returns the single candidate when there is exactly one, or
// prompts the user to pick when several are present. When stdin is not an
// interactive terminal it returns an error listing the candidates so the
// caller can re-run with an explicit manifest path.
func chooseManifest(cands []manifestCandidate) (manifestCandidate, error) {
	if len(cands) == 1 {
		return cands[0], nil
	}
	if !stdinIsTerminal() {
		names := make([]string, len(cands))
		for i, c := range cands {
			names[i] = c.name
		}
		return manifestCandidate{}, fmt.Errorf(
			"multiple manifests found (%s); re-run with an explicit path, e.g. `jh scan %s`",
			strings.Join(names, ", "), cands[0].path)
	}
	fmt.Fprintln(os.Stderr, "Multiple manifests found:")
	for i, c := range cands {
		fmt.Fprintf(os.Stderr, "  [%d] %s\n", i+1, c.name)
	}
	fmt.Fprintf(os.Stderr, "Select a manifest [1-%d] (default 1): ", len(cands))
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return cands[0], nil
	}
	idx, err := strconv.Atoi(line)
	if err != nil || idx < 1 || idx > len(cands) {
		return manifestCandidate{}, fmt.Errorf("invalid selection %q", line)
	}
	return cands[idx-1], nil
}

// stdinIsTerminal reports whether stdin is an interactive terminal, used to
// decide whether prompting for a manifest selection is possible.
func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// findProjectFile returns the path to the project file in dir that Pkg would
// pair with a manifest (JuliaProject.toml preferred over Project.toml), or ""
// if neither exists.
func findProjectFile(dir string) string {
	for _, name := range projectNames {
		candidate := filepath.Join(dir, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// resolveScanInputs takes a CLI positional argument and produces the manifest
// (and optional sibling project file) contents the scan request will carry.
//
// path semantics:
//   - "" or ".": current directory; discover manifest + project file inside
//   - a directory: same discovery inside that directory
//   - a file: treated as the manifest; a sibling project file is picked up by default
//
// Manifest discovery recognizes every name Julia/Pkg accepts — Manifest.toml,
// JuliaManifest.toml, and version-specific variants like Manifest-v1.11.toml.
// When a directory holds more than one, the user is prompted to select one.
//
// projectOverride: explicit path to a project file. "" means auto-detect.
// noProject: skip the project file even if a sibling exists.
func resolveScanInputs(path, projectOverride string, noProject bool) (ScanInputs, error) {
	if path == "" {
		path = "."
	}
	info, err := os.Stat(path)
	if err != nil {
		return ScanInputs{}, fmt.Errorf("cannot read %q: %w", path, err)
	}

	var manifestPath, dir string
	if info.IsDir() {
		dir = path
		cands, err := findManifestCandidates(dir)
		if err != nil {
			return ScanInputs{}, err
		}
		if len(cands) == 0 {
			return ScanInputs{}, fmt.Errorf(
				"no manifest found in %q (looked for Manifest.toml, JuliaManifest.toml, and version-specific variants)", dir)
		}
		chosen, err := chooseManifest(cands)
		if err != nil {
			return ScanInputs{}, err
		}
		manifestPath = chosen.path
	} else {
		manifestPath = path
		dir = filepath.Dir(path)
	}

	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return ScanInputs{}, fmt.Errorf("failed to read manifest %q: %w", manifestPath, err)
	}
	if len(bytes.TrimSpace(manifestBytes)) == 0 {
		return ScanInputs{}, fmt.Errorf("manifest %q is empty", manifestPath)
	}

	out := ScanInputs{ManifestPath: manifestPath, ManifestBody: string(manifestBytes)}

	if noProject {
		return out, nil
	}

	projectPath := projectOverride
	if projectPath == "" {
		projectPath = findProjectFile(dir)
	}
	if projectPath != "" {
		projectBytes, err := os.ReadFile(projectPath)
		if err != nil {
			return ScanInputs{}, fmt.Errorf("failed to read project file %q: %w", projectPath, err)
		}
		out.ProjectPath = projectPath
		out.ProjectBody = string(projectBytes)
	}

	return out, nil
}

// submitManifestScan POSTs the manifest (and optional Project.toml) to the
// backend and returns the server-assigned run_uuid.
func submitManifestScan(server, toolID string, inputs ScanInputs) (string, error) {
	token, err := ensureValidToken()
	if err != nil {
		return "", fmt.Errorf("authentication required: %w", err)
	}

	payload := map[string]interface{}{
		"manifest_toml": inputs.ManifestBody,
		"jhub_tool_id":  toolID,
	}
	if inputs.ProjectBody != "" {
		payload["project_toml"] = inputs.ProjectBody
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint := fmt.Sprintf("https://%s%s/manifest/scan", server, staticAnalysisPathPrefix)
	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.IDToken))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("scan submission failed (status %d): %s",
			resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	// The server returns the uuid as a bare string body; JSON-encoded
	// `"uuid"` is also accepted in case the response format is tightened.
	runUUID := strings.TrimSpace(string(respBody))
	if strings.HasPrefix(runUUID, "\"") {
		if err := json.Unmarshal(respBody, &runUUID); err != nil {
			return "", fmt.Errorf("failed to parse run_uuid from response: %w (body: %s)",
				err, string(respBody))
		}
	}
	if runUUID == "" {
		return "", fmt.Errorf("server returned empty run_uuid")
	}
	return runUUID, nil
}

func fetchManifestScanStatus(server, runUUID string) (*ManifestScanStatus, error) {
	token, err := ensureValidToken()
	if err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}

	endpoint := fmt.Sprintf("https://%s%s/manifest/scan/status/%s",
		server, staticAnalysisPathPrefix, url.PathEscape(runUUID))
	body, err := apiGet(endpoint, token.IDToken)
	if err != nil {
		return nil, err
	}

	var status ManifestScanStatus
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, fmt.Errorf("failed to parse status response: %w", err)
	}
	return &status, nil
}

// fetchManifestScanResults retrieves the scan results. When wantCSV is true the
// Accept header asks for text/csv; otherwise JSON is returned.
func fetchManifestScanResults(server, runUUID string, wantCSV bool) ([]byte, error) {
	token, err := ensureValidToken()
	if err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}

	endpoint := fmt.Sprintf("https://%s%s/results/manifest/%s",
		server, staticAnalysisPathPrefix, url.PathEscape(runUUID))
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.IDToken))
	if wantCSV {
		req.Header.Set("Accept", "text/csv")
	} else {
		req.Header.Set("Accept", "application/json")
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("results fetch failed (status %d): %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// pollManifestScanUntilDone polls status every pollInterval until the scan
// reaches a terminal status ("completed" or "failed") or the deadline expires.
// Progress is reported to stderr so stdout stays clean for piping.
func pollManifestScanUntilDone(server, runUUID string, pollInterval, timeout time.Duration) (*ManifestScanStatus, error) {
	deadline := time.Now().Add(timeout)
	lastStatus := ""
	for {
		status, err := fetchManifestScanStatus(server, runUUID)
		if err != nil {
			return nil, err
		}
		if status.Status != lastStatus {
			fmt.Fprintf(os.Stderr, "scan %s: %s\n", runUUID, status.Status)
			lastStatus = status.Status
		}
		switch status.Status {
		case "completed":
			return status, nil
		case "failed":
			reason := ""
			if status.FailureReason != nil {
				reason = *status.FailureReason
			}
			if reason == "" {
				return status, fmt.Errorf("scan %s failed (no reason reported)", runUUID)
			}
			return status, fmt.Errorf("scan %s failed: %s", runUUID, reason)
		}
		if time.Now().After(deadline) {
			return status, fmt.Errorf("timed out waiting for scan %s (last status: %s)",
				runUUID, status.Status)
		}
		time.Sleep(pollInterval)
	}
}

func writeResultsOutput(body []byte, outputPath string, wantCSV bool) error {
	// Pretty-print JSON for terminal output; leave CSV as-is.
	var out []byte
	if wantCSV {
		out = body
	} else {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, body, "", "  "); err != nil {
			out = body
		} else {
			out = pretty.Bytes()
			if len(out) == 0 || out[len(out)-1] != '\n' {
				out = append(out, '\n')
			}
		}
	}

	if outputPath == "" {
		_, err := os.Stdout.Write(out)
		return err
	}
	if err := os.WriteFile(outputPath, out, 0644); err != nil {
		return fmt.Errorf("failed to write %q: %w", outputPath, err)
	}
	fmt.Fprintf(os.Stderr, "Wrote results to %s\n", outputPath)
	return nil
}

func printManifestScanStatus(status *ManifestScanStatus) {
	fmt.Printf("Status:                       %s\n", status.Status)
	fmt.Printf("Created:                      %s\n", status.CreatedAt)
	if status.FinishedAt != nil {
		fmt.Printf("Finished:                     %s\n", *status.FinishedAt)
	}
	fmt.Printf("Tool:                         %s\n", status.JhubToolID)
	if status.JhubToolRulesetFingerprint != nil {
		fmt.Printf("Ruleset fingerprint:          %s\n", *status.JhubToolRulesetFingerprint)
	}
	if status.FailureReason != nil && *status.FailureReason != "" {
		fmt.Printf("Failure reason:               %s\n", *status.FailureReason)
	}
}
