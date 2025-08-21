package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeServer(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"juliahub.com", "juliahub.com"},
		{"custom.dev", "custom.dev"},
		{"internal", "internal.juliahub.com"},
		{"test", "test.juliahub.com"},
	}

	for _, test := range tests {
		result := normalizeServer(test.input)
		if result != test.expected {
			t.Errorf("normalizeServer(%s) = %s, expected %s", test.input, result, test.expected)
		}
	}
}

func TestGetConfigFilePath(t *testing.T) {
	path := getConfigFilePath()
	if !strings.HasSuffix(path, ".juliahub") {
		t.Errorf("Config file path should end with .juliahub, got: %s", path)
	}
	if !filepath.IsAbs(path) {
		t.Errorf("Config file path should be absolute, got: %s", path)
	}
}

func TestReadConfigFileDefault(t *testing.T) {
	// Test reading from non-existent config file should return default
	tempHome, err := os.MkdirTemp("", "jh-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempHome)

	// Temporarily set HOME to our temp directory
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tempHome)
	defer os.Setenv("HOME", origHome)

	server, err := readConfigFile()
	if err != nil {
		t.Errorf("readConfigFile() should not error on missing file, got: %v", err)
	}
	if server != "juliahub.com" {
		t.Errorf("readConfigFile() should return default server, got: %s", server)
	}
}

func TestWriteAndReadConfigFile(t *testing.T) {
	tempHome, err := os.MkdirTemp("", "jh-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempHome)

	// Temporarily set HOME to our temp directory
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tempHome)
	defer os.Setenv("HOME", origHome)

	testServer := "test.juliahub.com"

	// Write config file
	err = writeConfigFile(testServer)
	if err != nil {
		t.Errorf("writeConfigFile() failed: %v", err)
	}

	// Read it back
	server, err := readConfigFile()
	if err != nil {
		t.Errorf("readConfigFile() failed: %v", err)
	}
	if server != testServer {
		t.Errorf("readConfigFile() returned %s, expected %s", server, testServer)
	}

	// Check file permissions
	configPath := getConfigFilePath()
	info, err := os.Stat(configPath)
	if err != nil {
		t.Errorf("Failed to stat config file: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("Config file permissions should be 0600, got %o", info.Mode().Perm())
	}
}
