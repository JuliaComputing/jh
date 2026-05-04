package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// Version information (set during build)
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func getConfigFilePath() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".juliahub")
}

func readConfigFile() (string, error) {
	configPath := getConfigFilePath()
	file, err := os.Open(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "juliahub.com", nil // default server
		}
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "server=") {
			return strings.TrimPrefix(line, "server="), nil
		}
	}
	return "juliahub.com", nil // default if no server line found
}

func writeConfigFile(server string) error {
	configPath := getConfigFilePath()
	file, err := os.Create(configPath)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = fmt.Fprintf(file, "server=%s\n", server)
	if err != nil {
		return err
	}

	return os.Chmod(configPath, 0600)
}

func writeTokenToConfig(server string, token TokenResponse) error {
	configPath := getConfigFilePath()
	file, err := os.Create(configPath)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = fmt.Fprintf(file, "server=%s\n", server)
	if err != nil {
		return err
	}

	if token.AccessToken != "" {
		_, err = fmt.Fprintf(file, "access_token=%s\n", token.AccessToken)
		if err != nil {
			return err
		}
	}

	if token.TokenType != "" {
		_, err = fmt.Fprintf(file, "token_type=%s\n", token.TokenType)
		if err != nil {
			return err
		}
	}

	if token.RefreshToken != "" {
		_, err = fmt.Fprintf(file, "refresh_token=%s\n", token.RefreshToken)
		if err != nil {
			return err
		}
	}

	if token.ExpiresIn != 0 {
		_, err = fmt.Fprintf(file, "expires_in=%d\n", token.ExpiresIn)
		if err != nil {
			return err
		}
	}

	if token.IDToken != "" {
		_, err = fmt.Fprintf(file, "id_token=%s\n", token.IDToken)
		if err != nil {
			return err
		}

		// Extract name and email from ID token
		if claims, err := decodeJWT(token.IDToken); err == nil {
			if claims.Name != "" {
				_, err = fmt.Fprintf(file, "name=%s\n", claims.Name)
				if err != nil {
					return err
				}
			}
			if claims.Email != "" {
				_, err = fmt.Fprintf(file, "email=%s\n", claims.Email)
				if err != nil {
					return err
				}
			}
		}
	}

	return os.Chmod(configPath, 0600)
}

func getServerFromFlagOrConfig(cmd *cobra.Command) (string, error) {
	server, _ := cmd.Flags().GetString("server")
	serverFlagUsed := cmd.Flags().Changed("server")

	if !serverFlagUsed {
		// Read from config file if no -s flag was provided
		configServer, err := readConfigFile()
		if err != nil {
			return "", err
		}
		server = configServer
	}

	return normalizeServer(server), nil
}

func normalizeServer(server string) string {
	if server == "juliahub" {
		return "juliahub.com"
	}
	if strings.HasSuffix(server, ".com") || strings.HasSuffix(server, ".dev") {
		return server
	}
	return server + ".juliahub.com"
}

var rootCmd = &cobra.Command{
	Use:     "jh",
	Short:   "JuliaHub CLI",
	Version: version,
	Long: `A command line interface for interacting with JuliaHub.

JuliaHub is a platform for Julia computing that provides dataset management,
job execution, project management, Git integration, and package hosting capabilities.

Available command categories:
  auth      - Authentication and token management
  dataset   - Dataset operations (list, download, upload, status)
  package   - Package search and exploration
  registry  - Registry management (list registries)
  project   - Project management (list, filter by user)
  user      - User information and profile
  admin     - Administrative commands (user management, token management)
  clone     - Clone projects with automatic authentication
  push      - Push changes with authentication
  fetch     - Fetch updates with authentication
  pull      - Pull changes with authentication
  julia     - Julia installation and management
  run       - Run Julia with JuliaHub configuration
  vuln      - Scan a package for known vulnerabilities

Use 'jh <command> --help' for more information about a specific command.`,
}

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Authentication commands",
	Long: `Manage authentication with JuliaHub.

Authentication uses OAuth2 device flow to securely authenticate with JuliaHub.
Tokens are stored in ~/.juliahub and automatically refreshed when needed.`,
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Login to JuliaHub",
	Long: `Authenticate with JuliaHub using OAuth2 device flow.

This command will:
1. Request a device code from the authentication server
2. Display a verification URL for you to visit
3. Wait for you to authorize the device in your browser
4. Store the authentication tokens locally`,
	Example: "  jh auth login\n  jh auth login -s custom-server.com",
	Run: func(cmd *cobra.Command, args []string) {
		server, _ := cmd.Flags().GetString("server")
		serverFlagUsed := cmd.Flags().Changed("server")

		if !serverFlagUsed {
			// Read from config file if no -s flag was provided
			configServer, err := readConfigFile()
			if err != nil {
				fmt.Printf("Failed to read config: %v\n", err)
				os.Exit(1)
			}
			server = configServer
		}

		server = normalizeServer(server)
		fmt.Printf("Logging in to %s...\n", server)

		token, err := deviceFlow(server)
		if err != nil {
			fmt.Printf("Login failed: %v\n", err)
			os.Exit(1)
		}

		// Save token and server to config file
		if err := writeTokenToConfig(server, *token); err != nil {
			fmt.Printf("Warning: Failed to save auth config: %v\n", err)
		}

		fmt.Println("Successfully authenticated!")

		// Setup Julia credentials after successful authentication
		if err := setupJuliaCredentials(); err != nil {
			fmt.Printf("Warning: Failed to setup Julia credentials: %v\n", err)
		}
	},
}

var authRefreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Refresh authentication token",
	Long: `Refresh the stored authentication token using the refresh token.

This command manually refreshes your authentication token. Tokens are
automatically refreshed when needed, but you can use this command to
refresh them proactively.`,
	Example: "  jh auth refresh",
	Run: func(cmd *cobra.Command, args []string) {
		// Read the current stored token
		storedToken, err := readStoredToken()
		if err != nil {
			fmt.Printf("Failed to read stored token: %v\n", err)
			os.Exit(1)
		}

		if storedToken.RefreshToken == "" {
			fmt.Println("No refresh token found in configuration")
			os.Exit(1)
		}

		fmt.Printf("Refreshing token for server: %s\n", storedToken.Server)

		// Refresh the token
		refreshedToken, err := refreshToken(storedToken.Server, storedToken.RefreshToken)
		if err != nil {
			fmt.Printf("Failed to refresh token: %v\n", err)
			os.Exit(1)
		}

		// Save the refreshed token
		if err := writeTokenToConfig(storedToken.Server, *refreshedToken); err != nil {
			fmt.Printf("Failed to save refreshed token: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("Token refreshed successfully!")

		// Setup Julia credentials after successful refresh
		if err := setupJuliaCredentials(); err != nil {
			fmt.Printf("Warning: Failed to setup Julia credentials: %v\n", err)
		}
	},
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show authentication status",
	Long: `Display information about the current authentication token.

Shows details including:
- Server configuration
- Token validity status
- User information
- Token expiration times
- Available refresh capabilities`,
	Example: "  jh auth status",
	Run: func(cmd *cobra.Command, args []string) {
		// Read the current stored token
		storedToken, err := readStoredToken()
		if err != nil {
			fmt.Printf("Failed to read stored token: %v\n", err)
			fmt.Println("You may need to run 'jh auth login' first")
			os.Exit(1)
		}

		// Display formatted token information
		fmt.Print(formatTokenInfo(storedToken))
	},
}

var authEnvCmd = &cobra.Command{
	Use:   "env",
	Short: "Print environment variables for authentication",
	Long: `Print environment variables for authentication in shell format.

This command ensures you have a valid authentication token and outputs
environment variables that can be used by other tools or scripts.`,
	Example: "  jh auth env\n  eval $(jh auth env)",
	Run: func(cmd *cobra.Command, args []string) {
		if err := authEnvCommand(); err != nil {
			fmt.Printf("Failed to get authentication environment: %v\n", err)
			os.Exit(1)
		}
	},
}

var jobCmd = &cobra.Command{
	Use:   "job",
	Short: "Job management commands",
	Long: `Manage jobs on JuliaHub.

Jobs are computational tasks that run on JuliaHub's infrastructure.
You can submit, monitor, and manage jobs through these commands.

Note: Job functionality is currently in development.`,
}

var jobListCmd = &cobra.Command{
	Use:   "list",
	Short: "List jobs",
	Long: `List all jobs on JuliaHub.

Displays information about your submitted jobs including status,
creation time, and resource usage.

Note: This functionality is currently in development.`,
	Example: "  jh job list",
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Listing jobs from %s...\n", server)
		fmt.Println("This is a placeholder for the job list functionality")
	},
}

var jobStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a job",
	Long: `Start a new job on JuliaHub.

Submit a computational job to run on JuliaHub's infrastructure.
Jobs can include Julia scripts, notebooks, or other computational tasks.

Note: This functionality is currently in development.`,
	Example: "  jh job start",
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Starting job on %s...\n", server)
		fmt.Println("This is a placeholder for the job start functionality")
	},
}

var datasetCmd = &cobra.Command{
	Use:   "dataset",
	Short: "Dataset management commands",
	Long: `Manage datasets on JuliaHub.

Datasets are versioned collections of data files that can be shared
and accessed across JuliaHub. Each dataset has a unique ID and can
have multiple versions.

Supported operations:
- List available datasets
- Download datasets by ID, name, or user/name
- Upload new datasets or new versions
- Check dataset status and download information`,
}

var datasetListCmd = &cobra.Command{
	Use:   "list",
	Short: "List datasets",
	Long: `List all datasets accessible to you on JuliaHub.

Displays information including:
- Dataset ID (UUID)
- Dataset name
- Owner information
- Size and version
- Visibility and tags
- Last modification date`,
	Example: "  jh dataset list\n  jh dataset list -s custom-server.com",
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}

		if err := listDatasets(server); err != nil {
			fmt.Printf("Failed to list datasets: %v\n", err)
			os.Exit(1)
		}
	},
}

var datasetDownloadCmd = &cobra.Command{
	Use:   "download <dataset-identifier> [version] [local-path]",
	Short: "Download a dataset",
	Long: `Download a dataset from JuliaHub.

Dataset identifier can be:
- UUID (e.g., 12345678-1234-5678-9abc-123456789abc)
- Dataset name (e.g., my-dataset)
- User/dataset format (e.g., username/my-dataset)

Version format: v1, v2, v3, etc. If not provided, downloads latest version.
Local path is optional - if not provided, uses dataset name with .tar.gz extension.`,
	Example: "  jh dataset download my-dataset\n  jh dataset download username/my-dataset v2\n  jh dataset download 12345678-1234-5678-9abc-123456789abc v1 ./local-file.tar.gz",
	Args:    cobra.RangeArgs(1, 3),
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}

		datasetID := args[0]
		version := ""
		localPath := ""

		// Parse arguments based on count
		if len(args) >= 2 {
			// Check if second argument is a version (starts with 'v')
			if strings.HasPrefix(args[1], "v") {
				version = args[1]
				if len(args) >= 3 {
					localPath = args[2]
				}
			} else {
				// Second argument is local path
				localPath = args[1]
			}
		}

		if err := downloadDataset(server, datasetID, version, localPath); err != nil {
			fmt.Printf("Failed to download dataset: %v\n", err)
			os.Exit(1)
		}
	},
}

var datasetUploadCmd = &cobra.Command{
	Use:   "upload [dataset-identifier] <file-path>",
	Short: "Upload a dataset",
	Long: `Upload a file to create a new dataset or add a new version to an existing dataset.

Two modes of operation:
1. Create new dataset: Use --new flag with just the file path
2. Add version to existing dataset: Provide dataset identifier and file path

Dataset identifier can be:
- UUID (e.g., 12345678-1234-5678-9abc-123456789abc)
- Dataset name (e.g., my-dataset)
- User/dataset format (e.g., username/my-dataset)

The upload process uses a secure 3-step presigned URL workflow.`,
	Example: "  jh dataset upload --new ./my-data.tar.gz\n  jh dataset upload my-dataset ./new-version.tar.gz\n  jh dataset upload username/my-dataset ./update.tar.gz",
	Args:    cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}

		isNew, _ := cmd.Flags().GetBool("new")

		var datasetID, filePath string

		if len(args) == 1 {
			// Only one argument - must be file path with --new flag
			if !isNew {
				fmt.Printf("Error: --new flag is required when no dataset UUID is provided\n")
				os.Exit(1)
			}
			filePath = args[0]
		} else {
			// Two arguments - first is dataset UUID, second is file path
			if isNew {
				fmt.Printf("Error: --new flag cannot be used with dataset UUID\n")
				os.Exit(1)
			}
			datasetID = args[0]
			filePath = args[1]
		}

		if err := uploadDataset(server, datasetID, filePath, isNew); err != nil {
			fmt.Printf("Failed to upload dataset: %v\n", err)
			os.Exit(1)
		}
	},
}

var datasetStatusCmd = &cobra.Command{
	Use:   "status <dataset-identifier> [version]",
	Short: "Show dataset status",
	Long: `Show dataset status and download information.

Dataset identifier can be:
- UUID (e.g., 12345678-1234-5678-9abc-123456789abc)
- Dataset name (e.g., my-dataset)
- User/dataset format (e.g., username/my-dataset)

Version format: v1, v2, v3, etc. If not provided, shows latest version.

Displays:
- Dataset name and version
- Download URL (presigned)
- Availability status`,
	Example: "  jh dataset status my-dataset\n  jh dataset status username/my-dataset v2\n  jh dataset status 12345678-1234-5678-9abc-123456789abc",
	Args:    cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}

		datasetIdentifier := args[0]
		version := ""
		if len(args) > 1 {
			version = args[1]
		}

		if err := statusDataset(server, datasetIdentifier, version); err != nil {
			fmt.Printf("Failed to get dataset status: %v\n", err)
			os.Exit(1)
		}
	},
}

func registryMutateHelp(verb string) string {
	var verbLine, nameNote, updateNote string
	if verb == "add" {
		verbLine = "Add a new Julia package registry on JuliaHub."
		nameNote = "// required"
	} else {
		verbLine = "Update an existing Julia package registry on JuliaHub."
		nameNote = "// required; identifies the registry to update"
		updateNote = "\n\nThe registry is identified by the \"name\" field. All fields are replaced\nwith the provided values; omitted optional fields revert to defaults."
	}

	return verbLine + `

Reads the registry configuration from a JSON file (--file) or stdin.
The JSON is validated and forwarded to the API as-is.` + updateNote + `

Registry ` + verb + ` is polled until completion (up to 2 minutes).
Use ` + "`jh registry config <name>`" + ` to inspect the result afterward.

REGISTRY JSON SCHEMA

  {
    "name":            "<registry-name>",  ` + nameNote + `
    "license_detect":  true,
    "artifact":        { "download": true },
    "docs":            {
                         "download": true,
                         "docgen_check_installable": false,
                         "html_size_threshold_bytes": null
                       },
    "metadata":        { "download": true },
    "pkg":             { "download": true, "static_analysis_runs": [] },
    "enabled":         true,
    "display_apps":    true,
    "owner":           "<username>",  // optional; defaults to current user
    "sync_schedule":   null,          // or: see SYNC SCHEDULE below
    "download_providers": [ <provider>, ... ]  // required; one or more entries
  }

SYNC SCHEDULE

  {
    "interval_sec": 420,
    "days":         [1, 2, 3, 4, 5, 6, 7],
    "start_hour":   0,
    "end_hour":     24,
    "timezone":     "UTC"
  }

PROVIDER TYPES

  gitserver — sync from a Git repository:
  {
    "type":                   "gitserver",
    "url":                    "<repo-url>",
    "server_type":            "github|gitlab|bitbucket|bare-git",
    "github_credential_type": "pat|app",
    "user_name":              "<user>",
    "credential_key":         "<token-id>",
    "api_host":               null,
    "host":                   ""
  }

  cacheserver — sync from a JuliaHub package cache:
  {
    "type":           "cacheserver",
    "host":           "<hostname>",
    "credential_key": "<token-id>"
  }

  bundle — local bundle (sets license_detect: false automatically):
  {
    "type":           "bundle",
    "credential_key": ""
  }

  genericserver — generic server with basic auth:
  {
    "type": "genericserver",
    "auth": {
      "type":           "basic",
      "user_name":      "<user>",
      "credential_key": "<token-id>"
    }
  }`
}

