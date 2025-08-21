package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func createJuliaAuthFile(server string, token *StoredToken) error {
	// Get user home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get user home directory: %w", err)
	}

	// Create ~/.julia/servers/{server}/ directory
	serverDir := filepath.Join(homeDir, ".julia", "servers", server)
	if err := os.MkdirAll(serverDir, 0755); err != nil {
		return fmt.Errorf("failed to create server directory: %w", err)
	}

	// Parse token to get expiration time
	claims, err := decodeJWT(token.IDToken)
	if err != nil {
		return fmt.Errorf("failed to decode JWT token: %w", err)
	}

	// Create auth.toml file
	authFilePath := filepath.Join(serverDir, "auth.toml")
	file, err := os.Create(authFilePath)
	if err != nil {
		return fmt.Errorf("failed to create auth.toml file: %w", err)
	}
	defer file.Close()

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

	_, err = file.WriteString(content)
	if err != nil {
		return fmt.Errorf("failed to write auth.toml content: %w", err)
	}

	return nil
}

func runJulia() error {
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

	// Check if Julia is available
	if _, err := exec.LookPath("julia"); err != nil {
		return fmt.Errorf("Julia not found in PATH. Please install Julia first using 'jh julia install'")
	}

	// Set up environment variables
	env := os.Environ()
	env = append(env, fmt.Sprintf("JULIA_PKG_SERVER=https://%s", server))
	env = append(env, "JULIA_PKG_USE_CLI_GIT=true")

	// Prepare Julia command with --project flag
	cmd := exec.Command("julia", "--project=.")
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Execute Julia and replace current process
	return cmd.Run()
}
