package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetJuliaDepotPath(t *testing.T) {
	t.Run("uses first entry of JULIA_DEPOT_PATH", func(t *testing.T) {
		first := filepath.Join(t.TempDir(), "depotA")
		second := filepath.Join(t.TempDir(), "depotB")
		t.Setenv("JULIA_DEPOT_PATH", first+string(os.PathListSeparator)+second)
		got, err := getJuliaDepotPath()
		if err != nil {
			t.Fatalf("getJuliaDepotPath: %v", err)
		}
		if got != first {
			t.Errorf("got %q, want first entry %q", got, first)
		}
	})

	t.Run("falls back to ~/.julia", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("JULIA_DEPOT_PATH", "")
		t.Setenv("HOME", home)
		got, err := getJuliaDepotPath()
		if err != nil {
			t.Fatalf("getJuliaDepotPath: %v", err)
		}
		if want := filepath.Join(home, ".julia"); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestCreateJuliaAuthFile(t *testing.T) {
	depot := t.TempDir()
	t.Setenv("JULIA_DEPOT_PATH", depot)

	token := &StoredToken{
		IDToken:      makeJWT(t, JWTClaims{ExpiresAt: 1893456000, PreferredUsername: "ab"}),
		AccessToken:  "access-xyz",
		RefreshToken: "refresh-xyz",
		Email:        "a@b.com",
		Name:         "A B",
		ExpiresIn:    3600,
	}

	t.Run("writes auth.toml for a custom server", func(t *testing.T) {
		if err := createJuliaAuthFile("nightly.juliahub.dev", token); err != nil {
			t.Fatalf("createJuliaAuthFile: %v", err)
		}
		path := filepath.Join(depot, "servers", "nightly.juliahub.dev", "auth.toml")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("expected auth.toml at %s: %v", path, err)
		}
		content := string(data)
		for _, want := range []string{
			`id_token = "` + token.IDToken + `"`,
			`access_token = "access-xyz"`,
			`refresh_token = "refresh-xyz"`,
			`refresh_url = "https://nightly.juliahub.dev/dex/token"`,
			`expires_at = 1893456000`,
			`user_name = "ab"`,
		} {
			if !strings.Contains(content, want) {
				t.Errorf("auth.toml missing %q\n---\n%s", want, content)
			}
		}
	})

	t.Run("juliahub.com uses the auth.juliahub.com refresh host", func(t *testing.T) {
		if err := createJuliaAuthFile("juliahub.com", token); err != nil {
			t.Fatalf("createJuliaAuthFile: %v", err)
		}
		data, _ := os.ReadFile(filepath.Join(depot, "servers", "juliahub.com", "auth.toml"))
		if !strings.Contains(string(data), `refresh_url = "https://auth.juliahub.com/dex/token"`) {
			t.Errorf("expected auth.juliahub.com refresh url, got:\n%s", data)
		}
	})

	t.Run("rejects an unparseable id token", func(t *testing.T) {
		bad := &StoredToken{IDToken: "not-a-jwt"}
		if err := createJuliaAuthFile("x.example.com", bad); err == nil {
			t.Error("expected error for invalid id token")
		}
	})
}
