package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

//go:embed package_search.gql package_search_count.gql
var packageSearchFS embed.FS

type PackageMetadata struct {
	DocsHostedURI string   `json:"docshosteduri"`
	Versions      []string `json:"versions"`
	Description   string   `json:"description"`
	DocsLink      string   `json:"docslink"`
	Repo          string   `json:"repo"`
	Owner         string   `json:"owner"`
	Tags          []string `json:"tags"`
	StarCount     int      `json:"starcount"`
}

type PackageRegistryMap struct {
	Version    string `json:"version"`
	RegistryID int    `json:"registryid"`
	Status     bool   `json:"status"`
	IsApp      bool   `json:"isapp"`
	IsJSML     *bool  `json:"isjsml"`
}

type PackageFailure struct {
	PackageVersion string `json:"package_version"`
}

type PackageSearchResponse struct {
	Data struct {
		PackageSearch    []Package `json:"package_search"`
		PackageAggregate struct {
			Aggregate struct {
				Count int `json:"count"`
			} `json:"aggregate"`
		} `json:"package_search_aggregate"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type PackageDependency struct {
	Direct   bool     `json:"direct"`
	Name     string   `json:"name"`
	UUID     string   `json:"uuid"`
	Versions []string `json:"versions"`
	Registry string   `json:"registry"`
	Slug     string   `json:"slug"`
}

type PackageDocsResponse struct {
	Name         string              `json:"name"`
	Version      string              `json:"version"`
	Dependencies []PackageDependency `json:"deps"`
}

type Package struct {
	Name        string              `json:"name"`
	Owner       string              `json:"owner"`
	Slug        *string             `json:"slug"`
	License     string              `json:"license"`
	IsApp       bool                `json:"isapp"`
	Score       float64             `json:"score"`
	RegistryMap *PackageRegistryMap `json:"registrymap"`
	Metadata    *PackageMetadata    `json:"metadata"`
	UUID        string              `json:"uuid"`
	Installed   bool                `json:"installed"`
	Failures    []PackageFailure    `json:"failures"`
}

type RESTPackage struct {
	Name                   string   `json:"name"`
	UUID                   string   `json:"uuid"`
	Registry               string   `json:"registry"`
	Description            string   `json:"description"`
	StargazersCount        int      `json:"stargazers_count"`
	SourceURL              string   `json:"source_url"`
	JHubDocsURL            string   `json:"jhub_docs_url"`
	LatestStableVersion    string   `json:"latest_stable_version"`
	DetectedSourceLicenses []string `json:"detected_source_licenses"`
	Downloads              struct {
		Count int `json:"count"`
	} `json:"downloads"`
	Tags []string `json:"tags"`
}

type PackageRESTListResponse struct {
	Packages []RESTPackage `json:"packages"`
	Meta     struct {
		Total int `json:"total"`
	} `json:"meta"`
}

type PackageSearchParams struct {
	Server        string
	Search        string
	Limit         int
	Offset        int
	RegistryIDs   []int
	RegistryNames []string
	Verbose       bool
	JSON          bool // print {"results": [...], "total": N} instead of the table
}

// packageInfo is a common display struct used by both REST and GraphQL paths.
// The json tags define the --json output of `jh package search` /
// `jh search packages`.
type packageInfo struct {
	Name        string   `json:"name"`
	UUID        string   `json:"uuid"`
	Owner       string   `json:"owner"`
	Registry    string   `json:"registry"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	SourceURL   string   `json:"source_url"`
	Tags        []string `json:"tags"`
	Stars       int      `json:"stars"`
	DocsURL     string   `json:"docs_url"`
	License     string   `json:"license"`
	IsApp       bool     `json:"is_app"`
	Score       float64  `json:"score"`
	Status      string   `json:"status"`
}

// writePackagesJSON writes `{"results": [...], "total": N}`, 2-space indented
// with a trailing newline — the only thing on stdout in --json mode. Tags is
// always an array (never null) so consumers can iterate it unconditionally.
func writePackagesJSON(w io.Writer, pkgs []packageInfo, total int) error {
	results := make([]packageInfo, len(pkgs))
	for i, p := range pkgs {
		if p.Tags == nil {
			p.Tags = []string{}
		}
		results[i] = p
	}
	doc := struct {
		Results []packageInfo `json:"results"`
		Total   int           `json:"total"`
	}{results, total}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode packages: %w", err)
	}
	_, err = w.Write(append(out, '\n'))
	return err
}

