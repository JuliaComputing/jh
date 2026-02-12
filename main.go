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
  registry  - Registry management (list registries)
  project   - Project management (list, filter by user)
  user      - User information and profile
  admin     - Administrative commands (user management)
  clone     - Clone projects with automatic authentication
  push      - Push changes with authentication
  fetch     - Fetch updates with authentication
  pull      - Pull changes with authentication
  julia     - Julia installation and management
  run       - Run Julia with JuliaHub configuration

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

var registryCmd = &cobra.Command{
	Use:   "registry",
	Short: "Registry management commands",
	Long: `Manage Julia package registries on JuliaHub.

Registries are collections of Julia packages that can be registered and
installed. JuliaHub supports multiple registries including the General
registry, custom organizational registries, and test registries.`,
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

This command uses the /app/config/features/manage endpoint which requires
appropriate permissions to view all users.`,
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
resources such as users, groups, and system configuration.

Note: Some commands may require administrative permissions.`,
}

var adminUserCmd = &cobra.Command{
	Use:   "user",
	Short: "User management commands",
	Long: `Administrative commands for managing users on JuliaHub.

Provides commands to list and manage users across the JuliaHub instance.

Note: These commands require appropriate administrative permissions.`,
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
	registryListCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	registryListCmd.Flags().Bool("verbose", false, "Show detailed registry information")
	projectListCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	projectListCmd.Flags().String("user", "", "Filter projects by user (leave empty to show only your own projects)")
	userInfoCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	userListCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	userListCmd.Flags().Bool("verbose", false, "Show detailed user information")
	cloneCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	pushCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	fetchCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	pullCmd.Flags().StringP("server", "s", "juliahub.com", "JuliaHub server")
	updateCmd.Flags().Bool("force", false, "Force update even if current version is newer than latest release")

	authCmd.AddCommand(authLoginCmd, authRefreshCmd, authStatusCmd, authEnvCmd)
	jobCmd.AddCommand(jobListCmd, jobStartCmd)
	datasetCmd.AddCommand(datasetListCmd, datasetDownloadCmd, datasetUploadCmd, datasetStatusCmd)
	registryCmd.AddCommand(registryListCmd)
	projectCmd.AddCommand(projectListCmd)
	userCmd.AddCommand(userInfoCmd)
	adminUserCmd.AddCommand(userListCmd)
	adminCmd.AddCommand(adminUserCmd)
	juliaCmd.AddCommand(juliaInstallCmd)
	runCmd.AddCommand(runSetupCmd)
	gitCredentialCmd.AddCommand(gitCredentialHelperCmd, gitCredentialGetCmd, gitCredentialStoreCmd, gitCredentialEraseCmd, gitCredentialSetupCmd)

	rootCmd.AddCommand(authCmd, jobCmd, datasetCmd, projectCmd, registryCmd, userCmd, adminCmd, juliaCmd, cloneCmd, pushCmd, fetchCmd, pullCmd, runCmd, gitCredentialCmd, updateCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
