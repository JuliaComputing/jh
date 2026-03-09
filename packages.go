package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

//go:embed package_search.gql
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
	Description  string              `json:"description"`
	Owner        string              `json:"owner"`
	License      string              `json:"predicted_license"`
	LicenseURL   string              `json:"license_url"`
	Homepage     string              `json:"homepage"`
	Dependencies []PackageDependency `json:"deps"`
}

type Package struct {
	Name        string               `json:"name"`
	Owner       string               `json:"owner"`
	Slug        *string              `json:"slug"`
	License     string               `json:"license"`
	IsApp       bool                 `json:"isapp"`
	Score       float64              `json:"score"`
	RegistryMap []PackageRegistryMap `json:"-"` // Custom unmarshaling
	Metadata    *PackageMetadata     `json:"metadata"`
	UUID        string               `json:"uuid"`
	Installed   bool                 `json:"installed"`
	Failures    []PackageFailure     `json:"failures"`
}

// UnmarshalJSON custom unmarshaler to handle registrymap as both object and array
func (p *Package) UnmarshalJSON(data []byte) error {
	type Alias Package
	aux := &struct {
		RegistryMapRaw json.RawMessage `json:"registrymap"`
		*Alias
	}{
		Alias: (*Alias)(p),
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	if len(aux.RegistryMapRaw) > 0 && string(aux.RegistryMapRaw) != "null" {
		var registryMapArray []PackageRegistryMap
		if err := json.Unmarshal(aux.RegistryMapRaw, &registryMapArray); err == nil {
			p.RegistryMap = registryMapArray
		} else {
			var registryMapObj PackageRegistryMap
			if err := json.Unmarshal(aux.RegistryMapRaw, &registryMapObj); err == nil {
				p.RegistryMap = []PackageRegistryMap{registryMapObj}
			}
		}
	}

	return nil
}

type PackageSearchResponse struct {
	Data struct {
		PackageSearch []Package `json:"package_search"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
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

// executePackageQuery executes a GraphQL package search query and returns the results
func executePackageQuery(server string, variables map[string]interface{}) ([]Package, error) {
	token, err := ensureValidToken()
	if err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}

	queryBytes, err := packageSearchFS.ReadFile("package_search.gql")
	if err != nil {
		return nil, fmt.Errorf("failed to read GraphQL query: %w", err)
	}
	query := string(queryBytes)

	graphqlReq := GraphQLRequest{
		OperationName: "FilteredPackages",
		Query:         query,
		Variables:     variables,
	}

	jsonData, err := json.Marshal(graphqlReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal GraphQL request: %w", err)
	}

	url := fmt.Sprintf("https://%s/v1/graphql", server)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.IDToken))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Hasura-Role", "jhuser")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GraphQL request failed (status %d): %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var response PackageSearchResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(response.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL errors: %v", response.Errors)
	}

	return response.Data.PackageSearch, nil
}

// fetchPackagesREST calls the REST /packages/info endpoint and returns the results
func fetchPackagesREST(server string, search string, limit int, offset int, registryNames []string) ([]RESTPackage, error) {
	token, err := ensureValidToken()
	if err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}

	url := fmt.Sprintf("https://%s/packages/info", server)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	q := req.URL.Query()
	if search != "" {
		q.Add("name", search)
	}
	if limit > 0 {
		q.Add("limit", fmt.Sprintf("%d", limit))
	}
	if offset > 0 {
		q.Add("offset", fmt.Sprintf("%d", offset))
	}
	if len(registryNames) > 0 {
		q.Add("registries", strings.Join(registryNames, ","))
	}
	req.URL.RawQuery = q.Encode()

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.IDToken))
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed (status %d): %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var response PackageRESTListResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return response.Packages, nil
}

// displayRESTPackage prints a single REST package in verbose or concise format
func displayRESTPackage(pkg RESTPackage, verbose bool) {
	if verbose {
		fmt.Printf("Name: %s\n", pkg.Name)
		fmt.Printf("UUID: %s\n", pkg.UUID)
		fmt.Printf("Registry: %s\n", pkg.Registry)
		if pkg.Description != "" {
			fmt.Printf("Description: %s\n", pkg.Description)
		}
		if pkg.SourceURL != "" {
			fmt.Printf("Repository: %s\n", pkg.SourceURL)
		}
		if len(pkg.Tags) > 0 {
			fmt.Printf("Tags: %s\n", strings.Join(pkg.Tags, ", "))
		}
		if pkg.StargazersCount > 0 {
			fmt.Printf("Stars: %d\n", pkg.StargazersCount)
		}
		if pkg.JHubDocsURL != "" {
			fmt.Printf("Documentation: %s\n", pkg.JHubDocsURL)
		}
		if len(pkg.DetectedSourceLicenses) > 0 {
			fmt.Printf("License: %s\n", strings.Join(pkg.DetectedSourceLicenses, ", "))
		}
		if pkg.LatestStableVersion != "" {
			fmt.Printf("Latest Version: %s\n", pkg.LatestStableVersion)
		}
	} else {
		fmt.Printf("%-30s %-20s", pkg.Name, pkg.Registry)
		if pkg.LatestStableVersion != "" {
			fmt.Printf(" v%-10s", pkg.LatestStableVersion)
		} else {
			fmt.Printf(" %-12s", "N/A")
		}
		if pkg.Description != "" {
			desc := pkg.Description
			if len(desc) > 40 {
				desc = desc[:40] + "..."
			}
			fmt.Printf("%s", desc)
		}
		fmt.Printf("\n")
	}
	fmt.Println()
}

// displayPackageDetails displays detailed information about a GraphQL package
func displayPackageDetails(pkg *Package, registryLookup map[int]string) {
	fmt.Printf("Name: %s\n", pkg.Name)
	fmt.Printf("UUID: %s\n", pkg.UUID)
	fmt.Printf("Owner: %s\n", pkg.Owner)

	if pkg.Metadata != nil {
		if pkg.Metadata.Description != "" {
			fmt.Printf("Description: %s\n", pkg.Metadata.Description)
		}
		if pkg.Metadata.Repo != "" {
			fmt.Printf("Repository: %s\n", pkg.Metadata.Repo)
		}
		if len(pkg.Metadata.Tags) > 0 {
			fmt.Printf("Tags: %s\n", strings.Join(pkg.Metadata.Tags, ", "))
		}
		if pkg.Metadata.StarCount > 0 {
			fmt.Printf("Stars: %d\n", pkg.Metadata.StarCount)
		}
		if pkg.Metadata.DocsLink != "" {
			fmt.Printf("Documentation: %s\n", pkg.Metadata.DocsLink)
		}
	}

	if pkg.License != "" {
		fmt.Printf("License: %s\n", pkg.License)
	}

	if len(pkg.RegistryMap) > 0 {
		latestEntry := pkg.RegistryMap[0]
		fmt.Printf("Latest Version: %s\n", latestEntry.Version)
		if latestEntry.Status {
			fmt.Printf("Status: Active\n")
		} else {
			fmt.Printf("Status: Inactive\n")
		}

		if registryLookup != nil {
			registryNames := []string{}
			for _, entry := range pkg.RegistryMap {
				if registryName, ok := registryLookup[entry.RegistryID]; ok {
					registryNames = append(registryNames, registryName)
				} else {
					registryNames = append(registryNames, fmt.Sprintf("Registry-%d", entry.RegistryID))
				}
			}
			if len(registryNames) > 0 {
				fmt.Printf("Registries: %s\n", strings.Join(registryNames, ", "))
			}
		}
	}

	if pkg.IsApp {
		fmt.Printf("Type: Application\n")
	}

	fmt.Printf("Score: %.2f\n", pkg.Score)
}

// buildRegistriesParam converts registry IDs to PostgreSQL array format
func buildRegistriesParam(registries []int) string {
	registryStrs := make([]string, len(registries))
	for i, id := range registries {
		registryStrs[i] = fmt.Sprintf("%d", id)
	}
	return fmt.Sprintf("{%s}", strings.Join(registryStrs, ","))
}

// buildRegistryLookup creates a map from registry ID to registry name
func buildRegistryLookup(server string) map[int]string {
	registries, err := fetchRegistries(server)
	if err != nil {
		return map[int]string{}
	}

	lookup := make(map[int]string)
	for _, registry := range registries {
		lookup[registry.RegistryID] = registry.Name
	}
	return lookup
}

func searchPackages(server string, search string, limit int, offset int, registryIDs []int, registryNames []string, verbose bool) error {
	variables := map[string]interface{}{
		"filter":       map[string]interface{}{},
		"order":        map[string]string{"score": "desc"},
		"matchtags":    "{}",
		"licenses":     "{}",
		"search":       "",
		"offset":       0,
		"hasfailures":  false,
		"installed":    true,
		"notinstalled": true,
	}

	if search != "" {
		variables["search"] = search
	}
	if limit > 0 {
		variables["limit"] = limit
	}
	if offset > 0 {
		variables["offset"] = offset
	}
	if len(registryIDs) > 0 {
		variables["registries"] = buildRegistriesParam(registryIDs)
	}

	packages, err := executePackageQuery(server, variables)
	if err != nil {
		fmt.Fprintf(os.Stderr, "GraphQL search failed (%v), falling back to REST API...\n", err)
		restPackages, restErr := fetchPackagesREST(server, search, limit, offset, registryNames)
		if restErr != nil {
			return restErr
		}
		if len(restPackages) == 0 {
			fmt.Println("No packages found")
			return nil
		}
		fmt.Printf("Found %d package(s):\n\n", len(restPackages))
		if !verbose {
			fmt.Printf("%-30s %-20s %-12s %s\n", "NAME", "REGISTRY", "VERSION", "DESCRIPTION")
			fmt.Printf("%-30s %-20s %-12s %s\n", strings.Repeat("-", 30), strings.Repeat("-", 20), strings.Repeat("-", 12), strings.Repeat("-", 50))
		}
		for _, pkg := range restPackages {
			displayRESTPackage(pkg, verbose)
		}
		return nil
	}

	if len(packages) == 0 {
		fmt.Println("No packages found")
		return nil
	}

	registryLookup := buildRegistryLookup(server)
	fmt.Printf("Found %d package(s):\n\n", len(packages))

	if !verbose {
		fmt.Printf("%-30s %-20s %-12s %-25s %s\n", "NAME", "OWNER", "VERSION", "REGISTRIES", "DESCRIPTION")
		fmt.Printf("%-30s %-20s %-12s %-25s %s\n", strings.Repeat("-", 30), strings.Repeat("-", 20), strings.Repeat("-", 12), strings.Repeat("-", 25), strings.Repeat("-", 40))
	}

	for _, pkg := range packages {
		if verbose {
			displayPackageDetails(&pkg, registryLookup)
		} else {
			fmt.Printf("%-30s %-20s", pkg.Name, pkg.Owner)

			if len(pkg.RegistryMap) > 0 {
				fmt.Printf(" v%-10s", pkg.RegistryMap[0].Version)
			} else {
				fmt.Printf(" %-12s", "N/A")
			}

			if len(pkg.RegistryMap) > 0 {
				pkgRegistryNames := []string{}
				for _, entry := range pkg.RegistryMap {
					if name, ok := registryLookup[entry.RegistryID]; ok {
						pkgRegistryNames = append(pkgRegistryNames, name)
					}
				}
				registryStr := strings.Join(pkgRegistryNames, ", ")
				if len(registryStr) > 25 {
					registryStr = registryStr[:22] + "..."
				}
				fmt.Printf(" %-25s", registryStr)
			} else {
				fmt.Printf(" %-25s", "")
			}

			if pkg.Metadata != nil && pkg.Metadata.Description != "" {
				desc := pkg.Metadata.Description
				if len(desc) > 40 {
					desc = desc[:40] + "..."
				}
				fmt.Printf("%s", desc)
			}

			fmt.Printf("\n")
		}
		fmt.Println()
	}

	return nil
}

func getPackageInfo(server string, packageName string, registryIDs []int, registryNames []string) error {
	variables := map[string]interface{}{
		"filter":       map[string]interface{}{},
		"order":        map[string]string{"score": "desc"},
		"matchtags":    "{}",
		"licenses":     "{}",
		"search":       packageName,
		"offset":       0,
		"hasfailures":  false,
		"installed":    true,
		"notinstalled": true,
		"limit":        100,
	}

	if len(registryIDs) > 0 {
		variables["registries"] = buildRegistriesParam(registryIDs)
	}

	packages, err := executePackageQuery(server, variables)
	if err != nil {
		fmt.Fprintf(os.Stderr, "GraphQL info failed (%v), falling back to REST API...\n", err)
		restPackages, restErr := fetchPackagesREST(server, packageName, 100, 0, registryNames)
		if restErr != nil {
			return restErr
		}
		for _, pkg := range restPackages {
			if strings.EqualFold(pkg.Name, packageName) {
				displayRESTPackage(pkg, true)
				return nil
			}
		}
		fmt.Println("Package not found")
		return nil
	}

	var pkg *Package
	for i := range packages {
		if strings.EqualFold(packages[i].Name, packageName) {
			pkg = &packages[i]
			break
		}
	}

	if pkg == nil {
		fmt.Println("Package not found")
		return nil
	}

	registryLookup := buildRegistryLookup(server)
	displayPackageDetails(pkg, registryLookup)
	return nil
}

func getPackageDependencies(server string, packageName string, registryName string, showIndirect bool, showAll bool) error {
	// Fetch all registries to get registry IDs for the query
	allRegistries, err := fetchRegistries(server)
	if err != nil {
		return fmt.Errorf("failed to fetch registries: %w", err)
	}

	// Get all registry IDs for the search
	var registryIDs []int
	for _, reg := range allRegistries {
		registryIDs = append(registryIDs, reg.RegistryID)
	}

	// Get package info to find the registry it belongs to
	variables := map[string]interface{}{
		"filter":       map[string]interface{}{},
		"order":        map[string]string{"score": "desc"},
		"matchtags":    "{}",
		"licenses":     "{}",
		"search":       packageName,
		"offset":       0,
		"hasfailures":  false,
		"installed":    true,
		"notinstalled": true,
		"limit":        100,
		"registries":   buildRegistriesParam(registryIDs),
	}

	packages, err := executePackageQuery(server, variables)
	if err != nil {
		return err
	}

	// Find exact match (case-insensitive)
	var pkg *Package
	for i := range packages {
		if strings.EqualFold(packages[i].Name, packageName) {
			pkg = &packages[i]
			break
		}
	}

	if pkg == nil {
		return fmt.Errorf("package not found: %s", packageName)
	}

	if len(pkg.RegistryMap) == 0 {
		return fmt.Errorf("no registry information found for package: %s", packageName)
	}

	// Determine which registry to use
	var targetRegistry string
	if registryName != "" {
		// Use the specified registry
		targetRegistry = registryName
	} else {
		// Use the first registry and fetch its name
		registries, err := fetchRegistries(server)
		if err != nil {
			return fmt.Errorf("failed to fetch registries: %w", err)
		}

		// Find the registry name from the first entry in RegistryMap
		firstRegistryID := pkg.RegistryMap[0].RegistryID
		found := false
		for _, reg := range registries {
			if reg.RegistryID == firstRegistryID {
				targetRegistry = reg.Name
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("failed to find registry name for ID: %d", firstRegistryID)
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

	// Filter dependencies based on showIndirect flag
	var deps []PackageDependency
	if showIndirect {
		deps = docsResp.Dependencies
	} else {
		// Show only direct dependencies
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

	// Display results
	fmt.Printf("Dependencies for %s (v%s) from registry '%s':\n\n", docsResp.Name, docsResp.Version, targetRegistry)
	if !showIndirect {
		// Apply limit for direct dependencies
		directLimit := 10
		displayDeps := deps
		truncated := false
		if !showAll && len(deps) > directLimit {
			displayDeps = deps[:directLimit]
			truncated = true
		}

		if truncated {
			fmt.Printf("Showing %d of %d direct dependencies (use --all to see all, --indirect for indirect)\n\n", len(displayDeps), len(deps))
		} else {
			fmt.Printf("Showing %d direct dependencies (use --indirect to include indirect dependencies)\n\n", len(deps))
		}

		// Print table header
		fmt.Printf("%-35s %-15s %-38s %s\n", "NAME", "REGISTRY", "UUID", "VERSIONS")
		fmt.Printf("%-35s %-15s %-38s %s\n", strings.Repeat("-", 35), strings.Repeat("-", 15), strings.Repeat("-", 38), strings.Repeat("-", 20))

		// Print dependencies
		for _, dep := range displayDeps {
			versionsStr := strings.Join(dep.Versions, ", ")
			if len(versionsStr) > 20 {
				versionsStr = versionsStr[:17] + "..."
			}
			registry := dep.Registry
			if len(registry) > 15 {
				registry = registry[:12] + "..."
			}
			fmt.Printf("%-35s %-15s %-38s %s\n", dep.Name, registry, dep.UUID, versionsStr)
		}
	} else {
		// Separate direct and indirect dependencies
		var directDeps []PackageDependency
		var indirectDeps []PackageDependency
		for _, dep := range deps {
			if dep.Direct {
				directDeps = append(directDeps, dep)
			} else {
				indirectDeps = append(indirectDeps, dep)
			}
		}

		// Apply limits
		directLimit := 10
		indirectLimit := 50
		displayDirectDeps := directDeps
		displayIndirectDeps := indirectDeps
		directTruncated := false
		indirectTruncated := false

		if !showAll {
			if len(directDeps) > directLimit {
				displayDirectDeps = directDeps[:directLimit]
				directTruncated = true
			}
			if len(indirectDeps) > indirectLimit {
				displayIndirectDeps = indirectDeps[:indirectLimit]
				indirectTruncated = true
			}
		}

		// Summary line
		if directTruncated || indirectTruncated {
			fmt.Printf("Showing %d of %d total dependencies (%d of %d direct, %d of %d indirect) - use --all to see all\n\n",
				len(displayDirectDeps)+len(displayIndirectDeps), len(deps),
				len(displayDirectDeps), len(directDeps),
				len(displayIndirectDeps), len(indirectDeps))
		} else {
			fmt.Printf("Showing %d total dependencies (%d direct, %d indirect)\n\n", len(deps), len(directDeps), len(indirectDeps))
		}

		// Print direct dependencies first
		if len(displayDirectDeps) > 0 {
			if directTruncated {
				fmt.Printf("Direct Dependencies (showing %d of %d):\n", len(displayDirectDeps), len(directDeps))
			} else {
				fmt.Printf("Direct Dependencies (%d):\n", len(directDeps))
			}
			fmt.Printf("%-35s %-15s %-38s %s\n", "NAME", "REGISTRY", "UUID", "VERSIONS")
			fmt.Printf("%-35s %-15s %-38s %s\n", strings.Repeat("-", 35), strings.Repeat("-", 15), strings.Repeat("-", 38), strings.Repeat("-", 20))

			for _, dep := range displayDirectDeps {
				versionsStr := strings.Join(dep.Versions, ", ")
				if len(versionsStr) > 20 {
					versionsStr = versionsStr[:17] + "..."
				}
				registry := dep.Registry
				if len(registry) > 15 {
					registry = registry[:12] + "..."
				}
				fmt.Printf("%-35s %-15s %-38s %s\n", dep.Name, registry, dep.UUID, versionsStr)
			}
			fmt.Println()
		}

		// Print indirect dependencies
		if len(displayIndirectDeps) > 0 {
			if indirectTruncated {
				fmt.Printf("Indirect Dependencies (showing %d of %d):\n", len(displayIndirectDeps), len(indirectDeps))
			} else {
				fmt.Printf("Indirect Dependencies (%d):\n", len(indirectDeps))
			}
			fmt.Printf("%-35s %-15s %-38s %s\n", "NAME", "REGISTRY", "UUID", "VERSIONS")
			fmt.Printf("%-35s %-15s %-38s %s\n", strings.Repeat("-", 35), strings.Repeat("-", 15), strings.Repeat("-", 38), strings.Repeat("-", 20))

			for _, dep := range displayIndirectDeps {
				versionsStr := strings.Join(dep.Versions, ", ")
				if len(versionsStr) > 20 {
					versionsStr = versionsStr[:17] + "..."
				}
				registry := dep.Registry
				if len(registry) > 15 {
					registry = registry[:12] + "..."
				}
				fmt.Printf("%-35s %-15s %-38s %s\n", dep.Name, registry, dep.UUID, versionsStr)
			}
		}
	}

	return nil
}
