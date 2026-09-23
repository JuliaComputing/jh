package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// JuliaHub's search service is reachable at POST /search/v1/{code,sym,docs}
// (JSON body, `{"results": [...], "truncated": bool}` on 200, a structured
// `{"code","message","details"}` on any failure). Older installs only have the
// legacy routes POST /search/{code,sym,docs}, which answer 200 for every outcome
// with `{"success": bool, "data": <results or error string>}`. The client calls
// v1 first and falls back to the legacy route when v1 is absent (404/405).
const (
	searchV1Prefix     = "/search/v1/"
	searchLegacyPrefix = "/search/"
)

// searchKind selects the search endpoint.
type searchKind string

const (
	searchCode searchKind = "code"
	searchSym  searchKind = "sym"
	searchDocs searchKind = "docs"
)

// Valid values for the symbol search `types` and `usages` filters.
var (
	symbolTypes  = []string{"function", "type", "macro", "module"}
	symbolUsages = []string{"define", "use"}
)

// codeSearchRequest is the body of POST /search/v1/code.
type codeSearchRequest struct {
	Pattern    string   `json:"pattern"`
	Package    []string `json:"package,omitempty"`
	Registry   []string `json:"registry,omitempty"`
	MaxResults int      `json:"maxresults,omitempty"`
	PathFilter string   `json:"pathfilter,omitempty"`
	IgnoreCase bool     `json:"ignorecase,omitempty"`
}

// symbolSearchRequest is the body of POST /search/v1/sym.
type symbolSearchRequest struct {
	Pattern    string   `json:"pattern"`
	Package    []string `json:"package,omitempty"`
	Registry   []string `json:"registry,omitempty"`
	MaxResults int      `json:"maxresults,omitempty"`
	Types      []string `json:"types,omitempty"`
	Usages     []string `json:"usages,omitempty"`
}

// docsSearchRequest is the body of POST /search/v1/docs.
type docsSearchRequest struct {
	Pattern      string   `json:"pattern"`
	Package      []string `json:"package,omitempty"`
	Registry     []string `json:"registry,omitempty"`
	MaxResults   int      `json:"maxresults,omitempty"`
	Threshold    *float64 `json:"threshold,omitempty"`
	StrictPhrase bool     `json:"strictphrase,omitempty"`
}

// searchResponse is the 200 body of the v1 endpoints.
type searchResponse struct {
	Results   json.RawMessage `json:"results"`
	Truncated bool            `json:"truncated"`
}

// searchErrorResponse is the non-2xx body of the v1 endpoints.
type searchErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// legacySearchResponse is the envelope returned by the pre-v1 routes.
type legacySearchResponse struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
}

// searchError is a failed search call. Code is the server's stable error id
// (e.g. invalid_pattern, invalid_registry), or "legacy_error" for a
// `success:false` legacy envelope; it is empty when the body could not be
// decoded, in which case Message holds the raw body text.
type searchError struct {
	Status  int
	Code    string
	Message string
	Details any
}

