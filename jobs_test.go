package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A job in the shape GET /api/rest/jobs/{jobname} returns (trimmed), with a
// result file, input/source files, and a result whose upload is incomplete.
const sampleJobDetails = `{"details":[{
  "jobname":"jr-abc123","jobname_alias":"nightly fit","status":"Completed",
  "outputs":"{}","files":[
    {"name":"code.jl","type":"input","size":3,"hash":{"algorithm":"sha2_256","value":"AAAA"},"upload_timestamp":"2025-03-15T07:56:51+00:00"},
    {"name":"code.jl","type":"source","size":3,"hash":{"algorithm":"sha2_256","value":"AAAA"},"upload_timestamp":"2025-03-15T07:56:51+00:00"},
    {"name":"outdir.tar.gz","type":"result","size":632143,"hash":{"algorithm":"sha2_256","value":"BBBB"},"upload_timestamp":"2025-03-15T07:59:29+00:00"},
    {"name":"partial.csv","type":"result","size":null,"hash":{"algorithm":null,"value":null},"upload_timestamp":null}
  ]}]}`

func parseSampleJob(t *testing.T) *JobDetails {
	t.Helper()
	var r jobDetailsResponse
	if err := json.Unmarshal([]byte(sampleJobDetails), &r); err != nil {
		t.Fatalf("unmarshal job: %v", err)
	}
	return &r.Details[0]
}

func names(files []JobFile) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.Type+"/"+f.Name)
	}
	return out
}

func TestFetchJobDetails(t *testing.T) {
	var gotPath, gotAuth, gotRole string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotRole = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-Hasura-Role")
		if r.URL.Path == "/api/rest/jobs/jr-missing" {
			w.Write([]byte(`{"details":[]}`))
			return
		}
		w.Write([]byte(sampleJobDetails))
	}))
	defer srv.Close()

	job, err := fetchJobDetails(srv.Client(), srv.URL, "tok", "jr-abc123")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/rest/jobs/jr-abc123" || gotAuth != "Bearer tok" || gotRole != "jhuser" {
		t.Errorf("request path=%q auth=%q role=%q", gotPath, gotAuth, gotRole)
	}
	if job.JobName != "jr-abc123" || job.Status != "Completed" || len(job.Files) != 4 {
		t.Errorf("unexpected job %+v", job)
	}
	if f := job.Files[3]; f.Size != nil || f.Hash == nil || f.Hash.Algorithm != nil {
		t.Errorf("null size/hash should decode to nil, got %+v", f)
	}

	if _, err := fetchJobDetails(srv.Client(), srv.URL, "tok", "jr-missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing job: want a not-found error, got %v", err)
	}
}

func TestFetchJobDetailsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("nope"))
	}))
	defer srv.Close()
	if _, err := fetchJobDetails(srv.Client(), srv.URL, "tok", "jr-abc123"); err == nil || !strings.Contains(err.Error(), "status 401") {
		t.Errorf("want a status 401 error, got %v", err)
	}
}

func TestFilterJobFiles(t *testing.T) {
	job := parseSampleJob(t)
	if got, want := names(filterJobFiles(job.Files, "result")), []string{"result/outdir.tar.gz", "result/partial.csv"}; !reflect.DeepEqual(got, want) {
		t.Errorf("result files = %v, want %v", got, want)
	}
	if got, want := names(filterJobFiles(job.Files, "")), []string{"input/code.jl", "result/outdir.tar.gz", "result/partial.csv", "source/code.jl"}; !reflect.DeepEqual(got, want) {
		t.Errorf("all files = %v, want %v", got, want)
	}
	if got := filterJobFiles(job.Files, "project"); len(got) != 0 {
		t.Errorf("project files = %v, want none", names(got))
	}
}