// emitPackages prints the search outcome in the format params ask for.
func emitPackages(params PackageSearchParams, infos []packageInfo, total int) error {
	if params.JSON {
		return writePackagesJSON(os.Stdout, infos, total)
	}
	printPackages(infos, total, params.Verbose)
	return nil
}

func printPackages(pkgs []packageInfo, total int, verbose bool) {
	if len(pkgs) == 0 {
		fmt.Println("No packages found")
		return
	}

	if total > len(pkgs) {
		fmt.Printf("Showing %d of %d package(s):\n\n", len(pkgs), total)
	} else {
		fmt.Printf("Found %d package(s):\n\n", len(pkgs))
	}

	if !verbose {
		fmt.Printf("%-30s %-20s %-20s %-12s %s\n", "NAME", "REGISTRY", "OWNER", "VERSION", "DESCRIPTION")
		fmt.Printf("%-30s %-20s %-20s %-12s %s\n", strings.Repeat("-", 30), strings.Repeat("-", 20), strings.Repeat("-", 30), strings.Repeat("-", 12), strings.Repeat("-", 50))
	}

	for _, pkg := range pkgs {
		if verbose {
			fmt.Printf("Name: %s\n", pkg.Name)
			fmt.Printf("UUID: %s\n", pkg.UUID)
			if pkg.Registry != "" {
				fmt.Printf("Registry: %s\n", pkg.Registry)
			}
			if pkg.Owner != "" {
				fmt.Printf("Owner: %s\n", pkg.Owner)
			}
			if pkg.Description != "" {
				fmt.Printf("Description: %s\n", pkg.Description)
			}
			if pkg.SourceURL != "" {
				fmt.Printf("Repository: %s\n", pkg.SourceURL)
			}
			if len(pkg.Tags) > 0 {
				fmt.Printf("Tags: %s\n", strings.Join(pkg.Tags, ", "))
			}
			if pkg.Stars > 0 {
				fmt.Printf("Stars: %d\n", pkg.Stars)
			}
			if pkg.DocsURL != "" {
				fmt.Printf("Documentation: %s\n", pkg.DocsURL)
			}
			if pkg.License != "" {
				fmt.Printf("License: %s\n", pkg.License)
			}
			if pkg.Version != "" {
				fmt.Printf("Latest Version: %s\n", pkg.Version)
			}
			if pkg.Status != "" {
				fmt.Printf("Status: %s\n", pkg.Status)
			}
			if pkg.IsApp {
				fmt.Printf("Type: Application\n")
			}
			if pkg.Score != 0 {
				fmt.Printf("Score: %.2f\n", pkg.Score)
			}
		} else {
			fmt.Printf("%-30s %-20s %-20s", pkg.Name, pkg.Registry, pkg.Owner)
			if pkg.Version != "" {
				fmt.Printf(" v%-10s", pkg.Version)
			} else {
				fmt.Printf(" %-12s", "N/A")
			}
			if pkg.Description != "" {
				desc := pkg.Description
				if len(desc) > 50 {
					desc = desc[:50] + "..."
				}
				fmt.Printf("%s", desc)
			}
			fmt.Printf("\n")
		}
		fmt.Println()
	}
}

// restToInfo converts a RESTPackage to the common packageInfo display struct.
func restToInfo(p RESTPackage) packageInfo {
	return packageInfo{
		Name:        p.Name,
		UUID:        p.UUID,
		Registry:    p.Registry,
		Version:     p.LatestStableVersion,
		Description: p.Description,
		SourceURL:   p.SourceURL,
		Tags:        p.Tags,
		Stars:       p.StargazersCount,
		DocsURL:     p.JHubDocsURL,
		License:     strings.Join(p.DetectedSourceLicenses, ", "),
	}
}

// gqlToInfo converts a GraphQL Package to the common packageInfo display struct.
func gqlToInfo(p Package, registryIDToName map[int]string) packageInfo {
	info := packageInfo{
		Name:    p.Name,
		UUID:    p.UUID,
		Owner:   p.Owner,
		License: p.License,
		IsApp:   p.IsApp,
		Score:   p.Score,
	}
	if p.Metadata != nil {
		info.Description = p.Metadata.Description
		info.SourceURL = p.Metadata.Repo
		info.Tags = p.Metadata.Tags
		info.Stars = p.Metadata.StarCount
		info.DocsURL = p.Metadata.DocsLink
	}
	if p.RegistryMap != nil {
		info.Registry = registryIDToName[p.RegistryMap.RegistryID]
		info.Version = p.RegistryMap.Version
		if p.RegistryMap.Status {
			info.Status = "Active"
		} else {
			info.Status = "Inactive"
		}
	}
	return info
}

