package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// Package registration through the registrator API (/registrator/v1). A
// registration is asynchronous: POST /register queues it and returns an id,
// and GET /status/{id} reports "pending" until it ends as "success" (the
// message is usually the registry pull/merge request URL) or "errored".
// Status is kept per user, so it must be polled with the same identity.

// gitServerTypes are the git_server_type values the registrator understands.
// Anything else is treated as a plain git server ("bare-git").
var gitServerTypes = []string{"github", "gitlab", "bitbucket", "bare-git"}

type registerRequest struct {
	RegistryName  string `json:"registry_name"`
	RepoURL       string `json:"repo_url"`
	GitServerType string `json:"git_server_type"`
	Branch        string `json:"branch,omitempty"`
	Notes         string `json:"notes,omitempty"`
	Subdir        string `json:"subdir,omitempty"`
}

type registerResponse struct {
	Message string `json:"message"`
	ID      string `json:"id"`
}

type registrationStatus struct {
	Message string `json:"message"`
	State   string `json:"state"`
}

// registratorRegistry is one entry of GET /registrator/v1/configStatus: a
// registry the registrator knows, and what the caller may do with it.
type registratorRegistry struct {
	Ready         bool   `json:"ready"`
	RegistryName  string `json:"registry_name"`
	RegistryURL   string `json:"registry_url"`
	GitServerType string `json:"git_server_type"`
	CanRegister   bool   `json:"can_register"`
	Message       string `json:"message"`
}

type registerOptions struct {
	Registry      string
	RepoURL       string
	Branch        string
	Subdir        string
	Notes         string
	GitServerType string
	Wait          bool
	PollInterval  time.Duration
	Timeout       time.Duration
}

// registratorAPIError is a non-2xx answer from the registrator API.
type registratorAPIError struct {
	Status int
	Body   string
}

func (e *registratorAPIError) Error() string {
	msg := strings.TrimSpace(e.Body)
	// The API answers errors as {"message": "..."} (or {"error": "..."}).
	var parsed struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal([]byte(msg), &parsed) == nil {
		if parsed.Message != "" {
			msg = parsed.Message
		} else if parsed.Error != "" {
			msg = parsed.Error
		}
	}
	if msg == "" {
		return fmt.Sprintf("registrator API request failed (HTTP %d)", e.Status)
	}
	return fmt.Sprintf("registrator API request failed (HTTP %d): %s", e.Status, msg)
}

// registratorDo sends one request to the registrator API and decodes a 200
// answer into out.
func registratorDo(client *http.Client, method, apiURL, idToken string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to encode request: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, apiURL, reader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+idToken)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return &registratorAPIError{Status: resp.StatusCode, Body: string(data)}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	return nil
}

// fetchRegistratorRegistries lists the registries the registrator serves for
// the caller (GET /registrator/v1/configStatus).
func fetchRegistratorRegistries(client *http.Client, baseURL, idToken string) ([]registratorRegistry, error) {
	var cfg struct {
		Registries []registratorRegistry `json:"registries"`
	}
	if err := registratorDo(client, http.MethodGet, baseURL+"/registrator/v1/configStatus", idToken, nil, &cfg); err != nil {
		return nil, err
	}
	return cfg.Registries, nil
}

// findRegistratorRegistry picks the registry named name (case-insensitively)
// and checks the caller may register to it.
func findRegistratorRegistry(registries []registratorRegistry, name string) (*registratorRegistry, error) {
	for i := range registries {
		r := &registries[i]
		if !strings.EqualFold(r.RegistryName, name) {
			continue
		}
		if !r.CanRegister {
			return nil, fmt.Errorf("you are not allowed to register packages in registry %q; ask an administrator for the register privilege", r.RegistryName)
		}
		return r, nil
	}
	names := make([]string, 0, len(registries))
	for _, r := range registries {
		names = append(names, r.RegistryName)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("registry %q not found: the registrator has no registries configured", name)
	}
	return nil, fmt.Errorf("registry %q not found; registries with a registrator: %s", name, strings.Join(names, ", "))
}

