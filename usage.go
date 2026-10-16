package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// ResourceUsage is the current usage of one per-user compute limit and the
// effective limit on it (the user's override, else the platform default).
type ResourceUsage struct {
	Used  int64 `json:"used"`
	Limit int64 `json:"limit"`
}

// JobUsage is GET /api/v1/jobs/usage: the calling user's concurrent jobs, and
// the vCPUs and GPUs allocated to them, against their limits. Usage counts the
// user's Submitted, SubmitInitialized and Running jobs, as job submission does
// when it enforces the limits.
type JobUsage struct {
	Jobs  ResourceUsage `json:"jobs"`
	VCPUs ResourceUsage `json:"vcpus"`
	GPUs  ResourceUsage `json:"gpus"`
}

// errJobUsageUnsupported: servers without the endpoint route the request to
// GET /api/v1/jobs/{job_name} as a job called "usage", which answers 404.
var errJobUsageUnsupported = errors.New("this JuliaHub server does not report compute usage (it needs a newer JuliaHub version)")

func fetchJobUsage(client *http.Client, baseURL, idToken string) (*JobUsage, error) {
	req, err := http.NewRequest("GET", baseURL+"/api/v1/jobs/usage", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+idToken)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, errJobUsageUnsupported
	case http.StatusForbidden:
		return nil, fmt.Errorf("you are not allowed to run jobs on this server (status 403): %s", string(body))
	default:
		return nil, fmt.Errorf("request failed (status %d): %s", resp.StatusCode, string(body))
	}

	// Decode into pointers first so a missing resource is an error, not a
	// silent zero usage against a zero limit.
	var parsed struct {
		Jobs  *ResourceUsage `json:"jobs"`
		VCPUs *ResourceUsage `json:"vcpus"`
		GPUs  *ResourceUsage `json:"gpus"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	if parsed.Jobs == nil || parsed.VCPUs == nil || parsed.GPUs == nil {
		return nil, fmt.Errorf("unexpected response, missing jobs, vcpus or gpus: %s", string(body))
	}
	return &JobUsage{Jobs: *parsed.Jobs, VCPUs: *parsed.VCPUs, GPUs: *parsed.GPUs}, nil
}

// usageLine is "<used> of <limit>", plus the share of the limit in use when
// anything is used, flagged once the limit is reached.
func usageLine(u ResourceUsage) string {
	line := formatCount(int(u.Used)) + " of " + formatCount(int(u.Limit))
	if u.Used == 0 || u.Limit <= 0 {
		return line
	}
	pct := fmt.Sprintf("%d%%", u.Used*100/u.Limit)
	if u.Used >= u.Limit {
		return line + "  (" + pct + ", at limit)"
	}
	return line + "  (" + pct + ")"
}

func printJobUsage(w io.Writer, server string, usage JobUsage) {
	fmt.Fprintf(w, "%s\n", bold("Compute usage on "+server))
	for _, f := range []struct {
		label string
		usage ResourceUsage
	}{
		{"Jobs", usage.Jobs},
		{"vCPUs", usage.VCPUs},
		{"GPUs", usage.GPUs},
	} {
		fmt.Fprintf(w, "  %s%s\n", dim(pad(f.label, fieldLabelWidth)), usageLine(f.usage))
	}
}

func showJobUsage(server string, asJSON bool) error {
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	usage, err := fetchJobUsage(client, jobBaseURL(server), token.IDToken)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(os.Stdout, usage)
	}
	printJobUsage(os.Stdout, server, *usage)
	return nil
}
