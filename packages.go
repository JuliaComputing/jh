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
	"sort"
	"strings"
	"time"
	"unicode/utf8"
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

// PackageDependent is one entry of pkg.json's `reversedeps`. A dependent can
// appear twice, once with direct=true and once with direct=false.
type PackageDependent struct {
	Direct   bool   `json:"direct"`
	Name     string `json:"name"`
	UUID     string `json:"uuid"`
	Registry string `json:"registry"`
	Slug     string `json:"slug"`
}

type PackageContributor struct {
	Name          string `json:"name"`
	Contributions int    `json:"contributions"`
	Type          string `json:"type"`
	URL           string `json:"url"`
}

type PackageDocsResponse struct {
	Name              string               `json:"name"`
	Version           string               `json:"version"`
	Dependencies      []PackageDependency  `json:"deps"`
	Dependents        []PackageDependent   `json:"reversedeps"`
	Homepage          string               `json:"homepage"`
	ReleaseDate       string               `json:"release_date"`
	Contributors      []PackageContributor `json:"contributors"`
	DocType           string               `json:"doctype"`
	DocumenterErrored bool                 `json:"documenter_errored"`
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
	Name                   string          `json:"name"`
	UUID                   string          `json:"uuid"`
	Registry               string          `json:"registry"`
	Description            string          `json:"description"`
	StargazersCount        int             `json:"stargazers_count"`
	SourceURL              string          `json:"source_url"`
	JHubDocsURL            string          `json:"jhub_docs_url"`
	LatestStableVersion    string          `json:"latest_stable_version"`
	DetectedSourceLicenses []string        `json:"detected_source_licenses"`
	DownloadsTimeseries    []DownloadCount `json:"downloads_timeseries"`
	Tags                   []string        `json:"tags"`
	Owner                  string          `json:"owner"`
	IsApp                  bool            `json:"is_app"`
	IsDyad                 bool            `json:"is_dyad"`
}

// DownloadCount is one day of a package's download counts.
type DownloadCount struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
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
}

// packageInfo is a common display struct used by both REST and GraphQL paths.
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
	IsDyad      bool     `json:"is_dyad"`
	Score       float64  `json:"score"`
	Status      string   `json:"status"`
	// Downloads30d is the sum of the REST listing's daily download counts,
	// which cover the last 30 days; absent when the listing was not used.
	Downloads30d        *int            `json:"downloads_30d,omitempty"`
	DownloadsTimeseries []DownloadCount `json:"-"`
	// Details is filled by `jh package info` only.
	Details *packageDetails `json:"details,omitempty"`
}

// packageDetails holds what `jh package info` adds to a listing entry: the
// daily download counts and the stable version's pkg.json metadata.
type packageDetails struct {
	DownloadsTimeseries []DownloadCount      `json:"downloads_timeseries"`
	Version             string               `json:"version,omitempty"`
	Homepage            string               `json:"homepage,omitempty"`
	ReleaseDate         string               `json:"release_date,omitempty"`
	Contributors        []PackageContributor `json:"contributors"`
	DocType             string               `json:"doc_type,omitempty"`
	DocsBuildErrored    bool                 `json:"docs_build_errored"`
	DirectDependencies  int                  `json:"direct_dependencies"`
	Dependencies        int                  `json:"dependencies"`
	DirectDependents    int                  `json:"direct_dependents"`
	Dependents          int                  `json:"dependents"`
}

// writeJSON writes doc 2-space indented with a trailing newline — the only
// thing on stdout in --json mode.
func writeJSON(w io.Writer, doc interface{}) error {
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode JSON: %w", err)
	}
	_, err = w.Write(append(out, '\n'))
	return err
}

// writePackagesJSON writes `{"results": [...], "total": N}` with writeJSON.
// Tags is always an array (never null) so consumers can iterate it
// unconditionally.
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
	return writeJSON(w, doc)
}

func printPackages(server string, pkgs []packageInfo, total int, verbose bool) {
	if len(pkgs) == 0 {
		fmt.Println("No packages found")
		return
	}

	if total > len(pkgs) {
		fmt.Printf("Showing %s of %s packages\n\n", formatCount(len(pkgs)), formatCount(total))
	} else {
		fmt.Printf("Found %s %s\n\n", formatCount(len(pkgs)), plural(len(pkgs), "package", "packages"))
	}

	if verbose {
		printPackageCards(server, pkgs)
		return
	}
	printPackageTable(pkgs)
}