var vulnCmd = &cobra.Command{
	Use:   "vuln <package-name>",
	Short: "Show known vulnerabilities for a package",
	Long: `Show known security vulnerabilities for a Julia package.

Defaults to checking the latest stable version of the package. Use --version to
check a specific version. Only advisories that affect the queried version are shown
by default; use --all to list all advisories regardless of affected status.

Use --advisory to look up a specific advisory by ID.`,
	Example: "  jh vuln MbedTLS_jll\n  jh vuln MbedTLS_jll --version 2.28.1010+0\n  jh vuln MbedTLS_jll --all\n  jh vuln MbedTLS_jll --advisory JLSEC-2025-232",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}

		packageName := args[0]
		version, _ := cmd.Flags().GetString("version")
		advisory, _ := cmd.Flags().GetString("advisory")
		registry, _ := cmd.Flags().GetString("registry")
		all, _ := cmd.Flags().GetBool("all")
		verbose, _ := cmd.Flags().GetBool("verbose")

		if version == "" {
			latest, err := fetchLatestVersion(server, registry, packageName)
			if err != nil {
				fmt.Printf("Failed to fetch latest version: %v\n", err)
				os.Exit(1)
			}
			version = latest
		}

		vulns, err := fetchVulnerabilities(server, packageName, version)
		if err != nil {
			fmt.Printf("Failed to fetch vulnerabilities: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Package: %s", packageName)
		if version != "" {
			fmt.Printf(" (%s)", version)
		}
		fmt.Println()
		fmt.Println()

		if advisory != "" {
			for _, v := range vulns {
				if strings.EqualFold(v.AdvisoryID, advisory) {
					printAdvisory(&v, verbose)
					return
				}
			}
			fmt.Printf("Advisory %q not found for package %s.\n", advisory, packageName)
			os.Exit(1)
		}

		var toShow []PackageVulnerability
		for _, v := range vulns {
			if all || (v.IsAffected != nil && *v.IsAffected) {
				toShow = append(toShow, v)
			}
		}

		if len(toShow) == 0 {
			if all {
				fmt.Println("No vulnerabilities found.")
			} else {
				fmt.Println("No known vulnerabilities affecting this version.")
			}
			return
		}

		suffix := "ies"
		if len(toShow) == 1 {
			suffix = "y"
		}
		fmt.Printf("Found %d vulnerabilit%s:\n\n", len(toShow), suffix)

		for i := range toShow {
			if i > 0 {
				fmt.Println()
			}
			printAdvisory(&toShow[i], verbose)
		}
	},
}

var packageCmd = &cobra.Command{
	Use:   "package",
	Short: "Package search commands",
	Long: `Search and explore Julia packages on JuliaHub.

Packages are Julia libraries that provide reusable functionality. JuliaHub
hosts packages from multiple registries and provides comprehensive search
capabilities including filtering by tags, registries, and more.`,
}

var packageSearchCmd = &cobra.Command{
	Use:   "search [search-term]",
	Short: "Search for packages",
	Long: `Search for Julia packages on JuliaHub.

Displays package information including:
- Package name, owner, and UUID
- Version information
- Description and repository
- Tags and star count
- License information

Filtering options:
- Filter by registry using --registries flag (searches all registries by default)

Use --verbose flag for comprehensive output, or get a concise summary by default.`,
	Example: "  jh package search dataframes\n  jh package search --verbose plots\n  jh package search --limit 20 ml\n  jh package search --registries General optimization",
	Args:    cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}

		search := ""
		if len(args) > 0 {
			search = args[0]
		}

		limit, _ := cmd.Flags().GetInt("limit")
		offset, _ := cmd.Flags().GetInt("offset")
		verbose, _ := cmd.Flags().GetBool("verbose")
		registryNamesStr, _ := cmd.Flags().GetString("registries")

		// Fetch all registries from the API
		allRegistries, err := fetchRegistries(server)
		if err != nil {
			fmt.Printf("Failed to fetch registries: %v\n", err)
			os.Exit(1)
		}

		// Determine which registry IDs and names to use
		var registryIDs []int
		var registryNames []string
		if registryNamesStr != "" {
			// Use only specified registries
			requestedNames := strings.Split(registryNamesStr, ",")
			for _, requestedName := range requestedNames {
				requestedName = strings.TrimSpace(requestedName)
				if requestedName == "" {
					continue
				}

				// Find matching registry (case-insensitive)
				found := false
				for _, reg := range allRegistries {
					if strings.EqualFold(reg.Name, requestedName) {
						registryIDs = append(registryIDs, reg.RegistryID)
						registryNames = append(registryNames, reg.Name)
						found = true
						break
					}
				}

				if !found {
					fmt.Printf("Registry not found: '%s'\n", requestedName)
					os.Exit(1)
				}
			}
		} else {
			// Use all registries
			for _, reg := range allRegistries {
				registryIDs = append(registryIDs, reg.RegistryID)
				registryNames = append(registryNames, reg.Name)
			}
		}

		if err := searchPackages(PackageSearchParams{
			Server:        server,
			Search:        search,
			Limit:         limit,
			Offset:        offset,
			RegistryIDs:   registryIDs,
			RegistryNames: registryNames,
			Verbose:       verbose,
		}); err != nil {
			fmt.Printf("Failed to search packages: %v\n", err)
			os.Exit(1)
		}
	},
}

var packageInfoCmd = &cobra.Command{
	Use:   "info <package-name>",
	Short: "Get detailed information about a package",
	Long: `Display detailed information about a specific Julia package by exact name match.

Shows comprehensive package information including:
- Package name, UUID, and owner
- Version information and status
- Description and repository
- Tags and star count
- License information
- Documentation links

The package name must match exactly (case-insensitive).`,
	Example: "  jh package info DataFrames\n  jh package info Plots\n  jh package info CSV",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}

		packageName := args[0]
		registryNamesStr, _ := cmd.Flags().GetString("registries")

		// Fetch all registries from the API
		allRegistries, err := fetchRegistries(server)
		if err != nil {
			fmt.Printf("Failed to fetch registries: %v\n", err)
			os.Exit(1)
		}

		var registryIDs []int
		var registryNames []string
		if registryNamesStr != "" {
			requestedNames := strings.Split(registryNamesStr, ",")
			for _, requestedName := range requestedNames {
				requestedName = strings.TrimSpace(requestedName)
				if requestedName == "" {
					continue
				}

				found := false
				for _, reg := range allRegistries {
					if strings.EqualFold(reg.Name, requestedName) {
						registryIDs = append(registryIDs, reg.RegistryID)
						registryNames = append(registryNames, reg.Name)
						found = true
						break
					}
				}

				if !found {
					fmt.Printf("Registry not found: '%s'\n", requestedName)
					os.Exit(1)
				}
			}
		} else {
			for _, reg := range allRegistries {
				registryIDs = append(registryIDs, reg.RegistryID)
				registryNames = append(registryNames, reg.Name)
			}
		}

		if err := getPackageInfo(server, packageName, registryIDs, registryNames); err != nil {
			fmt.Printf("Failed to get package info: %v\n", err)
			os.Exit(1)
		}
	},
}

var packageDependencyCmd = &cobra.Command{
	Use:   "dependency <package-name>",
	Short: "List package dependencies",
	Long: `List dependencies for a specific Julia package.

By default, shows all direct dependencies. Use --indirect flag to include
both direct and indirect dependencies.

The command fetches dependency information from the package documentation
JSON endpoint. If a package exists in multiple registries, it uses the
first registry by default. You can specify a different registry using
the --registry flag.`,
	Example: "  jh package dependency DataFrames\n  jh package dependency --indirect Plots\n  jh package dependency --registry General CSV",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}

		packageName := args[0]
		registryName, _ := cmd.Flags().GetString("registry")
		showIndirect, _ := cmd.Flags().GetBool("indirect")

		if err := getPackageDependencies(server, packageName, registryName, showIndirect); err != nil {
			fmt.Printf("Failed to get package dependencies: %v\n", err)
			os.Exit(1)
		}
	},
}

var registryCmd = &cobra.Command{
	Use:   "registry",
	Short: "Registry management commands",
	Long: `Manage Julia package registries on JuliaHub.

Registries are collections of Julia packages that can be registered and
installed. JuliaHub supports multiple registries including the General
registry, custom organizational registries, and test registries.`,
}

var registryConfigCmd = &cobra.Command{
	Use:     "config <name>",
	Short:   "Show or modify the configuration for a registry",
	Example: "  jh registry config JuliaSimRegistry\n  jh registry config JuliaSimRegistry -s nightly.juliahub.dev",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}
		if err := getRegistryConfig(server, args[0]); err != nil {
			fmt.Printf("Failed to get registry config: %v\n", err)
			os.Exit(1)
		}
	},
}

var registryConfigAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a new registry",
	Long:  registryMutateHelp("add"),
	Example: `  # Cache server — pipe JSON via stdin
  echo '{
    "name": "MyRegistry",
    "license_detect": true,
    "artifact":  {"download": true},
    "docs":      {"download": true, "docgen_check_installable": false, "html_size_threshold_bytes": null},
    "metadata":  {"download": true},
    "pkg":       {"download": true, "static_analysis_runs": []},
    "enabled": true, "display_apps": true, "owner": "admin", "sync_schedule": null,
    "download_providers": [{
      "type": "cacheserver", "host": "https://pkg.juliahub.com",
      "credential_key": "JC Auth Token"
    }]
  }' | jh registry config add

  # Read from file
  jh registry config add --file registry.json

  # GitHub with Personal Access Token
  echo '{
    "name": "MyRegistry",
    "license_detect": true,
    "artifact": {"download": true}, "docs": {"download": true, "docgen_check_installable": false, "html_size_threshold_bytes": null},
    "metadata": {"download": true}, "pkg": {"download": true, "static_analysis_runs": []},
    "enabled": true, "display_apps": true, "owner": "", "sync_schedule": null,
    "download_providers": [{
      "type": "gitserver",
      "url": "https://github.com/MyOrg/MyRegistry.git",
      "server_type": "github", "github_credential_type": "pat",
      "user_name": "myuser", "credential_key": "my-pat-token-id",
      "api_host": null, "host": ""
    }]
  }' | jh registry config add`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}

		filePath, _ := cmd.Flags().GetString("file")

		payload, err := readRegistryPayload(filePath)
		if err != nil {
			fmt.Printf("Failed to read registry payload: %v\n", err)
			os.Exit(1)
		}

		if err := createRegistry(server, payload); err != nil {
			fmt.Printf("Failed to add registry: %v\n", err)
			os.Exit(1)
		}
	},
}

var registryConfigUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update an existing registry",
	Long:  registryMutateHelp("update"),
	Example: `  # Update cache server URL — pipe JSON via stdin
  echo '{
    "name": "MyRegistry",
    "license_detect": true,
    "artifact":  {"download": true},
    "docs":      {"download": true, "docgen_check_installable": false, "html_size_threshold_bytes": null},
    "metadata":  {"download": true},
    "pkg":       {"download": true, "static_analysis_runs": []},
    "enabled": true, "display_apps": true, "owner": "admin", "sync_schedule": null,
    "download_providers": [{
      "type": "cacheserver", "host": "https://pkg-new.juliahub.com",
      "credential_key": "JC Auth Token"
    }]
  }' | jh registry config update

  # Read from file
  jh registry config update --file registry.json

  # Update GitHub registry to use a new credential
  echo '{
    "name": "MyRegistry",
    "license_detect": true,
    "artifact": {"download": true}, "docs": {"download": true, "docgen_check_installable": false, "html_size_threshold_bytes": null},
    "metadata": {"download": true}, "pkg": {"download": true, "static_analysis_runs": []},
    "enabled": true, "display_apps": true, "owner": "", "sync_schedule": null,
    "download_providers": [{
      "type": "gitserver",
      "url": "https://github.com/MyOrg/MyRegistry.git",
      "server_type": "github", "github_credential_type": "pat",
      "user_name": "myuser", "credential_key": "new-pat-token-id",
      "api_host": null, "host": ""
    }]
  }' | jh registry config update`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}

		filePath, _ := cmd.Flags().GetString("file")

		payload, err := readRegistryPayload(filePath)
		if err != nil {
			fmt.Printf("Failed to read registry payload: %v\n", err)
			os.Exit(1)
		}

		if err := updateRegistry(server, payload); err != nil {
			fmt.Printf("Failed to update registry: %v\n", err)
			os.Exit(1)
		}
	},
}

var registryListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registries",
	Long: `List all package registries on JuliaHub.

By default, displays only UUID and Name for each registry.
Use --verbose flag to display comprehensive information including:
- Registry UUID
- Registry name and ID
- Owner information
- Creation date
- Package count
- Description
- Registration status`,
	Example: "  jh registry list\n  jh registry list --verbose\n  jh registry list -s custom-server.com",
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}

		verbose, _ := cmd.Flags().GetBool("verbose")

		if err := listRegistries(server, verbose); err != nil {
			fmt.Printf("Failed to list registries: %v\n", err)
			os.Exit(1)
		}
	},
}

var registryPermissionCmd = &cobra.Command{
	Use:   "permission",
	Short: "Manage registry permissions",
	Long: `Manage access permissions for a Julia package registry.

Permissions control which users and groups can access the registry.
Supported privilege levels:
  download          - read-only access to download packages
  register          - download access plus ability to register packages

The registry owner and admins can always manage permissions regardless of settings.`,
}

var registryPermissionListCmd = &cobra.Command{
	Use:     "list <registry>",
	Short:   "List permissions for a registry",
	Example: "  jh registry permission list MyRegistry\n  jh registry permission list MyRegistry -s custom.juliahub.com",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}
		if err := listRegistryPermissions(server, args[0]); err != nil {
			fmt.Printf("Failed to list permissions: %v\n", err)
			os.Exit(1)
		}
	},
}

var registryPermissionSetCmd = &cobra.Command{
	Use:   "set <registry>",
	Short: "Add or update a permission for a user or group",
	Long: `Add or update access permission for a user or group on a registry.

Exactly one of --user or --group must be provided.
Privilege must be 'download' or 'register'.`,
	Example: "  jh registry permission set MyRegistry --user alice --privilege download\n  jh registry permission set MyRegistry --group devs --privilege register",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}
		user, _ := cmd.Flags().GetString("user")
		group, _ := cmd.Flags().GetString("group")
		privilege, _ := cmd.Flags().GetString("privilege")
		if user == "" && group == "" {
			fmt.Println("Error: one of --user or --group is required")
			os.Exit(1)
		}
		if user != "" && group != "" {
			fmt.Println("Error: only one of --user or --group may be specified")
			os.Exit(1)
		}
		if privilege == "" {
			fmt.Println("Error: --privilege is required (download or register)")
			os.Exit(1)
		}
		if err := setRegistryPermission(server, args[0], user, group, privilege); err != nil {
			fmt.Printf("Failed to set permission: %v\n", err)
			os.Exit(1)
		}
	},
}

var registryPermissionRemoveCmd = &cobra.Command{
	Use:   "remove <registry>",
	Short: "Remove a permission for a user or group",
	Long: `Remove access permission for a user or group from a registry.

Exactly one of --user or --group must be provided.`,
	Example: "  jh registry permission remove MyRegistry --user alice\n  jh registry permission remove MyRegistry --group devs",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}
		user, _ := cmd.Flags().GetString("user")
		group, _ := cmd.Flags().GetString("group")
		if user == "" && group == "" {
			fmt.Println("Error: one of --user or --group is required")
			os.Exit(1)
		}
		if user != "" && group != "" {
			fmt.Println("Error: only one of --user or --group may be specified")
			os.Exit(1)
		}
		if err := removeRegistryPermission(server, args[0], user, group); err != nil {
			fmt.Printf("Failed to remove permission: %v\n", err)
			os.Exit(1)
		}
	},
}

var registryRegistratorCmd = &cobra.Command{
	Use:     "registrator <name>",
	Short:   "Show the registrator configuration for a registry",
	Example: "  jh registry registrator MyRegistry\n  jh registry registrator MyRegistry -s nightly.juliahub.dev",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}
		if err := getRegistrator(server, args[0]); err != nil {
			fmt.Printf("Failed to get registrator config: %v\n", err)
			os.Exit(1)
		}
	},
}

var registryRegistratorUpdateCmd = &cobra.Command{
	Use:   "update <registry>",
	Short: "Update the registrator configuration for a registry",
	Long: `Update the registrator configuration for a Julia package registry.

Reads the registrator configuration from a JSON file (--file) or stdin.

REGISTRATOR JSON SCHEMA

  {
    "enabled":           true,               // enable/disable registrator
    "email":             "<email>",          // required when enabled
    "authorization":     true,               // allow only package authors to register
    "ssl_verify":        true,               // verify SSL certificates
    "registry_fork_url": "<url>",            // URL to a forked registry with write access (optional, null to unset)
    "registry_deps":     ["<registry>", ...] // registries whose packages may be dependencies
  }`,
	Example: "  jh registry registrator update MyRegistry --file registrator.json\n  jh registry registrator MyRegistry | jh registry registrator update MyRegistry",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}
		filePath, _ := cmd.Flags().GetString("file")
		if err := setRegistrator(server, args[0], filePath); err != nil {
			fmt.Printf("Failed to update registrator config: %v\n", err)
			os.Exit(1)
		}
	},
}

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Project management commands",
	Long: `Manage projects on JuliaHub.

Projects are collections of code, data, and configurations that define
a computational environment. They can include Julia packages, datasets,
and job configurations.

Project listing functionality is fully implemented using GraphQL API with
support for filtering by user (--user flag).`,
}

