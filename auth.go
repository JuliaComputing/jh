package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type DeviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	IDToken      string `json:"id_token"`
	Error        string `json:"error"`
}

type JWTClaims struct {
	IssuedAt          int64  `json:"iat"`
	ExpiresAt         int64  `json:"exp"`
	Subject           string `json:"sub"`
	Issuer            string `json:"iss"`
	Audience          string `json:"aud"`
	Name              string `json:"name"`
	Email             string `json:"email"`
	PreferredUsername string `json:"preferred_username"`
}

type StoredToken struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	ExpiresIn    int
	IDToken      string
	Server       string
	Name         string
	Email        string
}

func decodeJWT(tokenString string) (*JWTClaims, error) {
	parts := strings.Split(tokenString, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format")
	}

	payload := parts[1]

	// Add padding if needed
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}

	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to decode JWT payload: %w", err)
	}

	var claims JWTClaims
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return nil, fmt.Errorf("failed to parse JWT claims: %w", err)
	}

	return &claims, nil
}

func isTokenExpired(accessToken string, expiresIn int) (bool, error) {
	claims, err := decodeJWT(accessToken)
	if err != nil {
		return true, err
	}

	// Check if token has expired based on JWT exp claim
	if claims.ExpiresAt > 0 {
		return time.Now().Unix() >= claims.ExpiresAt, nil
	}

	// Fallback: use issued at + expires_in if exp claim is not present
	if claims.IssuedAt > 0 && expiresIn > 0 {
		expiryTime := claims.IssuedAt + int64(expiresIn)
		return time.Now().Unix() >= expiryTime, nil
	}

	// If we can't determine expiry, assume it's expired for safety
	return true, fmt.Errorf("unable to determine token expiry")
}

func readStoredToken() (*StoredToken, error) {
	configPath := getConfigFilePath()
	file, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	token := &StoredToken{}
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "server=") {
			token.Server = strings.TrimPrefix(line, "server=")
		} else if strings.HasPrefix(line, "access_token=") {
			token.AccessToken = strings.TrimPrefix(line, "access_token=")
		} else if strings.HasPrefix(line, "refresh_token=") {
			token.RefreshToken = strings.TrimPrefix(line, "refresh_token=")
		} else if strings.HasPrefix(line, "token_type=") {
			token.TokenType = strings.TrimPrefix(line, "token_type=")
		} else if strings.HasPrefix(line, "id_token=") {
			token.IDToken = strings.TrimPrefix(line, "id_token=")
		} else if strings.HasPrefix(line, "expires_in=") {
			fmt.Sscanf(line, "expires_in=%d", &token.ExpiresIn)
		} else if strings.HasPrefix(line, "name=") {
			token.Name = strings.TrimPrefix(line, "name=")
		} else if strings.HasPrefix(line, "email=") {
			token.Email = strings.TrimPrefix(line, "email=")
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	if token.AccessToken == "" {
		return nil, fmt.Errorf("no access token found in config")
	}

	return token, nil
}

// authServerCache remembers discovered auth (dex) hosts for this process.
var (
	authServerMu    sync.Mutex
	authServerCache = map[string]string{}
)

// authServerFor returns the host that serves the dex OIDC endpoints for a
// JuliaHub server. Enterprise installs serve dex at <server>/dex; JuliaHub.com
// style installs (juliahub.com included) serve it from auth.<server>/dex.
// juliahub.com is known; for any other server both candidates are probed via
// the OIDC discovery document, falling back to the server itself.
func authServerFor(server string) string {
	if server == "juliahub.com" {
		return "auth.juliahub.com"
	}
	authServerMu.Lock()
	defer authServerMu.Unlock()
	if host, ok := authServerCache[server]; ok {
		return host
	}
	host := discoverAuthServer(server)
	authServerCache[server] = host
	return host
}