const (
	nameColWidth      = 28
	registryColWidth  = 16
	ownerColWidth     = 18
	versionColWidth   = 11
	starsColWidth     = 7
	downloadsColWidth = 11
	descColWidth      = 50
)

func printPackageTable(pkgs []packageInfo) {
	header := pad("NAME", nameColWidth) + " " + pad("REGISTRY", registryColWidth) + " " + pad("OWNER", ownerColWidth) + " " +
		pad("VERSION", versionColWidth) + " " + padLeft("STARS", starsColWidth) + " " + padLeft("DL/30 DAYS", downloadsColWidth) + "  DESCRIPTION"
	fmt.Println(bold(header))
	for _, pkg := range pkgs {
		version := "-"
		if pkg.Version != "" {
			version = "v" + pkg.Version
		}
		downloads := "-"
		if pkg.Downloads30d != nil {
			downloads = formatCount(*pkg.Downloads30d)
		}
		fmt.Printf("%s %s %s %s %s %s  %s\n",
			bold(pad(truncate(pkg.Name, nameColWidth), nameColWidth)),
			pad(truncate(pkg.Registry, registryColWidth), registryColWidth),
			pad(truncate(pkg.Owner, ownerColWidth), ownerColWidth),
			pad(truncate(version, versionColWidth), versionColWidth),
			padLeft(formatCount(pkg.Stars), starsColWidth),
			padLeft(downloads, downloadsColWidth),
			dim(truncate(pkg.Description, descColWidth)))
	}
}

// maxListedContributors caps the contributors a package card names and
// maxTagsWidth the runes of its tag list; --json lists all of them.
const (
	maxListedContributors = 5
	maxTagsWidth          = 64
)

func printPackageCards(server string, pkgs []packageInfo) {
	for i, pkg := range pkgs {
		if i > 0 {
			fmt.Println()
		}
		printPackageCard(server, pkg)
	}
}

// printPackageCard prints one package: a header line, the description, then
// aligned fields grouped as numbers, links and install hint. Fields the
// package lacks are skipped.
func printPackageCard(server string, pkg packageInfo) {
	header := []string{}
	if pkg.Version != "" {
		header = append(header, "v"+pkg.Version)
	}
	for _, part := range []string{pkg.Registry, pkg.Owner} {
		if part != "" {
			header = append(header, part)
		}
	}
	fmt.Printf("%s  %s\n", bold(pkg.Name), dim(strings.Join(header, " · ")))
	if pkg.Description != "" {
		fmt.Println(pkg.Description)
	}
	fmt.Println()

	d := pkg.Details
	printField("Stars", formatCount(pkg.Stars))
	if pkg.Downloads30d != nil {
		downloads := formatCount(*pkg.Downloads30d) + " in the last 30 days"
		if d != nil && len(d.DownloadsTimeseries) > 1 {
			counts := make([]int, len(d.DownloadsTimeseries))
			for i, c := range d.DownloadsTimeseries {
				counts[i] = c.Count
			}
			downloads += "  " + sparkline(counts)
		}
		printField("Downloads", downloads)
	}
	if d != nil {
		if d.Dependencies > 0 {
			printField("Dependencies", fmt.Sprintf("%s direct · %s total", formatCount(d.DirectDependencies), formatCount(d.Dependencies)))
		}
		if d.Dependents > 0 {
			printField("Dependents", fmt.Sprintf("%s direct · %s total", formatCount(d.DirectDependents), formatCount(d.Dependents)))
		}
		if n := len(d.Contributors); n > 0 {
			var names []string
			for i := 0; i < n && i < maxListedContributors; i++ {
				names = append(names, d.Contributors[i].Name)
			}
			if n > maxListedContributors {
				names = append(names, "…")
			}
			printField("Contributors", formatCount(n)+" · "+strings.Join(names, ", "))
		}
	}
	printField("License", pkg.License)
	if d != nil {
		printField("Released", d.ReleaseDate)
		docs := d.DocType
		if docs != "" && d.DocsBuildErrored {
			docs += " (build errored)"
		}
		printField("Docs build", docs)
	}
	var kinds []string
	if pkg.IsApp {
		kinds = append(kinds, "Application")
	}
	if pkg.IsDyad {
		kinds = append(kinds, "Dyad library")
	}
	printField("Type", strings.Join(kinds, ", "))
	printField("Tags", listTags(pkg.Tags))
	printField("Status", pkg.Status)
	if pkg.Score != 0 {
		printField("Score", fmt.Sprintf("%.2f", pkg.Score))
	}

	fmt.Println()
	printField("Repository", strings.TrimSuffix(pkg.SourceURL, ".git"))
	if d != nil {
		printField("Homepage", d.Homepage)
	}
	printField("Documentation", pkg.DocsURL)
	if server != "" && pkg.Registry != "" {
		printField("JuliaHub", packagePageURL(server, pkg.Registry, pkg.Name))
	}
	printField("UUID", pkg.UUID)

	fmt.Println()
	printField("Install", "] add "+pkg.Name)
}