// buildRegistryIDToName creates a registry ID → name lookup from parallel slices.
func buildRegistryIDToName(ids []int, names []string) map[int]string {
	m := make(map[int]string, len(ids))
	for i, id := range ids {
		if i < len(names) {
			m[id] = names[i]
		}
	}
	return m
}

// packagesInfoPath is the package listing endpoint behind the web UI's package
// pages. It pages with pagination[page]/[per_page] (1-based pages, at most
// packagesInfoMaxPerPage rows each) rather than limit/offset.
const (
	packagesInfoPath       = "/api/v1/ui/packages/info"
	packagesInfoMaxPerPage = 100
)

// restPage is one page request against packagesInfoPath.
type restPage struct {
	Page    int
	PerPage int
}

// planRESTPages maps the CLI's limit/offset window onto the pages that cover
// it, all of one size: min(limit, packagesInfoMaxPerPage). The window starts
// skip rows into the first page; an offset that is not a multiple of the page
// size costs one extra page.
func planRESTPages(limit, offset int) (pages []restPage, skip int) {
	if limit <= 0 {
		limit = 10
	}
	if offset < 0 {
		offset = 0
	}
	per := min(limit, packagesInfoMaxPerPage)
	first := offset / per
	last := (offset + limit - 1) / per
	for p := first; p <= last; p++ {
		pages = append(pages, restPage{Page: p + 1, PerPage: per})
	}
	return pages, offset - first*per
}

// buildPackagesInfoQuery returns the query string for one page. With a search
// term the results are ordered by the server's relevance score (exact name >
// prefix > substring, then stars), like the GraphQL search; without one, by
// stars (the server's default). Name breaks ties: packages of one repository
// share a score, and without a tiebreak their order changes between requests,
// so consecutive pages could repeat or skip rows.
func buildPackagesInfoQuery(search string, registryNames []string, page restPage) url.Values {
	q := url.Values{}
	q.Set("sorts[0]", "-stargazers_count")
	if search != "" {
		q.Set("name", search)
		q.Set("sorts[0]", "-score")
	}
	q.Set("sorts[1]", "name")
	if len(registryNames) > 0 {
		q.Set("registries", strings.Join(registryNames, ","))
	}
	q.Set("pagination[type]", "offset")
	q.Set("pagination[page]", fmt.Sprintf("%d", page.Page))
	q.Set("pagination[per_page]", fmt.Sprintf("%d", page.PerPage))
	return q
}

// fetchRESTPackages lists packages matching search via packagesInfoPath and
// returns the limit/offset window and the total match count.
//
// The listing is public on JuliaHub.com-style installs, so the stored login's
// token is sent only when it belongs to server (tokenForServer); enterprise
// installs answer 401 without one.
func fetchRESTPackages(server, search string, limit, offset int, registryNames []string) ([]RESTPackage, int, error) {
	token := tokenForServer(server)
	client := &http.Client{Timeout: 30 * time.Second}
	pkgs, total, err := fetchRESTPackagesFrom(client, "https://"+server, token, search, limit, offset, registryNames)
	if err != nil && token == "" && strings.Contains(err.Error(), "status 401") {
		err = fmt.Errorf("%w (this server requires a login: run 'jh auth login -s %s')", err, server)
	}
	return pkgs, total, err
}

// fetchRESTPackagesFrom is the testable core of fetchRESTPackages (client,
// base URL and token are injected; an empty token sends no Authorization).
func fetchRESTPackagesFrom(client *http.Client, baseURL, token, search string, limit, offset int, registryNames []string) ([]RESTPackage, int, error) {
	if limit <= 0 {
		limit = 10
	}
	pages, skip := planRESTPages(limit, offset)
	var all []RESTPackage
	total := 0
	for _, page := range pages {
		pkgs, t, err := fetchPackagesInfoPage(client, baseURL, token, buildPackagesInfoQuery(search, registryNames, page))
		if err != nil {
			return nil, 0, err
		}
		total = t
		all = append(all, pkgs...)
		if len(pkgs) < page.PerPage {
			break // last page
		}
	}
	if skip >= len(all) {
		return []RESTPackage{}, total, nil
	}
	all = all[skip:]
	if len(all) > limit {
		all = all[:limit]
	}
	return all, total, nil
}

