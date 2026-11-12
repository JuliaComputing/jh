package main

import (
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Non-interactive authentication with a JuliaHub token taken from the
// environment, for CI jobs and other unattended use. The intended token is a
// long-term token created by an administrator (Admin → Token, downloaded as
// token.toml), but any JuliaHub ID token works until it expires. Such a token
// cannot be refreshed: jh uses it as is and fails once it has expired.
//
// When a token is set in the environment it takes precedence over the login
// stored in ~/.juliahub, and ~/.juliahub is neither read for the token nor
// written.
const (
	// envToken holds the token itself: the JWT, or the whole token.toml.
	envToken = "JULIAHUB_TOKEN"
	// envTokenFile names a file holding the JWT or a token.toml (e.g. a CI
	// "file" variable). JULIAHUB_TOKEN wins when both are set.
	envTokenFile = "JULIAHUB_TOKEN_FILE"
	// envServer and envHost name the JuliaHub server the token is for.
	// JULIAHUB_HOST is what `jh auth env` prints and what the JuliaHub CI
	// documentation uses; JULIAHUB_SERVER wins when both are set. Without
	// either, the server is taken from the token's issuer.
	envServer = "JULIAHUB_SERVER"
	envHost   = "JULIAHUB_HOST"
	// envPkgServer is Julia's package server setting, which JuliaHub.jl also
	// uses as its default server. It is the last resort for the server, after
	// the token's issuer: it often names a public server or a mirror rather
	// than JuliaHub, and the token must not be sent there.
	envPkgServer = "JULIA_PKG_SERVER"
)

// tokenTOMLIDToken finds the id_token value in a token.toml / auth.toml.
var tokenTOMLIDToken = regexp.MustCompile(`(?m)^\s*id_token\s*=\s*"([^"]+)"`)

// parseTokenValue accepts a bare JWT or the contents of a token.toml (or a
// Julia auth.toml), and returns the JWT. The JWT's claims must decode.
func parseTokenValue(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("the token is empty")
	}
	if strings.Contains(value, "id_token") {
		m := tokenTOMLIDToken.FindStringSubmatch(value)
		if m == nil {
			return "", fmt.Errorf("the token looks like a TOML file but has no id_token = \"...\" line")
		}
		value = m[1]
	}
	value = strings.TrimPrefix(value, "Bearer ")
	if _, err := decodeJWT(value); err != nil {
		return "", fmt.Errorf("the token is not a valid JWT: %w", err)
	}
	return value, nil
}

// tokenFromEnv returns the token set in the environment and where it came
// from (a description for messages), or "" when none is set.
func tokenFromEnv() (token, source string, err error) {
	if v := os.Getenv(envToken); strings.TrimSpace(v) != "" {
		token, err = parseTokenValue(v)
		if err != nil {
			return "", "", fmt.Errorf("%s: %w", envToken, err)
		}
		return token, envToken + " environment variable", nil
	}
	if path := os.Getenv(envTokenFile); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", "", fmt.Errorf("%s: %w", envTokenFile, err)
		}
		token, err = parseTokenValue(string(data))
		if err != nil {
			return "", "", fmt.Errorf("%s (%s): %w", envTokenFile, path, err)
		}
		return token, fmt.Sprintf("%s (%s)", envTokenFile, path), nil
	}
	return "", "", nil
}

// cleanHost reduces a server given as a host or a URL (https://host/...) to
// the host, the form jh stores and normalizes.
func cleanHost(s string) string {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil && u.Host != "" {
			return u.Host
		}
	}
	return strings.TrimRight(s, "/")
}

// issuerServer is the JuliaHub server named by a token's issuer
// (https://<server>/dex, or https://auth.<server>/dex on JuliaHub.com-style
// installs), or "".
func issuerServer(token string) string {
	claims, err := decodeJWT(token)
	if err != nil || claims.Issuer == "" {
		return ""
	}
	u, err := url.Parse(claims.Issuer)
	if err != nil || u.Host == "" || strings.TrimSuffix(u.Path, "/") != "/dex" {
		return ""
	}
	return strings.TrimPrefix(u.Host, "auth.")
}

// pkgServerHost is the host in JULIA_PKG_SERVER, or "" when it is unset or
// names the public Julia package servers (pkg.julialang.org), which are never
// a JuliaHub server.
func pkgServerHost() string {
	host := cleanHost(os.Getenv(envPkgServer))
	name := strings.ToLower(host)
	if n, _, err := net.SplitHostPort(name); err == nil {
		name = n
	}
	if name == "pkg.julialang.org" || strings.HasSuffix(name, ".pkg.julialang.org") {
		return ""
	}
	return host
}