// discoverAuthServer is a variable so tests can avoid the network.
var discoverAuthServer = func(server string) string {
	client := &http.Client{Timeout: 10 * time.Second}
	discoverable := func(host string) bool {
		return dexDiscoverable(client, "https://"+host)
	}
	// Read the login redirect alongside the probes: it is needed only when
	// neither usual candidate answers, and the <server>/dex probe can take
	// seconds to fail.
	redirect := make(chan string, 1)
	go func() { redirect <- loginRedirectAuthServer(client, server) }()
	if host, ok := probeAuthServer(server, discoverable); ok {
		return host
	}
	// A redirect to a candidate that just failed discovery is not re-probed.
	if host := <-redirect; host != "" && host != server && host != "auth."+server && discoverable(host) {
		return host
	}
	return server
}

// loginRedirectAuthServer returns the host the server's web login redirects
// to for dex authorization (<host>/dex/auth), or "" if it does not redirect
// there. This finds the dex of installs whose auth host is shared with
// another install, so it is neither the server itself nor auth.<server>.
// The redirect is read, not followed, whatever the client's own redirect policy.
func loginRedirectAuthServer(client *http.Client, server string) string {
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Get("https://" + server + "/login")
	if err != nil {
		return ""
	}
	resp.Body.Close()
	return dexAuthHost(resp.StatusCode, resp.Header.Get("Location"))
}

// dexAuthHost is the pure part of loginRedirectAuthServer: the host of an
// https redirect to a dex authorization endpoint, else "".
func dexAuthHost(status int, location string) string {
	if status < 300 || status > 399 || location == "" {
		return ""
	}
	u, err := url.Parse(location)
	if err != nil || u.Scheme != "https" || u.Host == "" || strings.TrimSuffix(u.Path, "/") != "/dex/auth" {
		return ""
	}
	return u.Host
}

// probeAuthServer returns the first of auth.<server>, server whose dex
// answers discovery and true, else server and false (the fallback is a
// guess). auth.<server> goes first because it is the cheap probe either way:
// on JuliaHub.com-style installs it answers at once while <server>/dex is slow
// to fail (a gateway error after several seconds), and on enterprise installs
// the auth. name normally does not resolve at all.
func probeAuthServer(server string, discoverable func(host string) bool) (string, bool) {
	var candidates []string
	if !strings.HasPrefix(server, "auth.") {
		candidates = append(candidates, "auth."+server)
	}
	candidates = append(candidates, server)
	// Probe concurrently (a failing probe can take seconds), pick in order.
	up := make([]bool, len(candidates))
	var wg sync.WaitGroup
	for i, host := range candidates {
		wg.Add(1)
		go func() {
			defer wg.Done()
			up[i] = discoverable(host)
		}()
	}
	wg.Wait()
	for i, host := range candidates {
		if up[i] {
			return host, true
		}
	}
	return server, false
}

// rememberAuthServerFromToken records the auth host named by a stored token's
// issuer (https://<host>/dex), so refreshing it needs no discovery probe. Only
// an issuer on server itself or on auth.<server> is trusted.
func rememberAuthServerFromToken(server, accessToken string) {
	claims, err := decodeJWT(accessToken)
	if err != nil {
		return
	}
	u, err := url.Parse(claims.Issuer)
	if err != nil || u.Scheme != "https" || strings.TrimSuffix(u.Path, "/") != "/dex" {
		return
	}
	if host := u.Host; host == server || host == "auth."+server {
		authServerMu.Lock()
		authServerCache[server] = host
		authServerMu.Unlock()
	}
}

