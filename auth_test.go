package main

import (
	"encoding/base64"
	"encoding/json"
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