func (e *searchError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("HTTP %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Hint returns a follow-up line for the user when the error details carry
// something actionable — currently the `known` registry list the server sends
// with invalid_registry. Empty when there is nothing to add.
func (e *searchError) Hint() string {
	details, ok := e.Details.(map[string]any)
	if !ok {
		return ""
	}
	known, ok := details["known"].([]any)
	if !ok || len(known) == 0 {
		return ""
	}
	names := make([]string, 0, len(known))
	for _, k := range known {
		names = append(names, fmt.Sprint(k))
	}
	return "known registries: " + strings.Join(names, ", ")
}

// printSearchError writes err (and its hint, if any) to w, one line each.
func printSearchError(w io.Writer, err error) {
	fmt.Fprintf(w, "%v\n", err)
	var se *searchError
	if errors.As(err, &se) {
		if hint := se.Hint(); hint != "" {
			fmt.Fprintln(w, hint)
		}
	}
}

// searchBaseURL turns the --server value into the URL the search paths are
// appended to. An explicit http:// or https:// URL is used verbatim (minus a
// trailing slash) so the commands can be pointed at a local search service;
// anything else goes through the usual server normalization.
func searchBaseURL(server string) string {
	if strings.HasPrefix(server, "http://") || strings.HasPrefix(server, "https://") {
		return strings.TrimRight(server, "/")
	}
	return "https://" + normalizeServer(server)
}

// isLocalSearchBaseURL reports whether baseURL is a plain-http endpoint, i.e. a
// local development service rather than a JuliaHub install.
func isLocalSearchBaseURL(baseURL string) bool {
	return strings.HasPrefix(baseURL, "http://")
}

// --- request builders (pure; unit-tested without HTTP) ---

func validateSearchCommon(pattern string, limit int) error {
	if strings.TrimSpace(pattern) == "" {
		return fmt.Errorf("search pattern must not be empty")
	}
	if limit < 0 {
		return fmt.Errorf("--limit must be a positive integer")
	}
	return nil
}

func buildCodeSearchRequest(pattern string, packages, registries []string, pathFilter string, ignoreCase bool, limit int) (codeSearchRequest, error) {
	if err := validateSearchCommon(pattern, limit); err != nil {
		return codeSearchRequest{}, err
	}
	return codeSearchRequest{
		Pattern:    pattern,
		Package:    packages,
		Registry:   registries,
		MaxResults: limit,
		PathFilter: pathFilter,
		IgnoreCase: ignoreCase,
	}, nil
}

// validateChoices checks every value in got against allowed (case-sensitive),
// returning a usage error naming the flag and the valid values otherwise.
func validateChoices(flag string, got, allowed []string) error {
	for _, v := range got {
		ok := false
		for _, a := range allowed {
			if v == a {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("invalid --%s %q: must be one of %s", flag, v, strings.Join(allowed, ", "))
		}
	}
	return nil
}

func buildSymbolSearchRequest(pattern string, packages, registries, types, usages []string, limit int) (symbolSearchRequest, error) {
	if err := validateSearchCommon(pattern, limit); err != nil {
		return symbolSearchRequest{}, err
	}
	if err := validateChoices("type", types, symbolTypes); err != nil {
		return symbolSearchRequest{}, err
	}
	if err := validateChoices("usage", usages, symbolUsages); err != nil {
		return symbolSearchRequest{}, err
	}
	return symbolSearchRequest{
		Pattern:    pattern,
		Package:    packages,
		Registry:   registries,
		MaxResults: limit,
		Types:      types,
		Usages:     usages,
	}, nil
}

func buildDocsSearchRequest(pattern string, packages, registries []string, threshold *float64, strictPhrase bool, limit int) (docsSearchRequest, error) {
	if err := validateSearchCommon(pattern, limit); err != nil {
		return docsSearchRequest{}, err
	}
	return docsSearchRequest{
		Pattern:      pattern,
		Package:      packages,
		Registry:     registries,
		MaxResults:   limit,
		Threshold:    threshold,
		StrictPhrase: strictPhrase,
	}, nil
}

// --- response decoding (pure) ---

// decodeSearchResponse interprets a v1 reply. A 2xx body is
// `{"results": [...], "truncated": bool}`; anything else is a *searchError,
// decoded from the structured error body when possible and carrying the
// trimmed raw body as Message otherwise.
func decodeSearchResponse(status int, body []byte) (json.RawMessage, bool, error) {
	if status < 200 || status > 299 {
		return nil, false, decodeSearchError(status, body)
	}
	var resp searchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, false, fmt.Errorf("failed to parse search response: %w", err)
	}
	if len(resp.Results) == 0 || string(resp.Results) == "null" {
		resp.Results = json.RawMessage("[]")
	}
	return resp.Results, resp.Truncated, nil
}

func decodeSearchError(status int, body []byte) *searchError {
	var er searchErrorResponse
	if err := json.Unmarshal(body, &er); err == nil && er.Code != "" {
		return &searchError{Status: status, Code: er.Code, Message: er.Message, Details: er.Details}
	}
	return &searchError{Status: status, Message: strings.TrimSpace(string(body))}
}

// decodeLegacySearchResponse interprets the pre-v1 `{"success","data"}`
// envelope (always delivered with HTTP 200). On success data is the results
// array; on failure it is the error string, surfaced as a *searchError with
// Code "legacy_error". The legacy API has no truncation flag.
func decodeLegacySearchResponse(body []byte) (json.RawMessage, bool, error) {
	var env legacySearchResponse
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, false, fmt.Errorf("failed to parse legacy search response: %w", err)
	}
	if !env.Success {
		msg := strings.TrimSpace(string(env.Data))
		var s string
		if err := json.Unmarshal(env.Data, &s); err == nil {
			msg = s
		}
		return nil, false, &searchError{Status: http.StatusOK, Code: "legacy_error", Message: msg}
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		env.Data = json.RawMessage("[]")
	}
	return env.Data, false, nil
}

// --- HTTP ---

// doSearchRequest POSTs body as JSON to baseURL+path and returns the status
// and body without interpreting them. An empty token sends no Authorization
// header (local development against a plain-http service).
func doSearchRequest(client *http.Client, baseURL, path, token string, body []byte) (int, []byte, error) {
	req, err := http.NewRequest("POST", baseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("failed to create request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("failed to read response: %w", err)
	}
	return resp.StatusCode, respBody, nil
}

// postSearchWithToken runs one search against baseURL: the v1 route first,
// then the legacy route when v1 answers 404/405. It is the testable core of
// postSearch (token and client are injected).
func postSearchWithToken(client *http.Client, baseURL string, kind searchKind, token string, body any) (json.RawMessage, bool, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, false, fmt.Errorf("failed to marshal request: %w", err)
	}

	status, respBody, err := doSearchRequest(client, baseURL, searchV1Prefix+string(kind), token, payload)
	if err != nil {
		return nil, false, err
	}
	if status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
		return decodeSearchResponse(status, respBody)
	}

	// v1 is absent on this install: try the legacy envelope route.
	legacyStatus, legacyBody, err := doSearchRequest(client, baseURL, searchLegacyPrefix+string(kind), token, payload)
	if err != nil {
		return nil, false, err
	}
	if legacyStatus == http.StatusNotFound || legacyStatus == http.StatusMethodNotAllowed {
		return nil, false, &searchError{
			Status:  legacyStatus,
			Code:    "search_unavailable",
			Message: fmt.Sprintf("search API not available on this server (HTTP %d from %s%s, HTTP %d from %s%s)", status, searchV1Prefix, kind, legacyStatus, searchLegacyPrefix, kind),
		}
	}
	if legacyStatus < 200 || legacyStatus > 299 {
		return nil, false, decodeSearchError(legacyStatus, legacyBody)
	}
	return decodeLegacySearchResponse(legacyBody)
}

