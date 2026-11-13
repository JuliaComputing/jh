package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNormalizeRepoURL(t *testing.T) {
	cases := map[string]string{
		"https://github.com/org/Pkg.jl.git":  "https://github.com/org/Pkg.jl.git",
		"https://github.com/org/Pkg.jl":      "https://github.com/org/Pkg.jl",
		"ssh://git@git.example.com/t/Pkg.jl": "https://git.example.com/t/Pkg.jl",
		"ssh://git@git.example.com:2222/t/P": "https://git.example.com/t/P",
		"ssh://git.example.com/t/Pkg.jl":     "ssh://git.example.com/t/Pkg.jl",
		"https://oauth2:tok@gitlab.com/g/P":  "https://gitlab.com/g/P",
		"https://user@bitbucket.org/t/P.jl":  "https://bitbucket.org/t/P.jl",
		"git@github.com:org/Pkg.jl.git":      "https://github.com/org/Pkg.jl.git",
		"git@git.example.com:/team/Pkg.jl":   "https://git.example.com/team/Pkg.jl",
		"  git@gitlab.com:g/sub/Pkg.jl\n":    "https://gitlab.com/g/sub/Pkg.jl",
		"/srv/git/Pkg.jl":                    "/srv/git/Pkg.jl",
	}
	for in, want := range cases {
		if got := normalizeRepoURL(in); got != want {
			t.Errorf("normalizeRepoURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDetectGitServerType(t *testing.T) {
	cases := map[string]string{
		"https://github.com/org/Pkg.jl":      "github",
		"https://GitHub.com/org/Pkg.jl":      "github",
		"git@github.com:org/Pkg.jl.git":      "github",
		"https://gitlab.com/g/Pkg.jl":        "gitlab",
		"https://bitbucket.org/t/Pkg.jl":     "bitbucket",
		"https://git.example.com/t/Pkg.jl":   "",
		"https://gitlab.example.com/t/Pkg":   "",
		"ssh://git@vmdepot.example.com/t/P":  "",
		"not a url":                          "",
		"https://notgithub.com/org/Pkg.jl":   "",
		"https://github.com.evil.io/o/Pkg":   "",
		"https://enterprise.github.com/o/P":  "github",
		"https://github.com:443/org/Pkg.jl":  "github",
		"https://user@bitbucket.org/t/P.jl":  "bitbucket",
		"git@bitbucket.org:team/Pkg.jl.git":  "bitbucket",
		"https://gitlab.com.example.org/x/y": "",
	}
	for in, want := range cases {
		if got := detectGitServerType(in); got != want {
			t.Errorf("detectGitServerType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveGitServerType(t *testing.T) {
	gitlabRegistry := &registratorRegistry{RegistryName: "R", GitServerType: "gitlab"}

	got, err := resolveGitServerType("bitbucket", "https://github.com/o/P.jl", gitlabRegistry)
	if err != nil || got != "bitbucket" {
		t.Errorf("explicit value: got %q, %v", got, err)
	}
	if _, err := resolveGitServerType("gitea", "https://x/o/P.jl", nil); err == nil || !strings.Contains(err.Error(), "invalid --git-server-type") {
		t.Errorf("invalid explicit value: got %v", err)
	}
	got, err = resolveGitServerType("", "https://github.com/o/P.jl", gitlabRegistry)
	if err != nil || got != "github" {
		t.Errorf("detected host should win over the registry: got %q, %v", got, err)
	}
	got, err = resolveGitServerType("", "https://git.example.com/o/P.jl", gitlabRegistry)
	if err != nil || got != "gitlab" {
		t.Errorf("registry fallback: got %q, %v", got, err)
	}
	if _, err := resolveGitServerType("", "https://git.example.com/o/P.jl", &registratorRegistry{}); err == nil || !strings.Contains(err.Error(), "--git-server-type") {
		t.Errorf("undeterminable: got %v", err)
	}
}

func TestFindRegistratorRegistry(t *testing.T) {
	regs := []registratorRegistry{
		{RegistryName: "General", CanRegister: false},
		{RegistryName: "MyRegistry", CanRegister: true},
	}
	r, err := findRegistratorRegistry(regs, "myregistry")
	if err != nil || r.RegistryName != "MyRegistry" {
		t.Fatalf("case-insensitive match: got %+v, %v", r, err)
	}
	if _, err := findRegistratorRegistry(regs, "General"); err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("no register privilege: got %v", err)
	}
	_, err = findRegistratorRegistry(regs, "Missing")
	if err == nil || !strings.Contains(err.Error(), "General, MyRegistry") {
		t.Errorf("unknown registry should list the known ones: got %v", err)
	}
	if _, err := findRegistratorRegistry(nil, "X"); err == nil || !strings.Contains(err.Error(), "no registries configured") {
		t.Errorf("empty list: got %v", err)
	}
}

func TestRepoFromCheckout(t *testing.T) {
	orig := gitOutput
	t.Cleanup(func() { gitOutput = orig })

	answers := map[string]string{
		"remote get-url origin":       "git@github.com:org/Pkg.jl.git",
		"rev-parse --abbrev-ref HEAD": "release-1",
	}
	gitOutput = func(dir string, args ...string) (string, error) {
		if v, ok := answers[strings.Join(args, " ")]; ok {
			return v, nil
		}
		return "", fmt.Errorf("unexpected git %v", args)
	}
	repo, branch, err := repoFromCheckout(".")
	if err != nil || repo != "https://github.com/org/Pkg.jl.git" || branch != "release-1" {
		t.Errorf("got %q, %q, %v", repo, branch, err)
	}

	answers["rev-parse --abbrev-ref HEAD"] = "HEAD" // detached
	if _, branch, _ := repoFromCheckout("."); branch != "" {
		t.Errorf("detached HEAD should leave the branch empty, got %q", branch)
	}

	delete(answers, "remote get-url origin")
	if _, _, err := repoFromCheckout("."); err == nil || !strings.Contains(err.Error(), "pass it as an argument") {
		t.Errorf("missing remote: got %v", err)
	}
}

// fakeRegistrator is an in-memory /registrator/v1 server. statuses are the
// successive answers to status polls (the last one repeats); an entry with a
// non-zero code is answered with that HTTP status instead.
type fakeRegistrator struct {
	t          *testing.T
	registries []registratorRegistry
	registerFn func(w http.ResponseWriter, body map[string][]registerRequest)
	statuses   []fakeStatus

	mu        sync.Mutex
	submitted []registerRequest
	polls     int
}

type fakeStatus struct {
	code  int
	state string
	msg   string
}

func (f *fakeRegistrator) server() *httptest.Server {
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
			return false
		}
		return true
	}
	mux.HandleFunc("GET /registrator/v1/configStatus", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"registries": f.registries})
	})
	mux.HandleFunc("POST /registrator/v1/register", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			f.t.Errorf("register Content-Type = %q", ct)
		}
		var body map[string][]registerRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Errorf("bad register body: %v", err)
		}
		f.mu.Lock()
		f.submitted = append(f.submitted, body["requests"]...)
		f.mu.Unlock()
		if f.registerFn != nil {
			f.registerFn(w, body)
			return
		}
		json.NewEncoder(w).Encode(registerResponse{Message: "Registration in progress. Please wait...", ID: "abc-123"})
	})
	mux.HandleFunc("GET /registrator/v1/status/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if id := r.PathValue("id"); id != "abc-123" {
			f.t.Errorf("status polled for id %q", id)
		}
		f.mu.Lock()
		i := min(f.polls, len(f.statuses)-1)
		f.polls++
		f.mu.Unlock()
		s := f.statuses[i]
		if s.code != 0 {
			w.WriteHeader(s.code)
			return
		}
		json.NewEncoder(w).Encode(registrationStatus{State: s.state, Message: s.msg})
	})
	return httptest.NewServer(mux)
}