var projectListCmd = &cobra.Command{
	Use:   "list",
	Short: "List projects",
	Long: `List all projects on JuliaHub.

Displays comprehensive information about your projects including:
- Project ID and name
- Owner information
- Description and visibility
- Product type and creation date
- Deployment status (total, running, pending)
- Associated resources and Git repositories
- Tags and user roles
- Archive and deployment status

Uses GraphQL API to fetch detailed project information.`,
	Example: "  jh project list\n  jh project list --user\n  jh project list --user john",
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}

		userFilter, _ := cmd.Flags().GetString("user")
		userFilterProvided := cmd.Flags().Changed("user")

		if err := listProjects(server, userFilter, userFilterProvided); err != nil {
			fmt.Printf("Failed to list projects: %v\n", err)
			os.Exit(1)
		}
	},
}

var juliaCmd = &cobra.Command{
	Use:   "julia",
	Short: "Julia installation and management",
	Long: `Install and manage Julia programming language.

Provides commands to install Julia on your system using platform-specific
installers. Supports Windows (via winget), macOS, and Linux (via official installer).

Julia is required to use the 'jh run' command.`,
}

var juliaInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install Julia",
	Long: `Install Julia programming language on your system.

Installation methods by platform:
- Windows: Uses winget to install from Microsoft Store
- macOS/Linux: Uses the official Julia installer script

If Julia is already installed, this command will report the current version.`,
	Example: "  jh julia install",
	Run: func(cmd *cobra.Command, args []string) {
		if err := juliaInstallCommand(); err != nil {
			fmt.Printf("Failed to install Julia: %v\n", err)
			os.Exit(1)
		}
	},
}

var userCmd = &cobra.Command{
	Use:   "user",
	Short: "User information commands",
	Long: `Display user information from JuliaHub.

Shows comprehensive user information including:
- User ID, name, and username
- Email addresses
- Group memberships
- Roles and permissions
- Terms of service acceptance status

Uses GraphQL API to fetch detailed user information.`,
}

var userListGQLCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all users",
	Example: "  jh user list\n  jh user list -s custom.juliahub.com",
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}
		if err := listUsersGQL(server); err != nil {
			fmt.Printf("Failed to list users: %v\n", err)
			os.Exit(1)
		}
	},
}

var groupCmd = &cobra.Command{
	Use:   "group",
	Short: "Group information commands",
}

var groupListGQLCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all groups",
	Example: "  jh group list\n  jh group list -s custom.juliahub.com",
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}
		if err := listGroupsGQL(server); err != nil {
			fmt.Printf("Failed to list groups: %v\n", err)
			os.Exit(1)
		}
	},
}

var userInfoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show user information",
	Long: `Show detailed user information from JuliaHub.

Displays comprehensive information about the current user including:
- User ID and personal details
- Email addresses
- Group memberships
- Roles and permissions
- Terms of service acceptance status
- Survey submission status`,
	Example: "  jh user info",
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}

		if err := showUserInfo(server); err != nil {
			fmt.Printf("Failed to get user info: %v\n", err)
			os.Exit(1)
		}
	},
}

var userListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all users",
	Long: `List all users from JuliaHub.

By default, displays only Name and Email for each user.
Use --verbose flag to display comprehensive information including:
- UUID and email addresses
- Names
- JuliaHub groups and site groups
- Feature flags

This command requires appropriate administrator permissions to view all users (including staged).`,
	Example: "  jh admin user list\n  jh admin user list --verbose",
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}

		verbose, _ := cmd.Flags().GetBool("verbose")

		if err := listUsers(server, verbose); err != nil {
			fmt.Printf("Failed to list users: %v\n", err)
			os.Exit(1)
		}
	},
}

var cloneCmd = &cobra.Command{
	Use:   "clone <username/project> [local-path]",
	Short: "Clone a project from JuliaHub",
	Long: `Clone a project from JuliaHub using Git.

This command:
1. Looks up the project by username and project name
2. Retrieves the project UUID from JuliaHub
3. Clones the project using Git with proper authentication

The project identifier must be in the format 'username/project'.
The local path is optional - if not provided, clones to './project-name'.

Requires Git to be installed and available in PATH.`,
	Example: "  jh clone john/my-project\n  jh clone jane/data-analysis ./my-local-folder",
	Args:    cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		// Check if git is installed
		if err := checkGitInstalled(); err != nil {
			fmt.Printf("Git check failed: %v\n", err)
			os.Exit(1)
		}

		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}

		projectIdentifier := args[0]
		localPath := ""
		if len(args) > 1 {
			localPath = args[1]
		}

		if err := cloneProject(server, projectIdentifier, localPath); err != nil {
			fmt.Printf("Failed to clone project: %v\n", err)
			os.Exit(1)
		}
	},
}

var pushCmd = &cobra.Command{
	Use:   "push [git-args...]",
	Short: "Push to JuliaHub using Git with authentication",
	Long: `Push to JuliaHub using Git with proper authentication.

This command is a wrapper around 'git push' that automatically adds the
required authentication headers for JuliaHub. All arguments are passed
through to the underlying git push command.

This command must be run from within a cloned JuliaHub project directory.`,
	Example: "  jh push\n  jh push origin main\n  jh push --force\n  jh push origin feature-branch\n  jh push --set-upstream origin main",
	Run: func(cmd *cobra.Command, args []string) {
		// Check if git is installed
		if err := checkGitInstalled(); err != nil {
			fmt.Printf("Git check failed: %v\n", err)
			os.Exit(1)
		}

		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}

		if err := pushProject(server, args); err != nil {
			fmt.Printf("Failed to push: %v\n", err)
			os.Exit(1)
		}
	},
}

var fetchCmd = &cobra.Command{
	Use:   "fetch [git-args...]",
	Short: "Fetch from JuliaHub using Git with authentication",
	Long: `Fetch from JuliaHub using Git with proper authentication.

This command is a wrapper around 'git fetch' that automatically adds the
required authentication headers for JuliaHub. All arguments are passed
through to the underlying git fetch command.

This command must be run from within a cloned JuliaHub project directory.`,
	Example: "  jh fetch\n  jh fetch origin\n  jh fetch --all\n  jh fetch origin main\n  jh fetch --prune",
	Run: func(cmd *cobra.Command, args []string) {
		// Check if git is installed
		if err := checkGitInstalled(); err != nil {
			fmt.Printf("Git check failed: %v\n", err)
			os.Exit(1)
		}

		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}

		if err := fetchProject(server, args); err != nil {
			fmt.Printf("Failed to fetch: %v\n", err)
			os.Exit(1)
		}
	},
}

var pullCmd = &cobra.Command{
	Use:   "pull [git-args...]",
	Short: "Pull from JuliaHub using Git with authentication",
	Long: `Pull from JuliaHub using Git with proper authentication.

This command is a wrapper around 'git pull' that automatically adds the
required authentication headers for JuliaHub. All arguments are passed
through to the underlying git pull command.

This command must be run from within a cloned JuliaHub project directory.`,
	Example: "  jh pull\n  jh pull origin main\n  jh pull --rebase\n  jh pull --no-commit\n  jh pull origin feature-branch",
	Run: func(cmd *cobra.Command, args []string) {
		// Check if git is installed
		if err := checkGitInstalled(); err != nil {
			fmt.Printf("Git check failed: %v\n", err)
			os.Exit(1)
		}

		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}

		if err := pullProject(server, args); err != nil {
			fmt.Printf("Failed to pull: %v\n", err)
			os.Exit(1)
		}
	},
}