// detectGitServerType guesses git_server_type from the public hosting
// services' host names. Self-hosted servers are not recognized ("").
func detectGitServerType(repoURL string) string {
	host := repoHost(repoURL)
	switch {
	case host == "github.com" || strings.HasSuffix(host, ".github.com"):
		return "github"
	case host == "gitlab.com":
		return "gitlab"
	case host == "bitbucket.org":
		return "bitbucket"
	}
	return ""
}

// repoHost returns the lower-cased host of a repository URL, including the
// scp-like form git@host:owner/repo.git.
func repoHost(repoURL string) string {
	if u, err := url.Parse(repoURL); err == nil && u.Host != "" {
		return strings.ToLower(u.Hostname())
	}
	if at := strings.Index(repoURL, "@"); at >= 0 {
		rest := repoURL[at+1:]
		if colon := strings.Index(rest, ":"); colon > 0 {
			return strings.ToLower(rest[:colon])
		}
	}
	return ""
}

// resolveGitServerType chooses git_server_type: the explicit value, else the
// public host the URL points at, else the server type of the registry itself
// (a self-hosted GitLab usually hosts both the registry and its packages).
func resolveGitServerType(explicit, repoURL string, registry *registratorRegistry) (string, error) {
	if explicit != "" {
		for _, t := range gitServerTypes {
			if explicit == t {
				return explicit, nil
			}
		}
		return "", fmt.Errorf("invalid --git-server-type %q (want one of: %s)", explicit, strings.Join(gitServerTypes, ", "))
	}
	if t := detectGitServerType(repoURL); t != "" {
		return t, nil
	}
	if registry != nil && registry.GitServerType != "" {
		return registry.GitServerType, nil
	}
	return "", fmt.Errorf("cannot tell where %s is hosted; pass --git-server-type (%s)", repoURL, strings.Join(gitServerTypes, ", "))
}

// normalizeRepoURL makes a git remote acceptable to the registrator, which
// rejects any URL with a user part (anything with an "@" after the scheme)
// and any scheme but https, http and ssh. The scp-like git@host:owner/repo.git
// form and ssh://git@host/... become https://host/owner/repo.git; a user (and
// password) in an http(s) URL is dropped. Other URLs are returned unchanged.
func normalizeRepoURL(remote string) string {
	remote = strings.TrimSpace(remote)
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil || u.User == nil || u.Host == "" {
			return remote
		}
		switch u.Scheme {
		case "ssh", "git+ssh", "ssh+git":
			return "https://" + u.Hostname() + u.EscapedPath()
		case "http", "https":
			u.User = nil
			return u.String()
		}
		return remote
	}
	at := strings.Index(remote, "@")
	if at < 0 {
		return remote
	}
	rest := remote[at+1:]
	colon := strings.Index(rest, ":")
	if colon <= 0 {
		return remote
	}
	return "https://" + rest[:colon] + "/" + strings.TrimPrefix(rest[colon+1:], "/")
}

