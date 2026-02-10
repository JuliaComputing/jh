package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func getJuliaDepotPath() (string, error) {
	var depotPath string
	if juliaDepot := os.Getenv("JULIA_DEPOT_PATH"); juliaDepot != "" {
		// Use first path from JULIA_DEPOT_PATH (paths are separated by : on Unix, ; on Windows)
		depotPaths := filepath.SplitList(juliaDepot)
		if len(depotPaths) > 0 {
			depotPath = depotPaths[0]
		}
	}

	// Fall back to ~/.julia if JULIA_DEPOT_PATH is not set
	if depotPath == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get user home directory: %w", err)
		}
		depotPath = filepath.Join(homeDir, ".julia")
	}

	return depotPath, nil
}

func createJuliaAuthFile(server string, token *StoredToken) error {
	depotPath, err := getJuliaDepotPath()
	if err != nil {
		return err
	}

	// Create {depot}/servers/{server}/ directory
	serverDir := filepath.Join(depotPath, "servers", server)
	if err := os.MkdirAll(serverDir, 0755); err != nil {
		return fmt.Errorf("failed to create server directory: %w", err)
	}

	// Parse token to get expiration time
	claims, err := decodeJWT(token.IDToken)
	if err != nil {
		return fmt.Errorf("failed to decode JWT token: %w", err)
	}

	// Calculate refresh URL
	var authServer string
	if server == "juliahub.com" {
		authServer = "auth.juliahub.com"
	} else {
		authServer = server
	}
	refreshURL := fmt.Sprintf("https://%s/dex/token", authServer)

	// Write TOML content
	content := fmt.Sprintf(`expires_at = %d
id_token = "%s"
access_token = "%s"
refresh_token = "%s"
refresh_url = "%s"
expires_in = %d
user_email = "%s"
expires = %d
user_name = "%s"
name = "%s"
`,
		claims.ExpiresAt,
		token.IDToken,
		token.AccessToken,
		token.RefreshToken,
		refreshURL,
		token.ExpiresIn,
		token.Email,
		claims.ExpiresAt,
		claims.PreferredUsername,
		token.Name,
	)

	// Use atomic write: write to temp file, then rename
	authFilePath := filepath.Join(serverDir, "auth.toml")
	tempFile, err := os.CreateTemp(serverDir, ".auth.toml.tmp.*")
	if err != nil {
		return fmt.Errorf("failed to create temporary auth file: %w", err)
	}
	tempPath := tempFile.Name()

	// Clean up temp file on error
	defer func() {
		if tempFile != nil {
			tempFile.Close()
			os.Remove(tempPath)
		}
	}()

	// Write content to temp file
	if _, err := tempFile.WriteString(content); err != nil {
		return fmt.Errorf("failed to write auth.toml content: %w", err)
	}

	// Sync to ensure data is written to disk
	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync auth.toml file: %w", err)
	}

	// Close temp file before rename
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("failed to close temporary auth file: %w", err)
	}
	tempFile = nil // Prevent defer cleanup

	// Atomically rename temp file to final location
	if err := os.Rename(tempPath, authFilePath); err != nil {
		return fmt.Errorf("failed to rename auth.toml file: %w", err)
	}

	return nil
}

// setupJuliaCredentials ensures Julia authentication files are created
// This should be called after successful authentication or token refresh
func setupJuliaCredentials() error {
	// Read server configuration
	server, err := readConfigFile()
	if err != nil {
		return fmt.Errorf("failed to read configuration: %w", err)
	}

	// Get valid token
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	// Create Julia auth file
	if err := createJuliaAuthFile(server, token); err != nil {
		return fmt.Errorf("failed to create Julia auth file: %w", err)
	}

	return nil
}

func runJulia(args []string) error {
	// Setup Julia credentials
	if err := setupJuliaCredentials(); err != nil {
		return err
	}

	// Read server for environment setup
	server, err := readConfigFile()
	if err != nil {
		return fmt.Errorf("failed to read configuration: %w", err)
	}

	// Check if Julia is available
	if _, err := exec.LookPath("julia"); err != nil {
		return fmt.Errorf("Julia not found in PATH. Please install Julia first using 'jh julia install'")
	}

	// Set up environment variables
	env := os.Environ()
	env = append(env, fmt.Sprintf("JULIA_PKG_SERVER=https://%s", server))
	env = append(env, "JULIA_PKG_USE_CLI_GIT=true")

	// Prepare Julia command with user-provided arguments
	// Do not automatically add --project=. - let user control this
	cmd := exec.Command("julia", args...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Execute Julia
	return cmd.Run()
}
