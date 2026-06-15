package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadRegistryPayload(t *testing.T) {
	writePayload := func(t *testing.T, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "registry.json")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write payload: %v", err)
		}
		return path
	}

	t.Run("valid payload", func(t *testing.T) {
		path := writePayload(t, `{"name":"MyReg","download_providers":[{"type":"cacheserver"}]}`)
		payload, err := readRegistryPayload(path)
		if err != nil {
			t.Fatalf("readRegistryPayload: %v", err)
		}
		if payload["name"] != "MyReg" {
			t.Errorf("name = %v, want MyReg", payload["name"])
		}
	})

	t.Run("missing name", func(t *testing.T) {
		path := writePayload(t, `{"download_providers":[{"type":"cacheserver"}]}`)
		if _, err := readRegistryPayload(path); err == nil {
			t.Error("expected error for missing name")
		}
	})

	t.Run("missing download_providers", func(t *testing.T) {
		path := writePayload(t, `{"name":"MyReg"}`)
		if _, err := readRegistryPayload(path); err == nil {
			t.Error("expected error for missing download_providers")
		}
	})

	t.Run("empty download_providers", func(t *testing.T) {
		path := writePayload(t, `{"name":"MyReg","download_providers":[]}`)
		if _, err := readRegistryPayload(path); err == nil {
			t.Error("expected error for empty download_providers")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		path := writePayload(t, `{not json`)
		if _, err := readRegistryPayload(path); err == nil {
			t.Error("expected error for invalid JSON")
		}
	})

	t.Run("missing file", func(t *testing.T) {
		if _, err := readRegistryPayload(filepath.Join(t.TempDir(), "nope.json")); err == nil {
			t.Error("expected error for missing file")
		}
	})
}
