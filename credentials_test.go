package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToDataURL(t *testing.T) {
	got := toDataURL([]byte("hello"))
	const prefix = "data:application/octet-stream;base64,"
	if !strings.HasPrefix(got, prefix) {
		t.Fatalf("toDataURL missing prefix: %q", got)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(got, prefix))
	if err != nil {
		t.Fatalf("payload is not valid base64: %v", err)
	}
	if string(decoded) != "hello" {
		t.Errorf("decoded payload = %q, want %q", decoded, "hello")
	}
}

func TestResolvePrivateKey(t *testing.T) {
	// Inline key.
	inline, err := resolvePrivateKey("PEMDATA", "")
	if err != nil {
		t.Fatalf("inline: %v", err)
	}
	if inline != toDataURL([]byte("PEMDATA")) {
		t.Errorf("inline data URL mismatch: %q", inline)
	}

	// File-based key.
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(keyPath, []byte("FILEDATA"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	fromFile, err := resolvePrivateKey("", keyPath)
	if err != nil {
		t.Fatalf("file: %v", err)
	}
	if fromFile != toDataURL([]byte("FILEDATA")) {
		t.Errorf("file data URL mismatch: %q", fromFile)
	}

	// Neither → empty, no error.
	none, err := resolvePrivateKey("", "")
	if err != nil || none != "" {
		t.Errorf("resolvePrivateKey(\"\",\"\") = (%q, %v), want (\"\", nil)", none, err)
	}

	// Missing file → error.
	if _, err := resolvePrivateKey("", filepath.Join(dir, "missing.pem")); err == nil {
		t.Error("expected error for missing key file")
	}
}

func TestSSHHostList(t *testing.T) {
	creds := &Credentials{SSHCreds: []CredSSH{
		{KnownHost: "github.com ssh-ed25519 AAAA", PrivateKey: "secret1"},
		{KnownHost: "gitlab.com ssh-rsa BBBB", PrivateKey: "secret2"},
	}}
	got := sshHostList(creds)
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	for i, s := range got {
		if s.PrivateKey != "" {
			t.Errorf("entry %d retained private key: %q", i, s.PrivateKey)
		}
		if s.KnownHost != creds.SSHCreds[i].KnownHost {
			t.Errorf("entry %d host = %q, want %q", i, s.KnownHost, creds.SSHCreds[i].KnownHost)
		}
	}
}

func TestReadJSONInput(t *testing.T) {
	got, err := readJSONInput([]string{`{"a":1}`})
	if err != nil {
		t.Fatalf("readJSONInput: %v", err)
	}
	if string(got) != `{"a":1}` {
		t.Errorf("readJSONInput arg = %q, want %q", got, `{"a":1}`)
	}
}

// Credential add/update validation runs before any network call, so these error
// branches are exercised without a backend. A bogus server is supplied to prove
// nothing reaches the wire.
func TestCredentialValidation(t *testing.T) {
	const srv = "credential-validation.invalid"
	tests := []struct {
		name string
		fn   func() error
	}{
		{"add token: invalid JSON", func() error { return addCredentialToken(srv, []byte("{")) }},
		{"add token: missing name", func() error { return addCredentialToken(srv, []byte(`{"url":"u","value":"v"}`)) }},
		{"add token: missing url", func() error { return addCredentialToken(srv, []byte(`{"name":"n","value":"v"}`)) }},
		{"add token: missing value", func() error { return addCredentialToken(srv, []byte(`{"name":"n","url":"u"}`)) }},
		{"add ssh: missing host_key", func() error { return addCredentialSSH(srv, []byte(`{}`)) }},
		{"add ssh: both keys", func() error {
			return addCredentialSSH(srv, []byte(`{"host_key":"h","private_key":"k","private_key_file":"f"}`))
		}},
		{"add github-app: missing app_id", func() error { return addCredentialGitHubApp(srv, []byte(`{"url":"u"}`)) }},
		{"add github-app: missing url", func() error { return addCredentialGitHubApp(srv, []byte(`{"app_id":"1"}`)) }},
		{"add github-app: both keys", func() error {
			return addCredentialGitHubApp(srv, []byte(`{"app_id":"1","url":"u","private_key":"k","private_key_file":"f"}`))
		}},
		{"update token: missing name", func() error { return updateCredentialToken(srv, []byte(`{"url":"u"}`)) }},
		{"update token: nothing to update", func() error { return updateCredentialToken(srv, []byte(`{"name":"n"}`)) }},
		{"update ssh: invalid index", func() error { return updateCredentialSSH(srv, []byte(`{"index":0,"host_key":"h"}`)) }},
		{"update ssh: nothing to update", func() error { return updateCredentialSSH(srv, []byte(`{"index":1}`)) }},
		{"update github-app: missing app_id", func() error { return updateCredentialGitHubApp(srv, []byte(`{"url":"u"}`)) }},
		{"update github-app: nothing to update", func() error { return updateCredentialGitHubApp(srv, []byte(`{"app_id":"1"}`)) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.fn(); err == nil {
				t.Errorf("%s: expected validation error, got nil", tt.name)
			}
		})
	}
}