func fetchPackagesInfoPage(client *http.Client, baseURL, token string, q url.Values) ([]RESTPackage, int, error) {
	req, err := http.NewRequest("GET", baseURL+packagesInfoPath+"?"+q.Encode(), nil)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(body))
		if strings.HasPrefix(msg, "<") {
			msg = http.StatusText(resp.StatusCode) // an HTML error page from the web server
		}
		return nil, 0, fmt.Errorf("API request failed (status %d): %s", resp.StatusCode, msg)
	}

	var response PackageRESTListResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, 0, fmt.Errorf("failed to parse response: %w", err)
	}
	return response.Packages, response.Meta.Total, nil
}

func buildGraphQLPackageVariables(search string, limit, offset int, registryIDs []int) map[string]interface{} {
	variables := map[string]interface{}{
		"filter":       map[string]interface{}{},
		"order":        map[string]string{"score": "desc"},
		"matchtags":    "{}",
		"licenses":     "{}",
		"search":       search,
		"offset":       offset,
		"hasfailures":  false,
		"installed":    true,
		"notinstalled": true,
	}
	if limit > 0 {
		variables["limit"] = limit
	}
	if len(registryIDs) > 0 {
		registryStrs := make([]string, len(registryIDs))
		for i, id := range registryIDs {
			registryStrs[i] = fmt.Sprintf("%d", id)
		}
		variables["registries"] = fmt.Sprintf("{%s}", strings.Join(registryStrs, ","))
	}
	return variables
}

func fetchGraphQLPackages(server, search string, limit, offset int, registryIDs []int) ([]Package, error) {
	token, err := ensureValidToken()
	if err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}

	queryBytes, err := packageSearchFS.ReadFile("package_search.gql")
	if err != nil {
		return nil, fmt.Errorf("failed to read GraphQL query: %w", err)
	}

	body, err := executeGraphQL(server, token, GraphQLRequest{
		OperationName: "FilteredPackages",
		Query:         string(queryBytes),
		Variables:     buildGraphQLPackageVariables(search, limit, offset, registryIDs),
	})
	if err != nil {
		return nil, err
	}

	var response struct {
		Data struct {
			PackageSearch []Package `json:"package_search"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse GraphQL response: %w", err)
	}
	if len(response.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL errors: %v", response.Errors)
	}

	return response.Data.PackageSearch, nil
}

func fetchGraphQLPackageCount(server, search string, registryIDs []int) (int, error) {
	token, err := ensureValidToken()
	if err != nil {
		return 0, fmt.Errorf("authentication required: %w", err)
	}

	queryBytes, err := packageSearchFS.ReadFile("package_search_count.gql")
	if err != nil {
		return 0, fmt.Errorf("failed to read GraphQL query: %w", err)
	}

	variables := map[string]interface{}{
		"filter":       map[string]interface{}{},
		"matchtags":    "{}",
		"licenses":     "{}",
		"search":       search,
		"hasfailures":  false,
		"installed":    true,
		"notinstalled": true,
	}
	if len(registryIDs) > 0 {
		registryStrs := make([]string, len(registryIDs))
		for i, id := range registryIDs {
			registryStrs[i] = fmt.Sprintf("%d", id)
		}
		variables["registries"] = fmt.Sprintf("{%s}", strings.Join(registryStrs, ","))
	}

	body, err := executeGraphQL(server, token, GraphQLRequest{
		OperationName: "FilteredPackagesCounts",
		Query:         string(queryBytes),
		Variables:     variables,
	})
	if err != nil {
		return 0, err
	}

	var response struct {
		Data struct {
			PackageAggregate struct {
				Aggregate struct {
					Count int `json:"count"`
				} `json:"aggregate"`
			} `json:"package_search_aggregate"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return 0, fmt.Errorf("failed to parse GraphQL response: %w", err)
	}
	if len(response.Errors) > 0 {
		return 0, fmt.Errorf("GraphQL errors: %v", response.Errors)
	}

	return response.Data.PackageAggregate.Aggregate.Count, nil
}

// fetchPackagesREST fetches one page of package search results over REST.
func fetchPackagesREST(params PackageSearchParams) ([]packageInfo, int, error) {
	pkgs, total, err := fetchRESTPackages(params.Server, params.Search, params.Limit, params.Offset, params.RegistryNames)
	if err != nil {
		return nil, 0, err
	}
	infos := make([]packageInfo, len(pkgs))
	for i, p := range pkgs {
		infos[i] = restToInfo(p)
	}
	return infos, total, nil
}

