package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func checkJuliaInstalled() (bool, string, error) {
	cmd := exec.Command("julia", "--version")
	output, err := cmd.Output()
	if err != nil {
		return false, "", nil
	}
	return true, strings.TrimSpace(string(output)), nil
}

func installJulia() error {
	switch runtime.GOOS {
	case "windows":
		return installJuliaWindows()
	case "linux", "darwin":
		return installJuliaUnix()
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}

func installJuliaWindows() error {
	fmt.Println("Installing Julia on Windows using winget...")
	cmd := exec.Command("winget", "install", "--name", "Julia", "--id", "9NJNWW8PVKMN", "-e", "-s", "msstore")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func installJuliaUnix() error {
	fmt.Println("Installing Julia using the official installer...")
	cmd := exec.Command("curl", "-fsSL", "https://install.julialang.org")
	curlOutput, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to download Julia installer: %w", err)
	}

	cmd = exec.Command("sh", "-c", "sh -- -y --default-channel stable")
	cmd.Stdin = strings.NewReader(string(curlOutput))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func juliaInstallCommand() error {
	installed, version, err := checkJuliaInstalled()
	if err != nil {
		return fmt.Errorf("failed to check Julia installation: %w", err)
	}

	if installed {
		fmt.Printf("Julia already installed: %s\n", version)
		return nil
	}

	fmt.Println("Julia not found in PATH. Installing...")
	return installJulia()
}
