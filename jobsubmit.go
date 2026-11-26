package main

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Job submission uses the job management API the web UI uses:
//
//	GET  /api/v1/config/compute/nodes     pick a node (unless --node is given)
//	GET  /jobs/appbundle_upload_url       presigned upload URL (--bundle only)
//	PUT  <upload_url>                     the bundle tarball (--bundle only)
//	POST /api/v1/jobs                     multipart submission, returns the job name

// defaultJobProduct is the product JuliaHub runs plain Julia batch jobs under.
const defaultJobProduct = "standard-batch"

// jobAPIError is a non-2xx answer from the job management API, whose error
// bodies are {"message": "..."}.
type jobAPIError struct {
	Status  int
	Message string
}

func (e *jobAPIError) Error() string {
	return fmt.Sprintf("%s (status %d)", e.Message, e.Status)
}

// newJobAPIError builds a jobAPIError from a response body, using the body
// itself as the message when it is not a {"message": ...} document.
func newJobAPIError(status int, body []byte) *jobAPIError {
	var parsed struct {
		Message string `json:"message"`
	}
	msg := strings.TrimSpace(string(body))
	if json.Unmarshal(body, &parsed) == nil && parsed.Message != "" {
		msg = parsed.Message
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	return &jobAPIError{Status: status, Message: msg}
}

// jobAPIDo sends an authenticated request to the job management API and
// returns the body of a 2xx answer, or a *jobAPIError.
func jobAPIDo(client *http.Client, method, url, idToken, contentType string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+idToken)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, newJobAPIError(resp.StatusCode, respBody)
	}
	return respBody, nil
}

// ComputeNode is one entry of GET /api/v1/config/compute/nodes: a machine
// type jobs can run on. Memory is in GB.
type ComputeNode struct {
	Node        string  `json:"node"`
	VCPUs       int     `json:"vcpus"`
	Memory      int     `json:"memory"`
	GPUType     *string `json:"gpu_type"`
	GPUCount    int     `json:"gpu_count"`
	HourlyPrice float64 `json:"hourly_price"`
}

func fetchComputeNodes(client *http.Client, baseURL, idToken string) ([]ComputeNode, error) {
	body, err := jobAPIDo(client, http.MethodGet, baseURL+"/api/v1/config/compute/nodes", idToken, "", nil)
	if err != nil {
		return nil, err
	}
	var nodes []ComputeNode
	if err := json.Unmarshal(body, &nodes); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return nodes, nil
}

// sortComputeNodes orders nodes CPU-only first, then by size (vCPUs, memory,
// GPUs), then price and name: smallest first.
func sortComputeNodes(nodes []ComputeNode) {
	sort.SliceStable(nodes, func(i, j int) bool {
		a, b := nodes[i], nodes[j]
		if (a.GPUCount > 0) != (b.GPUCount > 0) {
			return a.GPUCount == 0
		}
		if a.VCPUs != b.VCPUs {
			return a.VCPUs < b.VCPUs
		}
		if a.Memory != b.Memory {
			return a.Memory < b.Memory
		}
		if a.GPUCount != b.GPUCount {
			return a.GPUCount < b.GPUCount
		}
		if a.HourlyPrice != b.HourlyPrice {
			return a.HourlyPrice < b.HourlyPrice
		}
		return a.Node < b.Node
	})
}

// selectComputeNode is the node a job runs on when --node is not given: the
// smallest CPU-only node with at least cpu vCPUs and memory GB (0: any).
func selectComputeNode(nodes []ComputeNode, cpu, memory int) (ComputeNode, error) {
	sorted := append([]ComputeNode(nil), nodes...)
	sortComputeNodes(sorted)
	for _, n := range sorted {
		if n.GPUCount == 0 && n.VCPUs >= cpu && n.Memory >= memory {
			return n, nil
		}
	}
	if cpu > 0 || memory > 0 {
		return ComputeNode{}, fmt.Errorf("no CPU-only node has at least %d vCPUs and %d GB of memory (see 'jh job nodes')", cpu, memory)
	}
	return ComputeNode{}, errors.New("this server offers no CPU-only node; choose one with --node (see 'jh job nodes')")
}

func nodeSummary(n ComputeNode) string {
	s := fmt.Sprintf("%d vCPU, %d GB", n.VCPUs, n.Memory)
	if n.GPUCount > 0 {
		gpu := "GPU"
		if n.GPUType != nil && *n.GPUType != "" {
			gpu = *n.GPUType
		}
		s += fmt.Sprintf(", %d× %s", n.GPUCount, gpu)
	}
	return s
}

