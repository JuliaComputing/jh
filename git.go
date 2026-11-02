package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// projectLookupFilter matches a project by owner username and project name,
// case-insensitively and literally, so the server returns at most a handful of
// rows instead of every project the caller can see.
func projectLookupFilter(username, projectName string) map[string]interface{} {
	filter := ownerUsernameFilter(username)
	filter["name"] = map[string]interface{}{"_ilike": escapeLikePattern(projectName)}
	return filter
}

// pickProject chooses the requested project among the rows the server matched
// with ILIKE. An exact case-insensitive match wins. Failing that, a single row
// is the server's own case-insensitive match (Postgres folds case by its
// collation, which differs from Go's EqualFold for some non-ASCII letters).
func pickProject(projects []Project, username, projectName string) *Project {
	for i := range projects {
		if strings.EqualFold(projects[i].Owner.Username, username) && strings.EqualFold(projects[i].Name, projectName) {
			return &projects[i]
		}
	}
	if len(projects) == 1 {
		return &projects[0]
	}
	return nil
}

// findProjectByUserAndName finds a project by username and project name
func findProjectByUserAndName(server string, token *StoredToken, userID int64, username, projectName string) (string, error) {
	projects, _, err := fetchProjectsPage(server, token, userID, projectLookupFilter(username, projectName), 1)
	if err != nil {
		return "", fmt.Errorf("failed to query projects: %w", err)
	}

	matchedProject := pickProject(projects, username, projectName)
	if matchedProject == nil {
		return "", fmt.Errorf("project '%s' not found for user '%s'", projectName, username)
	}

	fmt.Printf("Found project: %s by %s (ID: %s)\n", matchedProject.Name, matchedProject.Owner.Username, matchedProject.ID)
	return matchedProject.ID, nil
}

// cloneProject clones a project using git with authorization
func cloneProject(server, projectIdentifier, localPath string) error {
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	userInfo, err := getUserInfo(server)
	if err != nil {
		return fmt.Errorf("failed to get user info: %w", err)
	}

	// Parse the project identifier
	var username, projectName string
	if strings.Contains(projectIdentifier, "/") {
		parts := strings.SplitN(projectIdentifier, "/", 2)
		username = parts[0]
		projectName = parts[1]
	} else {
		// No username provided, use the logged-in user's username
		username = userInfo.Username
		projectName = projectIdentifier
		fmt.Printf("Using logged-in user '%s' as project owner\n", username)
	}

	// Find the project by username and project name
	projectUUID, err := findProjectByUserAndName(server, token, userInfo.ID, username, projectName)
	if err != nil {
		return err
	}

	// Construct the Git URL
	gitURL := fmt.Sprintf("https://%s/git/projects/%s", server, projectUUID)

	// Prepare the git clone command with authorization header
	authHeader := fmt.Sprintf("Authorization: Bearer %s", token.IDToken)

	var cmd *exec.Cmd
	if localPath != "" {
		cmd = exec.Command("git", "-c", fmt.Sprintf("http.extraHeader=%s", authHeader), "clone", gitURL, localPath)
	} else {
		cmd = exec.Command("git", "-c", fmt.Sprintf("http.extraHeader=%s", authHeader), "clone", gitURL)
	}

	// Set up command output
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	fmt.Printf("Cloning project: %s/%s\n", username, projectName)
	fmt.Printf("Git URL: %s\n", gitURL)

	// Execute the git clone command
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("git clone failed: %w", err)
	}

	// If no local path was specified, rename the UUID folder to project name
	var projectFolderPath string
	if localPath == "" {
		uuidFolderPath := fmt.Sprintf("./%s", projectUUID)
		projectFolderPath = fmt.Sprintf("./%s", projectName)

		// Check if the UUID folder exists
		if _, err := os.Stat(uuidFolderPath); err == nil {
			// Check if target folder already exists
			if _, err := os.Stat(projectFolderPath); err == nil {
				// Target folder exists, find an available name
				projectFolderPath = findAvailableFolderName(projectName)
				fmt.Printf("Warning: Folder '%s' already exists, using '%s' instead\n", projectName, strings.TrimPrefix(projectFolderPath, "./"))
			}

			// Rename the folder from UUID to project name
			err = os.Rename(uuidFolderPath, projectFolderPath)
			if err != nil {
				return fmt.Errorf("failed to rename folder from %s to %s: %w", uuidFolderPath, projectFolderPath, err)
			}
			fmt.Printf("Renamed folder from %s to %s\n", projectUUID, strings.TrimPrefix(projectFolderPath, "./"))
		}
	}

	finalPath := getClonePath(localPath, projectName)
	if localPath == "" && projectFolderPath != "" {
		// Use the actual folder name after potential renaming
		finalPath = strings.TrimPrefix(projectFolderPath, "./")
	}
	fmt.Printf("Successfully cloned project to %s\n", finalPath)
	return nil
}