func TestSelectJobFiles(t *testing.T) {
	job := parseSampleJob(t)

	// Without a name, the pending partial.csv is set aside, not downloaded.
	all, pending, err := selectJobFiles(job, "result", "")
	if err != nil || !reflect.DeepEqual(names(all), []string{"result/outdir.tar.gz"}) || !reflect.DeepEqual(names(pending), []string{"result/partial.csv"}) {
		t.Errorf("no name: got %v, pending %v, %v; want outdir.tar.gz, pending partial.csv", names(all), names(pending), err)
	}
	one, _, err := selectJobFiles(job, "result", "outdir.tar.gz")
	if err != nil || !reflect.DeepEqual(names(one), []string{"result/outdir.tar.gz"}) {
		t.Errorf("by name: got %v, %v", names(one), err)
	}
	if _, _, err := selectJobFiles(job, "result", "partial.csv"); err == nil || !strings.Contains(err.Error(), "still being uploaded") {
		t.Errorf("named pending file: want a still-uploading error, got %v", err)
	}
	// code.jl exists, but not as a result file.
	if _, _, err := selectJobFiles(job, "result", "code.jl"); err == nil || !strings.Contains(err.Error(), "available: outdir.tar.gz, partial.csv") {
		t.Errorf("wrong type: want an error listing available files, got %v", err)
	}
	if _, _, err := selectJobFiles(job, "project", ""); err == nil || !strings.Contains(err.Error(), "no project files") {
		t.Errorf("no files of type: got %v", err)
	}

	onlyPending := &JobDetails{JobName: "jr-x", Status: "Running", Files: []JobFile{{Name: "partial.csv", Type: "result"}}}
	if _, _, err := selectJobFiles(onlyPending, "result", ""); err == nil || !strings.Contains(err.Error(), "1 still uploading") {
		t.Errorf("only pending files: want a still-uploading error, got %v", err)
	}
}

func int64p(n int64) *int64 { return &n }

func TestPlanJobDownloads(t *testing.T) {
	job := parseSampleJob(t)

	// Download-all treats -o as a directory even when only one file is
	// selected, so the on-disk shape doesn't depend on the job.
	plan, pending, err := planJobDownloads(job, "result", "", "results")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 || plan[0].Dest != filepath.Join("results", "outdir.tar.gz") || len(pending) != 1 {
		t.Errorf("download-all plan = %+v, pending %v", plan, names(pending))
	}
	// A named file with -o keeps the file-or-directory rule.
	plan, _, err = planJobDownloads(job, "result", "outdir.tar.gz", "results")
	if err != nil || len(plan) != 1 || plan[0].Dest != "results" {
		t.Errorf("named plan = %+v, %v; want dest results", plan, err)
	}

	for _, bad := range []string{"", "results", "all"} {
		if _, _, err := planJobDownloads(job, bad, "", ""); err == nil || !strings.Contains(err.Error(), "invalid file type") {
			t.Errorf("type %q: want an invalid-type error, got %v", bad, err)
		}
	}

	// Distinct server names that flatten to the same base name are caught
	// before anything is downloaded.
	dup := &JobDetails{JobName: "jr-x", Files: []JobFile{
		{Name: "a/x.csv", Type: "result", Size: int64p(1)},
		{Name: "b/x.csv", Type: "result", Size: int64p(1)},
	}}
	if _, _, err := planJobDownloads(dup, "result", "", "out"); err == nil || !strings.Contains(err.Error(), "would both be saved as") {
		t.Errorf("duplicate destinations: want an error, got %v", err)
	}
}

func TestJobFileDestination(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		output, name string
		multiple     bool
		want         string
	}{
		{"", "out.tar.gz", false, "out.tar.gz"},
		{"", "out.tar.gz", true, "out.tar.gz"},
		{"renamed.tar.gz", "out.tar.gz", false, "renamed.tar.gz"},
		{dir, "out.tar.gz", false, filepath.Join(dir, "out.tar.gz")},
		{"newdir/", "out.tar.gz", false, filepath.Join("newdir", "out.tar.gz")},
		{"newdir", "out.tar.gz", true, filepath.Join("newdir", "out.tar.gz")},
		// Server-supplied names cannot escape the destination directory.
		{dir, "../../etc/passwd", false, filepath.Join(dir, "passwd")},
	}
	for _, c := range cases {
		got, err := jobFileDestination(c.output, c.name, c.multiple)
		if err != nil || got != c.want {
			t.Errorf("jobFileDestination(%q, %q, %v) = %q, %v; want %q", c.output, c.name, c.multiple, got, err, c.want)
		}
	}
	bad := []string{"", ".", ".."}
	if filepath.Separator == '\\' {
		bad = append(bad, "NUL", "con", "COM1", "aux.txt")
	}
	for _, bad := range bad {
		if _, err := jobFileDestination("", bad, false); err == nil {
			t.Errorf("jobFileDestination(%q) should be rejected", bad)
		}
	}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func strp(s string) *string { return &s }