func printComputeNodes(w io.Writer, nodes []ComputeNode) {
	if len(nodes) == 0 {
		fmt.Fprintln(w, "No nodes available.")
		return
	}
	sorted := append([]ComputeNode(nil), nodes...)
	sortComputeNodes(sorted)
	nameWidth := len("NODE")
	for _, n := range sorted {
		nameWidth = max(nameWidth, utf8.RuneCountInString(n.Node))
	}
	fmt.Fprintln(w, bold(pad("NODE", nameWidth)+"  "+padLeft("VCPUS", 5)+"  "+padLeft("MEMORY", 8)+"  "+pad("GPU", 16)+"  "+padLeft("$/HOUR", 8)))
	for _, n := range sorted {
		gpu := "-"
		if n.GPUCount > 0 {
			gpu = strconv.Itoa(n.GPUCount) + "×"
			if n.GPUType != nil && *n.GPUType != "" {
				gpu += " " + *n.GPUType
			}
		}
		fmt.Fprintln(w, pad(n.Node, nameWidth)+"  "+padLeft(strconv.Itoa(n.VCPUs), 5)+"  "+padLeft(strconv.Itoa(n.Memory)+" GB", 8)+"  "+pad(truncate(gpu, 16), 16)+"  "+padLeft(fmt.Sprintf("%.2f", n.HourlyPrice), 8))
	}
}

// JobImage is one image of a product's image group.
type JobImage struct {
	Key         string `json:"image_key_name"`
	Tag         string `json:"image_tag,omitempty"`
	SHA         string `json:"image_sha,omitempty"`
	Type        string `json:"type"`
	DisplayName string `json:"display_name"`
}

// JobProduct is the subset of GET /api/v1/config/compute/products the CLI uses.
type JobProduct struct {
	Name        string     `json:"product_name"`
	DisplayName string     `json:"display_name"`
	Images      []JobImage `json:"images"`
}

func fetchJobProducts(client *http.Client, baseURL, idToken string) ([]JobProduct, error) {
	body, err := jobAPIDo(client, http.MethodGet, baseURL+"/api/v1/config/compute/products", idToken, "", nil)
	if err != nil {
		return nil, err
	}
	var products []JobProduct
	if err := json.Unmarshal(body, &products); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return products, nil
}

func findJobProduct(products []JobProduct, name string) (JobProduct, error) {
	for _, p := range products {
		if p.Name == name {
			return p, nil
		}
	}
	names := make([]string, len(products))
	for i, p := range products {
		names[i] = p.Name
	}
	sort.Strings(names)
	return JobProduct{}, fmt.Errorf("product %q is not available to you (available: %s)", name, strings.Join(names, ", "))
}

// imageRef is how --image names an image: KEY, or KEY:TAG for a non-default one.
func imageRef(img JobImage) string {
	if img.Tag == "" {
		return img.Key
	}
	return img.Key + ":" + img.Tag
}

func isGPUImage(img JobImage) bool { return strings.HasSuffix(img.Type, "-gpu") }

// resolveJobImage finds the image --image names among a product's: KEY:TAG,
// KEY (the image without a tag, the key's default), or a display name such
// as "Julia 1.10" (case-insensitive). A display name shared by a CPU and a
// GPU image picks the one matching gpu, whether the node has GPUs.
func resolveJobImage(images []JobImage, spec string, gpu bool) (JobImage, error) {
	key, tag, hasTag := strings.Cut(spec, ":")
	var byName []JobImage
	for _, img := range images {
		if img.Key == key && (img.Tag == tag || !hasTag && img.Tag == "") {
			return img, nil
		}
		if strings.EqualFold(img.DisplayName, spec) {
			byName = append(byName, img)
		}
	}
	if len(byName) > 1 {
		var kind []JobImage
		for _, img := range byName {
			if isGPUImage(img) == gpu {
				kind = append(kind, img)
			}
		}
		if len(kind) > 0 {
			byName = kind
		}
	}
	switch len(byName) {
	case 1:
		return byName[0], nil
	case 0:
		refs := make([]string, len(images))
		for i, img := range images {
			refs[i] = imageRef(img)
		}
		return JobImage{}, fmt.Errorf("no image %q (available: %s; see 'jh job images')", spec, strings.Join(refs, ", "))
	}
	refs := make([]string, len(byName))
	for i, img := range byName {
		refs[i] = imageRef(img)
	}
	return JobImage{}, fmt.Errorf("image %q is ambiguous: use one of %s", spec, strings.Join(refs, ", "))
}

func printJobImages(w io.Writer, product JobProduct) {
	fmt.Fprintf(w, "%s %s\n", bold(product.DisplayName), dim("("+product.Name+")"))
	if len(product.Images) == 0 {
		fmt.Fprintln(w, "No images.")
		return
	}
	keyWidth := len("IMAGE")
	for _, img := range product.Images {
		keyWidth = max(keyWidth, utf8.RuneCountInString(imageRef(img)))
	}
	fmt.Fprintln(w, bold(pad("IMAGE", keyWidth)+"  "+pad("TYPE", 10)+"  DESCRIPTION"))
	for _, img := range product.Images {
		fmt.Fprintln(w, pad(imageRef(img), keyWidth)+"  "+pad(img.Type, 10)+"  "+img.DisplayName)
	}
}

// jobEnvFiles are the Julia environment files sent with a job. Nil fields
// are not sent.
type jobEnvFiles struct {
	Dir           string
	ProjectPath   string
	ManifestPath  string
	ArtifactsPath string
	Project       []byte
	Manifest      []byte
	Artifacts     []byte
}

// artifactsNames are the artifact file names Pkg recognizes, in precedence order.
var artifactsNames = []string{"JuliaArtifacts.toml", "Artifacts.toml"}