var runCmd = &cobra.Command{
	Use:   "run [-- julia-args...]",
	Short: "Run Julia with JuliaHub configuration",
	Long: `Run Julia with JuliaHub configuration and credentials.

This command:
1. Sets up JuliaHub credentials (~/.julia/servers/<server>/auth.toml)
2. Starts Julia with the specified arguments

Arguments after -- are passed directly to Julia without modification.
Use 'jh run setup' to only setup credentials without starting Julia.

Environment variables set when running Julia:
- JULIA_PKG_SERVER: Points to your JuliaHub server
- JULIA_PKG_USE_CLI_GIT: Enables CLI git usage

Requires Julia to be installed (use 'jh julia install' if needed).`,
	Example: `  jh run                                      # Start Julia REPL
  jh run -- script.jl                         # Run a script
  jh run -- -e "println(\"Hi\")"               # Execute code
  jh run -- --project=. --threads=4 script.jl # Run with options`,
	Run: func(cmd *cobra.Command, args []string) {
		// Setup credentials and run Julia
		if err := runJulia(args); err != nil {
			fmt.Printf("Failed to run Julia: %v\n", err)
			os.Exit(1)
		}
	},
}

var runSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Setup JuliaHub credentials for Julia",
	Long: `Setup JuliaHub credentials in ~/.julia/servers/<server>/auth.toml without starting Julia.

This command:
1. Ensures you have valid JuliaHub authentication
2. Creates/updates Julia authentication files (~/.julia/servers/<server>/auth.toml)

Credentials are automatically setup when:
- Running 'jh auth login'
- Running 'jh auth refresh'
- Running 'jh run' (before starting Julia)

This command is useful for explicitly updating credentials without starting Julia.`,
	Example: `  jh run setup  # Setup credentials only`,
	Run: func(cmd *cobra.Command, args []string) {
		// Only setup Julia credentials
		if err := setupJuliaCredentials(); err != nil {
			fmt.Printf("Failed to setup Julia credentials: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Julia credentials setup complete")
	},
}

var gitCredentialCmd = &cobra.Command{
	Use:   "git-credential",
	Short: "Git credential helper commands",
	Long: `Git credential helper for JuliaHub authentication.

This command provides Git credential helper functionality for seamless
authentication with JuliaHub repositories. Use 'jh git-credential setup'
to configure Git to use this helper.`,
}

var gitCredentialHelperCmd = &cobra.Command{
	Use:   "helper",
	Short: "Act as git credential helper (internal use)",
	Long: `Internal command used by Git as a credential helper.

This command is called by Git automatically when credentials are needed
for JuliaHub repositories. It should not be run directly by users.

Git will call this with different actions as separate commands:
- get: Return credentials for authentication
- store: Store credentials (no-op for JuliaHub)
- erase: Erase credentials (no-op for JuliaHub)`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		// Git credential helpers are called as separate commands with action names
		// This shouldn't be called directly - individual action commands should be used
		fmt.Printf("Git credential helper: use specific action commands (get, store, erase)\n")
		os.Exit(1)
	},
}

var gitCredentialGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get credentials for Git (internal use)",
	Long:  `Internal command called by Git to get credentials for JuliaHub repositories.`,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		if err := gitCredentialHelper("get"); err != nil {
			fmt.Printf("Git credential helper failed: %v\n", err)
			os.Exit(1)
		}
	},
}

var gitCredentialStoreCmd = &cobra.Command{
	Use:   "store",
	Short: "Store credentials for Git (internal use)",
	Long:  `Internal command called by Git to store credentials. This is a no-op for JuliaHub.`,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		if err := gitCredentialHelper("store"); err != nil {
			fmt.Printf("Git credential helper failed: %v\n", err)
			os.Exit(1)
		}
	},
}

var gitCredentialEraseCmd = &cobra.Command{
	Use:   "erase",
	Short: "Erase credentials for Git (internal use)",
	Long:  `Internal command called by Git to erase credentials. This is a no-op for JuliaHub.`,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		if err := gitCredentialHelper("erase"); err != nil {
			fmt.Printf("Git credential helper failed: %v\n", err)
			os.Exit(1)
		}
	},
}

var gitCredentialSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Setup git credential helper for JuliaHub",
	Long: `Configure Git to use JuliaHub CLI as a credential helper.

This command configures your local Git installation to automatically
use JuliaHub authentication when accessing JuliaHub repositories.

The configuration is applied globally and will affect all Git operations
for JuliaHub repositories on this machine.

After running this command, you can use standard Git commands
(git clone, git push, git pull, git fetch) with JuliaHub repositories
without needing to use the 'jh' wrapper commands.`,
	Example: "  jh git-credential setup\n  git clone https://juliahub.com/git/projects/username/project.git",
	Run: func(cmd *cobra.Command, args []string) {
		if err := gitCredentialSetup(); err != nil {
			fmt.Printf("Failed to setup git credential helper: %v\n", err)
			os.Exit(1)
		}
	},
}

var adminCmd = &cobra.Command{
	Use:   "admin",
	Short: "Administrative commands",
	Long: `Administrative commands for JuliaHub.

These commands provide administrative functionality for managing JuliaHub
resources such as users, tokens, groups, and system configuration.

Note: Some commands may require administrative permissions.`,
}

var adminUserCmd = &cobra.Command{
	Use:   "user",
	Short: "User management commands",
	Long: `Administrative commands for managing users on JuliaHub.

Provides commands to list and manage users across the JuliaHub instance.

Note: These commands require appropriate administrative permissions.`,
}

var adminLandingCmd = &cobra.Command{
	Use:   "landing-page",
	Short: "Landing page management commands",
	Long: `Administrative commands for managing the custom landing page on JuliaHub.

Provides commands to get, set, or remove the custom markdown landing page
shown to users on the JuliaHub home screen.

Note: These commands require appropriate administrative permissions.`,
}

var landingShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the current landing page content",
	Long: `Fetch the current custom landing page content from JuliaHub.

Displays the markdown content and last-modified date of the custom landing
page. If no custom landing page is set, reports that the default is in use.`,
	Example: "  jh admin landing-page show",
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}
		if err := showLandingPage(server); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	},
}

var landingUpdateCmd = &cobra.Command{
	Use:   "update [markdown-content]",
	Short: "Update the custom landing page content",
	Long: `Update the custom landing page content on JuliaHub.

Provide the markdown content directly as an argument or use --file to read
it from a file. If neither is provided, content is read from stdin.
The content must be valid markdown.`,
	Example: "  jh admin landing-page update '# Welcome'\n  jh admin landing-page update --file landing.md\n  cat landing.md | jh admin landing-page update",
	Args:    cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}

		filePath, _ := cmd.Flags().GetString("file")
		contentArg := ""
		if len(args) > 0 {
			contentArg = args[0]
		}

		content, err := readContentFromFileOrArgOrStdin(filePath, contentArg)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		if err := setLandingPage(server, content); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	},
}

var landingRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove the custom landing page",
	Long: `Remove the custom landing page on JuliaHub.

Removes the custom landing page content, reverting to the default landing
screen. This action can be undone by setting a new landing page with 'update'.`,
	Example: "  jh admin landing-page remove",
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}
		if err := removeLandingPage(server); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	},
}

var adminTokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Token management commands",
	Long: `Administrative commands for managing API tokens on JuliaHub.

Provides commands to list and manage API tokens across the JuliaHub instance.

Note: These commands require appropriate administrative permissions.`,
}

var adminGroupCmd = &cobra.Command{
	Use:   "group",
	Short: "Group management commands",
	Long: `Administrative commands for managing groups on JuliaHub.

Provides commands to list and manage groups across the JuliaHub instance.`,
}

var groupListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all groups",
	Example: "  jh admin group list\n  jh admin group list -s custom.juliahub.com",
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}
		if err := listGroups(server); err != nil {
			fmt.Printf("Failed to list groups: %v\n", err)
			os.Exit(1)
		}
	},
}

var adminCredentialCmd = &cobra.Command{
	Use:   "credential",
	Short: "Credential management commands",
	Long: `Administrative commands for managing credentials on JuliaHub.

Provides commands to list and add credentials including tokens,
SSH keys, and GitHub Apps used for private package registry access.

Note: These commands require appropriate administrative permissions.`,
}

var credentialListCmd = &cobra.Command{
	Use:   "list",
	Short: "List credentials",
	Long: `List all credentials configured on JuliaHub.

Displays credentials grouped by type: Tokens, SSH Keys, and GitHub Apps.

By default, shows Name and URL for tokens, and index number and hostname for SSH keys.
Use --verbose flag to display additional details including:
- Token account login, expiry, scopes, and rate limit info
- SSH host key strings`,
	Example: "  jh admin credential list\n  jh admin credential list --verbose",
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}

		verbose, _ := cmd.Flags().GetBool("verbose")

		if err := listCredentials(server, verbose); err != nil {
			fmt.Printf("Failed to list credentials: %v\n", err)
			os.Exit(1)
		}
	},
}

var credentialAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a credential",
	Long: `Add a new credential to JuliaHub.

Use one of the subcommands to add a specific credential type:
  token      - Add a token-based credential (e.g. GitHub PAT)
  ssh        - Add SSH key credentials (host key + private key)
  github-app - Add a GitHub App credential`,
}

var credentialAddTokenCmd = &cobra.Command{
	Use:   "token [JSON]",
	Short: "Add a token credential",
	Long: `Add a token-based registry credential (e.g. a GitHub personal access token).

Accepts a JSON object as a positional argument or from stdin (use "-" or omit
the argument to read from stdin).

JSON fields:
  name   string  Token name (required)
  url    string  URL prefix this token applies to (required)
  value  string  Token value (required)`,
	Example: `  jh admin credential add token '{"name":"MyGHToken","url":"https://github.com","value":"ghp_xxxx"}'
  echo '{"name":"MyGHToken","url":"https://github.com","value":"ghp_xxxx"}' | jh admin credential add token`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}

		jsonData, err := readJSONInput(args)
		if err != nil {
			fmt.Printf("Failed to read input: %v\n", err)
			os.Exit(1)
		}

		if err := addCredentialToken(server, jsonData); err != nil {
			fmt.Printf("Failed to add token credential: %v\n", err)
			os.Exit(1)
		}
	},
}

var credentialAddSSHCmd = &cobra.Command{
	Use:   "ssh [JSON]",
	Short: "Add an SSH credential",
	Long: `Add SSH key credential.

Accepts a JSON object as a positional argument or from stdin (use "-" or omit
the argument to read from stdin).

JSON fields:
  host_key         string  SSH host key string, e.g. from ssh-keyscan (required)
  private_key      string  Raw SSH private key content (PEM)
  private_key_file string  Path to SSH private key file

Provide either private_key or private_key_file, not both.`,
	Example: `  jh admin credential add ssh '{"host_key":"github.com ssh-ed25519 AAAA...","private_key_file":"/home/user/.ssh/id_ed25519"}'
  jh admin credential add ssh '{"host_key":"github.com ssh-ed25519 AAAA...","private_key":"-----BEGIN OPENSSH PRIVATE KEY-----\n..."}'`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}

		jsonData, err := readJSONInput(args)
		if err != nil {
			fmt.Printf("Failed to read input: %v\n", err)
			os.Exit(1)
		}

		if err := addCredentialSSH(server, jsonData); err != nil {
			fmt.Printf("Failed to add SSH credential: %v\n", err)
			os.Exit(1)
		}
	},
}

var credentialAddGitHubAppCmd = &cobra.Command{
	Use:   "github-app [JSON]",
	Short: "Add a GitHub App credential",
	Long: `Add a GitHub App credential.

Accepts a JSON object as a positional argument or from stdin (use "-" or omit
the argument to read from stdin).

JSON fields:
  app_id           string  GitHub App numeric ID (required)
  url              string  URL prefix this App applies to (required)
  private_key      string  Raw App private key content (PEM)
  private_key_file string  Path to GitHub App private key (.pem) file

Provide either private_key or private_key_file, not both.`,
	Example: `  jh admin credential add github-app '{"app_id":"12345","url":"https://github.com/my-org","private_key_file":"app.pem"}'
  jh admin credential add github-app '{"app_id":"12345","url":"https://github.com/my-org","private_key":"-----BEGIN RSA PRIVATE KEY-----\n..."}'`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}

		jsonData, err := readJSONInput(args)
		if err != nil {
			fmt.Printf("Failed to read input: %v\n", err)
			os.Exit(1)
		}

		if err := addCredentialGitHubApp(server, jsonData); err != nil {
			fmt.Printf("Failed to add GitHub App credential: %v\n", err)
			os.Exit(1)
		}
	},
}

var credentialUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update an existing credential",
	Long: `Update an existing credential on JuliaHub.

Use one of the subcommands to update a specific credential type:
  token      - Update a token credential (url and/or value)
  ssh        - Update an SSH credential (host key and/or private key)
  github-app - Update a GitHub App credential (url and/or private key)`,
}

var credentialUpdateTokenCmd = &cobra.Command{
	Use:   "token [JSON]",
	Short: "Update a token credential",
	Long: `Update an existing token credential.

Accepts a JSON object as a positional argument or from stdin.

JSON fields:
  name   string  Token name — identifies the token to update (required)
  url    string  New URL prefix
  value  string  New token value

At least one of url or value must be provided.`,
	Example: `  jh admin credential update token '{"name":"MyGHToken","url":"https://github.com/new-org"}'
  jh admin credential update token '{"name":"MyGHToken","value":"ghp_newvalue"}'`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}
		jsonData, err := readJSONInput(args)
		if err != nil {
			fmt.Printf("Failed to read input: %v\n", err)
			os.Exit(1)
		}
		if err := updateCredentialToken(server, jsonData); err != nil {
			fmt.Printf("Failed to update token credential: %v\n", err)
			os.Exit(1)
		}
	},
}

var credentialUpdateSSHCmd = &cobra.Command{
	Use:   "ssh [JSON]",
	Short: "Update an SSH credential",
	Long: `Update an existing SSH credential by its 1-based index.

Accepts a JSON object as a positional argument or from stdin.

JSON fields:
  index            int     1-based position in the SSH key list (required)
  host_key         string  New SSH host key string
  private_key      string  New raw SSH private key content (PEM)
  private_key_file string  Path to new SSH private key file

At least one of host_key, private_key, or private_key_file must be provided.
Provide either private_key or private_key_file, not both.`,
	Example: `  jh admin credential update ssh '{"index":1,"host_key":"github.com ssh-ed25519 AAAA..."}'
  jh admin credential update ssh '{"index":1,"private_key_file":"/home/user/.ssh/new_key"}'`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}
		jsonData, err := readJSONInput(args)
		if err != nil {
			fmt.Printf("Failed to read input: %v\n", err)
			os.Exit(1)
		}
		if err := updateCredentialSSH(server, jsonData); err != nil {
			fmt.Printf("Failed to update SSH credential: %v\n", err)
			os.Exit(1)
		}
	},
}

var credentialUpdateGitHubAppCmd = &cobra.Command{
	Use:   "github-app [JSON]",
	Short: "Update a GitHub App credential",
	Long: `Update an existing GitHub App credential.

Accepts a JSON object as a positional argument or from stdin.

JSON fields:
  app_id           string  GitHub App ID — identifies the App to update (required)
  url              string  New URL prefix
  private_key      string  New raw App private key content (PEM)
  private_key_file string  Path to new GitHub App private key (.pem) file

At least one of url, private_key, or private_key_file must be provided.
Provide either private_key or private_key_file, not both.`,
	Example: `  jh admin credential update github-app '{"app_id":"12345","url":"https://github.com/new-org"}'
  jh admin credential update github-app '{"app_id":"12345","private_key_file":"new_app.pem"}'`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}
		jsonData, err := readJSONInput(args)
		if err != nil {
			fmt.Printf("Failed to read input: %v\n", err)
			os.Exit(1)
		}
		if err := updateCredentialGitHubApp(server, jsonData); err != nil {
			fmt.Printf("Failed to update GitHub App credential: %v\n", err)
			os.Exit(1)
		}
	},
}

var credentialDeleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a credential",
	Long: `Delete a credential from JuliaHub.

Use one of the subcommands to delete a specific credential type:
  token      - Delete a token by name
  ssh        - Delete an SSH key by 1-based index
  github-app - Delete a GitHub App by App ID`,
}

var credentialDeleteTokenCmd = &cobra.Command{
	Use:     "token <name>",
	Short:   "Delete a token credential",
	Example: "  jh admin credential delete token MyGHToken",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}
		if err := deleteCredentialToken(server, args[0]); err != nil {
			fmt.Printf("Failed to delete token credential: %v\n", err)
			os.Exit(1)
		}
	},
}

var credentialDeleteSSHCmd = &cobra.Command{
	Use:     "ssh <index>",
	Short:   "Delete an SSH credential by 1-based index",
	Example: "  jh admin credential delete ssh 1",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}
		var index int
		if _, err := fmt.Sscan(args[0], &index); err != nil || index <= 0 {
			fmt.Printf("Invalid index %q: must be a positive integer\n", args[0])
			os.Exit(1)
		}
		if err := deleteCredentialSSH(server, index); err != nil {
			fmt.Printf("Failed to delete SSH credential: %v\n", err)
			os.Exit(1)
		}
	},
}