// jobFileServers starts an API server whose file endpoint redirects to a
// separate "object store" server, like JuliaHub's presigned-URL redirect.
// storeAuth records the Authorization and Referer headers the store received.
func jobFileServers(t *testing.T, content []byte) (api *httptest.Server, storeAuth *string) {
	t.Helper()
	var auth string
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization") + r.Header.Get("Referer")
		w.Write(content)
	}))
	t.Cleanup(store.Close)
	api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.EscapedPath() != "/jobs/jr-abc123/files/out%20dir.tar.gz" || r.URL.Query().Get("filetype") != "result" {
			http.Error(w, "File not found", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, store.URL+"/bucket/key?X-Amz-Signature=sig", http.StatusFound)
	}))
	t.Cleanup(api.Close)
	return api, &auth
}

func TestDownloadJobFile(t *testing.T) {
	content := []byte("result payload")
	api, storeAuth := jobFileServers(t, content)
	f := JobFile{Name: "out dir.tar.gz", Type: "result", Size: int64p(int64(len(content))), Hash: &JobFileHash{Algorithm: strp("sha2_256"), Value: strp(sha256Hex(content))}}
	dest := filepath.Join(t.TempDir(), "sub", "out.tar.gz")

	res, err := downloadJobFile(newJobHTTPClient(), api.URL, "tok", "jr-abc123", f, dest, false)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dest); res.Bytes != int64(len(content)) || !res.Verified || !bytes.Equal(got, content) {
		t.Errorf("downloaded %+v %q, want %q verified", res, got, content)
	}
	// Neither the bearer token nor a Referer naming the job follows the
	// redirect to the presigned URL.
	if *storeAuth != "" {
		t.Errorf("object store received Authorization/Referer %q, want none", *storeAuth)
	}

	// An existing file is kept unless forced.
	if _, err := downloadJobFile(newJobHTTPClient(), api.URL, "tok", "jr-abc123", f, dest, false); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("want an already-exists error, got %v", err)
	}
	if _, err := downloadJobFile(newJobHTTPClient(), api.URL, "tok", "jr-abc123", f, dest, true); err != nil {
		t.Errorf("--force download: %v", err)
	}
}

// A chain API -> API -> store -> store keeps the token on the first hop only.
func TestDownloadJobFileRedirectChain(t *testing.T) {
	var leaked []string
	record := func(r *http.Request) {
		if a := r.Header.Get("Authorization"); a != "" {
			leaked = append(leaked, r.Host+r.URL.Path+": "+a)
		}
	}
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record(r)
		if r.URL.Path == "/hop" {
			http.Redirect(w, r, "/bucket/key", http.StatusFound)
			return
		}
		w.Write([]byte("x"))
	}))
	defer store.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/next" {
			record(r)
			http.Redirect(w, r, store.URL+"/hop", http.StatusFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	defer api.Close()

	dest := filepath.Join(t.TempDir(), "x")
	if _, err := downloadJobFile(newJobHTTPClient(), api.URL, "tok", "jr-abc123", JobFile{Name: "x", Type: "result"}, dest, false); err != nil {
		t.Fatal(err)
	}
	if len(leaked) != 0 {
		t.Errorf("Authorization sent after the first hop: %v", leaked)
	}
}

func TestDownloadJobFileUnverified(t *testing.T) {
	api, _ := jobFileServers(t, []byte("payload"))
	// An empty recorded hash is treated as no hash: downloadable, not verified.
	f := JobFile{Name: "out dir.tar.gz", Type: "result", Hash: &JobFileHash{Algorithm: strp("sha2_256"), Value: strp("")}}
	res, err := downloadJobFile(newJobHTTPClient(), api.URL, "tok", "jr-abc123", f, filepath.Join(t.TempDir(), "out"), false)
	if err != nil || res.Verified {
		t.Errorf("empty hash: got %+v, %v; want an unverified download", res, err)
	}
}

func TestDownloadJobFileSizeMismatch(t *testing.T) {
	api, _ := jobFileServers(t, []byte("truncated"))
	f := JobFile{Name: "out dir.tar.gz", Type: "result", Size: int64p(1000)}
	dir := t.TempDir()
	if _, err := downloadJobFile(newJobHTTPClient(), api.URL, "tok", "jr-abc123", f, filepath.Join(dir, "out"), false); err == nil || !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("want a size mismatch, got %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("directory not empty after failed download: %v", entries)
	}
}