func defaultRegisterOpts() registerOptions {
	return registerOptions{
		Registry:     "MyRegistry",
		RepoURL:      "https://git.example.com/team/Pkg.jl",
		Branch:       "main",
		Notes:        "notes",
		Wait:         true,
		PollInterval: 5 * time.Second,
		Timeout:      time.Minute,
	}
}

func myRegistry() []registratorRegistry {
	return []registratorRegistry{{RegistryName: "MyRegistry", Ready: true, CanRegister: true, GitServerType: "gitlab"}}
}

func TestRunRegistrationSuccess(t *testing.T) {
	f := &fakeRegistrator{t: t, registries: myRegistry(), statuses: []fakeStatus{
		{state: "pending", msg: "Please wait..."},
		{code: http.StatusBadGateway},
		{state: "pending", msg: "Please wait..."},
		{state: "pending", msg: "Creating merge request"},
		{state: "success", msg: "https://git.example.com/team/Registry/-/merge_requests/7"},
	}}
	srv := f.server()
	defer srv.Close()

	var out, errOut bytes.Buffer
	clock := &fakeClock{now: time.Unix(0, 0)}
	err := runRegistration(srv.Client(), srv.URL, "tok", defaultRegisterOpts(), clock, &out, &errOut)
	if err != nil {
		t.Fatalf("runRegistration: %v (stderr %q)", err, errOut.String())
	}
	want := registerRequest{RegistryName: "MyRegistry", RepoURL: "https://git.example.com/team/Pkg.jl", GitServerType: "gitlab", Branch: "main", Notes: "notes"}
	if len(f.submitted) != 1 || f.submitted[0] != want {
		t.Errorf("submitted %+v, want %+v", f.submitted, want)
	}
	if f.polls != 5 {
		t.Errorf("polled %d times, want 5", f.polls)
	}
	if !strings.Contains(out.String(), "id abc-123") || !strings.Contains(out.String(), "merge_requests/7") {
		t.Errorf("stdout = %q", out.String())
	}
	// Repeated pending messages are printed once each time they change.
	if got := strings.Count(errOut.String(), "pending:"); got != 2 {
		t.Errorf("stderr pending lines = %d, want 2: %q", got, errOut.String())
	}
}