var credentialDeleteGitHubAppCmd = &cobra.Command{
	Use:     "github-app <app-id>",
	Short:   "Delete a GitHub App credential by App ID",
	Example: "  jh admin credential delete github-app 12345",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}
		if err := deleteCredentialGitHubApp(server, args[0]); err != nil {
			fmt.Printf("Failed to delete GitHub App credential: %v\n", err)
			os.Exit(1)
		}
	},
}

var tokenListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all tokens",
	Long: `List all API tokens from JuliaHub.

By default, displays only Subject, Created By, and Expired status for each token.
Use --verbose flag to display comprehensive information including:
- Subject and signature
- Created by and creation date
- Expiration date (with estimate indicator)
- Expiration status

This command requires appropriate permissions to view all tokens.`,
	Example: "  jh admin token list\n  jh admin token list --verbose",
	Run: func(cmd *cobra.Command, args []string) {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			fmt.Printf("Failed to get server config: %v\n", err)
			os.Exit(1)
		}

		verbose, _ := cmd.Flags().GetBool("verbose")

		if err := listTokens(server, verbose); err != nil {
			fmt.Printf("Failed to list tokens: %v\n", err)
			os.Exit(1)
		}
	},
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update jh to the latest version",
	Long: `Check for updates and automatically download and install the latest version of jh.

This command fetches the latest release information from GitHub and compares
it with the current version. If an update is available, it downloads and runs
the appropriate install script for your platform.

The update process will replace the current installation with the latest version.`,
	Example: "  jh update\n  jh update --force",
	Run: func(cmd *cobra.Command, args []string) {
		force, _ := cmd.Flags().GetBool("force")
		if err := runUpdate(force); err != nil {
			fmt.Printf("Update failed: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	authLoginCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	jobListCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	jobStartCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	datasetListCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	datasetDownloadCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	datasetUploadCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	datasetUploadCmd.Flags().Bool("new", false, "Create a new dataset")
	datasetStatusCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	packageSearchCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	packageSearchCmd.Flags().Int("limit", 10, "Maximum number of results to return")
	packageSearchCmd.Flags().Int("offset", 0, "Number of results to skip")
	packageSearchCmd.Flags().String("registries", "", "Filter by registry names (comma-separated, e.g., 'General,CustomRegistry')")
	packageSearchCmd.Flags().Bool("verbose", false, "Show detailed package information")
	packageInfoCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	packageInfoCmd.Flags().String("registries", "", "Filter by registry names (comma-separated, e.g., 'General,CustomRegistry')")
	packageDependencyCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	packageDependencyCmd.Flags().String("registry", "", "Specify registry name (uses first registry if not specified)")
	packageDependencyCmd.Flags().Bool("indirect", false, "Include indirect dependencies")
	registryListCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	registryListCmd.Flags().Bool("verbose", false, "Show detailed registry information")
	projectListCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	projectListCmd.Flags().String("user", "", "Filter projects by user (leave empty to show only your own projects)")
	userInfoCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	userListCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	userListCmd.Flags().Bool("verbose", false, "Show detailed user information")
	tokenListCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	tokenListCmd.Flags().Bool("verbose", false, "Show detailed token information")
	credentialListCmd.Flags().Bool("verbose", false, "Show detailed credential information")
	landingShowCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	landingUpdateCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	landingUpdateCmd.Flags().StringP("file", "f", "", "Path to a markdown file to use as landing page content")
	landingRemoveCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	cloneCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	pushCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	fetchCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	pullCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	updateCmd.Flags().Bool("force", false, "Force update even if current version is newer than latest release")
	vulnCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	vulnCmd.Flags().StringP("version", "V", "", "Package version to check (defaults to latest stable)")
	vulnCmd.Flags().StringP("advisory", "a", "", "Look up a specific advisory by ID")
	vulnCmd.Flags().StringP("registry", "r", "General", "Registry name for version lookup")
	vulnCmd.Flags().Bool("all", false, "Show all advisories regardless of affected status")
	vulnCmd.Flags().BoolP("verbose", "v", false, "Show full advisory details (aliases, dates, details, references)")

	authCmd.AddCommand(authLoginCmd, authRefreshCmd, authStatusCmd, authEnvCmd)
	jobCmd.AddCommand(jobListCmd, jobStartCmd)
	datasetCmd.AddCommand(datasetListCmd, datasetDownloadCmd, datasetUploadCmd, datasetStatusCmd)
	packageCmd.AddCommand(packageSearchCmd, packageInfoCmd, packageDependencyCmd)
	registryConfigCmd.Flags().StringP("server", "s", "", "JuliaHub server")
	registryConfigAddCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	registryConfigAddCmd.Flags().StringP("file", "f", "", "Path to JSON config file (reads from stdin if omitted)")
	registryConfigUpdateCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	registryConfigUpdateCmd.Flags().StringP("file", "f", "", "Path to JSON config file (reads from stdin if omitted)")
	registryConfigCmd.AddCommand(registryConfigAddCmd, registryConfigUpdateCmd)
	registryPermissionListCmd.Flags().StringP("server", "s", "", "JuliaHub server")
	registryPermissionSetCmd.Flags().StringP("server", "s", "", "JuliaHub server")
	registryPermissionSetCmd.Flags().String("user", "", "Username to set permission for")
	registryPermissionSetCmd.Flags().String("group", "", "Group name to set permission for")
	registryPermissionSetCmd.Flags().String("privilege", "", "Privilege level: download or register")
	registryPermissionRemoveCmd.Flags().StringP("server", "s", "", "JuliaHub server")
	registryPermissionRemoveCmd.Flags().String("user", "", "Username to remove permission for")
	registryPermissionRemoveCmd.Flags().String("group", "", "Group name to remove permission for")
	registryPermissionCmd.AddCommand(registryPermissionListCmd, registryPermissionSetCmd, registryPermissionRemoveCmd)
	registryRegistratorCmd.Flags().StringP("server", "s", "", "JuliaHub server")
	registryRegistratorUpdateCmd.Flags().StringP("server", "s", "", "JuliaHub server")
	registryRegistratorUpdateCmd.Flags().StringP("file", "f", "", "Path to JSON config file (reads from stdin if omitted)")
	registryRegistratorCmd.AddCommand(registryRegistratorUpdateCmd)
	registryCmd.AddCommand(registryListCmd, registryConfigCmd, registryPermissionCmd, registryRegistratorCmd)
	projectCmd.AddCommand(projectListCmd)
	userListGQLCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	userCmd.AddCommand(userInfoCmd, userListGQLCmd)
	groupListGQLCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	groupCmd.AddCommand(groupListGQLCmd)
	groupListCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	adminGroupCmd.AddCommand(groupListCmd)
	adminUserCmd.AddCommand(userListCmd)
	adminTokenCmd.AddCommand(tokenListCmd)
	credentialAddCmd.AddCommand(credentialAddTokenCmd, credentialAddSSHCmd, credentialAddGitHubAppCmd)
	credentialUpdateCmd.AddCommand(credentialUpdateTokenCmd, credentialUpdateSSHCmd, credentialUpdateGitHubAppCmd)
	credentialDeleteCmd.AddCommand(credentialDeleteTokenCmd, credentialDeleteSSHCmd, credentialDeleteGitHubAppCmd)
	adminCredentialCmd.AddCommand(credentialListCmd, credentialAddCmd, credentialUpdateCmd, credentialDeleteCmd)
	adminLandingCmd.AddCommand(landingShowCmd, landingUpdateCmd, landingRemoveCmd)
	adminCmd.AddCommand(adminUserCmd, adminTokenCmd, adminGroupCmd, adminCredentialCmd, adminLandingCmd)
	juliaCmd.AddCommand(juliaInstallCmd)
	runCmd.AddCommand(runSetupCmd)
	gitCredentialCmd.AddCommand(gitCredentialHelperCmd, gitCredentialGetCmd, gitCredentialStoreCmd, gitCredentialEraseCmd, gitCredentialSetupCmd)

	rootCmd.AddCommand(authCmd, jobCmd, datasetCmd, projectCmd, packageCmd, registryCmd, userCmd, groupCmd, adminCmd, juliaCmd, cloneCmd, pushCmd, fetchCmd, pullCmd, runCmd, gitCredentialCmd, updateCmd, vulnCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