// postSearch performs an authenticated search against baseURL (see
// searchBaseURL). Against a plain-http local service a missing stored token is
// tolerated: the request goes out unauthenticated with a note on stderr.
func postSearch(baseURL string, kind searchKind, body any) (json.RawMessage, bool, error) {
	var bearer string
	token, err := ensureValidToken()
	switch {
	case err == nil:
		bearer = token.IDToken
	case isLocalSearchBaseURL(baseURL):
		fmt.Fprintf(os.Stderr, "note: no stored JuliaHub token; sending an unauthenticated request to %s\n", baseURL)
	default:
		return nil, false, fmt.Errorf("authentication required: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	return postSearchWithToken(client, baseURL, kind, bearer, body)
}

// --- package / registry filter resolution ---

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// isUUID reports whether s is a canonical 8-4-4-4-12 hex UUID.
func isUUID(s string) bool {
	return uuidRe.MatchString(s)
}

// dedupeStrings returns values without duplicates, first occurrence wins,
// order preserved.
func dedupeStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	var out []string
	for _, v := range values {
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// packageUUIDsByName returns the UUIDs of the packages named name (exact,
// case-insensitive) from a package listing. A package appears once per
// registry with the same UUID, so the UUIDs are deduplicated; distinct
// packages that share a name each contribute their UUID.
func packageUUIDsByName(pkgs []RESTPackage, name string) []string {
	var uuids []string
	for _, p := range pkgs {
		if strings.EqualFold(p.Name, name) && p.UUID != "" {
			uuids = append(uuids, p.UUID)
		}
	}
	return dedupeStrings(uuids)
}

// lookupPackageUUIDs resolves one package name to its UUID(s): /packages/info
// first, then the GraphQL package search, mirroring getPackageInfo's fallback
// for installs without the REST endpoint.
func lookupPackageUUIDs(server, name string) ([]string, error) {
	pkgs, _, restErr := fetchRESTPackages(server, name, 100, 0, nil)
	if restErr == nil {
		if found := packageUUIDsByName(pkgs, name); len(found) > 0 {
			return found, nil
		}
		return nil, fmt.Errorf("package not found: %s", name)
	}
	gql, gqlErr := fetchGraphQLPackages(server, name, 100, 0, nil)
	if gqlErr != nil {
		return nil, fmt.Errorf("failed to look up package %q: %v; GraphQL fallback: %w", name, restErr, gqlErr)
	}
	if found := packageUUIDsByNameGQL(gql, name); len(found) > 0 {
		return found, nil
	}
	return nil, fmt.Errorf("package not found: %s", name)
}

// packageUUIDsByNameGQL is packageUUIDsByName over GraphQL package rows.
func packageUUIDsByNameGQL(pkgs []Package, name string) []string {
	var uuids []string
	for _, p := range pkgs {
		if strings.EqualFold(p.Name, name) && p.UUID != "" {
			uuids = append(uuids, p.UUID)
		}
	}
	return dedupeStrings(uuids)
}

// resolveSearchPackages turns --package values into the UUID list the search
// API expects. UUIDs pass through; names are looked up via /packages/info on
// server (a host, not a URL — the lookup is unavailable against a local
// http:// search service, where UUIDs must be given).
func resolveSearchPackages(server string, values []string, localOnly bool) ([]string, error) {
	var uuids []string
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if isUUID(v) {
			uuids = append(uuids, strings.ToLower(v))
			continue
		}
		if localOnly {
			return nil, fmt.Errorf("package %q cannot be resolved by name against a local http:// server; pass its UUID", v)
		}
		found, err := lookupPackageUUIDs(server, v)
		if err != nil {
			return nil, err
		}
		uuids = append(uuids, found...)
	}
	return dedupeStrings(uuids), nil
}

// --- output ---

// searchCodeResult / searchSymbolResult / searchDocsResult mirror the result
// objects of the three endpoints, for the human-readable tables. The JSON
// output path forwards the server's results untouched.
type searchCodeResult struct {
	File        string `json:"file"`
	Line        int    `json:"line"`
	Text        string `json:"text"`
	Package     string `json:"package"`
	Registry    string `json:"registry"`
	PackageName string `json:"packagename"`
}

type searchSymbolResult struct {
	File        string `json:"file"`
	Line        int    `json:"line"`
	Text        string `json:"text"`
	Package     string `json:"package"`
	Registry    string `json:"registry"`
	PackageName string `json:"packagename"`
	Usage       string `json:"usage"`
	Type        string `json:"type"`
}

type searchDocsSection struct {
	Page     string `json:"page"`
	Title    string `json:"title"`
	Category string `json:"category"`
	DocName  string `json:"docname"`
}

type searchDocsResult struct {
	Package     string              `json:"package"`
	PackageName string              `json:"packagename"`
	Version     string              `json:"version"`
	Registry    string              `json:"registry"`
	Score       float64             `json:"score"`
	Sections    []searchDocsSection `json:"sections"`
}

// oneLine collapses a match text onto a single trimmed line for table output.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func fileLine(file string, line int) string {
	if line > 0 {
		return fmt.Sprintf("%s:%d", file, line)
	}
	return file
}

func printSearchCount(w io.Writer, n int) {
	fmt.Fprintf(w, "Found %d result(s):\n\n", n)
}

func renderCodeResults(w io.Writer, raw json.RawMessage) error {
	var results []searchCodeResult
	if err := json.Unmarshal(raw, &results); err != nil {
		return fmt.Errorf("failed to parse code search results: %w", err)
	}
	if len(results) == 0 {
		fmt.Fprintln(w, "No results found")
		return nil
	}
	printSearchCount(w, len(results))
	fmt.Fprintf(w, "%-30s %-15s %-50s %s\n", "PACKAGE", "REGISTRY", "FILE:LINE", "TEXT")
	fmt.Fprintf(w, "%-30s %-15s %-50s %s\n", strings.Repeat("-", 30), strings.Repeat("-", 15), strings.Repeat("-", 50), strings.Repeat("-", 40))
	for _, r := range results {
		fmt.Fprintf(w, "%-30s %-15s %-50s %s\n", r.PackageName, r.Registry, fileLine(r.File, r.Line), oneLine(r.Text))
	}
	return nil
}

func renderSymbolResults(w io.Writer, raw json.RawMessage) error {
	var results []searchSymbolResult
	if err := json.Unmarshal(raw, &results); err != nil {
		return fmt.Errorf("failed to parse symbol search results: %w", err)
	}
	if len(results) == 0 {
		fmt.Fprintln(w, "No results found")
		return nil
	}
	printSearchCount(w, len(results))
	fmt.Fprintf(w, "%-30s %-15s %-8s %-6s %-50s %s\n", "PACKAGE", "REGISTRY", "TYPE", "USAGE", "FILE:LINE", "TEXT")
	fmt.Fprintf(w, "%-30s %-15s %-8s %-6s %-50s %s\n", strings.Repeat("-", 30), strings.Repeat("-", 15), strings.Repeat("-", 8), strings.Repeat("-", 6), strings.Repeat("-", 50), strings.Repeat("-", 40))
	for _, r := range results {
		fmt.Fprintf(w, "%-30s %-15s %-8s %-6s %-50s %s\n", r.PackageName, r.Registry, r.Type, r.Usage, fileLine(r.File, r.Line), oneLine(r.Text))
	}
	return nil
}

func renderDocsResults(w io.Writer, raw json.RawMessage) error {
	var results []searchDocsResult
	if err := json.Unmarshal(raw, &results); err != nil {
		return fmt.Errorf("failed to parse docs search results: %w", err)
	}
	if len(results) == 0 {
		fmt.Fprintln(w, "No results found")
		return nil
	}
	printSearchCount(w, len(results))
	fmt.Fprintf(w, "%-30s %-12s %-15s %-8s %s\n", "PACKAGE", "VERSION", "REGISTRY", "SCORE", "SECTION")
	fmt.Fprintf(w, "%-30s %-12s %-15s %-8s %s\n", strings.Repeat("-", 30), strings.Repeat("-", 12), strings.Repeat("-", 15), strings.Repeat("-", 8), strings.Repeat("-", 40))
	for _, r := range results {
		section := ""
		if len(r.Sections) > 0 {
			section = oneLine(r.Sections[0].Title)
			if len(r.Sections) > 1 {
				section += fmt.Sprintf(" (+%d more)", len(r.Sections)-1)
			}
		}
		fmt.Fprintf(w, "%-30s %-12s %-15s %-8.3f %s\n", r.PackageName, r.Version, r.Registry, r.Score, section)
	}
	return nil
}

// writeJSONResults writes the machine-readable document `{"results": <raw>,
// "truncated": bool}`, 2-space indented, with a trailing newline. The results
// array is the server's, re-indented but otherwise untouched.
func writeJSONResults(w io.Writer, results json.RawMessage, truncated bool) error {
	if len(results) == 0 {
		results = json.RawMessage("[]")
	}
	doc := struct {
		Results   json.RawMessage `json:"results"`
		Truncated bool            `json:"truncated"`
	}{results, truncated}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode results: %w", err)
	}
	_, err = w.Write(append(out, '\n'))
	return err
}

// runSearch executes one search and writes the output: with asJSON only the
// JSON document goes to stdout; otherwise the table goes to stdout and a
// truncation notice, if any, to stderr.
func runSearch(baseURL string, kind searchKind, body any, asJSON bool, render func(io.Writer, json.RawMessage) error) error {
	results, truncated, err := postSearch(baseURL, kind, body)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSONResults(os.Stdout, results, truncated)
	}
	if err := render(os.Stdout, results); err != nil {
		return err
	}
	if truncated {
		fmt.Fprintln(os.Stderr, "note: results truncated (search stopped early)")
	}
	return nil
}