// fetchPackagesGraphQL fetches the same page over GraphQL.
func fetchPackagesGraphQL(params PackageSearchParams) ([]packageInfo, int, error) {
	pkgs, err := fetchGraphQLPackages(params.Server, params.Search, params.Limit, params.Offset, params.RegistryIDs)
	if err != nil {
		return nil, 0, err
	}
	total, err := fetchGraphQLPackageCount(params.Server, params.Search, params.RegistryIDs)
	if err != nil {
		return nil, 0, err
	}
	registryIDToName := buildRegistryIDToName(params.RegistryIDs, params.RegistryNames)
	infos := make([]packageInfo, len(pkgs))
	for i, p := range pkgs {
		infos[i] = gqlToInfo(p, registryIDToName)
	}
	return infos, total, nil
}

// searchPackages fetches over REST and falls back to GraphQL when that fetch
// fails, announcing the fallback on stderr so stdout stays clean for --json.
// Only the fetch is retried: the results are emitted exactly once, so a
// failure while writing them (a closed pipe, say) cannot trigger a second
// search and a second document on stdout.
func searchPackages(params PackageSearchParams) error {
	infos, total, err := fetchPackagesREST(params)
	if err != nil {
		restErr := err
		fmt.Fprintf(os.Stderr, "warning: REST package search failed (%v); falling back to GraphQL\n", restErr)
		if infos, total, err = fetchPackagesGraphQL(params); err != nil {
			return fmt.Errorf("%v; GraphQL fallback: %w", restErr, err)
		}
	}
	return emitPackages(params, infos, total)
}

func executeGraphQL(server string, token *StoredToken, gqlReq GraphQLRequest) ([]byte, error) {
	jsonData, err := json.Marshal(gqlReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal GraphQL request: %w", err)
	}

	url := fmt.Sprintf("https://%s/v1/graphql", server)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create GraphQL request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.IDToken))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Hasura-Role", "jhuser")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GraphQL request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read GraphQL response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GraphQL request failed (status %d): %s", resp.StatusCode, string(body))
	}

	return body, nil
}

func getPackageInfo(server, packageName string, registryIDs []int, registryNames []string) error {
	if err := getPackageInfoREST(server, packageName, registryNames); err != nil {
		return getPackageInfoGraphQL(server, packageName, registryIDs, registryNames)
	}
	return nil
}

func getPackageInfoREST(server, packageName string, registryNames []string) error {
	pkgs, _, err := fetchRESTPackages(server, packageName, 100, 0, registryNames)
	if err != nil {
		return err
	}
	var matches []packageInfo
	for _, p := range pkgs {
		if strings.EqualFold(p.Name, packageName) {
			matches = append(matches, restToInfo(p))
		}
	}
	if len(matches) == 0 {
		fmt.Println("Package not found")
		return nil
	}
	printPackages(matches, len(matches), true)
	return nil
}

func getPackageInfoGraphQL(server, packageName string, registryIDs []int, registryNames []string) error {
	pkgs, err := fetchGraphQLPackages(server, packageName, 100, 0, registryIDs)
	if err != nil {
		return err
	}
	registryIDToName := buildRegistryIDToName(registryIDs, registryNames)
	var matches []packageInfo
	for _, p := range pkgs {
		if strings.EqualFold(p.Name, packageName) {
			matches = append(matches, gqlToInfo(p, registryIDToName))
		}
	}
	if len(matches) == 0 {
		fmt.Println("Package not found")
		return nil
	}
	printPackages(matches, len(matches), true)
	return nil
}

// findPackageRegistry returns the registry of the package named packageName
// (exact, case-insensitive): from the REST package listing, or, when that is
// unavailable, from the GraphQL package search.
func findPackageRegistry(server, packageName string) (string, error) {
	pkgs, _, restErr := fetchRESTPackages(server, packageName, 100, 0, nil)
	if restErr == nil {
		for _, p := range pkgs {
			if strings.EqualFold(p.Name, packageName) && p.Registry != "" {
				return p.Registry, nil
			}
		}
		return "", fmt.Errorf("package not found: %s", packageName)
	}

	allRegistries, err := fetchRegistries(server)
	if err != nil {
		return "", fmt.Errorf("failed to search for package %q: %v; failed to fetch registries: %w", packageName, restErr, err)
	}
	var registryIDs []int
	for _, reg := range allRegistries {
		registryIDs = append(registryIDs, reg.RegistryID)
	}
	gqlPkgs, err := fetchGraphQLPackages(server, packageName, 100, 0, registryIDs)
	if err != nil {
		return "", fmt.Errorf("failed to search for package %q: %v; GraphQL fallback: %w", packageName, restErr, err)
	}
	for i := range gqlPkgs {
		if strings.EqualFold(gqlPkgs[i].Name, packageName) && gqlPkgs[i].RegistryMap != nil {
			for _, reg := range allRegistries {
				if reg.RegistryID == gqlPkgs[i].RegistryMap.RegistryID {
					return reg.Name, nil
				}
			}
		}
	}
	return "", fmt.Errorf("package not found: %s", packageName)
}