// listTags joins as many tags as fit in maxTagsWidth runes (at least one)
// and counts the rest.
func listTags(tags []string) string {
	shown, width := 0, 0
	for _, t := range tags {
		w := utf8.RuneCountInString(t) + 2
		if shown > 0 && width+w > maxTagsWidth {
			break
		}
		shown, width = shown+1, width+w
	}
	out := strings.Join(tags[:shown], ", ")
	if extra := len(tags) - shown; extra > 0 {
		out += fmt.Sprintf(", … (+%d)", extra)
	}
	return out
}

// packagePageURL is the package's page in the JuliaHub web UI.
func packagePageURL(server, registry, name string) string {
	return fmt.Sprintf("https://%s/ui/Packages/%s/%s", server, url.PathEscape(registry), url.PathEscape(name))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// restToInfo converts a RESTPackage to the common packageInfo display struct.
func restToInfo(p RESTPackage) packageInfo {
	downloads := 0
	for _, d := range p.DownloadsTimeseries {
		downloads += d.Count
	}
	return packageInfo{
		Name:                p.Name,
		UUID:                p.UUID,
		Owner:               p.Owner,
		Registry:            p.Registry,
		Version:             p.LatestStableVersion,
		Description:         p.Description,
		SourceURL:           p.SourceURL,
		Tags:                p.Tags,
		Stars:               p.StargazersCount,
		DocsURL:             p.JHubDocsURL,
		License:             strings.Join(p.DetectedSourceLicenses, ", "),
		IsApp:               p.IsApp,
		IsDyad:              p.IsDyad,
		Downloads30d:        &downloads,
		DownloadsTimeseries: p.DownloadsTimeseries,
	}
}

// newPackageDetails builds the `jh package info` details from the listing's
// download counts and, when docs is non-nil, the package's pkg.json. A
// dependency or dependent listed both as direct and as indirect counts once,
// as direct.
func newPackageDetails(info packageInfo, docs *PackageDocsResponse) *packageDetails {
	d := &packageDetails{
		DownloadsTimeseries: info.DownloadsTimeseries,
		Contributors:        []PackageContributor{},
	}
	if d.DownloadsTimeseries == nil {
		d.DownloadsTimeseries = []DownloadCount{}
	}
	if docs == nil {
		return d
	}
	d.Version = docs.Version
	d.Homepage = docs.Homepage
	d.ReleaseDate = docs.ReleaseDate
	if docs.Contributors != nil {
		d.Contributors = docs.Contributors
	}
	d.DocType = docs.DocType
	d.DocsBuildErrored = docs.DocumenterErrored

	depDirect := make(map[string]bool)
	for _, dep := range docs.Dependencies {
		depDirect[dep.UUID] = depDirect[dep.UUID] || dep.Direct
	}
	d.Dependencies = len(depDirect)
	for _, direct := range depDirect {
		if direct {
			d.DirectDependencies++
		}
	}
	direct, indirect := splitDependents(docs.Dependents)
	d.DirectDependents = len(direct)
	d.Dependents = len(direct) + len(indirect)
	return d
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
//
// Every sort key carries a direction sign: the API rejects unsigned keys
// (`^[+-].+$`), and servers only honour the `-` form, so the tiebreak is
// descending. Any fixed direction keeps the paging stable.
func buildPackagesInfoQuery(search string, registryNames []string, page restPage) url.Values {
	q := url.Values{}
	q.Set("sorts[0]", "-stargazers_count")
	if search != "" {
		q.Set("name", search)
		q.Set("sorts[0]", "-score")
	}
	q.Set("sorts[1]", "-name")
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
func fetchRESTPackages(server, search string, limit, offset int, registryNames []string) ([]RESTPackage, int, error) {
	token, err := ensureValidToken()
	if err != nil {
		return nil, 0, fmt.Errorf("authentication required: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	return fetchRESTPackagesFrom(client, "https://"+server, token.IDToken, search, limit, offset, registryNames)
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
		return nil, 0, fmt.Errorf("API request failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
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

func searchPackagesREST(params PackageSearchParams) error {
	pkgs, total, err := fetchRESTPackages(params.Server, params.Search, params.Limit, params.Offset, params.RegistryNames)
	if err != nil {
		return err
	}
	infos := make([]packageInfo, len(pkgs))
	for i, p := range pkgs {
		infos[i] = restToInfo(p)
	}
	printPackages(params.Server, infos, total, params.Verbose)
	return nil
}

func searchPackagesGraphQL(params PackageSearchParams) error {
	pkgs, err := fetchGraphQLPackages(params.Server, params.Search, params.Limit, params.Offset, params.RegistryIDs)
	if err != nil {
		return err
	}
	total, err := fetchGraphQLPackageCount(params.Server, params.Search, params.RegistryIDs)
	if err != nil {
		return err
	}
	registryIDToName := buildRegistryIDToName(params.RegistryIDs, params.RegistryNames)
	infos := make([]packageInfo, len(pkgs))
	for i, p := range pkgs {
		infos[i] = gqlToInfo(p, registryIDToName)
	}
	printPackages(params.Server, infos, total, params.Verbose)
	return nil
}

func searchPackages(params PackageSearchParams) error {
	if err := searchPackagesREST(params); err != nil {
		return searchPackagesGraphQL(params)
	}
	return nil
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

// getPackageInfo prints the packages named packageName (exact,
// case-insensitive), one per registry, as a verbose listing or, with asJSON,
// as `{"results": [...], "total": N}`.
func getPackageInfo(server, packageName string, registryIDs []int, registryNames []string, asJSON bool) error {
	matches, err := findPackageInfoREST(server, packageName, registryNames)
	if err != nil {
		if matches, err = findPackageInfoGraphQL(server, packageName, registryIDs, registryNames); err != nil {
			return err
		}
	}
	for i := range matches {
		var docs *PackageDocsResponse
		if matches[i].Registry != "" {
			if docs, _, err = fetchPackageDocs(server, matches[i].Name, matches[i].Registry); err != nil {
				fmt.Fprintf(os.Stderr, "warning: no package details for %s in %s: %v\n", matches[i].Name, matches[i].Registry, err)
				docs = nil
			}
		}
		matches[i].Details = newPackageDetails(matches[i], docs)
	}
	if asJSON {
		return writePackagesJSON(os.Stdout, matches, len(matches))
	}
	if len(matches) == 0 {
		fmt.Println("Package not found")
		return nil
	}
	if len(matches) == 1 {
		printPackageCard(server, matches[0])
		return nil
	}
	printPackages(server, matches, len(matches), true)
	return nil
}

func findPackageInfoREST(server, packageName string, registryNames []string) ([]packageInfo, error) {
	pkgs, _, err := fetchRESTPackages(server, packageName, 100, 0, registryNames)
	if err != nil {
		return nil, err
	}
	var matches []packageInfo
	for _, p := range pkgs {
		if strings.EqualFold(p.Name, packageName) {
			matches = append(matches, restToInfo(p))
		}
	}
	return matches, nil
}

func findPackageInfoGraphQL(server, packageName string, registryIDs []int, registryNames []string) ([]packageInfo, error) {
	pkgs, err := fetchGraphQLPackages(server, packageName, 100, 0, registryIDs)
	if err != nil {
		return nil, err
	}
	registryIDToName := buildRegistryIDToName(registryIDs, registryNames)
	var matches []packageInfo
	for _, p := range pkgs {
		if strings.EqualFold(p.Name, packageName) {
			matches = append(matches, gqlToInfo(p, registryIDToName))
		}
	}
	return matches, nil
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

// fetchPackageDocs returns the stable version's pkg.json of packageName in
// registryName, or in the registry findPackageRegistry picks when it is empty.
func fetchPackageDocs(server, packageName, registryName string) (*PackageDocsResponse, string, error) {
	targetRegistry := registryName
	if targetRegistry == "" {
		var err error
		if targetRegistry, err = findPackageRegistry(server, packageName); err != nil {
			return nil, "", err
		}
	}

	docsURL := fmt.Sprintf("https://%s/docs/%s/%s/stable/pkg.json", server, targetRegistry, packageName)

	token, err := ensureValidToken()
	if err != nil {
		return nil, "", fmt.Errorf("authentication required: %w", err)
	}

	req, err := http.NewRequest("GET", docsURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.IDToken))
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch package documentation: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("API request failed (status %d): %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read response: %w", err)
	}

	var docsResp PackageDocsResponse
	if err := json.Unmarshal(body, &docsResp); err != nil {
		return nil, "", fmt.Errorf("failed to parse response: %w", err)
	}
	return &docsResp, targetRegistry, nil
}

// dependenciesDoc is the --json output of `jh package dependency`.
type dependenciesDoc struct {
	Name         string              `json:"name"`
	Version      string              `json:"version"`
	Registry     string              `json:"registry"`
	Dependencies []PackageDependency `json:"dependencies"`
}

func getPackageDependencies(server string, packageName string, registryName string, showIndirect, asJSON bool) error {
	docsResp, targetRegistry, err := fetchPackageDocs(server, packageName, registryName)
	if err != nil {
		return err
	}

	direct, indirect := splitDependencies(docsResp.Dependencies)
	deps := append([]PackageDependency{}, direct...)
	if showIndirect {
		deps = append(deps, indirect...)
	}

	if asJSON {
		return writeJSON(os.Stdout, dependenciesDoc{docsResp.Name, docsResp.Version, targetRegistry, deps})
	}

	printPackageHeading(docsResp, targetRegistry)
	if len(direct)+len(indirect) == 0 {
		fmt.Println("No dependencies")
		return nil
	}
	fmt.Printf("%s direct · %s indirect · %s total dependencies\n",
		formatCount(len(direct)), formatCount(len(indirect)), formatCount(len(direct)+len(indirect)))
	if len(direct) > 0 {
		fmt.Println()
		printDependencyTable("Direct dependencies", direct)
	}
	if showIndirect && len(indirect) > 0 {
		fmt.Println()
		printDependencyTable("Indirect dependencies", indirect)
	} else if len(indirect) > 0 {
		fmt.Printf("\n%s\n", dim(fmt.Sprintf("Use --indirect to list the %s indirect dependencies", formatCount(len(indirect)))))
	}
	return nil
}

// printPackageHeading prints "<name>  v<version> · <registry>" and a blank line.
func printPackageHeading(docsResp *PackageDocsResponse, registry string) {
	fmt.Printf("%s  %s\n\n", bold(docsResp.Name), dim("v"+docsResp.Version+" · "+registry))
}

// splitDependencies collapses entries to one per UUID, sorted by name. A
// dependency listed as direct in any entry is direct and keeps that entry's
// versions; the rest are indirect.
func splitDependencies(entries []PackageDependency) (direct, indirect []PackageDependency) {
	byUUID := make(map[string]PackageDependency)
	for _, e := range entries {
		if prev, ok := byUUID[e.UUID]; ok && (prev.Direct || !e.Direct) {
			continue
		}
		byUUID[e.UUID] = e
	}
	for _, e := range byUUID {
		if e.Direct {
			direct = append(direct, e)
		} else {
			indirect = append(indirect, e)
		}
	}
	byName := func(s []PackageDependency) {
		sort.Slice(s, func(i, j int) bool {
			if a, b := strings.ToLower(s[i].Name), strings.ToLower(s[j].Name); a != b {
				return a < b
			}
			return s[i].UUID < s[j].UUID
		})
	}
	byName(direct)
	byName(indirect)
	return direct, indirect
}

const uuidColWidth = 36

// registryLabel shows standard libraries, which pkg.json lists without a
// registry, as "stdlib".
func registryLabel(registry string) string {
	if registry == "" {
		return dim(pad("stdlib", registryColWidth))
	}
	return pad(truncate(registry, registryColWidth), registryColWidth)
}

func printDependencyTable(title string, deps []PackageDependency) {
	fmt.Printf("%s %s\n", bold(title), dim("("+formatCount(len(deps))+")"))
	fmt.Println(bold(pad("NAME", nameColWidth+7) + " " + pad("REGISTRY", registryColWidth) + " " + pad("UUID", uuidColWidth) + "  VERSIONS"))
	for _, dep := range deps {
		fmt.Printf("%s %s %s  %s\n", pad(truncate(dep.Name, nameColWidth+7), nameColWidth+7), registryLabel(dep.Registry),
			dim(pad(dep.UUID, uuidColWidth)), strings.Join(dep.Versions, ", "))
	}
}

// splitDependents collapses entries to one per UUID, sorted by name. A
// dependent listed as direct in any entry is direct; the rest are indirect.
func splitDependents(entries []PackageDependent) (direct, indirect []PackageDependent) {
	byUUID := make(map[string]PackageDependent)
	for _, e := range entries {
		if prev, ok := byUUID[e.UUID]; ok {
			e.Direct = e.Direct || prev.Direct
		}
		byUUID[e.UUID] = e
	}
	for _, e := range byUUID {
		if e.Direct {
			direct = append(direct, e)
		} else {
			indirect = append(indirect, e)
		}
	}
	byName := func(s []PackageDependent) {
		sort.Slice(s, func(i, j int) bool {
			if a, b := strings.ToLower(s[i].Name), strings.ToLower(s[j].Name); a != b {
				return a < b
			}
			return s[i].UUID < s[j].UUID
		})
	}
	byName(direct)
	byName(indirect)
	return direct, indirect
}

func printDependentsTable(title string, deps []PackageDependent) {
	fmt.Printf("%s %s\n", bold(title), dim("("+formatCount(len(deps))+")"))
	fmt.Println(bold(pad("NAME", nameColWidth+7) + " " + pad("REGISTRY", registryColWidth) + " UUID"))
	for _, d := range deps {
		fmt.Printf("%s %s %s\n", pad(truncate(d.Name, nameColWidth+7), nameColWidth+7), registryLabel(d.Registry), dim(d.UUID))
	}
}

// dependentsDoc is the --json output of `jh package dependents`. The counts
// cover every dependent; Dependents lists the indirect ones only with
// --indirect.
type dependentsDoc struct {
	Name       string             `json:"name"`
	Version    string             `json:"version"`
	Registry   string             `json:"registry"`
	Direct     int                `json:"direct"`
	Indirect   int                `json:"indirect"`
	Total      int                `json:"total"`
	Dependents []PackageDependent `json:"dependents"`
}

func newDependentsDoc(docsResp *PackageDocsResponse, registry string, direct, indirect []PackageDependent, showIndirect bool) dependentsDoc {
	listed := append([]PackageDependent{}, direct...)
	if showIndirect {
		listed = append(listed, indirect...)
	}
	return dependentsDoc{
		Name:       docsResp.Name,
		Version:    docsResp.Version,
		Registry:   registry,
		Direct:     len(direct),
		Indirect:   len(indirect),
		Total:      len(direct) + len(indirect),
		Dependents: listed,
	}
}

func getPackageDependents(server, packageName, registryName string, showIndirect, asJSON bool) error {
	docsResp, targetRegistry, err := fetchPackageDocs(server, packageName, registryName)
	if err != nil {
		return err
	}

	direct, indirect := splitDependents(docsResp.Dependents)
	if asJSON {
		return writeJSON(os.Stdout, newDependentsDoc(docsResp, targetRegistry, direct, indirect, showIndirect))
	}
	printPackageHeading(docsResp, targetRegistry)
	if len(direct)+len(indirect) == 0 {
		fmt.Println("No dependents")
		return nil
	}
	fmt.Printf("%s direct · %s indirect · %s total dependents\n",
		formatCount(len(direct)), formatCount(len(indirect)), formatCount(len(direct)+len(indirect)))
	if len(direct) > 0 {
		fmt.Println()
		printDependentsTable("Direct dependents", direct)
	}
	if showIndirect && len(indirect) > 0 {
		fmt.Println()
		printDependentsTable("Indirect dependents", indirect)
	} else if len(indirect) > 0 {
		fmt.Printf("\n%s\n", dim(fmt.Sprintf("Use --indirect to list the %s indirect dependents", formatCount(len(indirect)))))
	}
	return nil
}
