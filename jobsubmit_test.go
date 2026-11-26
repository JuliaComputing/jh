package main

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/spf13/pflag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func strPtr(s string) *string { return &s }

func TestSelectComputeNode(t *testing.T) {
	nodes := []ComputeNode{
		{Node: "gpu1", VCPUs: 2, Memory: 8, GPUCount: 1, GPUType: strPtr("T4"), HourlyPrice: 1},
		{Node: "m6-big", VCPUs: 8, Memory: 32, HourlyPrice: 0.8},
		{Node: "m6-small-pricey", VCPUs: 2, Memory: 8, HourlyPrice: 0.3},
		{Node: "m6-small", VCPUs: 2, Memory: 8, HourlyPrice: 0.2},
		{Node: "r6", VCPUs: 4, Memory: 64, HourlyPrice: 0.6},
	}
	for _, tc := range []struct {
		cpu, memory int
		want        string
	}{
		{0, 0, "m6-small"},
		{4, 0, "r6"},
		{0, 33, "r6"},
		{5, 16, "m6-big"},
	} {
		got, err := selectComputeNode(nodes, tc.cpu, tc.memory)
		if err != nil || got.Node != tc.want {
			t.Errorf("selectComputeNode(cpu=%d, memory=%d) = %q, %v; want %q", tc.cpu, tc.memory, got.Node, err, tc.want)
		}
	}
	if nodes[0].Node != "gpu1" {
		t.Error("selectComputeNode reordered its argument")
	}
	if _, err := selectComputeNode(nodes, 16, 0); err == nil || !strings.Contains(err.Error(), "at least 16 vCPUs") {
		t.Errorf("too big: err = %v", err)
	}
	if _, err := selectComputeNode(nodes[:1], 0, 0); err == nil {
		t.Error("want an error when only GPU nodes exist")
	}
}

func TestPrintComputeNodes(t *testing.T) {
	var b strings.Builder
	printComputeNodes(&b, []ComputeNode{
		{Node: "gpu1", VCPUs: 4, Memory: 16, GPUCount: 1, GPUType: strPtr("T4"), HourlyPrice: 1.5},
		{Node: "m6", VCPUs: 2, Memory: 8, HourlyPrice: 0.25},
	})
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[1], "m6 ") || !strings.Contains(lines[2], "1× T4") || !strings.Contains(lines[2], "1.50") {
		t.Errorf("unexpected table:\n%s", b.String())
	}
}

func TestParseJobTimeLimit(t *testing.T) {
	for in, want := range map[string]jobTimeLimit{
		"":          {},
		"4":         {Hours: 4},
		"4h":        {Hours: 4},
		" 12H ":     {Hours: 12},
		"unlimited": {Unlimited: true},
		"none":      {Unlimited: true},
	} {
		got, err := parseJobTimeLimit(in)
		if err != nil || got != want {
			t.Errorf("parseJobTimeLimit(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"0", "-1", "1.5", "90m", "2d", "h"} {
		if _, err := parseJobTimeLimit(in); err == nil {
			t.Errorf("parseJobTimeLimit(%q): want an error", in)
		}
	}
}

func TestParseJobEnv(t *testing.T) {
	lookup := func(k string) (string, bool) {
		if k == "HOME_DIR" {
			return "/home/me", true
		}
		return "", false
	}
	got, err := parseJobEnv([]string{"A=1", "B=x=y", "EMPTY=", "HOME_DIR"}, lookup)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "1", "B": "x=y", "EMPTY": "", "HOME_DIR": "/home/me"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseJobEnv = %v, want %v", got, want)
	}
	for _, bad := range []string{"=1", "UNSET"} {
		if _, err := parseJobEnv([]string{bad}, lookup); err == nil {
			t.Errorf("parseJobEnv(%q): want an error", bad)
		}
	}
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResolveJobEnvironment(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"Project.toml":        "[deps]\n",
		"Manifest.toml":       "# plain\n",
		"Manifest-v1.11.toml": "# versioned\n",
		"Artifacts.toml":      "# artifacts\n",
	})
	env, err := resolveJobEnvironment(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(env.Project) != "[deps]\n" || string(env.Manifest) != "# versioned\n" || string(env.Artifacts) != "# artifacts\n" {
		t.Errorf("unexpected environment: project=%q manifest=%q artifacts=%q", env.Project, env.Manifest, env.Artifacts)
	}

	// A project file path works too.
	env, err = resolveJobEnvironment(filepath.Join(dir, "Project.toml"))
	if err != nil || env.Dir != dir {
		t.Errorf("project file: env=%+v err=%v", env, err)
	}

	// No manifest: only the project is sent.
	bare := t.TempDir()
	writeFiles(t, bare, map[string]string{"JuliaProject.toml": "name = \"X\"\n"})
	env, err = resolveJobEnvironment(bare)
	if err != nil {
		t.Fatal(err)
	}
	if env.Manifest != nil || env.Artifacts != nil || filepath.Base(env.ProjectPath) != "JuliaProject.toml" {
		t.Errorf("bare project: %+v", env)
	}

	if _, err := resolveJobEnvironment(t.TempDir()); err == nil || !strings.Contains(err.Error(), "no Project.toml") {
		t.Errorf("directory without a project: err = %v", err)
	}
}

func TestDefaultJobProjectDir(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"Project.toml": "", "run.jl": "1"})
	if got := defaultJobProjectDir(filepath.Join(dir, "run.jl")); got != dir {
		t.Errorf("script in a project: got %q, want %q", got, dir)
	}
	other := t.TempDir()
	if got := defaultJobProjectDir(filepath.Join(other, "run.jl")); got != "" {
		t.Errorf("script outside a project: got %q", got)
	}
	t.Chdir(dir)
	if got := defaultJobProjectDir(""); got != "." {
		t.Errorf("-e in a project directory: got %q", got)
	}
	if got := defaultJobProjectDir("-"); got != "." {
		t.Errorf("stdin in a project directory: got %q", got)
	}
}

