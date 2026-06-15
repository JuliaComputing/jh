package main

import (
	"encoding/base64"
	"encoding/json"
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
