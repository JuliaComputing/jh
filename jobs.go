package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// jobResultFileType is the file type the job runtime gives the file named by
// the job's RESULTS_FILE environment variable.
const jobResultFileType = "result"

// JobFileHash is the hash JuliaHub records for an uploaded job file, e.g.
// Algorithm "sha2_256" with a hex-encoded Value. Both may be null while the
// upload is still in progress.
type JobFileHash struct {
	Algorithm *string `json:"algorithm"`
	Value     *string `json:"value"`
}

// JobFile is one entry of a job's "files" list: inputs, sources, project
// environment files and results, identified by (job, type, name).
type JobFile struct {
	Name            string       `json:"name"`
	Type            string       `json:"type"`
	Size            *int64       `json:"size"`
	Hash            *JobFileHash `json:"hash"`
	UploadTimestamp string       `json:"upload_timestamp"`
}

// JobDetails is the subset of GET /api/rest/jobs/{jobname} the CLI uses.
type JobDetails struct {
	JobName string    `json:"jobname"`
	Alias   *string   `json:"jobname_alias"`
	Status  string    `json:"status"`
	Files   []JobFile `json:"files"`
}

type jobDetailsResponse struct {
	Details []JobDetails `json:"details"`
}

// fetchJobDetails fetches a job by name (e.g. jr-abc123) from the Hasura REST
// endpoint, the same one JuliaHub.jl's JuliaHub.job uses.
func fetchJobDetails(client *http.Client, baseURL, idToken, jobName string) (*JobDetails, error) {
	req, err := http.NewRequest("GET", baseURL+"/api/rest/jobs/"+url.PathEscape(jobName), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+idToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Hasura-Role", "jhuser")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed (status %d): %s", resp.StatusCode, string(body))
	}

	var parsed jobDetailsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	switch len(parsed.Details) {
	case 0:
		return nil, fmt.Errorf("job '%s' not found", jobName)
	case 1:
		return &parsed.Details[0], nil
	default:
		return nil, fmt.Errorf("unexpected response: %d jobs named '%s'", len(parsed.Details), jobName)
	}
}

// filterJobFiles returns the files of the given type (all files when fileType
// is empty), sorted by type then name.
func filterJobFiles(files []JobFile, fileType string) []JobFile {
	var out []JobFile
	for _, f := range files {
		if fileType == "" || f.Type == fileType {
			out = append(out, f)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// formatBytes renders a byte count with a binary unit, e.g. 1.5 KiB.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func jobFileSizeLabel(f JobFile) string {
	if f.Size == nil {
		return "pending"
	}
	return formatBytes(*f.Size)
}

func jobDisplayName(job *JobDetails) string {
	if job.Alias != nil && *job.Alias != "" {
		return fmt.Sprintf("%s (%s)", job.JobName, *job.Alias)
	}
	return job.JobName
}

func printJobFiles(w io.Writer, job *JobDetails, files []JobFile, showType bool) {
	fmt.Fprintf(w, "%s %s %s\n", bold(jobDisplayName(job)), dim(job.Status), dim("("+formatCount(len(files))+" files)"))
	if len(files) == 0 {
		if showType {
			fmt.Fprintln(w, "No files.")
		} else {
			fmt.Fprintln(w, "No result files. A job uploads one when it sets ENV[\"RESULTS_FILE\"] to a file path.")
		}
		return
	}

	nameWidth := len("NAME")
	for _, f := range files {
		if n := len(f.Name); n > nameWidth {
			nameWidth = n
		}
	}
	if nameWidth > 60 {
		nameWidth = 60
	}

	header := pad("NAME", nameWidth) + "  " + padLeft("SIZE", 10) + "  UPLOADED"
	if showType {
		header = pad("TYPE", 8) + "  " + header
	}
	fmt.Fprintln(w, bold(header))
	for _, f := range files {
		line := pad(truncate(f.Name, nameWidth), nameWidth) + "  " + padLeft(jobFileSizeLabel(f), 10) + "  " + dim(formatTokenDate(f.UploadTimestamp))
		if showType {
			line = pad(f.Type, 8) + "  " + line
		}
		fmt.Fprintln(w, line)
	}
}

// jobBaseURL is the https base URL for a normalized server name.
func jobBaseURL(server string) string {
	return "https://" + server
}

func newJobHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Minute,
		// The file endpoint 302-redirects to a presigned object-store URL.
		// The signature is in the query string; also sending our bearer token
		// would be a second auth mechanism the object store rejects, and it
		// must not leak to the storage host. Go only strips Authorization on
		// cross-domain redirects, so drop it unconditionally.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			req.Header.Del("Authorization")
			return nil
		},
	}
}

func listJobResults(server, jobName string, all, asJSON bool) error {
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	job, err := fetchJobDetails(client, jobBaseURL(server), token.IDToken, jobName)
	if err != nil {
		return err
	}

	fileType := jobResultFileType
	if all {
		fileType = ""
	}
	files := filterJobFiles(job.Files, fileType)

	if asJSON {
		if files == nil {
			files = []JobFile{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"job":    job.JobName,
			"status": job.Status,
			"files":  files,
		})
	}
	printJobFiles(os.Stdout, job, files, all)
	return nil
}