func TestPrepareJobSubmission(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"Project.toml": "[deps]\n", "train.jl": "println(1)\n"})
	script := filepath.Join(dir, "train.jl")
	noEnv := func(string) (string, bool) { return "", false }

	sub, err := prepareJobSubmission(jobSubmitOptions{Script: script, Env: []string{"N=3"}, TimeLimit: "3h"}, nil, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Product != defaultJobProduct || sub.Alias != "train.jl" || string(sub.Code) != "println(1)\n" ||
		sub.Environment == nil || sub.Env["N"] != "3" || sub.TimeLimit.Hours != 3 || sub.MinWorkers != nil {
		t.Errorf("unexpected submission: %+v", sub)
	}

	sub, err = prepareJobSubmission(jobSubmitOptions{Script: script, NoProject: true, MinWorkers: 0, MinWorkersSet: true}, nil, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Environment != nil || sub.MinWorkers == nil || *sub.MinWorkers != 0 {
		t.Errorf("--no-project --min-workers=0: %+v", sub)
	}

	sub, err = prepareJobSubmission(jobSubmitOptions{Script: "-"}, strings.NewReader("1 + 1"), noEnv)
	if err != nil || string(sub.Code) != "1 + 1" || sub.Alias != "" {
		t.Errorf("stdin: sub=%+v err=%v", sub, err)
	}

	for desc, opts := range map[string]jobSubmitOptions{
		"no code":             {},
		"script and -e":       {Script: script, Expr: "1"},
		"project and none":    {Expr: "1", Project: dir, NoProject: true},
		"missing script":      {Script: filepath.Join(dir, "nope.jl")},
		"bad time limit":      {Expr: "1", TimeLimit: "soon"},
		"negative nodes":      {Expr: "1", NodeCount: -1},
		"project not a dir":   {Expr: "1", Project: filepath.Join(dir, "missing")},
		"bad env":             {Expr: "1", Env: []string{"=x"}},
		"empty stdin program": {Script: "-"},
		"command and script":  {Script: script, Command: "ls"},
		"bundle and project":  {Command: "ls", Bundle: dir, Project: dir},
		"bundle not a dir":    {Command: "ls", Bundle: script},
		"node and cpu":        {Expr: "1", Node: "m6", CPU: 2},
		"reserved env":        {Expr: "1", Env: []string{"JH_JOB_COMMAND=x"}},
	} {
		if _, err := prepareJobSubmission(opts, strings.NewReader("  \n"), noEnv); err == nil {
			t.Errorf("%s: want an error", desc)
		}
	}
}

func TestSubmitJob(t *testing.T) {
	var form map[string][]string
	files := map[string]string{}
	var gotAuth, gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath, gotMethod = r.Header.Get("Authorization"), r.URL.Path, r.Method
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse form: %v", err)
		}
		form = r.MultipartForm.Value
		for name, fhs := range r.MultipartForm.File {
			f, _ := fhs[0].Open()
			b, _ := io.ReadAll(f)
			files[name] = string(b)
		}
		w.Write([]byte(`{"message":"Job submission successful","data":{"job_name":"jr-abc123"}}`))
	}))
	defer srv.Close()

	minWorkers := 2
	sub := JobSubmission{
		Product:      "standard-batch",
		Node:         "m6",
		NodeCount:    3,
		ProcsPerNode: 4,
		Elastic:      true,
		MinWorkers:   &minWorkers,
		Image:        "julia",
		ImageTag:     "v1-1.10",
		Alias:        "train.jl",
		Env:          map[string]string{"N": "100"},
		TimeLimit:    jobTimeLimit{Hours: 4},
		Code:         []byte("println(1)"),
		Environment:  &jobEnvFiles{Project: []byte("[deps]"), Manifest: []byte("# m")},
	}
	name, err := submitJob(srv.Client(), srv.URL, "tok", sub)
	if err != nil {
		t.Fatal(err)
	}
	if name != "jr-abc123" {
		t.Errorf("job name = %q", name)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/jobs" || gotAuth != "Bearer tok" {
		t.Errorf("request %s %s auth=%q", gotMethod, gotPath, gotAuth)
	}
	wantForm := map[string][]string{
		"product_name":        {"standard-batch"},
		"node":                {"m6"},
		"node_count":          {"3"},
		"node_process_per":    {"4"},
		"worker_pool_type":    {"elastic"},
		"worker_min_required": {"2"},
		"image_name":          {"julia"},
		"image_tag":           {"v1-1.10"},
		"alias":               {"train.jl"},
		"user_env":            {`{"N":"100"}`},
		"limit_type":          {"time"},
		"limit_value":         {"4"},
	}
	if !reflect.DeepEqual(form, wantForm) {
		t.Errorf("form = %v\nwant %v", form, wantForm)
	}
	wantFiles := map[string]string{"file_code_jl": "println(1)", "file_project_toml": "[deps]", "file_manifest_toml": "# m"}
	if !reflect.DeepEqual(files, wantFiles) {
		t.Errorf("files = %v, want %v", files, wantFiles)
	}
}