func TestMoveJobFileIntoPlace(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "tmp")
	dest := filepath.Join(dir, "dest")
	os.WriteFile(tmp, []byte("new"), 0644)
	// A file that appeared at dest during the download is kept without force.
	os.WriteFile(dest, []byte("old"), 0644)
	if err := moveJobFileIntoPlace(tmp, dest, false); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("want an already-exists error, got %v", err)
	}
	if got, _ := os.ReadFile(dest); string(got) != "old" {
		t.Errorf("dest overwritten without force: %q", got)
	}
	if err := moveJobFileIntoPlace(tmp, dest, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dest); string(got) != "new" {
		t.Errorf("forced move: dest = %q, want new", got)
	}
}

func TestDownloadJobFileChecksumMismatch(t *testing.T) {
	api, _ := jobFileServers(t, []byte("corrupted"))
	f := JobFile{Name: "out dir.tar.gz", Type: "result", Hash: &JobFileHash{Algorithm: strp("sha2_256"), Value: strp(sha256Hex([]byte("expected")))}}
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.tar.gz")

	if _, err := downloadJobFile(newJobHTTPClient(), api.URL, "tok", "jr-abc123", f, dest, false); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want a checksum mismatch, got %v", err)
	}
	// Neither the destination nor the temporary file is left behind.
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("directory not empty after failed download: %v", entries)
	}
}

func TestDownloadJobFileNotFound(t *testing.T) {
	api, _ := jobFileServers(t, nil)
	f := JobFile{Name: "other.csv", Type: "result"}
	dest := filepath.Join(t.TempDir(), "other.csv")
	if _, err := downloadJobFile(newJobHTTPClient(), api.URL, "tok", "jr-abc123", f, dest, false); err == nil || !strings.Contains(err.Error(), "status 400") {
		t.Errorf("want a status 400 error, got %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("destination should not exist after a failed download")
	}
}

func TestVerifyJobFileHashEncodings(t *testing.T) {
	sum := sha256.Sum256([]byte("payload"))
	// JuliaHub records hex (as seen on a live instance); base64 is accepted too.
	for _, v := range []string{hex.EncodeToString(sum[:]), base64.StdEncoding.EncodeToString(sum[:])} {
		f := JobFile{Name: "f", Hash: &JobFileHash{Algorithm: strp("sha2_256"), Value: strp(v)}}
		if verified, err := verifyJobFileHash(f, sum[:]); err != nil || !verified {
			t.Errorf("value %q: verified=%v, %v", v, verified, err)
		}
		other := sha256.Sum256([]byte("other"))
		if _, err := verifyJobFileHash(f, other[:]); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
			t.Errorf("value %q: want a mismatch for other content, got %v", v, err)
		}
	}
	bad := JobFile{Name: "f", Hash: &JobFileHash{Algorithm: strp("sha2_256"), Value: strp("AAAA")}}
	if _, err := verifyJobFileHash(bad, sum[:]); err == nil || !strings.Contains(err.Error(), "invalid recorded hash") {
		t.Errorf("short hash: want an invalid-hash error, got %v", err)
	}
}

func TestVerifyJobFileHashSkipsUnknown(t *testing.T) {
	sum := sha256.Sum256([]byte("x"))
	for _, h := range []*JobFileHash{nil, {}, {Algorithm: strp("md5"), Value: strp("AAAA")}, {Algorithm: strp("sha2_256"), Value: strp("")}} {
		if verified, err := verifyJobFileHash(JobFile{Name: "f", Hash: h}, sum[:]); err != nil || verified {
			t.Errorf("hash %+v: want skip, got verified=%v, %v", h, verified, err)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 632143: "617.3 KiB", 5 << 30: "5.0 GiB"} {
		if got := formatBytes(n); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestPrintJobFiles(t *testing.T) {
	job := parseSampleJob(t)
	var buf bytes.Buffer
	printJobFiles(&buf, job, filterJobFiles(job.Files, "result"), false)
	out := buf.String()
	for _, want := range []string{"jr-abc123 (nightly fit)", "Completed", "outdir.tar.gz", "617.3 KiB", "partial.csv", "pending"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "TYPE") {
		t.Errorf("result-only listing should not have a TYPE column:\n%s", out)
	}

	buf.Reset()
	printJobFiles(&buf, job, nil, false)
	if !strings.Contains(buf.String(), "RESULTS_FILE") {
		t.Errorf("empty listing should explain RESULTS_FILE:\n%s", buf.String())
	}
}
