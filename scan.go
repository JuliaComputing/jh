package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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

// resolveScanInputs takes a CLI positional argument and produces the manifest
// (and optional sibling Project.toml) contents the scan request will carry.
//
// path semantics:
//   - "" or ".": current directory; look for Manifest.toml and Project.toml inside
//   - a directory: same lookup inside that directory
//   - a file: treated as the manifest; sibling Project.toml is picked up by default
//
// projectOverride: explicit path to a Project.toml. "" means auto-detect.
// noProject: skip Project.toml even if a sibling file exists.
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
		manifestPath = filepath.Join(dir, "Manifest.toml")
		if _, err := os.Stat(manifestPath); err != nil {
			return ScanInputs{}, fmt.Errorf("no Manifest.toml found in %q", dir)
		}
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
		candidate := filepath.Join(dir, "Project.toml")
		if _, err := os.Stat(candidate); err == nil {
			projectPath = candidate
		}
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
