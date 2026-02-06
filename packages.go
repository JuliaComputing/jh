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
		PackageSearch []Package `json:"package_search"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// executePackageQuery executes a GraphQL package search query and returns the results
func executePackageQuery(server string, variables map[string]interface{}) ([]Package, error) {
	token, err := ensureValidToken()
	if err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}

	// Read the GraphQL query from package_search.gql
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

	// Check for GraphQL errors
	if len(response.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL errors: %v", response.Errors)
	}

	return response.Data.PackageSearch, nil
}

// displayPackageDetails displays detailed information about a package
func displayPackageDetails(pkg *Package) {
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

	if pkg.RegistryMap != nil {
		fmt.Printf("Latest Version: %s\n", pkg.RegistryMap.Version)
		fmt.Printf("Status: ")
		if pkg.RegistryMap.Status {
			fmt.Printf("Active\n")
		} else {
			fmt.Printf("Inactive\n")
		}
	}

	fmt.Printf("Installed: %t\n", pkg.Installed)

	if pkg.IsApp {
		fmt.Printf("Type: Application\n")
	}

	if len(pkg.Failures) > 0 {
		fmt.Printf("Failed Versions: ")
		versions := make([]string, len(pkg.Failures))
		for i, failure := range pkg.Failures {
			versions[i] = failure.PackageVersion
		}
		fmt.Printf("%s\n", strings.Join(versions, ", "))
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

func searchPackages(server string, search string, limit int, offset int, installed *bool, notInstalled *bool, hasFailures *bool, registries []int, verbose bool) error {
	// Build variables for the GraphQL query
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

	if installed != nil {
		variables["installed"] = *installed
	}

	if notInstalled != nil {
		variables["notinstalled"] = *notInstalled
	}

	if hasFailures != nil {
		variables["hasfailures"] = *hasFailures
	}

	if len(registries) > 0 {
		variables["registries"] = buildRegistriesParam(registries)
	}

	packages, err := executePackageQuery(server, variables)
	if err != nil {
		return err
	}

	if len(packages) == 0 {
		fmt.Println("No packages found")
		return nil
	}

	fmt.Printf("Found %d package(s):\n\n", len(packages))

	// Print column headers for concise output
	if !verbose {
		fmt.Printf("%-30s %-20s %-12s %s\n", "NAME", "OWNER", "VERSION", "DESCRIPTION")
		fmt.Printf("%-30s %-20s %-12s %s\n", strings.Repeat("-", 30), strings.Repeat("-", 20), strings.Repeat("-", 12), strings.Repeat("-", 50))
	}

	for _, pkg := range packages {
		if verbose {
			// Verbose output with all details
			displayPackageDetails(&pkg)
		} else {
			// Concise output
			fmt.Printf("%-30s %-20s", pkg.Name, pkg.Owner)

			if pkg.RegistryMap != nil {
				fmt.Printf(" v%-10s", pkg.RegistryMap.Version)
			} else {
				fmt.Printf(" %-12s", "N/A")
			}

			if pkg.Installed {
				fmt.Printf(" [Installed]")
			}

			if pkg.Metadata != nil && pkg.Metadata.Description != "" {
				// Truncate description for concise view
				desc := pkg.Metadata.Description
				if len(desc) > 50 {
					desc = desc[:50] + "..."
				}
				fmt.Printf("%s", desc)
			}

			fmt.Printf("\n")
		}

		fmt.Println()
	}

	return nil
}

func getPackageInfo(server string, packageName string, registries []int) error {
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
		"limit":        100, // Get more results to find exact match
	}

	if len(registries) > 0 {
		variables["registries"] = buildRegistriesParam(registries)
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
		fmt.Println("Package not found")
		return nil
	}

	displayPackageDetails(pkg)
	return nil
}