func TestBuildSubmitFormDefaults(t *testing.T) {
	// Unset options are left out so the server's defaults apply.
	body, contentType, err := buildSubmitForm(JobSubmission{Product: "standard-batch", Node: "m6", Code: []byte("1"), TimeLimit: jobTimeLimit{Unlimited: true}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set("Content-Type", contentType)
	if err := req.ParseMultipartForm(1 << 20); err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"product_name": {"standard-batch"}, "node": {"m6"}, "limit_type": {"unlimited"}}
	if !reflect.DeepEqual(req.MultipartForm.Value, want) {
		t.Errorf("form = %v, want %v", req.MultipartForm.Value, want)
	}
	if len(req.MultipartForm.File) != 1 {
		t.Errorf("files = %v, want only file_code_jl", req.MultipartForm.File)
	}
}

func TestSubmitJobErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   string
	}{
		{http.StatusBadRequest, `{"message":"Node not found: m99"}`, "Node not found: m99 (status 400)"},
		{http.StatusInternalServerError, ``, "Internal Server Error (status 500)"},
		{http.StatusOK, `{"message":"ok","data":{}}`, "returned no job name"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			w.Write([]byte(tc.body))
		}))
		_, err := submitJob(srv.Client(), srv.URL, "tok", JobSubmission{Product: "p", Node: "n", Code: []byte("1")})
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("status %d: err = %v, want %q", tc.status, err, tc.want)
		}
		var apiErr *jobAPIError
		if tc.status != http.StatusOK && !errors.As(err, &apiErr) {
			t.Errorf("status %d: want a *jobAPIError, got %T", tc.status, err)
		}
	}
}