// pkgServerNote reports, once per run, that the server came from
// JULIA_PKG_SERVER, since that is a guess the user may not expect.
var pkgServerNote sync.Once

// envTokenServer is the server a token from the environment is for:
// JULIAHUB_SERVER, else JULIAHUB_HOST, else the token's issuer, else
// JULIA_PKG_SERVER, else "".
func envTokenServer(token string) string {
	if s := cleanHost(os.Getenv(envServer)); s != "" {
		return normalizeServer(s)
	}
	if s := cleanHost(os.Getenv(envHost)); s != "" {
		return normalizeServer(s)
	}
	if s := issuerServer(token); s != "" {
		return s
	}
	if s := pkgServerHost(); s != "" {
		pkgServerNote.Do(func() {
			fmt.Fprintf(os.Stderr, "Using JuliaHub server %s from %s; set %s to override.\n", s, envPkgServer, envServer)
		})
		return s
	}
	return ""
}

// storedTokenFromEnv builds the StoredToken for a token set in the
// environment. ok is false when no token is set. A server that cannot be
// determined falls back to the one in ~/.juliahub.
func storedTokenFromEnv() (token *StoredToken, ok bool, err error) {
	jwt, source, err := tokenFromEnv()
	if err != nil {
		return nil, true, err
	}
	if jwt == "" {
		return nil, false, nil
	}
	server := envTokenServer(jwt)
	if server == "" {
		if server, err = readConfigFileServer(); err != nil {
			return nil, true, err
		}
	}
	t := &StoredToken{
		AccessToken: jwt,
		IDToken:     jwt,
		TokenType:   "Bearer",
		Server:      server,
		Source:      source,
	}
	if claims, err := decodeJWT(jwt); err == nil {
		t.Name = claims.Name
		t.Email = claims.Email
	}
	return t, true, nil
}

// errExpiredUnrefreshable explains an expired token that has no refresh
// token: a token from the environment, or one stored with
// `jh auth login --with-token`.
func errExpiredUnrefreshable(t *StoredToken) error {
	when := ""
	if claims, err := decodeJWT(t.AccessToken); err == nil && claims.ExpiresAt > 0 {
		when = " at " + time.Unix(claims.ExpiresAt, 0).Format(time.RFC3339)
	}
	if t.Source != "" {
		return fmt.Errorf("the token from %s expired%s; ask a JuliaHub administrator for a new long-term token", t.Source, when)
	}
	return fmt.Errorf("access token expired%s and no refresh token available; run 'jh auth login' (or 'jh auth login --with-token' with a new long-term token)", when)
}

// maxTokenInput bounds what `jh auth login --with-token` reads from stdin.
const maxTokenInput = 1 << 20

// loginWithToken stores a long-term token read from r (the JWT, or a
// token.toml) in ~/.juliahub as the login for server, or, when server is "",
// for the server named by the token's issuer (falling back to the configured
// server). It refuses an expired token.
func loginWithToken(r io.Reader, server string) error {
	data, err := io.ReadAll(io.LimitReader(r, maxTokenInput))
	if err != nil {
		return fmt.Errorf("failed to read the token from stdin: %w", err)
	}
	jwt, err := parseTokenValue(string(data))
	if err != nil {
		return err
	}
	expired, err := isTokenExpired(jwt, 0)
	if err != nil {
		return fmt.Errorf("cannot tell when the token expires: %w", err)
	}
	if expired {
		return errExpiredUnrefreshable(&StoredToken{AccessToken: jwt})
	}

	if server == "" {
		server = issuerServer(jwt)
	}
	if server == "" {
		if server, err = readConfigFileServer(); err != nil {
			return err
		}
	}
	server = normalizeServer(cleanHost(server))

	if err := writeTokenToConfig(server, TokenResponse{AccessToken: jwt, IDToken: jwt, TokenType: "Bearer"}); err != nil {
		return fmt.Errorf("failed to save auth config: %w", err)
	}
	who := ""
	expires := ""
	if claims, err := decodeJWT(jwt); err == nil {
		if claims.Email != "" {
			who = " for " + claims.Email
		}
		if claims.ExpiresAt > 0 {
			expires = " (expires " + time.Unix(claims.ExpiresAt, 0).Format("2006-01-02") + ")"
		}
	}
	fmt.Printf("Stored the token%s on %s%s\n", who, server, expires)

	if _, source, _ := tokenFromEnv(); source != "" {
		fmt.Fprintf(os.Stderr, "Note: a token is set in %s; it takes precedence over the stored one until it is unset.\n", source)
		return nil
	}
	if err := setupJuliaCredentials(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to setup Julia credentials: %v\n", err)
	}
	return nil
}
