package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// clearTokenEnv makes a test independent of a token set in the developer's
// environment.
func clearTokenEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{envToken, envTokenFile, envServer, envHost, envPkgServer} {
		t.Setenv(v, "")
	}
}

// longTermJWT is a token shaped like the platform's long-term tokens.
func longTermJWT(t *testing.T, issuer string, exp time.Time) string {
	t.Helper()
	return makeJWT(t, JWTClaims{
		IssuedAt:  time.Now().Add(-time.Hour).Unix(),
		ExpiresAt: exp.Unix(),
		Subject:   "ci-bot",
		Issuer:    issuer,
		Audience:  "local",
		Email:     "ci@example.com",
	})
}

// tokenTOML is what Admin → Token downloads.
func tokenTOML(jwt string) string {
	return strings.Join([]string{
		`user_email = "ci@example.com"`,
		`refresh_token = "ci-bot"`,
		`user_name = "ci-bot"`,
		`id_token = "` + jwt + `"`,
		`expires = 1999999999`,
		`access_token = "` + jwt + `"`,
		`expires_at = 1999999999`,
		``,
	}, "\n")
}

func TestParseTokenValue(t *testing.T) {
	jwt := longTermJWT(t, "https://jh.example.com/dex", time.Now().Add(time.Hour))
	for name, in := range map[string]string{
		"bare":       jwt,
		"whitespace": "\n  " + jwt + "\n",
		"bearer":     "Bearer " + jwt,
		"token.toml": tokenTOML(jwt),
		"indented":   "  id_token = \"" + jwt + "\"\n",
	} {
		got, err := parseTokenValue(in)
		if err != nil || got != jwt {
			t.Errorf("%s: got %q, %v", name, got, err)
		}
	}
	for name, in := range map[string]string{
		"empty":         "  \n",
		"not a jwt":     "abc",
		"toml no value": "id_token = 42\n",
		"bad payload":   "a.!!!.c",
	} {
		if _, err := parseTokenValue(in); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestTokenFromEnv(t *testing.T) {
	clearTokenEnv(t)
	if tok, src, err := tokenFromEnv(); tok != "" || src != "" || err != nil {
		t.Fatalf("unset: got %q, %q, %v", tok, src, err)
	}

	jwt := longTermJWT(t, "https://jh.example.com/dex", time.Now().Add(time.Hour))
	path := filepath.Join(t.TempDir(), "token.toml")
	if err := os.WriteFile(path, []byte(tokenTOML(jwt)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envTokenFile, path)
	tok, src, err := tokenFromEnv()
	if err != nil || tok != jwt || !strings.Contains(src, envTokenFile) || !strings.Contains(src, path) {
		t.Errorf("file: got %q, %q, %v", tok, src, err)
	}

	other := longTermJWT(t, "https://other.example.com/dex", time.Now().Add(time.Hour))
	t.Setenv(envToken, other)
	if tok, src, _ := tokenFromEnv(); tok != other || src != envToken+" environment variable" {
		t.Errorf("JULIAHUB_TOKEN should win over the file: got %q from %q", tok, src)
	}

	t.Setenv(envToken, "garbage")
	if _, _, err := tokenFromEnv(); err == nil || !strings.Contains(err.Error(), envToken) {
		t.Errorf("bad JULIAHUB_TOKEN: got %v", err)
	}

	t.Setenv(envToken, "")
	t.Setenv(envTokenFile, filepath.Join(t.TempDir(), "missing.toml"))
	if _, _, err := tokenFromEnv(); err == nil || !strings.Contains(err.Error(), envTokenFile) {
		t.Errorf("missing file: got %v", err)
	}
}

func TestEnvTokenServer(t *testing.T) {
	clearTokenEnv(t)
	jwt := longTermJWT(t, "https://jh.example.com/dex", time.Now().Add(time.Hour))
	if got := envTokenServer(jwt); got != "jh.example.com" {
		t.Errorf("from issuer: %q", got)
	}
	if got := envTokenServer(longTermJWT(t, "https://auth.juliahub.com/dex", time.Now())); got != "juliahub.com" {
		t.Errorf("auth. issuer: %q", got)
	}
	if got := envTokenServer(longTermJWT(t, "", time.Now())); got != "" {
		t.Errorf("no issuer: %q", got)
	}
	if got := envTokenServer(longTermJWT(t, "https://jh.example.com/other", time.Now())); got != "" {
		t.Errorf("non-dex issuer: %q", got)
	}
	// JULIA_PKG_SERVER is used only when the issuer names no server, and
	// never when it is a public Julia package server.
	noIssuer := longTermJWT(t, "", time.Now())
	t.Setenv(envPkgServer, "https://pkg.example.com/")
	if got := envTokenServer(jwt); got != "jh.example.com" {
		t.Errorf("issuer should win over JULIA_PKG_SERVER: %q", got)
	}
	if got := envTokenServer(noIssuer); got != "pkg.example.com" {
		t.Errorf("JULIA_PKG_SERVER fallback: %q", got)
	}
	for _, public := range []string{"https://pkg.julialang.org", "https://us-east.pkg.julialang.org/", "https://PKG.julialang.org:443"} {
		t.Setenv(envPkgServer, public)
		if got := envTokenServer(noIssuer); got != "" {
			t.Errorf("public JULIA_PKG_SERVER %s used: %q", public, got)
		}
	}
	t.Setenv(envPkgServer, "https://pkg.example.com")
	t.Setenv(envHost, "https://host.example.com/")
	if got := envTokenServer(noIssuer); got != "host.example.com" {
		t.Errorf("JULIAHUB_HOST should win over JULIA_PKG_SERVER: %q", got)
	}
	if got := envTokenServer(jwt); got != "host.example.com" {
		t.Errorf("JULIAHUB_HOST URL: %q", got)
	}
	t.Setenv(envServer, "server.example.dev")
	if got := envTokenServer(jwt); got != "server.example.dev" {
		t.Errorf("JULIAHUB_SERVER should win: %q", got)
	}
	t.Setenv(envServer, "mycompany")
	if got := envTokenServer(jwt); got != "mycompany.juliahub.com" {
		t.Errorf("short name is normalized: %q", got)
	}
}

func TestReadStoredTokenPrefersEnv(t *testing.T) {
	clearTokenEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	config := "server=stored.example.com\naccess_token=acc\nrefresh_token=ref\nid_token=idt\n"
	configPath := filepath.Join(home, ".juliahub")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	jwt := longTermJWT(t, "https://jh.example.com/dex", time.Now().Add(time.Hour))
	t.Setenv(envToken, jwt)
	tok, err := readStoredToken()
	if err != nil {
		t.Fatal(err)
	}
	if tok.IDToken != jwt || tok.AccessToken != jwt || tok.RefreshToken != "" || tok.Server != "jh.example.com" ||
		tok.Email != "ci@example.com" || tok.Source != "JULIAHUB_TOKEN environment variable" {
		t.Errorf("env token: %+v", tok)
	}
	if server, err := readConfigFile(); err != nil || server != "jh.example.com" {
		t.Errorf("readConfigFile with env token: %q, %v", server, err)
	}

	// No server in the environment or the token: the stored server is used.
	t.Setenv(envToken, longTermJWT(t, "", time.Now().Add(time.Hour)))
	if server, err := readConfigFile(); err != nil || server != "stored.example.com" {
		t.Errorf("fallback server: %q, %v", server, err)
	}

	// A malformed token is an error, not a silent fallback to ~/.juliahub.
	t.Setenv(envToken, "nope")
	if _, err := readStoredToken(); err == nil {
		t.Error("malformed env token should fail readStoredToken")
	}
	if _, err := readConfigFile(); err == nil {
		t.Error("malformed env token should fail readConfigFile")
	}

	t.Setenv(envToken, "")
	if tok, err := readStoredToken(); err != nil || tok.IDToken != "idt" || tok.Source != "" {
		t.Errorf("without env token: %+v, %v", tok, err)
	}
	if data, _ := os.ReadFile(configPath); string(data) != config {
		t.Errorf("~/.juliahub was modified:\n%s", data)
	}
}

func TestEnsureValidTokenFromEnv(t *testing.T) {
	clearTokenEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	valid := longTermJWT(t, "https://jh.example.com/dex", time.Now().Add(time.Hour))
	t.Setenv(envToken, valid)
	tok, err := ensureValidToken()
	if err != nil || tok.IDToken != valid {
		t.Fatalf("valid env token: %+v, %v", tok, err)
	}

	t.Setenv(envToken, longTermJWT(t, "https://jh.example.com/dex", time.Now().Add(-time.Hour)))
	_, err = ensureValidToken()
	if err == nil || !strings.Contains(err.Error(), "JULIAHUB_TOKEN environment variable expired at") ||
		!strings.Contains(err.Error(), "long-term token") {
		t.Errorf("expired env token: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".juliahub")); !os.IsNotExist(err) {
		t.Errorf("~/.juliahub should not be written for an env token (stat: %v)", err)
	}
}

func TestEnsureValidTokenStoredWithoutRefresh(t *testing.T) {
	clearTokenEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	expired := longTermJWT(t, "https://jh.example.com/dex", time.Now().Add(-time.Hour))
	config := "server=jh.example.com\naccess_token=" + expired + "\nid_token=" + expired + "\n"
	if err := os.WriteFile(filepath.Join(home, ".juliahub"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ensureValidToken()
	if err == nil || !strings.Contains(err.Error(), "no refresh token available") || !strings.Contains(err.Error(), "--with-token") {
		t.Errorf("got %v", err)
	}
}

func TestLoginWithToken(t *testing.T) {
	clearTokenEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	depot := t.TempDir()
	t.Setenv("JULIA_DEPOT_PATH", depot)
	stubAuthDiscovery(t, func(server string) string { return server })

	jwt := longTermJWT(t, "https://jh.example.com/dex", time.Now().Add(24*time.Hour))
	if err := loginWithToken(strings.NewReader(tokenTOML(jwt)), ""); err != nil {
		t.Fatal(err)
	}
	stored, err := readStoredTokenFile()
	if err != nil {
		t.Fatal(err)
	}
	if stored.Server != "jh.example.com" || stored.AccessToken != jwt || stored.IDToken != jwt ||
		stored.RefreshToken != "" || stored.Email != "ci@example.com" {
		t.Errorf("stored: %+v", stored)
	}
	auth, err := os.ReadFile(filepath.Join(depot, "servers", "jh.example.com", "auth.toml"))
	if err != nil {
		t.Fatalf("Julia auth.toml not written: %v", err)
	}
	if !strings.Contains(string(auth), `id_token = "`+jwt+`"`) {
		t.Errorf("auth.toml:\n%s", auth)
	}
	// The stored token is used as is, without a refresh.
	if tok, err := ensureValidToken(); err != nil || tok.IDToken != jwt {
		t.Errorf("ensureValidToken after --with-token: %+v, %v", tok, err)
	}

	// An explicit server wins over the issuer.
	if err := loginWithToken(strings.NewReader(jwt), "https://other.example.com/"); err != nil {
		t.Fatal(err)
	}
	if s, _ := readConfigFileServer(); s != "other.example.com" {
		t.Errorf("explicit server: %q", s)
	}
}

func TestLoginWithTokenRejects(t *testing.T) {
	clearTokenEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	expired := longTermJWT(t, "https://jh.example.com/dex", time.Now().Add(-time.Minute))
	if err := loginWithToken(strings.NewReader(expired), ""); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("expired: %v", err)
	}
	noExp := makeJWT(t, JWTClaims{Issuer: "https://jh.example.com/dex"})
	if err := loginWithToken(strings.NewReader(noExp), ""); err == nil || !strings.Contains(err.Error(), "expires") {
		t.Errorf("no exp: %v", err)
	}
	if err := loginWithToken(strings.NewReader(""), ""); err == nil {
		t.Error("empty input should fail")
	}
	if _, err := os.Stat(filepath.Join(home, ".juliahub")); !os.IsNotExist(err) {
		t.Errorf("a rejected token must not be stored (stat: %v)", err)
	}
}

func TestFormatTokenInfoShowsSource(t *testing.T) {
	jwt := longTermJWT(t, "https://jh.example.com/dex", time.Now().Add(time.Hour))
	got := formatTokenInfo(&StoredToken{AccessToken: jwt, IDToken: jwt, Server: "jh.example.com", Source: "JULIAHUB_TOKEN environment variable"})
	if !strings.Contains(got, "Token Source: JULIAHUB_TOKEN environment variable") || !strings.Contains(got, "Has Refresh Token: false") {
		t.Errorf("got:\n%s", got)
	}
	if got := formatTokenInfo(&StoredToken{AccessToken: jwt, Server: "jh.example.com"}); strings.Contains(got, "Token Source") {
		t.Errorf("a ~/.juliahub token should not show a source:\n%s", got)
	}
}
