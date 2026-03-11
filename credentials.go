package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

// TokenMeta holds metadata about a token fetched.
type TokenMeta struct {
	Login              string `json:"login"`
	Expires            string `json:"expires"`
	Scopes             string `json:"scopes"`
	RateLimitRemaining int    `json:"rate_limit_remaining"`
	RateLimitMax       int    `json:"rate_limit_max"`
	RateLimitReset     int64  `json:"rate_limit_reset"`
}

// TokenMetadata wraps the metadata response for a single token.
type TokenMetadata struct {
	Success bool       `json:"success"`
	Data    *TokenMeta `json:"data,omitempty"`
	Message string     `json:"message,omitempty"`
}

// CredToken represents a token credential
type CredToken struct {
	ID        string         `json:"id"`
	URLPrefix string         `json:"urlprefix"`
	Value     string         `json:"value,omitempty"`
	Metadata  *TokenMetadata `json:"metadata,omitempty"`
}

// CredSSH represents an SSH credential.
type CredSSH struct {
	KnownHost  string `json:"known_host"`
	PrivateKey string `json:"private_key,omitempty"`
}

// CredGitHubApp represents a GitHub App credential
type CredGitHubApp struct {
	ID        string `json:"id"`
	URLPrefix string `json:"urlprefix"`
}

// Credentials is the full credentials payload returned by the GET endpoint.
type Credentials struct {
	Tokens     map[string]CredToken     `json:"tokens"`
	SSHCreds   []CredSSH                `json:"sshcreds"`
	GitHubApps map[string]CredGitHubApp `json:"githubApps"`
}

// CredentialsInfoResponse is the top-level response from the GET credentials endpoint.
type CredentialsInfoResponse struct {
	Success bool        `json:"success"`
	Creds   Credentials `json:"creds"`
	Message string      `json:"message,omitempty"`
}

// StoreToken is the token format expected by POST /app/config/credentials/store (old API).
type StoreToken struct {
	ID        string `json:"id"`
	URLPrefix string `json:"urlprefix"`
	Value     string `json:"value,omitempty"`
}

// StoreGitHubApp is the GitHub App format expected by the old store endpoint.
type StoreGitHubApp struct {
	ID         string `json:"id"`
	URLPrefix  string `json:"urlprefix"`
	PrivateKey string `json:"privateKey,omitempty"`
}

// StoreCredentials is the payload sent to POST /app/config/credentials/store (old API).
type StoreCredentials struct {
	Tokens     map[string]StoreToken     `json:"tokens"`
	SSHCreds   []CredSSH                 `json:"sshcreds"`
	GitHubApps map[string]StoreGitHubApp `json:"githubApps"`
}

// CredentialsStoreResponse is the response from credential write endpoints.
type CredentialsStoreResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// tryOldThenNew tries the old API first; if it fails, tries the new API.
func tryOldThenNew(oldFn, newFn func() error) error {
	if err := oldFn(); err != nil {
		return newFn()
	}
	return nil
}

// newTokenUpsert is the per-token value in POST/PUT /app/config/credentials.
type newTokenUpsert struct {
	ID        string `json:"id"`
	URLPrefix string `json:"urlprefix"`
	Value     string `json:"value,omitempty"`
}

// newGitHubAppUpsert is the per-app value in POST/PUT /app/config/credentials.
type newGitHubAppUpsert struct {
	ID         string `json:"id"`
	URLPrefix  string `json:"urlprefix"`
	PrivateKey string `json:"privateKey,omitempty"`
}

// newAddCredentialsRequest is the body for POST /app/config/credentials.
type newAddCredentialsRequest struct {
	Tokens     map[string]newTokenUpsert     `json:"tokens,omitempty"`
	GitHubApps map[string]newGitHubAppUpsert `json:"githubApps,omitempty"`
}

// newUpdateCredentialsRequest is the body for PUT /app/config/credentials.
// sshcreds performs a full replacement of all SSH credentials.
type newUpdateCredentialsRequest struct {
	Tokens     map[string]newTokenUpsert     `json:"tokens,omitempty"`
	GitHubApps map[string]newGitHubAppUpsert `json:"githubApps,omitempty"`
	SSHCreds   []CredSSH                     `json:"sshcreds,omitempty"`
}