func TestResolveJobImage(t *testing.T) {
	images := []JobImage{
		{Key: "julia", Type: "base-cpu", DisplayName: "Julia 1.13"},
		{Key: "julia", Tag: "v1-1.10", Type: "option-cpu", DisplayName: "Julia 1.10"},
		{Key: "juliagpu", Type: "base-gpu", DisplayName: "Julia 1.13"},
		{Key: "juliagpu", Tag: "v1-1.10", Type: "option-gpu", DisplayName: "Julia 1.10"},
		{Key: "custom", Tag: "a", Type: "option-cpu", DisplayName: "Custom"},
		{Key: "custom", Tag: "b", Type: "option-cpu", DisplayName: "Custom"},
	}
	for _, tc := range []struct {
		spec string
		gpu  bool
		want string
	}{
		{"julia", false, "julia"},
		{"julia:v1-1.10", false, "julia:v1-1.10"},
		{"juliagpu", false, "juliagpu"},
		{"Julia 1.10", false, "julia:v1-1.10"},
		{"julia 1.10", true, "juliagpu:v1-1.10"},
		{"Julia 1.13", true, "juliagpu"},
	} {
		img, err := resolveJobImage(images, tc.spec, tc.gpu)
		if err != nil || imageRef(img) != tc.want {
			t.Errorf("resolveJobImage(%q, gpu=%v) = %q, %v; want %q", tc.spec, tc.gpu, imageRef(img), err, tc.want)
		}
	}
	for spec, want := range map[string]string{
		"julia:nope": "no image",
		"custom":     "ambiguous: use one of custom:a, custom:b", // no untagged one; matches the description
		"Custom":     "ambiguous: use one of custom:a, custom:b",
	} {
		if _, err := resolveJobImage(images, spec, false); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("resolveJobImage(%q) error = %v; want %q", spec, err, want)
		}
	}
}

func TestFindJobProduct(t *testing.T) {
	products := []JobProduct{{Name: "standard-batch"}, {Name: "pluto"}}
	if p, err := findJobProduct(products, "pluto"); err != nil || p.Name != "pluto" {
		t.Errorf("found %+v, %v", p, err)
	}
	if _, err := findJobProduct(products, "nope"); err == nil || !strings.Contains(err.Error(), "pluto, standard-batch") {
		t.Errorf("err = %v", err)
	}
}

func TestDescribeJobEnvironment(t *testing.T) {
	if got := describeJobEnvironment(nil); !strings.Contains(got, "default") {
		t.Errorf("no environment: %q", got)
	}
	got := describeJobEnvironment(&jobEnvFiles{Dir: "/p", ProjectPath: "/p/Project.toml", ManifestPath: "/p/Manifest.toml"})
	if got != "Environment: Project.toml, Manifest.toml from /p" {
		t.Errorf("got %q", got)
	}
	got = describeJobEnvironment(&jobEnvFiles{Dir: "/p", ProjectPath: "/p/Project.toml"})
	if !strings.Contains(got, "no manifest") {
		t.Errorf("got %q", got)
	}
}

func readTar(t *testing.T, data []byte) map[string]string {
	t.Helper()
	out := map[string]string{}
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		out[hdr.Name] = string(b)
	}
}

func TestBuildAppbundle(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"run.mod": "$PROB", "data.csv": "1,2"})
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, filepath.Join(dir, "sub"), map[string]string{"x.txt": "x"})
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, filepath.Join(dir, ".git"), map[string]string{"HEAD": "ref"})

	var buf bytes.Buffer
	if err := buildAppbundle(dir, &buf, io.Discard); err != nil {
		t.Fatal(err)
	}
	files := readTar(t, buf.Bytes())
	for _, name := range []string{"appbundle/", "appbundle/run.mod", "appbundle/data.csv", "appbundle/sub/", "appbundle/sub/x.txt", "appbundle/Project.toml", "appbundle/Manifest.toml"} {
		if _, ok := files[name]; !ok {
			t.Errorf("bundle lacks %s; has %v", name, files)
		}
	}
	if _, ok := files["appbundle/.git/HEAD"]; ok {
		t.Error(".git was bundled")
	}
	if files["appbundle/run.mod"] != "$PROB" {
		t.Errorf("run.mod = %q", files["appbundle/run.mod"])
	}

	// An existing environment is kept, not replaced.
	writeFiles(t, dir, map[string]string{"JuliaProject.toml": "[deps]\n"})
	buf.Reset()
	if err := buildAppbundle(dir, &buf, io.Discard); err != nil {
		t.Fatal(err)
	}
	files = readTar(t, buf.Bytes())
	if _, ok := files["appbundle/Project.toml"]; ok {
		t.Error("a Project.toml was added next to JuliaProject.toml")
	}
}