// gitOutput runs git in dir and returns its trimmed stdout. It is a variable
// so tests can avoid a real repository.
var gitOutput = func(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// repoFromCheckout reads the repository URL (the origin remote) and the
// checked-out branch from the git checkout at dir. branch is "" on a
// detached HEAD (common in CI), which leaves the choice to the registrator
// (the default branch).
func repoFromCheckout(dir string) (repoURL, branch string, err error) {
	remote, err := gitOutput(dir, "remote", "get-url", "origin")
	if err != nil {
		return "", "", fmt.Errorf("cannot find the repository URL (pass it as an argument): %w", err)
	}
	branch, err = gitOutput(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || branch == "HEAD" {
		branch = ""
	}
	return normalizeRepoURL(remote), branch, nil
}

// submitRegistration queues a registration and returns its id.
func submitRegistration(client *http.Client, baseURL, idToken string, r registerRequest) (*registerResponse, error) {
	var resp registerResponse
	body := map[string][]registerRequest{"requests": {r}}
	if err := registratorDo(client, http.MethodPost, baseURL+"/registrator/v1/register", idToken, body, &resp); err != nil {
		return nil, err
	}
	if resp.ID == "" {
		return nil, fmt.Errorf("registrator accepted the request but returned no id (message: %q)", resp.Message)
	}
	return &resp, nil
}

// errRegistrationTimeout is returned when a registration is still pending at
// the deadline. It keeps running on the server.
var errRegistrationTimeout = errors.New("timed out waiting for the registration to finish")

// maxStatusPollFailures is how many status polls in a row may fail (network
// errors, 5xx answers) before waiting is abandoned.
const maxStatusPollFailures = 5

// waitForRegistration polls GET /registrator/v1/status/{id} until the
// registration succeeds or errors, or timeout passes. onPending is called
// with each pending status message that differs from the previous one.
func waitForRegistration(client *http.Client, baseURL, idToken, id string, interval, timeout time.Duration, clock pollClock, onPending func(string)) (*registrationStatus, error) {
	deadline := clock.Now().Add(timeout)
	statusURL := baseURL + "/registrator/v1/status/" + url.PathEscape(id)
	failures := 0
	lastMessage := ""
	for {
		clock.Sleep(interval)
		var st registrationStatus
		err := registratorDo(client, http.MethodGet, statusURL, idToken, nil, &st)
		if err != nil {
			var apiErr *registratorAPIError
			if errors.As(err, &apiErr) && apiErr.Status < 500 {
				return nil, err
			}
			failures++
			if failures >= maxStatusPollFailures {
				return nil, fmt.Errorf("checking registration status failed %d times in a row: %w", failures, err)
			}
		} else {
			failures = 0
			switch st.State {
			case "success", "errored":
				return &st, nil
			}
			if st.Message != lastMessage && onPending != nil {
				onPending(st.Message)
			}
			lastMessage = st.Message
		}
		if !clock.Now().Before(deadline) {
			return nil, errRegistrationTimeout
		}
	}
}

// registerPackage registers opts.RepoURL in opts.Registry and, unless
// opts.Wait is false, waits for the outcome.
func registerPackage(server string, opts registerOptions) error {
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	return runRegistration(client, "https://"+server, token.IDToken, opts, realClock{}, os.Stdout, os.Stderr)
}

// runRegistration does the work of registerPackage; results go to out,
// progress and warnings to errOut.
func runRegistration(client *http.Client, baseURL, idToken string, opts registerOptions, clock pollClock, out, errOut io.Writer) error {
	registries, err := fetchRegistratorRegistries(client, baseURL, idToken)
	if err != nil {
		return fmt.Errorf("failed to list registrator registries: %w", err)
	}
	registry, err := findRegistratorRegistry(registries, opts.Registry)
	if err != nil {
		return err
	}
	if !registry.Ready {
		msg := "the registrator is not ready for this registry yet"
		if registry.Message != "" {
			msg = registry.Message
		}
		fmt.Fprintf(errOut, "warning: registry %s: %s\n", registry.RegistryName, msg)
	}
	serverType, err := resolveGitServerType(opts.GitServerType, opts.RepoURL, registry)
	if err != nil {
		return err
	}

	req := registerRequest{
		RegistryName:  registry.RegistryName,
		RepoURL:       opts.RepoURL,
		GitServerType: serverType,
		Branch:        opts.Branch,
		Notes:         opts.Notes,
		Subdir:        opts.Subdir,
	}
	resp, err := submitRegistration(client, baseURL, idToken, req)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Registration of %s in %s submitted (id %s)\n", opts.RepoURL, registry.RegistryName, resp.ID)
	if !opts.Wait {
		return nil
	}

	st, err := waitForRegistration(client, baseURL, idToken, resp.ID, opts.PollInterval, opts.Timeout, clock, func(msg string) {
		if msg != "" {
			fmt.Fprintf(errOut, "pending: %s\n", msg)
		}
	})
	if errors.Is(err, errRegistrationTimeout) {
		return fmt.Errorf("%w after %s; it continues on the server; check the registry for the pull request later", err, opts.Timeout)
	}
	if err != nil {
		return err
	}
	if st.State == "errored" {
		return fmt.Errorf("registration failed: %s", st.Message)
	}
	fmt.Fprintf(out, "Registration succeeded: %s\n", st.Message)
	return nil
}