func getPackageDependencies(server string, packageName string, registryName string, showIndirect bool) error {
	targetRegistry := registryName
	if targetRegistry == "" {
		var err error
		if targetRegistry, err = findPackageRegistry(server, packageName); err != nil {
			return err
		}
	}

	docsURL := fmt.Sprintf("https://%s/docs/%s/%s/stable/pkg.json", server, targetRegistry, packageName)

	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	req, err := http.NewRequest("GET", docsURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.IDToken))
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch package documentation: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API request failed (status %d): %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var docsResp PackageDocsResponse
	if err := json.Unmarshal(body, &docsResp); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	var deps []PackageDependency
	if showIndirect {
		deps = docsResp.Dependencies
	} else {
		for _, dep := range docsResp.Dependencies {
			if dep.Direct {
				deps = append(deps, dep)
			}
		}
	}

	if len(deps) == 0 {
		if showIndirect {
			fmt.Printf("Package %s (v%s) has no dependencies\n", docsResp.Name, docsResp.Version)
		} else {
			fmt.Printf("Package %s (v%s) has no direct dependencies\n", docsResp.Name, docsResp.Version)
		}
		return nil
	}

	fmt.Printf("Dependencies for %s (v%s) from registry '%s':\n\n", docsResp.Name, docsResp.Version, targetRegistry)
	if !showIndirect {
		fmt.Printf("Showing %d direct dependencies (use --indirect to include indirect dependencies)\n\n", len(deps))
		fmt.Printf("%-35s %-15s %-38s %s\n", "NAME", "REGISTRY", "UUID", "VERSIONS")
		fmt.Printf("%-35s %-15s %-38s %s\n", strings.Repeat("-", 35), strings.Repeat("-", 15), strings.Repeat("-", 38), strings.Repeat("-", 20))
		for _, dep := range deps {
			fmt.Printf("%-35s %-15s %-38s %s\n", dep.Name, dep.Registry, dep.UUID, strings.Join(dep.Versions, ", "))
		}
	} else {
		var directDeps []PackageDependency
		var indirectDeps []PackageDependency
		for _, dep := range deps {
			if dep.Direct {
				directDeps = append(directDeps, dep)
			} else {
				indirectDeps = append(indirectDeps, dep)
			}
		}
		fmt.Printf("Showing %d total dependencies (%d direct, %d indirect)\n\n", len(deps), len(directDeps), len(indirectDeps))
		if len(directDeps) > 0 {
			fmt.Printf("Direct Dependencies (%d):\n", len(directDeps))
			fmt.Printf("%-35s %-15s %-38s %s\n", "NAME", "REGISTRY", "UUID", "VERSIONS")
			fmt.Printf("%-35s %-15s %-38s %s\n", strings.Repeat("-", 35), strings.Repeat("-", 15), strings.Repeat("-", 38), strings.Repeat("-", 20))
			for _, dep := range directDeps {
				fmt.Printf("%-35s %-15s %-38s %s\n", dep.Name, dep.Registry, dep.UUID, strings.Join(dep.Versions, ", "))
			}
			fmt.Println()
		}
		if len(indirectDeps) > 0 {
			fmt.Printf("Indirect Dependencies (%d):\n", len(indirectDeps))
			fmt.Printf("%-35s %-15s %-38s %s\n", "NAME", "REGISTRY", "UUID", "VERSIONS")
			fmt.Printf("%-35s %-15s %-38s %s\n", strings.Repeat("-", 35), strings.Repeat("-", 15), strings.Repeat("-", 38), strings.Repeat("-", 20))
			for _, dep := range indirectDeps {
				fmt.Printf("%-35s %-15s %-38s %s\n", dep.Name, dep.Registry, dep.UUID, strings.Join(dep.Versions, ", "))
			}
		}
	}

	return nil
}