func TestUploadAppbundle(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"a.txt": "hello"})
	bundle, err := createAppbundleFile(dir, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(bundle.Path)

	var uploaded []byte
	var putAuth string
	var query map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/jobs/appbundle_upload_url":
			query = map[string]string{}
			for k := range r.URL.Query() {
				query[k] = r.URL.Query().Get(k)
			}
			fmt.Fprintf(w, `{"success":true,"message":{"upload_url":"http://%s/upload?sig=1"}}`, r.Host)
		case r.Method == http.MethodPut && r.URL.Path == "/upload":
			putAuth = r.Header.Get("Authorization")
			uploaded, _ = io.ReadAll(r.Body)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	info, err := uploadAppbundle(srv.Client(), srv.Client(), srv.URL, "tok", bundle)
	if err != nil {
		t.Fatal(err)
	}
	want := appbundleUploadInfo{FileType: "input", FileName: "appbundle.tar", FileSize: bundle.Size, Hash: bundle.Hash, HashAlg: "sha2_256"}
	if info != want {
		t.Errorf("info = %+v, want %+v", info, want)
	}
	if query["hash"] != bundle.Hash || query["file_size"] != strconv.FormatInt(bundle.Size, 10) || query["file_name"] != "appbundle.tar" {
		t.Errorf("upload URL query = %v", query)
	}
	if putAuth != "" {
		t.Errorf("the presigned upload carried Authorization %q", putAuth)
	}
	sum := sha256.Sum256(uploaded)
	if int64(len(uploaded)) != bundle.Size || hex.EncodeToString(sum[:]) != bundle.Hash {
		t.Errorf("uploaded %d bytes that do not match the bundle", len(uploaded))
	}

	// The info is submitted as file_appbundle_info.
	body, ct, err := buildSubmitForm(JobSubmission{Product: "p", Node: "n", Code: []byte("1"), Appbundle: &info})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set("Content-Type", ct)
	if err := req.ParseMultipartForm(1 << 20); err != nil {
		t.Fatal(err)
	}
	var sent appbundleUploadInfo
	if err := json.Unmarshal([]byte(req.FormValue("file_appbundle_info")), &sent); err != nil || sent != want {
		t.Errorf("file_appbundle_info = %q (%v)", req.FormValue("file_appbundle_info"), err)
	}
}