// getClonePath determines the final clone path
func getClonePath(localPath, projectName string) string {
	if localPath != "" {
		return localPath
	}
	return fmt.Sprintf("./%s", projectName)
}

// findAvailableFolderName finds an available folder name by appending a number
func findAvailableFolderName(baseName string) string {
	counter := 1
	for {
		candidateName := fmt.Sprintf("./%s-%d", baseName, counter)
		if _, err := os.Stat(candidateName); os.IsNotExist(err) {
			return candidateName
		}
		counter++
	}
}

// pushProject executes git push with authentication headers and passes through all arguments
func pushProject(server string, args []string) error {
	// Check if git is installed
	if err := checkGitInstalled(); err != nil {
		return err
	}

	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	// Prepare the git push command with authorization header
	authHeader := fmt.Sprintf("Authorization: Bearer %s", token.IDToken)

	// Build the git command with auth header and all passed arguments
	gitArgs := []string{"-c", fmt.Sprintf("http.extraHeader=%s", authHeader), "push"}
	gitArgs = append(gitArgs, args...)

	cmd := exec.Command("git", gitArgs...)

	// Set up command output
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	// Execute the git push command
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("git push failed: %w", err)
	}

	return nil
}

// fetchProject executes git fetch with authentication headers and passes through all arguments
func fetchProject(server string, args []string) error {
	// Check if git is installed
	if err := checkGitInstalled(); err != nil {
		return err
	}

	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	// Prepare the git fetch command with authorization header
	authHeader := fmt.Sprintf("Authorization: Bearer %s", token.IDToken)

	// Build the git command with auth header and all passed arguments
	gitArgs := []string{"-c", fmt.Sprintf("http.extraHeader=%s", authHeader), "fetch"}
	gitArgs = append(gitArgs, args...)

	cmd := exec.Command("git", gitArgs...)

	// Set up command output
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	// Execute the git fetch command
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("git fetch failed: %w", err)
	}

	return nil
}

// pullProject executes git pull with authentication headers and passes through all arguments
func pullProject(server string, args []string) error {
	// Check if git is installed
	if err := checkGitInstalled(); err != nil {
		return err
	}

	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	// Prepare the git pull command with authorization header
	authHeader := fmt.Sprintf("Authorization: Bearer %s", token.IDToken)

	// Build the git command with auth header and all passed arguments
	gitArgs := []string{"-c", fmt.Sprintf("http.extraHeader=%s", authHeader), "pull"}
	gitArgs = append(gitArgs, args...)

	cmd := exec.Command("git", gitArgs...)

	// Set up command output
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	// Execute the git pull command
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("git pull failed: %w", err)
	}

	return nil
}

// checkGitInstalled checks if git is available
func checkGitInstalled() error {
	_, err := exec.LookPath("git")
	if err != nil {
		return fmt.Errorf("git is not installed or not in PATH")
	}
	return nil
}

// gitCredentialHelper implements the git credential helper protocol
func gitCredentialHelper(action string) error {
	switch action {
	case "get":
		return gitCredentialGet()
	case "store", "erase":
		// These are no-ops for JuliaHub since we manage tokens ourselves
		return nil
	default:
		return fmt.Errorf("unknown credential helper action: %s", action)
	}
}