// resolveJobEnvironment reads the Julia environment a job runs in. projectArg
// is a project directory or project file. The manifest and artifacts files
// are picked up from the project file's directory. A directory without a
// project file is an error: Pkg would not treat it as an environment either.
func resolveJobEnvironment(projectArg string) (*jobEnvFiles, error) {
	info, err := os.Stat(projectArg)
	if err != nil {
		return nil, fmt.Errorf("cannot read project %q: %w", projectArg, err)
	}
	env := &jobEnvFiles{}
	if info.IsDir() {
		env.Dir = projectArg
		env.ProjectPath = findProjectFile(projectArg)
		if env.ProjectPath == "" {
			return nil, fmt.Errorf("no Project.toml or JuliaProject.toml in %s", projectArg)
		}
	} else {
		env.Dir = filepath.Dir(projectArg)
		env.ProjectPath = projectArg
	}
	if env.Project, err = os.ReadFile(env.ProjectPath); err != nil {
		return nil, fmt.Errorf("cannot read project file: %w", err)
	}

	cands, err := findManifestCandidates(env.Dir)
	if err != nil {
		return nil, err
	}
	if len(cands) > 0 {
		// Pkg's precedence order; the job's Julia version is not known here,
		// so a versioned manifest is taken as it is.
		env.ManifestPath = cands[0].path
		if env.Manifest, err = os.ReadFile(env.ManifestPath); err != nil {
			return nil, fmt.Errorf("cannot read manifest: %w", err)
		}
	}
	for _, name := range artifactsNames {
		p := filepath.Join(env.Dir, name)
		if data, err := os.ReadFile(p); err == nil {
			env.ArtifactsPath, env.Artifacts = p, data
			break
		}
	}
	return env, nil
}

// defaultJobProjectDir is the environment a job uses when --project is not
// given: the script's directory, or the current directory for -e and stdin
// code, when that directory holds a project file; "" otherwise.
func defaultJobProjectDir(scriptPath string) string {
	dir := "."
	if scriptPath != "" && scriptPath != "-" {
		dir = filepath.Dir(scriptPath)
	}
	if findProjectFile(dir) == "" {
		return ""
	}
	return dir
}

// parseJobEnv turns --env values into the job's environment variables.
// KEY=VALUE sets a value; a bare KEY copies it from the local environment.
func parseJobEnv(values []string, lookup func(string) (string, bool)) (map[string]string, error) {
	env := map[string]string{}
	for _, v := range values {
		key, value, hasValue := strings.Cut(v, "=")
		if key == "" {
			return nil, fmt.Errorf("invalid --env %q: want KEY=VALUE or KEY", v)
		}
		if key == jobCommandEnv || key == jobResultsEnv {
			return nil, fmt.Errorf("--env %s: reserved for jh's job driver", key)
		}
		if !hasValue {
			local, ok := lookup(key)
			if !ok {
				return nil, fmt.Errorf("--env %s: %s is not set in the local environment", key, key)
			}
			value = local
		}
		env[key] = value
	}
	return env, nil
}

// jobTimeLimit is a parsed --time-limit: Hours > 0 for a time limit,
// Unlimited for none, the zero value for the server's default (2 hours).
type jobTimeLimit struct {
	Hours     int
	Unlimited bool
}

// parseJobTimeLimit accepts "unlimited", a whole number of hours ("4") or a
// whole number of hours with an h suffix ("4h"). JuliaHub limits jobs in
// whole hours.
func parseJobTimeLimit(s string) (jobTimeLimit, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return jobTimeLimit{}, nil
	}
	if s == "unlimited" || s == "none" {
		return jobTimeLimit{Unlimited: true}, nil
	}
	n, err := strconv.Atoi(strings.TrimSuffix(s, "h"))
	if err != nil || n < 1 {
		return jobTimeLimit{}, fmt.Errorf("invalid time limit %q: want a whole number of hours (e.g. 4 or 4h) or 'unlimited'", s)
	}
	return jobTimeLimit{Hours: n}, nil
}

// --- Bundles ----------------------------------------------------------------

// appbundleMaxSize is the largest bundle the submission can describe: the
// server reads file_appbundle_info.file_size as an int32.
const appbundleMaxSize = math.MaxInt32

// appbundleDirName is the single top-level directory of the bundle tarball.
// JuliaHub unpacks it and starts the job inside it (JuliaHub 6.2 and older
// start in its parent; the driver handles both).
const appbundleDirName = "appbundle"

// bundleSkipDirs are directories never bundled.
var bundleSkipDirs = map[string]bool{".git": true}