// newDeleteCredentialsRequest is the body for DELETE /app/config/credentials.
type newDeleteCredentialsRequest struct {
	Tokens     []string `json:"tokens,omitempty"`
	GitHubApps []string `json:"githubApps,omitempty"`
}

// API paths for credentials endpoints.
const (
	credentialsPath      = "/app/config/credentials"       // new API (v26.2.0+)
	credentialsInfoPath  = "/app/config/credentials/info"  // old API GET
	credentialsStorePath = "/app/config/credentials/store" // old API POST
)

// doFetchCredentials performs a GET against path and decodes the credentials response.
func doFetchCredentials(server, path string) (*Credentials, error) {
	token, err := ensureValidToken()
	if err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}
	req, err := http.NewRequest("GET", "https://"+server+path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.IDToken)
	req.Header.Set("Accept", "application/json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed (status %d): %s", resp.StatusCode, string(body))
	}
	var response CredentialsInfoResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	if !response.Success {
		return nil, fmt.Errorf("API request failed: %s", response.Message)
	}
	return &response.Creds, nil
}

// doFetchCredentialsDirect fetches credentials from the new API, which returns
// the Credentials object directly without a success/creds wrapper.
func doFetchCredentialsDirect(server string) (*Credentials, error) {
	token, err := ensureValidToken()
	if err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}
	req, err := http.NewRequest("GET", "https://"+server+credentialsPath, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.IDToken)
	req.Header.Set("Accept", "application/json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed (status %d): %s", resp.StatusCode, string(body))
	}
	var creds Credentials
	if err := json.Unmarshal(body, &creds); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return &creds, nil
}

// doWriteCredentials marshals payload and sends it with the given method to path.
func doWriteCredentials(server, method, path string, payload interface{}) error {
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}
	reqBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}
	req, err := http.NewRequest(method, "https://"+server+path, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.IDToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("request failed (status %d): %s", resp.StatusCode, string(respBody))
	}
	var response CredentialsStoreResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if !response.Success {
		return fmt.Errorf("API request failed: %s", response.Message)
	}
	return nil
}

// fetchCredentials fetches credentials, trying the old API first and falling
// back to the new API on failure.
func fetchCredentials(server string) (*Credentials, error) {
	creds, err := doFetchCredentials(server, credentialsInfoPath)
	if err != nil {
		return doFetchCredentialsDirect(server)
	}
	return creds, nil
}

// buildStoreCredentials converts a Credentials (GET response) into a
// StoreCredentials payload for the old API, omitting existing token/key values
// so masked server-side values are not re-sent.
func buildStoreCredentials(creds *Credentials) *StoreCredentials {
	store := &StoreCredentials{
		Tokens:     make(map[string]StoreToken),
		SSHCreds:   make([]CredSSH, 0),
		GitHubApps: make(map[string]StoreGitHubApp),
	}
	for name, tok := range creds.Tokens {
		store.Tokens[name] = StoreToken{ID: name, URLPrefix: tok.URLPrefix}
	}
	for _, ssh := range creds.SSHCreds {
		store.SSHCreds = append(store.SSHCreds, CredSSH{KnownHost: ssh.KnownHost})
	}
	for id, app := range creds.GitHubApps {
		store.GitHubApps[id] = StoreGitHubApp{ID: id, URLPrefix: app.URLPrefix}
	}
	return store
}

// sshHostList returns the current SSH credentials with private keys stripped.
// Used as the starting point for full-replacement PUT requests in the new API.
func sshHostList(creds *Credentials) []CredSSH {
	list := make([]CredSSH, len(creds.SSHCreds))
	for i, s := range creds.SSHCreds {
		list[i] = CredSSH{KnownHost: s.KnownHost}
	}
	return list
}

// toDataURL encodes raw bytes as a data URL.
func toDataURL(data []byte) string {
	return "data:application/octet-stream;base64," + base64.StdEncoding.EncodeToString(data)
}

// readFileAsDataURL reads a file and returns it as a data URL (base64-encoded).
func readFileAsDataURL(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file %s: %w", path, err)
	}
	return toDataURL(data), nil
}

