package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// GitHubRelease represents a GitHub release response
type GitHubRelease struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	Body    string `json:"body"`
}

// getLatestRelease fetches the latest release info from GitHub
func getLatestRelease() (*GitHubRelease, error) {
	url := "https://api.github.com/repos/JuliaComputing/jh/releases/latest"

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch release info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("failed to parse release info: %w", err)
	}

	return &release, nil
}

// compareVersions compares two version strings (e.g., "v1.2.3")
// Returns: -1 if current < latest, 0 if equal, 1 if current > latest
func compareVersions(current, latest string) int {
	// Remove 'v' prefix if present
	current = strings.TrimPrefix(current, "v")
	latest = strings.TrimPrefix(latest, "v")

	// Handle "dev" version
	if current == "dev" {
		return -1 // Always consider dev as older
	}

	// Simple string comparison for now (works for semantic versions)
	if current == latest {
		return 0
	} else if current < latest {
		return -1
	}
	return 1
}

// getInstallScript returns the appropriate install script URL and command for the current platform
func getInstallScript() (string, []string, error) {
	switch runtime.GOOS {
	case "windows":
		// Prefer PowerShell on Windows
		powershellCmd, err := exec.LookPath("powershell")
		if err == nil {
			return "https://raw.githubusercontent.com/JuliaComputing/jh/main/install.ps1",
				[]string{powershellCmd, "-ExecutionPolicy", "Bypass", "-Command",
					"Invoke-WebRequest -Uri 'https://raw.githubusercontent.com/JuliaComputing/jh/main/install.ps1' -OutFile 'install.ps1'; ./install.ps1 -NoPrompt; Remove-Item install.ps1"}, nil
		}
		// Fallback to cmd
		return "https://raw.githubusercontent.com/JuliaComputing/jh/main/install.bat",
			[]string{"cmd", "/c", "curl -L https://raw.githubusercontent.com/JuliaComputing/jh/main/install.bat -o install.bat && install.bat && del install.bat"}, nil
	case "darwin", "linux":
		// Prefer bash if available, fallback to sh
		shell := "bash"
		if _, err := exec.LookPath("bash"); err != nil {
			shell = "sh"
		}
		return "https://raw.githubusercontent.com/JuliaComputing/jh/main/install.sh",
			[]string{shell, "-c", fmt.Sprintf("curl -sSfL https://raw.githubusercontent.com/JuliaComputing/jh/main/install.sh -o /tmp/jh_install.sh && %s /tmp/jh_install.sh && rm -f /tmp/jh_install.sh", shell)}, nil
	default:
		return "", nil, fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

// runUpdate performs the actual update by executing the install script
func runUpdate(force bool) error {
	// Check current version vs latest
	fmt.Printf("Current version: %s\n", version)

	latest, err := getLatestRelease()
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	fmt.Printf("Latest version: %s\n", latest.TagName)

	// Compare versions
	comparison := compareVersions(version, latest.TagName)

	if comparison == 0 && !force {
		fmt.Println("You are already running the latest version!")
		return nil
	} else if comparison > 0 && !force {
		fmt.Printf("Your version (%s) is newer than the latest release (%s)\n", version, latest.TagName)
		fmt.Println("Use --force to downgrade to the latest release")
		return nil
	}

	if comparison < 0 {
		fmt.Printf("Update available: %s -> %s\n", version, latest.TagName)
	} else if force {
		fmt.Printf("Force updating: %s -> %s\n", version, latest.TagName)
	}

	// Get install script for current platform
	scriptURL, command, err := getInstallScript()
	if err != nil {
		return fmt.Errorf("failed to determine install script: %w", err)
	}

	fmt.Printf("Downloading and running install script from: %s\n", scriptURL)
	fmt.Println("This will replace the current installation...")

	// Execute the install command
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	fmt.Println("\nUpdate completed successfully!")
	fmt.Println("You may need to restart your terminal for the changes to take effect.")

	return nil
}