// buildAppbundle writes dir as a tarball with a single top-level directory to
// w. Symlinks to files are followed; symlinks to directories are skipped
// (they could loop), as are .git directories. If dir has no project file or
// manifest at its root, empty ones are added to the bundle (not to dir):
// JuliaHub instantiates the bundle as a Julia environment before running the
// job.
func buildAppbundle(dir string, w io.Writer, warn io.Writer) error {
	tw := tar.NewWriter(w)
	seen := map[string]bool{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return writeTarDir(tw, appbundleDirName+"/")
		}
		if d.IsDir() && bundleSkipDirs[d.Name()] {
			return filepath.SkipDir
		}
		name := path.Join(appbundleDirName, filepath.ToSlash(rel))
		info, err := os.Stat(p) // follows symlinks
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		switch {
		case info.IsDir() && d.Type()&fs.ModeSymlink != 0:
			fmt.Fprintf(warn, "Skipping %s: symlink to a directory\n", p)
			return nil
		case info.IsDir():
			return writeTarDir(tw, name+"/")
		case !info.Mode().IsRegular():
			fmt.Fprintf(warn, "Skipping %s: not a regular file\n", p)
			return nil
		}
		if !strings.ContainsRune(rel, filepath.Separator) {
			seen[rel] = true
		}
		return writeTarFile(tw, p, name, info)
	})
	if err != nil {
		return err
	}
	// An empty environment: Pkg accepts an empty Project.toml, and a manifest
	// with no dependencies.
	generated := []struct{ name, content string }{
		{"Project.toml", ""},
		{"Manifest.toml", "# This file is machine-generated - editing it directly is not advised\n\nmanifest_format = \"2.0\"\n\n[deps]\n"},
	}
	for _, f := range generated {
		if seen[f.name] || seen["Julia"+f.name] {
			continue
		}
		hdr := &tar.Header{Name: path.Join(appbundleDirName, f.name), Mode: 0o644, Size: int64(len(f.content)), ModTime: time.Now(), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := io.WriteString(tw, f.content); err != nil {
			return err
		}
	}
	return tw.Close()
}

func writeTarDir(tw *tar.Writer, name string) error {
	return tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Typeflag: tar.TypeDir, ModTime: time.Now()})
}

func writeTarFile(tw *tar.Writer, src, name string, info fs.FileInfo) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	hdr := &tar.Header{
		Name:     name,
		Mode:     int64(info.Mode().Perm()),
		Size:     info.Size(),
		ModTime:  info.ModTime(),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	n, err := io.Copy(tw, f)
	if err != nil {
		return fmt.Errorf("%s: %w", src, err)
	}
	if n != info.Size() {
		return fmt.Errorf("%s changed size while being bundled", src)
	}
	return nil
}

// appbundleFile is a bundle tarball written to a temporary file.
type appbundleFile struct {
	Path string
	Size int64
	Hash string // hex SHA-256
}

func createAppbundleFile(dir string, warn io.Writer) (*appbundleFile, error) {
	tmp, err := os.CreateTemp("", "jh-appbundle-*.tar")
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	if err := buildAppbundle(dir, io.MultiWriter(tmp, h), warn); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, fmt.Errorf("failed to bundle %s: %w", dir, err)
	}
	size, err := tmp.Seek(0, io.SeekCurrent)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	if size > appbundleMaxSize {
		os.Remove(tmp.Name())
		return nil, fmt.Errorf("the bundle of %s is %s, larger than the %s JuliaHub accepts", dir, formatBytes(size), formatBytes(appbundleMaxSize))
	}
	return &appbundleFile{Path: tmp.Name(), Size: size, Hash: hex.EncodeToString(h.Sum(nil))}, nil
}

// appbundleUploadInfo is the file_appbundle_info of the submission, and the
// query of /jobs/appbundle_upload_url.
type appbundleUploadInfo struct {
	FileType string `json:"file_type"`
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
	Hash     string `json:"hash"`
	HashAlg  string `json:"hash_alg"`
}

func newAppbundleUploadInfo(b *appbundleFile) appbundleUploadInfo {
	return appbundleUploadInfo{FileType: "input", FileName: "appbundle.tar", FileSize: b.Size, Hash: b.Hash, HashAlg: "sha2_256"}
}

// newUploadHTTPClient is for presigned uploads: no overall timeout, which
// would cut off large uploads over slow links; only waiting for the
// response headers after the body is sent is bounded.
func newUploadHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 5 * time.Minute
	return &http.Client{Transport: transport}
}