// dexDiscoverable reports whether baseURL serves its own dex: an OIDC
// discovery document (HTTP 200) whose issuer is baseURL/dex, as OIDC requires.
// A host that merely relays another host's document (a wildcard ingress, say)
// does not count.
func dexDiscoverable(client *http.Client, baseURL string) bool {
	resp, err := client.Get(baseURL + "/dex/.well-known/openid-configuration")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var doc struct {
		Issuer string `json:"issuer"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc) != nil {
		return false
	}
	return strings.TrimSuffix(doc.Issuer, "/") == baseURL+"/dex"
}

func deviceFlow(server string) (*TokenResponse, error) {
	authServer := authServerFor(server)

	deviceCodeURL := fmt.Sprintf("https://%s/dex/device/code", authServer)
	tokenURL := fmt.Sprintf("https://%s/dex/token", authServer)

	// Step 1: Request device code
	data := url.Values{}
	data.Set("client_id", "device")
	data.Set("scope", "openid email profile offline_access")
	data.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	resp, err := http.PostForm(deviceCodeURL, data)
	if err != nil {
		return nil, fmt.Errorf("failed to request device code: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read device code response: %w", err)
	}

	var deviceResp DeviceCodeResponse
	if err := json.Unmarshal(body, &deviceResp); err != nil {
		return nil, fmt.Errorf("failed to parse device code response: %w", err)
	}

	// Step 2: Display user instructions
	fmt.Printf("Go to %s and authorize this device\n", deviceResp.VerificationURIComplete)
	fmt.Printf("Waiting for authorization...\n")

	// Step 3: Poll for token
	client := &http.Client{Timeout: 30 * time.Second}
	token, err := pollDeviceToken(client, tokenURL, deviceResp, realClock{})
	if err != nil {
		return nil, err
	}
	if token.RefreshToken == "" {
		fmt.Printf("Warning: No refresh token received. This may indicate an issue with the authentication provider.\n")
		fmt.Printf("Consider trying the GitHub connector instead for better token management.\n")
	}
	return token, nil
}

// pollClock lets tests drive pollDeviceToken without waiting.
type pollClock interface {
	Now() time.Time
	Sleep(time.Duration)
}

type realClock struct{}

func (realClock) Now() time.Time        { return time.Now() }
func (realClock) Sleep(d time.Duration) { time.Sleep(d) }

// defaultDevicePollInterval is RFC 8628's default when the server sends none.
const defaultDevicePollInterval = 5 * time.Second

// defaultDeviceCodeLifetime bounds polling when the server sends no
// expires_in.
const defaultDeviceCodeLifetime = 15 * time.Minute

// maxDevicePollBackoff caps the interval doubling after transient failures.
const maxDevicePollBackoff = time.Minute

// pollDeviceToken polls the token endpoint until the user approves the device,
// following RFC 8628 section 3.5: wait the server's interval between polls,
// add 5 seconds to it on slow_down, keep going on authorization_pending, and
// stop on access_denied, expired_token or once the device code has expired.
// A poll that gets no answer, or a server error without an OAuth error body,
// doubles the interval (up to maxDevicePollBackoff) and is retried.
func pollDeviceToken(client *http.Client, tokenURL string, dc DeviceCodeResponse, clock pollClock) (*TokenResponse, error) {
	interval := time.Duration(dc.Interval) * time.Second
	if interval <= 0 {
		interval = defaultDevicePollInterval
	}
	lifetime := time.Duration(dc.ExpiresIn) * time.Second
	if lifetime <= 0 {
		lifetime = defaultDeviceCodeLifetime
	}
	deadline := clock.Now().Add(lifetime)
	expired := errors.New("the login request expired before it was approved; run 'jh auth login' again")

	var lastErr error
	for {
		clock.Sleep(interval)
		if clock.Now().After(deadline) {
			if lastErr != nil {
				return nil, fmt.Errorf("%w (last poll: %w)", expired, lastErr)
			}
			return nil, expired
		}

		token, err := postTokenForm(client, tokenURL, url.Values{
			"client_id":   {"device"},
			"device_code": {dc.DeviceCode},
			"scope":       {"openid email profile offline_access"},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		})
		if errors.Is(err, errTokenEndpointUnavailable) {
			lastErr = err
			if interval < maxDevicePollBackoff {
				interval = min(2*interval, maxDevicePollBackoff)
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		lastErr = nil

		switch token.Error {
		case "":
			if token.AccessToken == "" {
				return nil, fmt.Errorf("no access token in token response")
			}
			return token, nil
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		case "access_denied":
			return nil, fmt.Errorf("authorization was denied")
		case "expired_token":
			return nil, expired
		default:
			return nil, fmt.Errorf("authorization failed: %s", token.Error)
		}
	}
}

// errTokenEndpointUnavailable wraps postTokenForm failures that say nothing
// about the request itself: no answer, or a server error without a JSON body.
var errTokenEndpointUnavailable = errors.New("token endpoint unavailable")

// postTokenForm posts form to a dex token endpoint and decodes the reply. An
// OAuth error answer is returned in TokenResponse.Error, not as an error.
func postTokenForm(client *http.Client, tokenURL string, form url.Values) (*TokenResponse, error) {
	resp, err := client.PostForm(tokenURL, form)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errTokenEndpointUnavailable, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: reading the response: %w", errTokenEndpointUnavailable, err)
	}
	var token TokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		if resp.StatusCode >= 500 {
			return nil, fmt.Errorf("%w: HTTP %d", errTokenEndpointUnavailable, resp.StatusCode)
		}
		return nil, fmt.Errorf("failed to parse token response (HTTP %d): %w", resp.StatusCode, err)
	}
	return &token, nil
}

// refreshToken exchanges stored's refresh token at the dex host named by its
// access token's issuer (falling back to discovery).
func refreshToken(stored *StoredToken) (*TokenResponse, error) {
	rememberAuthServerFromToken(stored.Server, stored.AccessToken)
	tokenURL := fmt.Sprintf("https://%s/dex/token", authServerFor(stored.Server))

	client := &http.Client{Timeout: 30 * time.Second}
	token, err := postTokenForm(client, tokenURL, url.Values{
		"client_id":     {"device"},
		"grant_type":    {"refresh_token"},
		"refresh_token": {stored.RefreshToken},
	})
	if err != nil {
		return nil, err
	}

	if token.Error != "" {
		return nil, fmt.Errorf("failed to refresh token: %s", token.Error)
	}

	if token.AccessToken == "" {
		return nil, fmt.Errorf("no access token in refresh response")
	}

	return token, nil
}

func ensureValidToken() (*StoredToken, error) {
	storedToken, err := readStoredToken()
	if err != nil {
		return nil, fmt.Errorf("failed to read stored token: %w", err)
	}

	// Check if token is expired
	expired, err := isTokenExpired(storedToken.AccessToken, storedToken.ExpiresIn)
	if err != nil {
		return nil, fmt.Errorf("failed to check token expiry: %w", err)
	}

	if !expired {
		return storedToken, nil
	}

	// Token is expired, try to refresh
	if storedToken.RefreshToken == "" {
		return nil, fmt.Errorf("access token expired and no refresh token available")
	}

	refreshedToken, err := refreshToken(storedToken)
	if err != nil {
		return nil, fmt.Errorf("failed to refresh token: %w", err)
	}

	// Save the refreshed token
	err = writeTokenToConfig(storedToken.Server, *refreshedToken)
	if err != nil {
		return nil, fmt.Errorf("failed to save refreshed token: %w", err)
	}

	// Return the updated token
	updatedToken := &StoredToken{
		AccessToken:  refreshedToken.AccessToken,
		RefreshToken: refreshedToken.RefreshToken,
		TokenType:    refreshedToken.TokenType,
		ExpiresIn:    refreshedToken.ExpiresIn,
		IDToken:      refreshedToken.IDToken,
		Server:       storedToken.Server,
		Name:         storedToken.Name,
		Email:        storedToken.Email,
	}

	// Extract name and email from new ID token if available
	if refreshedToken.IDToken != "" {
		if claims, err := decodeJWT(refreshedToken.IDToken); err == nil {
			if claims.Name != "" {
				updatedToken.Name = claims.Name
			}
			if claims.Email != "" {
				updatedToken.Email = claims.Email
			}
		}
	}

	// Update Julia credentials after token refresh
	// We ignore errors here to avoid breaking token operations if Julia setup fails
	_ = updateJuliaCredentialsIfNeeded(storedToken.Server, updatedToken)

	return updatedToken, nil
}

// updateJuliaCredentialsIfNeeded updates Julia credentials if the auth file exists
// This is called after token refresh to keep credentials in sync
func updateJuliaCredentialsIfNeeded(server string, token *StoredToken) error {
	// Determine Julia depot path
	depotPath, err := getJuliaDepotPath()
	if err != nil {
		return err
	}

	// Check if the auth.toml file exists
	authFilePath := filepath.Join(depotPath, "servers", server, "auth.toml")
	if _, err := os.Stat(authFilePath); os.IsNotExist(err) {
		// File doesn't exist, so user hasn't used Julia integration yet
		return nil
	}

	// File exists, update it
	return createJuliaAuthFile(server, token)
}

func formatTokenInfo(token *StoredToken) string {
	claims, err := decodeJWT(token.AccessToken)
	if err != nil {
		return fmt.Sprintf("Error decoding token: %v", err)
	}

	expired, _ := isTokenExpired(token.AccessToken, token.ExpiresIn)
	status := "Valid"
	if expired {
		status = "Expired"
	}
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Server: %s\n", token.Server))
	result.WriteString(fmt.Sprintf("Token Status: %s\n", status))
	result.WriteString(fmt.Sprintf("Subject: %s\n", claims.Subject))
	result.WriteString(fmt.Sprintf("Issuer: %s\n", claims.Issuer))
	if claims.Audience != "" {
		result.WriteString(fmt.Sprintf("Audience: %s\n", claims.Audience))
	}
	if claims.IssuedAt > 0 {
		issuedTime := time.Unix(claims.IssuedAt, 0)
		result.WriteString(fmt.Sprintf("Issued At: %s\n", issuedTime.Format(time.RFC3339)))
	}
	if claims.ExpiresAt > 0 {
		expireTime := time.Unix(claims.ExpiresAt, 0)
		result.WriteString(fmt.Sprintf("Expires At: %s\n", expireTime.Format(time.RFC3339)))
	}
	if token.TokenType != "" {
		result.WriteString(fmt.Sprintf("Token Type: %s\n", token.TokenType))
	}
	result.WriteString(fmt.Sprintf("Has Refresh Token: %t\n", token.RefreshToken != ""))
	if token.Name != "" {
		result.WriteString(fmt.Sprintf("Name: %s\n", token.Name))
	}
	if token.Email != "" {
		result.WriteString(fmt.Sprintf("Email: %s\n", token.Email))
	}

	return result.String()
}

func authEnvCommand() error {
	// Ensure we have a valid token (login if needed, refresh if possible)
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("failed to ensure valid token: %w", err)
	}

	// Parse the ID token to get expiration time
	claims, err := decodeJWT(token.IDToken)
	if err != nil {
		return fmt.Errorf("failed to decode ID token: %w", err)
	}

	// Print environment variables to stdout
	fmt.Printf("JULIAHUB_HOST=%s\n", token.Server)
	fmt.Printf("JULIAHUB_PORT=443\n")
	fmt.Printf("JULIAHUB_ID_TOKEN=%s\n", token.IDToken)
	fmt.Printf("JULIAHUB_ID_TOKEN_EXPIRES=%d\n", claims.ExpiresAt)
	fmt.Printf("\n")
	fmt.Printf("INVOCATION_HOST=%s\n", token.Server)
	fmt.Printf("INVOCATION_PORT=443\n")
	fmt.Printf("INVOCATION_USER_EMAIL=%s\n", token.Email)

	return nil
}
