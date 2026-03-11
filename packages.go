package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

//go:embed package_search_with_count.gql
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
}

// packageInfo is a common display struct used by both REST and GraphQL paths.
type packageInfo struct {
	Name        string
	UUID        string
	Owner       string
	Registry    string
	Version     string
	Description string
	SourceURL   string
	Tags        []string
	Stars       int
	DocsURL     string
	License     string
	IsApp       bool
	Score       float64
	Status      string
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

func searchPackagesREST(params PackageSearchParams) error {
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	url := fmt.Sprintf("https://%s/packages/info", params.Server)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	q := req.URL.Query()
	if params.Search != "" {
		q.Add("name", params.Search)
	}
	if params.Limit > 0 {
		q.Add("limit", fmt.Sprintf("%d", params.Limit))
	}
	if params.Offset > 0 {
		q.Add("offset", fmt.Sprintf("%d", params.Offset))
	}
	if len(params.RegistryNames) > 0 {
		q.Add("registries", strings.Join(params.RegistryNames, ","))
	}
	req.URL.RawQuery = q.Encode()

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.IDToken))
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
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

	var response PackageRESTListResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	pkgs := make([]packageInfo, len(response.Packages))
	for i, p := range response.Packages {
		pkgs[i] = packageInfo{
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

	printPackages(pkgs, response.Meta.Total, params.Verbose)
	return nil
}

func searchPackages(params PackageSearchParams) error {
	err := searchPackagesREST(params)
	if err != nil {
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

func searchPackagesGraphQL(params PackageSearchParams) error {
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	queryBytes, err := packageSearchFS.ReadFile("package_search_with_count.gql")
	if err != nil {
		return fmt.Errorf("failed to read GraphQL query: %w", err)
	}

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
	if params.Search != "" {
		variables["search"] = params.Search
	}
	if params.Limit > 0 {
		variables["limit"] = params.Limit
	}
	if params.Offset > 0 {
		variables["offset"] = params.Offset
	}
	if len(params.RegistryIDs) > 0 {
		registryStrs := make([]string, len(params.RegistryIDs))
		for i, id := range params.RegistryIDs {
			registryStrs[i] = fmt.Sprintf("%d", id)
		}
		variables["registries"] = fmt.Sprintf("{%s}", strings.Join(registryStrs, ","))
	}

	body, err := executeGraphQL(params.Server, token, GraphQLRequest{
		OperationName: "FilteredPackagesWithCount",
		Query:         string(queryBytes),
		Variables:     variables,
	})
	if err != nil {
		return err
	}

	var response PackageSearchResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("failed to parse GraphQL response: %w", err)
	}
	if len(response.Errors) > 0 {
		return fmt.Errorf("GraphQL errors: %v", response.Errors)
	}

	registryIDToName := make(map[int]string, len(params.RegistryIDs))
	for i, id := range params.RegistryIDs {
		registryIDToName[id] = params.RegistryNames[i]
	}

	gqlPkgs := response.Data.PackageSearch
	pkgs := make([]packageInfo, len(gqlPkgs))
	for i, p := range gqlPkgs {
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
		pkgs[i] = info
	}

	printPackages(pkgs, response.Data.PackageAggregate.Aggregate.Count, params.Verbose)
	return nil
}