// readJSONInput returns raw JSON from the first positional arg or from stdin.
func readJSONInput(args []string) ([]byte, error) {
	if len(args) > 0 && args[0] != "-" {
		return []byte(args[0]), nil
	}
	return io.ReadAll(os.Stdin)
}

// resolvePrivateKey returns a data-URL encoded private key from either an
// inline PEM string or a file path. Returns "" when neither is provided.
func resolvePrivateKey(privateKey, privateKeyFile string) (string, error) {
	switch {
	case privateKeyFile != "":
		return readFileAsDataURL(privateKeyFile)
	case privateKey != "":
		return toDataURL([]byte(privateKey)), nil
	default:
		return "", nil
	}
}

// AddTokenInput is the JSON schema accepted by "jh admin credential add token".
type AddTokenInput struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Value string `json:"value"`
}

// AddSSHInput is the JSON schema accepted by "jh admin credential add ssh".
type AddSSHInput struct {
	HostKey        string `json:"host_key"`
	PrivateKey     string `json:"private_key"`
	PrivateKeyFile string `json:"private_key_file"`
}

// AddGitHubAppInput is the JSON schema accepted by "jh admin credential add github-app".
type AddGitHubAppInput struct {
	AppID          string `json:"app_id"`
	URL            string `json:"url"`
	PrivateKey     string `json:"private_key"`
	PrivateKeyFile string `json:"private_key_file"`
}

// UpdateTokenInput is the JSON schema accepted by "jh admin credential update token".
type UpdateTokenInput struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Value string `json:"value"`
}

// UpdateSSHInput is the JSON schema accepted by "jh admin credential update ssh".
type UpdateSSHInput struct {
	Index          int    `json:"index"`
	HostKey        string `json:"host_key"`
	PrivateKey     string `json:"private_key"`
	PrivateKeyFile string `json:"private_key_file"`
}

// UpdateGitHubAppInput is the JSON schema accepted by "jh admin credential update github-app".
type UpdateGitHubAppInput struct {
	AppID          string `json:"app_id"`
	URL            string `json:"url"`
	PrivateKey     string `json:"private_key"`
	PrivateKeyFile string `json:"private_key_file"`
}

func listCredentials(server string, verbose bool) error {
	creds, err := fetchCredentials(server)
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	// Tokens
	fmt.Printf("Tokens (%d total):\n\n", len(creds.Tokens))
	const indent = "  "
	if len(creds.Tokens) > 0 {
		if verbose {
			fmt.Fprintln(w, indent+"NAME\tURL\tVALUE\tACCOUNT\tEXPIRES\tSCOPES\tRATE LIMIT")
		} else {
			fmt.Fprintln(w, indent+"NAME\tURL\tVALUE")
		}
		for name, tok := range creds.Tokens {
			if verbose {
				account, expires, scopes, rateLimit := "", "", "", ""
				if tok.Metadata != nil {
					meta := tok.Metadata
					if meta.Success && meta.Data != nil {
						account = meta.Data.Login
						expires = meta.Data.Expires
						scopes = meta.Data.Scopes
						resetTime := time.Unix(meta.Data.RateLimitReset, 0).In(time.Local)
						rateLimit = fmt.Sprintf("%d/%d (resets %s)", meta.Data.RateLimitRemaining, meta.Data.RateLimitMax, resetTime.Format("2006-01-02 15:04:05 MST"))
					} else if meta.Message != "" {
						account = "error: " + meta.Message
					}
				}
				fmt.Fprintf(w, indent+"%s\t%s\t%s\t%s\t%s\t%s\t%s\n", name, tok.URLPrefix, tok.Value, account, expires, scopes, rateLimit)
			} else {
				fmt.Fprintf(w, indent+"%s\t%s\t%s\n", name, tok.URLPrefix, tok.Value)
			}
		}
		w.Flush()
	}

	// SSH Keys
	fmt.Printf("\nSSH Keys (%d total):\n\n", len(creds.SSHCreds))
	if len(creds.SSHCreds) > 0 {
		if verbose {
			fmt.Fprintln(w, indent+"#\tHOST KEY")
		} else {
			fmt.Fprintln(w, indent+"#\tHOST")
		}
		for i, ssh := range creds.SSHCreds {
			host := ssh.KnownHost
			if !verbose {
				if fields := strings.Fields(host); len(fields) > 0 {
					host = fields[0]
				}
			}
			fmt.Fprintf(w, indent+"%d\t%s\n", i+1, host)
		}
		w.Flush()
	}

	// GitHub Apps
	fmt.Printf("\nGitHub Apps (%d total):\n\n", len(creds.GitHubApps))
	if len(creds.GitHubApps) > 0 {
		fmt.Fprintln(w, indent+"APP ID\tURL")
		for id, app := range creds.GitHubApps {
			fmt.Fprintf(w, indent+"%s\t%s\n", id, app.URLPrefix)
		}
		w.Flush()
	}

	return nil
}

