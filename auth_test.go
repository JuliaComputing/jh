package main

import (
	"testing"
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
