package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// jobResultFileType is the file type the job runtime gives the file named by
// the job's RESULTS_FILE environment variable.
const jobResultFileType = "result"

// jobFileTypes are the job file types JuliaHub records.
var jobFileTypes = []string{"input", "source", "project", jobResultFileType}

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
	ProxyLink *string   `json:"proxy_link"`
	JobName   string    `json:"jobname"`
	Alias     *string   `json:"jobname_alias"`
	Status    string    `json:"status"`
	Files     []JobFile `json:"files"`
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
		if n := utf8.RuneCountInString(f.Name); n > nameWidth {
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
		line := pad(truncate(f.Name, nameWidth), nameWidth) + "  " + padLeft(jobFileSizeLabel(f), 10) + "  " + dim(formatTimestamp(f.UploadTimestamp))
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
	// No overall Client.Timeout: it would also bound reading the body, and abort
	// large downloads over slow links. Only waiting for response headers, the
	// phase that can hang silently, is bounded.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 60 * time.Second
	return &http.Client{
		Transport: transport,
		// The file endpoint 302-redirects to a presigned object-store URL.
		// The signature is in the query string; also sending our bearer token
		// would be a second auth mechanism the object store rejects, and it
		// must not leak to the storage host. Go only strips Authorization on
		// cross-domain redirects, so drop it unconditionally. Referer would
		// tell the storage host the job and file names, so drop that too.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			req.Header.Del("Authorization")
			req.Header.Del("Referer")
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

// jobFilePending reports whether a file's upload is still in progress; its
// size (and hash) are null until it completes.
func jobFilePending(f JobFile) bool {
	return f.Size == nil
}

// selectJobFiles picks the files to download: the named file of fileType, or
// every file of fileType when name is empty. Files still being uploaded are
// returned separately as pending when no name is given, and are an error when
// named explicitly.
func selectJobFiles(job *JobDetails, fileType, name string) (files, pending []JobFile, err error) {
	candidates := filterJobFiles(job.Files, fileType)
	if len(candidates) == 0 {
		return nil, nil, fmt.Errorf("job '%s' (%s) has no %s files", job.JobName, job.Status, fileType)
	}
	if name == "" {
		for _, f := range candidates {
			if jobFilePending(f) {
				pending = append(pending, f)
			} else {
				files = append(files, f)
			}
		}
		if len(files) == 0 {
			return nil, pending, fmt.Errorf("job '%s' (%s) has no completed %s files; %d still uploading", job.JobName, job.Status, fileType, len(pending))
		}
		return files, pending, nil
	}
	for _, f := range candidates {
		if f.Name == name {
			if jobFilePending(f) {
				return nil, nil, fmt.Errorf("'%s' of job '%s' (%s) is still being uploaded", name, job.JobName, job.Status)
			}
			return []JobFile{f}, nil, nil
		}
	}
	var names []string
	for _, f := range candidates {
		names = append(names, f.Name)
	}
	return nil, nil, fmt.Errorf("job '%s' has no %s file named '%s' (available: %s)", job.JobName, fileType, name, strings.Join(names, ", "))
}

// jobFileDestination resolves where a file is written. When downloading all
// files (multiple), output is a directory (created if needed), however many
// files there are. For a named file, an existing directory or a path ending in
// a separator receives the file under its own name; anything else is the
// destination file path. An empty output means the current directory.
func jobFileDestination(output, fileName string, multiple bool) (string, error) {
	// Job file names come from the server; never let one escape the target
	// directory. IsLocal also rejects Windows reserved names such as NUL.
	base := filepath.Base(filepath.FromSlash(fileName))
	if base == "." || !filepath.IsLocal(base) {
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
// values as base64. The digest length keeps the two apart: a 64-character hex
// string decodes as base64 to 48 bytes, not 32, and a 44-character base64
// digest (with its "=" padding) is never valid hex.
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
// when JuliaHub recorded a sha2_256 one, and reports whether it did. Other,
// missing or empty hashes are skipped.
func verifyJobFileHash(f JobFile, sum []byte) (verified bool, err error) {
	if f.Hash == nil || f.Hash.Algorithm == nil || f.Hash.Value == nil || *f.Hash.Algorithm != "sha2_256" || *f.Hash.Value == "" {
		return false, nil
	}
	want, err := decodeJobFileHash(*f.Hash.Value)
	if err != nil {
		return false, fmt.Errorf("invalid recorded hash for '%s': %w", f.Name, err)
	}
	if string(want) != string(sum) {
		return false, fmt.Errorf("checksum mismatch for '%s': expected sha256 %x, got %x", f.Name, want, sum)
	}
	return true, nil
}

func errJobFileExists(dest string) error {
	return fmt.Errorf("%s already exists (use --force to overwrite)", dest)
}

// moveJobFileIntoPlace moves a finished download from tmpName to dest. Without
// force it never replaces a file, including one created during the download:
// os.Link fails atomically when dest exists, unlike os.Rename. Where hard links
// are unsupported it falls back to re-checking dest just before the rename.
// The caller removes tmpName.
func moveJobFileIntoPlace(tmpName, dest string, force bool) error {
	if !force {
		err := os.Link(tmpName, dest)
		if err == nil {
			return nil
		}
		if errors.Is(err, fs.ErrExist) {
			return errJobFileExists(dest)
		}
		if _, err := os.Lstat(dest); err == nil {
			return errJobFileExists(dest)
		}
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("failed to move download into place: %w", err)
	}
	return nil
}

// jobFileDownload is the outcome of downloading one job file.
type jobFileDownload struct {
	Bytes    int64
	Verified bool // checked against a recorded SHA-256 hash
}

// downloadJobFile streams one job file to dest via a temporary file in the
// same directory, checking its size and hash before moving it into place. It
// refuses to overwrite dest unless force is set.
func downloadJobFile(client *http.Client, baseURL, idToken, jobName string, f JobFile, dest string, force bool) (jobFileDownload, error) {
	var res jobFileDownload
	if !force {
		if _, err := os.Stat(dest); err == nil {
			return res, errJobFileExists(dest)
		}
	}

	u := fmt.Sprintf("%s/jobs/%s/files/%s?filetype=%s", baseURL, url.PathEscape(jobName), url.PathEscape(f.Name), url.QueryEscape(f.Type))
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return res, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+idToken)

	resp, err := client.Do(req)
	if err != nil {
		return res, fmt.Errorf("failed to download '%s': %w", f.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return res, fmt.Errorf("download of '%s' failed (status %d): %s", f.Name, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return res, fmt.Errorf("failed to create directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dest)+".*.tmp")
	if err != nil {
		return res, fmt.Errorf("failed to create temporary file: %w", err)
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
		return res, fmt.Errorf("failed to write '%s': %w", f.Name, err)
	}
	if f.Size != nil && n != *f.Size {
		return res, fmt.Errorf("size mismatch for '%s': expected %d bytes, got %d", f.Name, *f.Size, n)
	}
	verified, err := verifyJobFileHash(f, h.Sum(nil))
	if err != nil {
		return res, err
	}
	if err := moveJobFileIntoPlace(tmpName, dest, force); err != nil {
		return res, err
	}
	return jobFileDownload{Bytes: n, Verified: verified}, nil
}

// jobDownload is one planned file download.
type jobDownload struct {
	File JobFile
	Dest string
}

// planJobDownloads selects the files to download and resolves each one's
// destination before anything is written, so a bad request fails up front. It
// also returns the pending files skipped when downloading all files.
func planJobDownloads(job *JobDetails, fileType, fileName, output string) ([]jobDownload, []JobFile, error) {
	if !slices.Contains(jobFileTypes, fileType) {
		return nil, nil, fmt.Errorf("invalid file type '%s' (want one of: %s)", fileType, strings.Join(jobFileTypes, ", "))
	}
	files, pending, err := selectJobFiles(job, fileType, fileName)
	if err != nil {
		return nil, nil, err
	}
	// Decide by the request, not by how many files the job has, so -o has the
	// same meaning for every job.
	multiple := fileName == ""
	seen := map[string]string{}
	var plan []jobDownload
	for _, f := range files {
		dest, err := jobFileDestination(output, f.Name, multiple)
		if err != nil {
			return nil, nil, err
		}
		if prev, ok := seen[dest]; ok {
			return nil, nil, fmt.Errorf("'%s' and '%s' would both be saved as %s", prev, f.Name, dest)
		}
		seen[dest] = f.Name
		plan = append(plan, jobDownload{File: f, Dest: dest})
	}
	return plan, pending, nil
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
	plan, pending, err := planJobDownloads(job, fileType, fileName, output)
	if err != nil {
		return err
	}
	for _, f := range pending {
		fmt.Fprintf(os.Stderr, "Skipping %s: upload in progress\n", f.Name)
	}

	for _, d := range plan {
		res, err := downloadJobFile(client, baseURL, token.IDToken, job.JobName, d.File, d.Dest, force)
		if err != nil {
			return err
		}
		note := ""
		if !res.Verified {
			note = " (not verified: no SHA-256 hash recorded)"
		}
		fmt.Fprintf(os.Stderr, "Downloaded %s (%s) to %s%s\n", d.File.Name, formatBytes(res.Bytes), d.Dest, note)
	}
	return nil
}
