package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseJWTClaims(t *testing.T) {
	// Test with malformed token
	_, err := decodeJWT("invalid.token")
	if err == nil {
		t.Error("decodeJWT should fail with malformed token")
	}

	// Test with empty token
	_, err = decodeJWT("")
	if err == nil {
		t.Error("decodeJWT should fail with empty token")
	}
}

// makeJWT builds a syntactically valid (unsigned) JWT carrying the given claims.
func makeJWT(t *testing.T, claims JWTClaims) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	header := enc(map[string]string{"alg": "none", "typ": "JWT"})
	return header + "." + enc(claims) + ".sig"
}

func TestDecodeJWTValid(t *testing.T) {
	want := JWTClaims{Subject: "user-1", Email: "a@b.com", PreferredUsername: "ab", ExpiresAt: 1893456000}
	got, err := decodeJWT(makeJWT(t, want))
	if err != nil {
		t.Fatalf("decodeJWT: %v", err)
	}
	if got.Subject != want.Subject || got.Email != want.Email || got.ExpiresAt != want.ExpiresAt {
		t.Errorf("decodeJWT = %+v, want subject/email/exp from %+v", got, want)
	}
}

func TestIsTokenExpired(t *testing.T) {
	past := time.Now().Add(-time.Hour).Unix()
	future := time.Now().Add(time.Hour).Unix()

	expired, err := isTokenExpired(makeJWT(t, JWTClaims{ExpiresAt: past}), 0)
	if err != nil {
		t.Fatalf("isTokenExpired(past): %v", err)
	}
	if !expired {
		t.Error("token with past exp should be expired")
	}

	expired, err = isTokenExpired(makeJWT(t, JWTClaims{ExpiresAt: future}), 0)
	if err != nil {
		t.Fatalf("isTokenExpired(future): %v", err)
	}
	if expired {
		t.Error("token with future exp should not be expired")
	}

	// A malformed token is treated as expired (fail-safe) and returns an error.
	if expired, err := isTokenExpired("garbage", 0); err == nil || !expired {
		t.Errorf("isTokenExpired(garbage) = (%v, %v), want (true, error)", expired, err)
	}
}

func TestFormatTokenInfo(t *testing.T) {
	tok := &StoredToken{
		AccessToken:  makeJWT(t, JWTClaims{Subject: "sub-1", Issuer: "https://s/dex", Audience: "device", ExpiresAt: 1893456000}),
		TokenType:    "Bearer",
		RefreshToken: "r",
		Server:       "nightly.juliahub.dev",
		Name:         "A B",
		Email:        "a@b.com",
	}
	out := formatTokenInfo(tok)
	for _, want := range []string{
		"Server: nightly.juliahub.dev",
		"Token Status: Valid", // future exp
		"Subject: sub-1",
		"Issuer: https://s/dex",
		"Audience: device",
		"Token Type: Bearer",
		"Has Refresh Token: true",
		"Email: a@b.com",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("formatTokenInfo missing %q\n---\n%s", want, out)
		}
	}

	// A bad access token yields an error string rather than panicking.
	if got := formatTokenInfo(&StoredToken{AccessToken: "bad"}); !strings.Contains(got, "Error decoding token") {
		t.Errorf("expected decode-error string, got %q", got)
	}
}

func TestReadStoredToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	config := strings.Join([]string{
		"server=nightly.juliahub.dev",
		"access_token=acc",
		"refresh_token=ref",
		"token_type=Bearer",
		"id_token=idt",
		"name=A B",
		"email=a@b.com",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(home, ".juliahub"), []byte(config), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	tok, err := readStoredToken()
	if err != nil {
		t.Fatalf("readStoredToken: %v", err)
	}
	if tok.Server != "nightly.juliahub.dev" || tok.AccessToken != "acc" || tok.IDToken != "idt" ||
		tok.RefreshToken != "ref" || tok.Email != "a@b.com" || tok.Name != "A B" {
		t.Errorf("parsed token mismatch: %+v", tok)
	}
}

// stubAuthDiscovery replaces the network probe behind authServerFor for the
// duration of a test and clears the per-process cache around it.
func stubAuthDiscovery(t *testing.T, fn func(server string) string) {
	t.Helper()
	old := discoverAuthServer
	reset := func() {
		authServerMu.Lock()
		authServerCache = map[string]string{}
		authServerMu.Unlock()
	}
	discoverAuthServer = fn
	reset()
	t.Cleanup(func() {
		discoverAuthServer = old
		reset()
	})
}

func TestAuthServerFor(t *testing.T) {
	calls := 0
	stubAuthDiscovery(t, func(server string) string { calls++; return "auth." + server })
	if got := authServerFor("juliahub.com"); got != "auth.juliahub.com" {
		t.Errorf("juliahub.com -> %q", got)
	}
	if calls != 0 {
		t.Errorf("juliahub.com must not be probed")
	}
	for i := 0; i < 2; i++ {
		if got := authServerFor("nightly-juliahub.juliahub.dev"); got != "auth.nightly-juliahub.juliahub.dev" {
			t.Errorf("got %q", got)
		}
	}
	if calls != 1 {
		t.Errorf("discovery should be cached per server, probed %d times", calls)
	}
}

func TestPickAuthServer(t *testing.T) {
	only := func(hosts ...string) func(string) bool {
		return func(h string) bool {
			for _, x := range hosts {
				if h == x {
					return true
				}
			}
			return false
		}
	}
	cases := []struct {
		server string
		up     func(string) bool
		want   string
	}{
		{"nightly.juliahub.dev", only("nightly.juliahub.dev"), "nightly.juliahub.dev"},
		{"nightly-juliahub.juliahub.dev", only("auth.nightly-juliahub.juliahub.dev"), "auth.nightly-juliahub.juliahub.dev"},
		{"both.dev", only("both.dev", "auth.both.dev"), "auth.both.dev"},
		{"auth.x.dev", only("auth.x.dev"), "auth.x.dev"},
		{"offline.dev", only(), "offline.dev"},
	}
	for _, c := range cases {
		if got := pickAuthServer(c.server, c.up); got != c.want {
			t.Errorf("pickAuthServer(%q) = %q, want %q", c.server, got, c.want)
		}
	}
}

func TestDexDiscoverable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok/dex/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"issuer":"https://x/dex"}`))
	})
	mux.HandleFunc("/html/dex/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>bad gateway</html>`))
	})
	mux.HandleFunc("/noissuer/dex/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	for path, want := range map[string]bool{"/ok": true, "/html": false, "/noissuer": false, "/missing": false} {
		if got := dexDiscoverable(srv.Client(), srv.URL+path); got != want {
			t.Errorf("dexDiscoverable(%s) = %v, want %v", path, got, want)
		}
	}
}

func TestRememberAuthServerFromToken(t *testing.T) {
	stubAuthDiscovery(t, func(server string) string {
		t.Errorf("discovery should not run for %s: the token names its issuer", server)
		return server
	})
	tok := makeJWT(t, JWTClaims{Issuer: "https://auth.nightly-juliahub.juliahub.dev/dex"})
	rememberAuthServerFromToken("nightly-juliahub.juliahub.dev", tok)
	if got := authServerFor("nightly-juliahub.juliahub.dev"); got != "auth.nightly-juliahub.juliahub.dev" {
		t.Errorf("got %q", got)
	}

	// An issuer on an unrelated host is ignored.
	stubAuthDiscovery(t, func(server string) string { return server })
	rememberAuthServerFromToken("a.juliahub.dev", makeJWT(t, JWTClaims{Issuer: "https://evil.example/dex"}))
	if got := authServerFor("a.juliahub.dev"); got != "a.juliahub.dev" {
		t.Errorf("untrusted issuer was used: %q", got)
	}
}