// selectJobFiles picks the files to download: the named file of fileType, or
// every file of fileType when name is empty.
func selectJobFiles(job *JobDetails, fileType, name string) ([]JobFile, error) {
	candidates := filterJobFiles(job.Files, fileType)
	if name == "" {
		if len(candidates) == 0 {
			return nil, fmt.Errorf("job '%s' (%s) has no %s files", job.JobName, job.Status, fileType)
		}
		return candidates, nil
	}
	for _, f := range candidates {
		if f.Name == name {
			return []JobFile{f}, nil
		}
	}
	var names []string
	for _, f := range candidates {
		names = append(names, f.Name)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("job '%s' (%s) has no %s files", job.JobName, job.Status, fileType)
	}
	return nil, fmt.Errorf("job '%s' has no %s file named '%s' (available: %s)", job.JobName, fileType, name, strings.Join(names, ", "))
}

// jobFileDestination resolves where a file is written. With several files,
// output is a directory (created if needed). With one, an existing directory
// or a path ending in a separator receives the file under its own name;
// anything else is the destination file path. An empty output means the
// current directory.
func jobFileDestination(output, fileName string, multiple bool) (string, error) {
	// Job file names come from the server; never let one escape the target
	// directory.
	base := filepath.Base(filepath.FromSlash(fileName))
	if base == "." || base == ".." || base == string(filepath.Separator) {
		return "", fmt.Errorf("refusing to write file with unsafe name '%s'", fileName)
	}
	if output == "" {
		return base, nil
	}
	if multiple || strings.HasSuffix(output, "/") || strings.HasSuffix(output, string(filepath.Separator)) {
		return filepath.Join(output, base), nil
	}
	if info, err := os.Stat(output); err == nil && info.IsDir() {
		return filepath.Join(output, base), nil
	}
	return output, nil
}

// decodeJobFileHash decodes a recorded SHA-256 hash. JuliaHub stores job file
// hashes as hex; base64 is accepted too since JuliaHub.jl documents FileHash
// values as base64. A 64-character hex string is also valid base64, so hex is
// tried first.
func decodeJobFileHash(value string) ([]byte, error) {
	if b, err := hex.DecodeString(value); err == nil && len(b) == sha256.Size {
		return b, nil
	}
	b, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	if len(b) != sha256.Size {
		return nil, fmt.Errorf("expected a %d-byte SHA-256 digest, got %d bytes", sha256.Size, len(b))
	}
	return b, nil
}

// verifyJobFileHash checks sum (a SHA-256 digest) against the recorded hash
// when JuliaHub recorded a sha2_256 one; other or missing hashes are skipped.
func verifyJobFileHash(f JobFile, sum []byte) error {
	if f.Hash == nil || f.Hash.Algorithm == nil || f.Hash.Value == nil || *f.Hash.Algorithm != "sha2_256" {
		return nil
	}
	want, err := decodeJobFileHash(*f.Hash.Value)
	if err != nil {
		return fmt.Errorf("invalid recorded hash for '%s': %w", f.Name, err)
	}
	if string(want) != string(sum) {
		return fmt.Errorf("checksum mismatch for '%s': expected sha256 %x, got %x", f.Name, want, sum)
	}
	return nil
}

// downloadJobFile streams one job file to dest via a temporary file in the
// same directory, verifying its hash before the final rename. It refuses to
// overwrite dest unless force is set.
func downloadJobFile(client *http.Client, baseURL, idToken, jobName string, f JobFile, dest string, force bool) (int64, error) {
	if !force {
		if _, err := os.Stat(dest); err == nil {
			return 0, fmt.Errorf("%s already exists (use --force to overwrite)", dest)
		}
	}

	u := fmt.Sprintf("%s/jobs/%s/files/%s?filetype=%s", baseURL, url.PathEscape(jobName), url.PathEscape(f.Name), url.QueryEscape(f.Type))
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+idToken)

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to download '%s': %w", f.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, fmt.Errorf("download of '%s' failed (status %d): %s", f.Name, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, fmt.Errorf("failed to create directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dest)+".*.tmp")
	if err != nil {
		return 0, fmt.Errorf("failed to create temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), resp.Body)
	if err == nil {
		// CreateTemp makes the file 0600; give it ordinary download permissions.
		err = tmp.Chmod(0644)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, fmt.Errorf("failed to write '%s': %w", f.Name, err)
	}
	if err := verifyJobFileHash(f, h.Sum(nil)); err != nil {
		return 0, err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return 0, fmt.Errorf("failed to move download into place: %w", err)
	}
	return n, nil
}

func downloadJobResults(server, jobName, fileName, output, fileType string, force bool) error {
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}
	baseURL := jobBaseURL(server)
	client := newJobHTTPClient()

	job, err := fetchJobDetails(&http.Client{Timeout: 30 * time.Second}, baseURL, token.IDToken, jobName)
	if err != nil {
		return err
	}
	files, err := selectJobFiles(job, fileType, fileName)
	if err != nil {
		return err
	}

	multiple := len(files) > 1
	for _, f := range files {
		dest, err := jobFileDestination(output, f.Name, multiple)
		if err != nil {
			return err
		}
		n, err := downloadJobFile(client, baseURL, token.IDToken, job.JobName, f, dest, force)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Downloaded %s (%s) to %s\n", f.Name, formatBytes(n), dest)
	}
	return nil
}