// gitCredentialGet handles the 'get' action for git credential helper
func gitCredentialGet() error {
	// Read input from stdin to get the credential request
	input, err := readCredentialInput()
	if err != nil {
		return fmt.Errorf("failed to read credential input: %w", err)
	}

	// Check if this is a JuliaHub URL
	if !isJuliaHubURL(input["host"]) {
		// Not a JuliaHub URL, return empty (let other credential helpers handle it)
		return nil
	}

	requestedServer := input["host"]

	// Check if we have a stored token and if the server matches
	storedToken, err := readStoredToken()
	if err != nil || storedToken.Server != requestedServer {
		// No stored token or server mismatch - need to authenticate
		fmt.Fprintf(os.Stderr, "JuliaHub CLI: Authenticating to %s...\n", requestedServer)

		// Normalize the server name
		normalizedServer := normalizeServer(requestedServer)

		// Perform device flow authentication
		token, err := deviceFlow(normalizedServer)
		if err != nil {
			return fmt.Errorf("authentication to %s failed: %w", requestedServer, err)
		}

		// Save token and server to config file
		if err := writeTokenToConfig(normalizedServer, *token); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: Failed to save auth config: %v\n", err)
		}

		fmt.Fprintf(os.Stderr, "Successfully authenticated to %s!\n", requestedServer)

		// Output credentials in the format git expects
		fmt.Printf("username=oauth2\n")
		fmt.Printf("password=%s\n", token.IDToken)

		return nil
	}

	// Server matches, ensure we have a valid token
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	// Output credentials in the format git expects
	fmt.Printf("username=oauth2\n")
	fmt.Printf("password=%s\n", token.IDToken)

	return nil
}

// readCredentialInput reads and parses credential input from stdin
func readCredentialInput() (map[string]string, error) {
	input := make(map[string]string)
	scanner := bufio.NewScanner(os.Stdin)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			break
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			input[parts[0]] = parts[1]
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return input, nil
}

// isJuliaHubURL checks if the host is a JuliaHub server
func isJuliaHubURL(host string) bool {
	if host == "" {
		return false
	}

	// Check for juliahub.com and its subdomains
	if strings.HasSuffix(host, "juliahub.com") {
		return true
	}

	// Check for any host that might be a JuliaHub server
	// This is a simple heuristic - in production, you might want to be more specific
	if strings.Contains(host, "juliahub") {
		return true
	}

	// Check against configured server
	configServer, err := readConfigFile()
	if err == nil && host == configServer {
		return true
	}

	return false
}

// gitCredentialSetup configures git to use the JuliaHub credential helper
func gitCredentialSetup() error {
	// Check if git is installed
	if err := checkGitInstalled(); err != nil {
		return err
	}

	// Get the path to the current executable
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Set up credential helper for JuliaHub domains
	juliaHubDomains := []string{
		"juliahub.com",
		"*.juliahub.com",
	}

	// Also check if there's a custom server configured
	configServer, err := readConfigFile()
	if err == nil && configServer != "" && configServer != "juliahub.com" {
		juliaHubDomains = append(juliaHubDomains, configServer)
	}

	fmt.Println("Configuring Git credential helper for JuliaHub...")

	for _, domain := range juliaHubDomains {
		credentialKey := fmt.Sprintf("credential.https://%s.helper", domain)
		credentialValue := fmt.Sprintf("%s git-credential", execPath)

		// Configure git credential helper for this domain
		cmd := exec.Command("git", "config", "--global", credentialKey, credentialValue)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to configure git credential helper for %s: %w", domain, err)
		}
		fmt.Printf("✓ Configured credential helper for %s\n", domain)
	}

	fmt.Println("\nGit credential helper setup complete!")
	fmt.Println("\nYou can now use standard Git commands with JuliaHub repositories:")
	fmt.Println("  git clone https://juliahub.com/git/projects/username/project.git")
	fmt.Println("  git push")
	fmt.Println("  git pull")
	fmt.Println("  git fetch")
	fmt.Println("\nThe JuliaHub CLI will automatically provide authentication when needed.")

	return nil
}