func addCredentialToken(server string, jsonData []byte) error {
	var input AddTokenInput
	if err := json.Unmarshal(jsonData, &input); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if input.Name == "" {
		return fmt.Errorf("missing required field: name")
	}
	if input.URL == "" {
		return fmt.Errorf("missing required field: url")
	}
	if input.Value == "" {
		return fmt.Errorf("missing required field: value")
	}

	creds, err := fetchCredentials(server)
	if err != nil {
		return err
	}
	if _, exists := creds.Tokens[input.Name]; exists {
		return fmt.Errorf("token with name %q already exists; remove it first before re-adding", input.Name)
	}

	err = tryOldThenNew(
		func() error {
			store := buildStoreCredentials(creds)
			store.Tokens[input.Name] = StoreToken{ID: input.Name, URLPrefix: input.URL, Value: input.Value}
			return doWriteCredentials(server, "POST", credentialsStorePath, store)
		},
		func() error {
			return doWriteCredentials(server, "POST", credentialsPath, &newAddCredentialsRequest{
				Tokens: map[string]newTokenUpsert{
					input.Name: {ID: input.Name, URLPrefix: input.URL, Value: input.Value},
				},
			})
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Token %q added successfully\n", input.Name)
	return nil
}

func addCredentialSSH(server string, jsonData []byte) error {
	var input AddSSHInput
	if err := json.Unmarshal(jsonData, &input); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if input.HostKey == "" {
		return fmt.Errorf("missing required field: host_key")
	}
	if input.PrivateKey != "" && input.PrivateKeyFile != "" {
		return fmt.Errorf("specify either private_key or private_key_file, not both")
	}

	creds, err := fetchCredentials(server)
	if err != nil {
		return err
	}
	encoded, err := resolvePrivateKey(input.PrivateKey, input.PrivateKeyFile)
	if err != nil {
		return err
	}
	newSSH := CredSSH{KnownHost: input.HostKey, PrivateKey: encoded}

	err = tryOldThenNew(
		func() error {
			store := buildStoreCredentials(creds)
			store.SSHCreds = append(store.SSHCreds, newSSH)
			return doWriteCredentials(server, "POST", credentialsStorePath, store)
		},
		func() error {
			// SSH uses full-replacement via PUT; append new entry to existing host list.
			return doWriteCredentials(server, "PUT", credentialsPath, &newUpdateCredentialsRequest{
				SSHCreds: append(sshHostList(creds), newSSH),
			})
		},
	)
	if err != nil {
		return err
	}
	fmt.Println("SSH credential added successfully")
	return nil
}

func addCredentialGitHubApp(server string, jsonData []byte) error {
	var input AddGitHubAppInput
	if err := json.Unmarshal(jsonData, &input); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if input.AppID == "" {
		return fmt.Errorf("missing required field: app_id")
	}
	if input.URL == "" {
		return fmt.Errorf("missing required field: url")
	}
	if input.PrivateKey != "" && input.PrivateKeyFile != "" {
		return fmt.Errorf("specify either private_key or private_key_file, not both")
	}

	creds, err := fetchCredentials(server)
	if err != nil {
		return err
	}
	if _, exists := creds.GitHubApps[input.AppID]; exists {
		return fmt.Errorf("GitHub App with ID %q already exists; remove it first before re-adding", input.AppID)
	}
	encoded, err := resolvePrivateKey(input.PrivateKey, input.PrivateKeyFile)
	if err != nil {
		return err
	}

	err = tryOldThenNew(
		func() error {
			store := buildStoreCredentials(creds)
			store.GitHubApps[input.AppID] = StoreGitHubApp{ID: input.AppID, URLPrefix: input.URL, PrivateKey: encoded}
			return doWriteCredentials(server, "POST", credentialsStorePath, store)
		},
		func() error {
			return doWriteCredentials(server, "POST", credentialsPath, &newAddCredentialsRequest{
				GitHubApps: map[string]newGitHubAppUpsert{
					input.AppID: {ID: input.AppID, URLPrefix: input.URL, PrivateKey: encoded},
				},
			})
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("GitHub App %q added successfully\n", input.AppID)
	return nil
}

func updateCredentialToken(server string, jsonData []byte) error {
	var input UpdateTokenInput
	if err := json.Unmarshal(jsonData, &input); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if input.Name == "" {
		return fmt.Errorf("missing required field: name")
	}
	if input.URL == "" && input.Value == "" {
		return fmt.Errorf("nothing to update: provide at least one of url or value")
	}

	creds, err := fetchCredentials(server)
	if err != nil {
		return err
	}
	existing, ok := creds.Tokens[input.Name]
	if !ok {
		return fmt.Errorf("token %q not found", input.Name)
	}

	err = tryOldThenNew(
		func() error {
			store := buildStoreCredentials(creds)
			entry := store.Tokens[input.Name]
			if input.URL != "" {
				entry.URLPrefix = input.URL
			}
			if input.Value != "" {
				entry.Value = input.Value
			}
			store.Tokens[input.Name] = entry
			return doWriteCredentials(server, "POST", credentialsStorePath, store)
		},
		func() error {
			urlPrefix := existing.URLPrefix
			if input.URL != "" {
				urlPrefix = input.URL
			}
			upsert := newTokenUpsert{ID: input.Name, URLPrefix: urlPrefix, Value: input.Value}
			return doWriteCredentials(server, "PUT", credentialsPath, &newUpdateCredentialsRequest{
				Tokens: map[string]newTokenUpsert{input.Name: upsert},
			})
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Token %q updated successfully\n", input.Name)
	return nil
}

func updateCredentialSSH(server string, jsonData []byte) error {
	var input UpdateSSHInput
	if err := json.Unmarshal(jsonData, &input); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if input.Index <= 0 {
		return fmt.Errorf("missing or invalid required field: index (must be >= 1)")
	}
	if input.HostKey == "" && input.PrivateKey == "" && input.PrivateKeyFile == "" {
		return fmt.Errorf("nothing to update: provide at least one of host_key, private_key, or private_key_file")
	}
	if input.PrivateKey != "" && input.PrivateKeyFile != "" {
		return fmt.Errorf("specify either private_key or private_key_file, not both")
	}

	creds, err := fetchCredentials(server)
	if err != nil {
		return err
	}
	idx := input.Index - 1
	if idx >= len(creds.SSHCreds) {
		return fmt.Errorf("SSH key #%d not found (only %d exist)", input.Index, len(creds.SSHCreds))
	}
	encoded, err := resolvePrivateKey(input.PrivateKey, input.PrivateKeyFile)
	if err != nil {
		return err
	}

	err = tryOldThenNew(
		func() error {
			store := buildStoreCredentials(creds)
			entry := store.SSHCreds[idx]
			if input.HostKey != "" {
				entry.KnownHost = input.HostKey
			}
			if encoded != "" {
				entry.PrivateKey = encoded
			}
			store.SSHCreds[idx] = entry
			return doWriteCredentials(server, "POST", credentialsStorePath, store)
		},
		func() error {
			updatedSSH := sshHostList(creds)
			if input.HostKey != "" {
				updatedSSH[idx].KnownHost = input.HostKey
			}
			if encoded != "" {
				updatedSSH[idx].PrivateKey = encoded
			}
			return doWriteCredentials(server, "PUT", credentialsPath, &newUpdateCredentialsRequest{
				SSHCreds: updatedSSH,
			})
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("SSH key #%d updated successfully\n", input.Index)
	return nil
}

func updateCredentialGitHubApp(server string, jsonData []byte) error {
	var input UpdateGitHubAppInput
	if err := json.Unmarshal(jsonData, &input); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if input.AppID == "" {
		return fmt.Errorf("missing required field: app_id")
	}
	if input.URL == "" && input.PrivateKey == "" && input.PrivateKeyFile == "" {
		return fmt.Errorf("nothing to update: provide at least one of url, private_key, or private_key_file")
	}
	if input.PrivateKey != "" && input.PrivateKeyFile != "" {
		return fmt.Errorf("specify either private_key or private_key_file, not both")
	}

	creds, err := fetchCredentials(server)
	if err != nil {
		return err
	}
	existing, ok := creds.GitHubApps[input.AppID]
	if !ok {
		return fmt.Errorf("GitHub App %q not found", input.AppID)
	}
	encoded, err := resolvePrivateKey(input.PrivateKey, input.PrivateKeyFile)
	if err != nil {
		return err
	}

	err = tryOldThenNew(
		func() error {
			store := buildStoreCredentials(creds)
			entry := store.GitHubApps[input.AppID]
			if input.URL != "" {
				entry.URLPrefix = input.URL
			}
			if encoded != "" {
				entry.PrivateKey = encoded
			}
			store.GitHubApps[input.AppID] = entry
			return doWriteCredentials(server, "POST", credentialsStorePath, store)
		},
		func() error {
			urlPrefix := existing.URLPrefix
			if input.URL != "" {
				urlPrefix = input.URL
			}
			upsert := newGitHubAppUpsert{ID: input.AppID, URLPrefix: urlPrefix, PrivateKey: encoded}
			return doWriteCredentials(server, "PUT", credentialsPath, &newUpdateCredentialsRequest{
				GitHubApps: map[string]newGitHubAppUpsert{input.AppID: upsert},
			})
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("GitHub App %q updated successfully\n", input.AppID)
	return nil
}

func deleteCredentialToken(server, name string) error {
	creds, err := fetchCredentials(server)
	if err != nil {
		return err
	}
	if _, ok := creds.Tokens[name]; !ok {
		return fmt.Errorf("token %q not found", name)
	}

	err = tryOldThenNew(
		func() error {
			store := buildStoreCredentials(creds)
			delete(store.Tokens, name)
			return doWriteCredentials(server, "POST", credentialsStorePath, store)
		},
		func() error {
			return doWriteCredentials(server, "DELETE", credentialsPath, &newDeleteCredentialsRequest{
				Tokens: []string{name},
			})
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Token %q deleted successfully\n", name)
	return nil
}

func deleteCredentialSSH(server string, index int) error {
	creds, err := fetchCredentials(server)
	if err != nil {
		return err
	}
	idx := index - 1
	if idx < 0 || idx >= len(creds.SSHCreds) {
		return fmt.Errorf("SSH key #%d not found (only %d exist)", index, len(creds.SSHCreds))
	}

	hosts := sshHostList(creds)
	updatedSSH := append(hosts[:idx], hosts[idx+1:]...)

	err = tryOldThenNew(
		func() error {
			store := buildStoreCredentials(creds)
			store.SSHCreds = updatedSSH
			return doWriteCredentials(server, "POST", credentialsStorePath, store)
		},
		func() error {
			return doWriteCredentials(server, "PUT", credentialsPath, &newUpdateCredentialsRequest{
				SSHCreds: updatedSSH,
			})
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("SSH key #%d deleted successfully\n", index)
	return nil
}

func deleteCredentialGitHubApp(server, appID string) error {
	creds, err := fetchCredentials(server)
	if err != nil {
		return err
	}
	if _, ok := creds.GitHubApps[appID]; !ok {
		return fmt.Errorf("GitHub App %q not found", appID)
	}

	err = tryOldThenNew(
		func() error {
			store := buildStoreCredentials(creds)
			delete(store.GitHubApps, appID)
			return doWriteCredentials(server, "POST", credentialsStorePath, store)
		},
		func() error {
			return doWriteCredentials(server, "DELETE", credentialsPath, &newDeleteCredentialsRequest{
				GitHubApps: []string{appID},
			})
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("GitHub App %q deleted successfully\n", appID)
	return nil
}