func TestUploadAppbundleErrors(t *testing.T) {
	bundle := &appbundleFile{Path: filepath.Join(t.TempDir(), "missing.tar"), Size: 1, Hash: strings.Repeat("0", 64)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"message":"Malformed hash received"}`))
	}))
	defer srv.Close()
	if _, err := uploadAppbundle(srv.Client(), srv.Client(), srv.URL, "tok", bundle); err == nil || !strings.Contains(err.Error(), "Malformed hash received") {
		t.Errorf("err = %v", err)
	}
}

func TestScriptInBundle(t *testing.T) {
	dir := t.TempDir()
	if got := scriptInBundle(filepath.Join(dir, "src", "run.jl"), dir); got != "src/run.jl" {
		t.Errorf("inside: %q", got)
	}
	if got := scriptInBundle(filepath.Join(t.TempDir(), "run.jl"), dir); got != "" {
		t.Errorf("outside: %q", got)
	}
	if got := scriptInBundle("-", dir); got != "" {
		t.Errorf("stdin: %q", got)
	}
}

func TestJuliaString(t *testing.T) {
	if got := juliaString("a\"b$c\\d\ne\x01"); got != `"a\"b\$c\\d\ne\x01"` {
		t.Errorf("juliaString = %s", got)
	}
}

func TestJobDriverCode(t *testing.T) {
	code := jobDriverCode(jobDriver{Bundle: true, Code: []byte("println(\"hi\")"), Name: "expr"})
	for _, want := range []string{`cd("appbundle")`, `include_string(Main, String(hex2bytes("` + hex.EncodeToString([]byte(`println("hi")`)) + `")), "expr")`} {
		if !strings.Contains(code, want) {
			t.Errorf("driver lacks %s:\n%s", want, code)
		}
	}
	if strings.Contains(code, "RESULTS_FILE") || strings.Contains(code, jobCommandEnv) {
		t.Errorf("driver handles results or a command it was not asked to:\n%s", code)
	}

	code = jobDriverCode(jobDriver{Command: true, Results: true})
	for _, want := range []string{`ENV["JH_JOB_COMMAND"]`, `ENV["RESULTS_FILE"] = __jh_results`, "tar -czf", "finally"} {
		if !strings.Contains(code, want) {
			t.Errorf("driver lacks %s:\n%s", want, code)
		}
	}
	if strings.Contains(code, `cd("appbundle")`) {
		t.Error("driver changes directory without a bundle")
	}

	code = jobDriverCode(jobDriver{Bundle: true, Include: "src/run.jl"})
	if !strings.Contains(code, `include(joinpath(pwd(), "src/run.jl"))`) {
		t.Errorf("driver does not include the script:\n%s", code)
	}
}

func TestPrepareJobSubmissionDriver(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"Project.toml": "", "run.jl": "1"})
	noEnv := func(string) (string, bool) { return "", false }

	// A plain script is sent as it is.
	sub, err := prepareJobSubmission(jobSubmitOptions{Script: filepath.Join(dir, "run.jl")}, nil, noEnv)
	if err != nil || string(sub.Code) != "1" {
		t.Fatalf("plain script: code=%q err=%v", sub.Code, err)
	}

	// A bundle: the driver runs the script from inside it, the bundle carries
	// the environment, and the alias is the script's name.
	sub, err = prepareJobSubmission(jobSubmitOptions{Script: filepath.Join(dir, "run.jl"), Bundle: dir}, nil, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(sub.Code), `include(joinpath(pwd(), "run.jl"))`) || sub.Environment != nil || sub.Alias != "run.jl" {
		t.Errorf("bundle: alias=%q env=%v code:\n%s", sub.Alias, sub.Environment, sub.Code)
	}

	// -e with a bundle: the alias defaults to the bundle's name.
	sub, err = prepareJobSubmission(jobSubmitOptions{Expr: "run(`sh x.sh`)", Bundle: dir}, nil, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Alias != filepath.Base(dir) || !strings.Contains(string(sub.Code), "include_string") {
		t.Errorf("-e with bundle: alias=%q code:\n%s", sub.Alias, sub.Code)
	}

	// A command and a results path travel in the job's environment.
	sub, err = prepareJobSubmission(jobSubmitOptions{Command: "./run.sh 'a b'", Results: "out", Bundle: dir}, nil, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Env[jobCommandEnv] != "./run.sh 'a b'" || sub.Env[jobResultsEnv] != "out" {
		t.Errorf("command env = %v", sub.Env)
	}
}

// A scripted submission of a directory with a shell command and a results
// file: scripts depend on these flags and on the result keeping the --results
// file's own name, so keep both stable.
func TestScriptedSubmitCall(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"run.sh": "true"})
	t.Chdir(dir)
	argv := []string{"--json", "--bundle", ".", "--command", "sh run.sh",
		"--results", "results.tar.gz", "--name", "run1",
		"--cpu", "4", "--memory", "16", "--time-limit", "6", "--image", "Julia 1.10", "-s", "example.com"}
	f := jobSubmitCmd.Flags()
	t.Cleanup(func() {
		f.VisitAll(func(fl *pflag.Flag) {
			if sv, ok := fl.Value.(pflag.SliceValue); ok {
				_ = sv.Replace(nil)
			} else {
				_ = fl.Value.Set(fl.DefValue)
			}
			fl.Changed = false
		})
	})
	if err := f.Parse(argv); err != nil {
		t.Fatalf("the flags no longer parse: %v", err)
	}
	if s, _ := f.GetString("server"); s != "example.com" {
		t.Errorf("-s = %q", s)
	}
	opts := jobSubmitOptionsFromFlags(f, f.Args())
	want := jobSubmitOptions{JSON: true, Bundle: ".", Command: "sh run.sh",
		Results: "results.tar.gz", Alias: "run1", CPU: 4, Memory: 16,
		TimeLimit: "6", Image: "Julia 1.10", Product: "standard-batch", Env: []string{}}
	if !reflect.DeepEqual(opts, want) {
		t.Errorf("options = %+v\nwant %+v", opts, want)
	}
	sub, err := prepareJobSubmission(opts, nil, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if sub.Alias != "run1" || sub.Env[jobCommandEnv] != "sh run.sh" || sub.Env[jobResultsEnv] != "results.tar.gz" {
		t.Errorf("submission alias=%q env=%v", sub.Alias, sub.Env)
	}
	// A results file is uploaded under its own name.
	code := string(sub.Code)
	for _, want := range []string{`cp(__jh_results, joinpath(upload_dir, basename(__jh_results)); force = true)`, `ENV["RESULTS_FILE"] = __jh_results`} {
		if !strings.Contains(code, want) {
			t.Errorf("driver lacks %s:\n%s", want, code)
		}
	}
}