func TestRunRegistrationErrored(t *testing.T) {
	f := &fakeRegistrator{t: t, registries: myRegistry(), statuses: []fakeStatus{
		{state: "errored", msg: "Version 1.0.0 already exists"},
	}}
	srv := f.server()
	defer srv.Close()
	var out, errOut bytes.Buffer
	err := runRegistration(srv.Client(), srv.URL, "tok", defaultRegisterOpts(), &fakeClock{}, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "registration failed: Version 1.0.0 already exists") {
		t.Fatalf("got %v", err)
	}
}

func TestRunRegistrationNoWait(t *testing.T) {
	f := &fakeRegistrator{t: t, registries: myRegistry(), statuses: []fakeStatus{{state: "pending"}}}
	srv := f.server()
	defer srv.Close()
	opts := defaultRegisterOpts()
	opts.Wait = false
	var out, errOut bytes.Buffer
	if err := runRegistration(srv.Client(), srv.URL, "tok", opts, &fakeClock{}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if f.polls != 0 {
		t.Errorf("--no-wait polled %d times", f.polls)
	}
}

func TestRunRegistrationTimeout(t *testing.T) {
	f := &fakeRegistrator{t: t, registries: myRegistry(), statuses: []fakeStatus{{state: "pending", msg: "Please wait..."}}}
	srv := f.server()
	defer srv.Close()
	opts := defaultRegisterOpts()
	opts.Timeout = 30 * time.Second
	var out, errOut bytes.Buffer
	err := runRegistration(srv.Client(), srv.URL, "tok", opts, &fakeClock{now: time.Unix(0, 0)}, &out, &errOut)
	if !errors.Is(err, errRegistrationTimeout) || !strings.Contains(err.Error(), "continues on the server") {
		t.Fatalf("got %v", err)
	}
	if f.polls != 6 {
		t.Errorf("polled %d times in 30s at 5s, want 6", f.polls)
	}
}

func TestRunRegistrationStatusKeepsFailing(t *testing.T) {
	f := &fakeRegistrator{t: t, registries: myRegistry(), statuses: []fakeStatus{{code: http.StatusServiceUnavailable}}}
	srv := f.server()
	defer srv.Close()
	var out, errOut bytes.Buffer
	err := runRegistration(srv.Client(), srv.URL, "tok", defaultRegisterOpts(), &fakeClock{}, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "failed 5 times in a row") {
		t.Fatalf("got %v", err)
	}
}

func TestRunRegistrationStatusClientErrorStops(t *testing.T) {
	f := &fakeRegistrator{t: t, registries: myRegistry(), statuses: []fakeStatus{{code: http.StatusNotFound}}}
	srv := f.server()
	defer srv.Close()
	var out, errOut bytes.Buffer
	err := runRegistration(srv.Client(), srv.URL, "tok", defaultRegisterOpts(), &fakeClock{}, &out, &errOut)
	var apiErr *registratorAPIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusNotFound || f.polls != 1 {
		t.Fatalf("got %v after %d polls", err, f.polls)
	}
}

func TestRunRegistrationRegisterRejected(t *testing.T) {
	f := &fakeRegistrator{t: t, registries: myRegistry(),
		registerFn: func(w http.ResponseWriter, _ map[string][]registerRequest) {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"message":"Not authorized to register packages with registry MyRegistry"}`))
		},
	}
	srv := f.server()
	defer srv.Close()
	var out, errOut bytes.Buffer
	err := runRegistration(srv.Client(), srv.URL, "tok", defaultRegisterOpts(), &fakeClock{}, &out, &errOut)
	if err == nil || err.Error() != "registrator API request failed (HTTP 401): Not authorized to register packages with registry MyRegistry" {
		t.Fatalf("got %v", err)
	}
}

func TestRunRegistrationNotReadyWarns(t *testing.T) {
	regs := myRegistry()
	regs[0].Ready = false
	regs[0].Message = "Registry is being cloned"
	f := &fakeRegistrator{t: t, registries: regs, statuses: []fakeStatus{{state: "success", msg: "ok"}}}
	srv := f.server()
	defer srv.Close()
	var out, errOut bytes.Buffer
	if err := runRegistration(srv.Client(), srv.URL, "tok", defaultRegisterOpts(), &fakeClock{}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.String(), "warning: registry MyRegistry: Registry is being cloned") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestRunRegistrationBadToken(t *testing.T) {
	f := &fakeRegistrator{t: t, registries: myRegistry()}
	srv := f.server()
	defer srv.Close()
	var out, errOut bytes.Buffer
	err := runRegistration(srv.Client(), srv.URL, "wrong", defaultRegisterOpts(), &fakeClock{}, &out, &errOut)
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") || len(f.submitted) != 0 {
		t.Fatalf("got %v, submitted %d", err, len(f.submitted))
	}
}

func TestRegistratorAPIErrorMessage(t *testing.T) {
	cases := []struct {
		body, want string
	}{
		{`{"message":"m"}`, "registrator API request failed (HTTP 400): m"},
		{`{"error":"e"}`, "registrator API request failed (HTTP 400): e"},
		{"plain text\n", "registrator API request failed (HTTP 400): plain text"},
		{"", "registrator API request failed (HTTP 400)"},
	}
	for _, c := range cases {
		if got := (&registratorAPIError{Status: 400, Body: c.body}).Error(); got != c.want {
			t.Errorf("body %q: got %q, want %q", c.body, got, c.want)
		}
	}
}