// uploadAppbundle uploads a bundle to the presigned URL JuliaHub hands out
// for it and returns the file_appbundle_info to submit with.
func uploadAppbundle(apiClient, uploadClient *http.Client, baseURL, idToken string, b *appbundleFile) (appbundleUploadInfo, error) {
	info := newAppbundleUploadInfo(b)
	q := url.Values{}
	q.Set("file_type", info.FileType)
	q.Set("file_name", info.FileName)
	q.Set("file_size", strconv.FormatInt(info.FileSize, 10))
	q.Set("hash", info.Hash)
	q.Set("hash_alg", info.HashAlg)
	body, err := jobAPIDo(apiClient, http.MethodGet, baseURL+"/jobs/appbundle_upload_url?"+q.Encode(), idToken, "", nil)
	if err != nil {
		return info, fmt.Errorf("failed to get an upload URL: %w", err)
	}
	// The legacy envelope: {"success": true, "message": {"upload_url": ...}}.
	var r struct {
		Success bool            `json:"success"`
		Message json.RawMessage `json:"message"`
	}
	var msg struct {
		UploadURL string `json:"upload_url"`
	}
	if json.Unmarshal(body, &r) != nil || !r.Success || json.Unmarshal(r.Message, &msg) != nil || msg.UploadURL == "" {
		return info, fmt.Errorf("failed to get an upload URL: %s", strings.TrimSpace(string(body)))
	}

	f, err := os.Open(b.Path)
	if err != nil {
		return info, err
	}
	defer f.Close()
	req, err := http.NewRequest(http.MethodPut, msg.UploadURL, f)
	if err != nil {
		return info, fmt.Errorf("failed to create upload request: %w", err)
	}
	req.ContentLength = b.Size
	// Presigned: the signature is in the URL, so no Authorization header.
	resp, err := uploadClient.Do(req)
	if err != nil {
		return info, fmt.Errorf("failed to upload the bundle: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return info, fmt.Errorf("failed to upload the bundle (status %d): %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	return info, nil
}

// --- Driver -----------------------------------------------------------------

// Environment variables the driver reads. The shell command travels in the
// job's environment rather than in Julia source, so it needs no escaping.
const (
	jobCommandEnv = "JH_JOB_COMMAND"
	jobResultsEnv = "JH_JOB_RESULTS"
)

// jobDriver describes the Julia program jh generates when a job needs more
// than the user's code: a bundle to start in, a shell command, or a results
// path.
type jobDriver struct {
	Bundle  bool
	Command bool   // run $JH_JOB_COMMAND with sh -c
	Results bool   // $JH_JOB_RESULTS becomes RESULTS_FILE
	Include string // script to include, relative to the bundle (slash-separated)
	Code    []byte // otherwise: code to evaluate in Main
	Name    string // file name Code is reported under in errors and backtraces
}

// juliaString renders s as a Julia string literal.
func juliaString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\' || r == '$':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// jobDriverCode is the Julia program a job with a driver runs. It starts in
// the bundle directory, makes the results path absolute and sets
// RESULTS_FILE (which JuliaHub uploads when the job ends) to it, runs the
// command or the user's code, and finally packs a results directory into a
// .tar.gz, since only a file can be uploaded. The user's code is passed
// hex-encoded so it needs no escaping, and evaluated with include_string so
// errors still point at its own lines.
func jobDriverCode(d jobDriver) string {
	var b strings.Builder
	b.WriteString("# Generated by jh job submit\n")
	if d.Bundle {
		b.WriteString(`if isdir("` + appbundleDirName + `") && !isfile("Project.toml") && !isfile("JuliaProject.toml")
    cd("` + appbundleDirName + `") # JuliaHub 6.2 and older start in the bundle's parent
end
`)
	}
	if d.Results {
		b.WriteString(`const __jh_results = rstrip(abspath(ENV["` + jobResultsEnv + `"]), '/')
ENV["RESULTS_FILE"] = __jh_results
`)
	}
	b.WriteString("try\n")
	switch {
	case d.Command:
		b.WriteString(`    let p = run(ignorestatus(Cmd(["sh", "-c", ENV["` + jobCommandEnv + `"]])))
        success(p) || error("command failed with exit code $(p.exitcode)")
    end
`)
	case d.Include != "":
		b.WriteString("    include(joinpath(pwd(), " + juliaString(d.Include) + "))\n")
	default:
		b.WriteString("    include_string(Main, String(hex2bytes(\"" + hex.EncodeToString(d.Code) + "\")), " + juliaString(d.Name) + ")\n")
	}
	b.WriteString("finally\n")
	if d.Results {
		// Current runtimes upload what is in $JULIAHUB_RESULTS_UPLOAD_DIR
		// and ignore RESULTS_FILE set from inside the job; older ones
		// upload the RESULTS_FILE path instead.
		b.WriteString(`    try
        local upload_dir = get(ENV, "JULIAHUB_RESULTS_UPLOAD_DIR", "")
        isempty(upload_dir) || mkpath(upload_dir)
        if isdir(__jh_results)
            local name = basename(__jh_results) * ".tar.gz"
            local tarball = isempty(upload_dir) ? __jh_results * ".tar.gz" : joinpath(upload_dir, name)
            run(` + "`tar -czf $tarball -C $(dirname(__jh_results)) $(basename(__jh_results))`" + `)
            isempty(upload_dir) && (ENV["RESULTS_FILE"] = tarball)
        elseif isfile(__jh_results)
            isempty(upload_dir) || cp(__jh_results, joinpath(upload_dir, basename(__jh_results)); force = true)
        else
            @warn "The results path does not exist; no result file is uploaded" path = __jh_results
        end
    catch err
        @error "Could not save the results" exception = err
    end
`)
	} else {
		b.WriteString("    nothing\n")
	}
	b.WriteString("end\n")
	return b.String()
}

// --- Submission ---------------------------------------------------------------

// JobSubmission is everything POST /api/v1/jobs is sent for a batch job.
type JobSubmission struct {
	Product      string
	Node         string
	NodeCount    int // 0: server default (1)
	ProcsPerNode int // 0: server default (1)
	Elastic      bool
	MinWorkers   *int   // nil: wait for every worker
	Image        string // image_name; empty: the node's base image
	ImageTag     string // image_tag; empty: the image's default
	Alias        string
	Env          map[string]string
	TimeLimit    jobTimeLimit
	Code         []byte
	Environment  *jobEnvFiles         // nil: the image's default environment
	Appbundle    *appbundleUploadInfo // an uploaded bundle, or nil
}

// buildSubmitForm encodes a submission as the multipart/form-data body
// POST /api/v1/jobs takes. Fields left at their zero value are omitted so the
// server's defaults apply.
func buildSubmitForm(sub JobSubmission) (*bytes.Buffer, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	field := func(name, value string) {
		if value != "" {
			_ = w.WriteField(name, value)
		}
	}
	file := func(name, fileName string, data []byte) error {
		if data == nil {
			return nil
		}
		part, err := w.CreateFormFile(name, fileName)
		if err != nil {
			return err
		}
		_, err = part.Write(data)
		return err
	}

	field("product_name", sub.Product)
	field("node", sub.Node)
	if sub.NodeCount > 0 {
		field("node_count", strconv.Itoa(sub.NodeCount))
	}
	if sub.ProcsPerNode > 0 {
		field("node_process_per", strconv.Itoa(sub.ProcsPerNode))
	}
	if sub.Elastic {
		field("worker_pool_type", "elastic")
	}
	if sub.MinWorkers != nil {
		field("worker_min_required", strconv.Itoa(*sub.MinWorkers))
	}
	field("image_name", sub.Image)
	field("image_tag", sub.ImageTag)
	field("alias", sub.Alias)
	if len(sub.Env) > 0 {
		envJSON, err := json.Marshal(sub.Env)
		if err != nil {
			return nil, "", err
		}
		field("user_env", string(envJSON))
	}
	switch {
	case sub.TimeLimit.Unlimited:
		field("limit_type", "unlimited")
	case sub.TimeLimit.Hours > 0:
		field("limit_type", "time")
		field("limit_value", strconv.Itoa(sub.TimeLimit.Hours))
	}
	if sub.Appbundle != nil {
		infoJSON, err := json.Marshal(sub.Appbundle)
		if err != nil {
			return nil, "", err
		}
		field("file_appbundle_info", string(infoJSON))
	}
	if err := file("file_code_jl", "code.jl", sub.Code); err != nil {
		return nil, "", err
	}
	if env := sub.Environment; env != nil {
		for _, f := range []struct {
			field, name string
			data        []byte
		}{
			{"file_project_toml", "Project.toml", env.Project},
			{"file_manifest_toml", "Manifest.toml", env.Manifest},
			{"file_artifacts_toml", "Artifacts.toml", env.Artifacts},
		} {
			if err := file(f.field, f.name, f.data); err != nil {
				return nil, "", err
			}
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return &buf, w.FormDataContentType(), nil
}

// submitJob submits a job and returns its name (e.g. jr-abc123).
func submitJob(client *http.Client, baseURL, idToken string, sub JobSubmission) (string, error) {
	body, contentType, err := buildSubmitForm(sub)
	if err != nil {
		return "", fmt.Errorf("failed to encode the submission: %w", err)
	}
	respBody, err := jobAPIDo(client, http.MethodPost, baseURL+"/api/v1/jobs", idToken, contentType, body)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Message string `json:"message"`
		Data    struct {
			JobName string `json:"job_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	if parsed.Data.JobName == "" {
		return "", fmt.Errorf("the server accepted the job but returned no job name: %s", strings.TrimSpace(string(respBody)))
	}
	return parsed.Data.JobName, nil
}

// jobSubmitOptions are the inputs of `jh job submit`.
type jobSubmitOptions struct {
	Script        string // script path, "-" for stdin
	Expr          string // -e code
	Command       string // shell command
	Bundle        string // directory to upload and run in
	Results       string // path whose file (or packed directory) becomes the job's result
	Project       string // project directory or file; "" for the default
	NoProject     bool
	Product       string
	Node          string
	CPU           int
	Memory        int
	NodeCount     int
	ProcsPerNode  int
	Elastic       bool
	MinWorkers    int
	MinWorkersSet bool
	Image         string
	Alias         string
	Env           []string
	TimeLimit     string
	Wait          bool
	Follow        bool
	JSON          bool
	Timestamps    bool
}

// readJobCode returns the Julia code a job runs: the -e expression, the
// script, or stdin for "-". It is nil for a shell command.
func readJobCode(opts jobSubmitOptions, stdin io.Reader) ([]byte, error) {
	given := 0
	for _, s := range []string{opts.Script, opts.Expr, opts.Command} {
		if s != "" {
			given++
		}
	}
	switch {
	case given > 1:
		return nil, errors.New("give only one of a script, -e and --command")
	case given == 0:
		return nil, errors.New("give a script to run (or - to read it from stdin), code with -e, or a shell command with --command")
	case opts.Command != "":
		return nil, nil
	case opts.Expr != "":
		return []byte(opts.Expr), nil
	}
	var code []byte
	var err error
	if opts.Script == "-" {
		code, err = io.ReadAll(stdin)
	} else {
		code, err = os.ReadFile(opts.Script)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read the script: %w", err)
	}
	if len(bytes.TrimSpace(code)) == 0 {
		return nil, errors.New("the script is empty")
	}
	return code, nil
}

// scriptInBundle returns the script's path relative to the bundle directory,
// slash-separated, or "" when it is not inside it.
func scriptInBundle(script, bundle string) string {
	if script == "" || script == "-" {
		return ""
	}
	absScript, err1 := filepath.Abs(script)
	absBundle, err2 := filepath.Abs(bundle)
	if err1 != nil || err2 != nil {
		return ""
	}
	rel, err := filepath.Rel(absBundle, absScript)
	if err != nil || !filepath.IsLocal(rel) {
		return ""
	}
	return filepath.ToSlash(rel)
}

// prepareJobSubmission validates the options and reads the job's files; it
// does not talk to the server. Node is left empty when --node was not given,
// and the bundle (if any) is still to be uploaded.
func prepareJobSubmission(opts jobSubmitOptions, stdin io.Reader, lookupEnv func(string) (string, bool)) (JobSubmission, error) {
	switch {
	case opts.NoProject && opts.Project != "":
		return JobSubmission{}, errors.New("--project and --no-project are mutually exclusive")
	case opts.Bundle != "" && (opts.Project != "" || opts.NoProject):
		return JobSubmission{}, errors.New("--bundle sends the bundle's own Project.toml and Manifest.toml; drop --project / --no-project")
	case opts.Node != "" && (opts.CPU != 0 || opts.Memory != 0):
		return JobSubmission{}, errors.New("--node and --cpu/--memory are mutually exclusive")
	case opts.NodeCount < 0 || opts.ProcsPerNode < 0 || opts.CPU < 0 || opts.Memory < 0 || (opts.MinWorkersSet && opts.MinWorkers < 0):
		return JobSubmission{}, errors.New("--nodes, --procs-per-node, --min-workers, --cpu and --memory must not be negative")
	case opts.Results != "" && strings.TrimSpace(opts.Results) == "":
		return JobSubmission{}, errors.New("--results must not be blank")
	}
	if opts.Bundle != "" {
		info, err := os.Stat(opts.Bundle)
		if err != nil {
			return JobSubmission{}, fmt.Errorf("cannot read the bundle directory: %w", err)
		}
		if !info.IsDir() {
			return JobSubmission{}, fmt.Errorf("--bundle %s is not a directory", opts.Bundle)
		}
	}
	code, err := readJobCode(opts, stdin)
	if err != nil {
		return JobSubmission{}, err
	}
	env, err := parseJobEnv(opts.Env, lookupEnv)
	if err != nil {
		return JobSubmission{}, err
	}
	limit, err := parseJobTimeLimit(opts.TimeLimit)
	if err != nil {
		return JobSubmission{}, err
	}
	sub := JobSubmission{
		Product:      opts.Product,
		Node:         opts.Node,
		NodeCount:    opts.NodeCount,
		ProcsPerNode: opts.ProcsPerNode,
		Elastic:      opts.Elastic,
		Image:        opts.Image,
		Alias:        opts.Alias,
		Env:          env,
		TimeLimit:    limit,
		Code:         code,
	}
	if sub.Product == "" {
		sub.Product = defaultJobProduct
	}
	if opts.MinWorkersSet {
		n := opts.MinWorkers
		sub.MinWorkers = &n
	}
	if sub.Alias == "" {
		switch {
		case opts.Script != "" && opts.Script != "-":
			sub.Alias = filepath.Base(opts.Script)
		case opts.Bundle != "":
			if abs, err := filepath.Abs(opts.Bundle); err == nil {
				sub.Alias = filepath.Base(abs)
			}
		}
	}

	if opts.Bundle != "" || opts.Command != "" || opts.Results != "" {
		d := jobDriver{Bundle: opts.Bundle != "", Command: opts.Command != "", Results: opts.Results != "", Code: code}
		switch {
		case opts.Script == "-":
			d.Name = "stdin"
		case opts.Script != "":
			d.Name = filepath.Base(opts.Script)
			if opts.Bundle != "" {
				d.Include = scriptInBundle(opts.Script, opts.Bundle)
			}
		default:
			d.Name = "expr"
		}
		if d.Command {
			sub.Env[jobCommandEnv] = opts.Command
		}
		if d.Results {
			sub.Env[jobResultsEnv] = opts.Results
		}
		sub.Code = []byte(jobDriverCode(d))
	}

	if opts.Bundle == "" && !opts.NoProject {
		dir := opts.Project
		if dir == "" {
			dir = defaultJobProjectDir(opts.Script)
		}
		if dir != "" {
			if sub.Environment, err = resolveJobEnvironment(dir); err != nil {
				return JobSubmission{}, err
			}
		}
	}
	return sub, nil
}

// describeJobEnvironment is the stderr note naming the environment files sent.
func describeJobEnvironment(env *jobEnvFiles) string {
	if env == nil {
		return "Environment: the image's default (no Project.toml found; use --project to send one)"
	}
	names := []string{filepath.Base(env.ProjectPath)}
	for _, p := range []string{env.ManifestPath, env.ArtifactsPath} {
		if p != "" {
			names = append(names, filepath.Base(p))
		}
	}
	dir := env.Dir
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	note := "Environment: " + strings.Join(names, ", ") + " from " + dir
	if env.ManifestPath == "" {
		note += " (no manifest: the job resolves the dependencies itself)"
	}
	return note
}

// errJobNotCompleted is returned when a waited-for job ends in any status but
// Completed.
type errJobNotCompleted struct {
	JobName string
	Status  string
	Reason  string
}

func (e *errJobNotCompleted) Error() string {
	msg := fmt.Sprintf("job %s ended with status %s", e.JobName, e.Status)
	if e.Reason != "" {
		msg += ": " + e.Reason
	}
	return msg
}

func jobOutcome(info *JobInfo) error {
	if info.Status == "Completed" {
		return nil
	}
	return &errJobNotCompleted{JobName: info.JobName, Status: info.Status, Reason: info.FailureReason}
}

// runJobSubmit is `jh job submit`.
func runJobSubmit(server string, opts jobSubmitOptions) error {
	if opts.Follow && opts.JSON {
		return errors.New("--follow and --json are mutually exclusive")
	}
	sub, err := prepareJobSubmission(opts, os.Stdin, os.LookupEnv)
	if err != nil {
		return err
	}
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}
	baseURL := jobBaseURL(server)
	client := &http.Client{Timeout: 3 * time.Minute}

	gpu := false
	if sub.Node == "" {
		nodes, err := fetchComputeNodes(client, baseURL, token.IDToken)
		if err != nil {
			return fmt.Errorf("failed to list nodes (choose one with --node): %w", err)
		}
		node, err := selectComputeNode(nodes, opts.CPU, opts.Memory)
		if err != nil {
			return err
		}
		sub.Node = node.Node
		fmt.Fprintf(os.Stderr, "Node: %s (%s)\n", node.Node, nodeSummary(node))
	} else if sub.Image != "" {
		// Only to choose between a display name's CPU and GPU images.
		if nodes, err := fetchComputeNodes(client, baseURL, token.IDToken); err == nil {
			for _, n := range nodes {
				if n.Node == sub.Node {
					gpu = n.GPUCount > 0
				}
			}
		}
	}

	if sub.Image != "" {
		products, err := fetchJobProducts(client, baseURL, token.IDToken)
		if err != nil {
			return fmt.Errorf("failed to list images: %w", err)
		}
		product, err := findJobProduct(products, sub.Product)
		if err != nil {
			return err
		}
		img, err := resolveJobImage(product.Images, sub.Image, gpu)
		if err != nil {
			return err
		}
		sub.Image, sub.ImageTag = img.Key, img.Tag
		fmt.Fprintf(os.Stderr, "Image: %s (%s)\n", imageRef(img), img.DisplayName)
	}

	if opts.Bundle != "" {
		bundle, err := createAppbundleFile(opts.Bundle, os.Stderr)
		if err != nil {
			return err
		}
		defer os.Remove(bundle.Path)
		fmt.Fprintf(os.Stderr, "Uploading bundle of %s (%s)\n", opts.Bundle, formatBytes(bundle.Size))
		info, err := uploadAppbundle(client, newUploadHTTPClient(), baseURL, token.IDToken, bundle)
		if err != nil {
			return err
		}
		sub.Appbundle = &info
	} else {
		fmt.Fprintln(os.Stderr, describeJobEnvironment(sub.Environment))
	}

	jobName, err := submitJob(client, baseURL, token.IDToken, sub)
	if err != nil {
		return err
	}

	if !opts.Wait && !opts.Follow {
		if opts.JSON {
			return writeJSON(os.Stdout, map[string]string{"job_name": jobName})
		}
		fmt.Printf("Submitted %s\n", bold(jobName))
		fmt.Printf("  %s%s\n", dim(pad("Status", 8)), "jh job status "+jobName)
		fmt.Printf("  %s%s\n", dim(pad("Logs", 8)), "jh job logs -f "+jobName)
		return nil
	}
	fmt.Fprintf(os.Stderr, "Submitted %s\n", jobName)
	return watchJob(server, jobName, opts.Follow, opts.JSON, opts.Timestamps)
}

func showJobNodes(server string, asJSON bool) error {
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	nodes, err := fetchComputeNodes(client, jobBaseURL(server), token.IDToken)
	if err != nil {
		return err
	}
	sortComputeNodes(nodes)
	if asJSON {
		if nodes == nil {
			nodes = []ComputeNode{}
		}
		return writeJSON(os.Stdout, map[string][]ComputeNode{"nodes": nodes})
	}
	printComputeNodes(os.Stdout, nodes)
	return nil
}

func showJobImages(server, product string, asJSON bool) error {
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	products, err := fetchJobProducts(client, jobBaseURL(server), token.IDToken)
	if err != nil {
		return err
	}
	p, err := findJobProduct(products, product)
	if err != nil {
		return err
	}
	if asJSON {
		if p.Images == nil {
			p.Images = []JobImage{}
		}
		return writeJSON(os.Stdout, p)
	}
	printJobImages(os.Stdout, p)
	return nil
}
