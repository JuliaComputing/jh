package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
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
	return pickAuthServer(server, func(host string) bool {
		return dexDiscoverable(client, "https://"+host)
	})
}

// pickAuthServer returns the first of auth.<server>, server whose dex answers
// discovery, else server. auth.<server> goes first because it is the cheap
// probe either way: on JuliaHub.com-style installs it answers at once while
// <server>/dex is slow to fail (a gateway error after several seconds), and on
// enterprise installs the auth. name normally does not resolve at all.
func pickAuthServer(server string, discoverable func(host string) bool) string {
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
			return host
		}
	}
	return server
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

// pollDeviceToken polls the token endpoint until the user approves the device,
// following RFC 8628 section 3.5: wait the server's interval between polls,
// add 5 seconds to it on slow_down, keep going on authorization_pending, and
// stop on access_denied, expired_token or once the device code has expired.
func pollDeviceToken(client *http.Client, tokenURL string, dc DeviceCodeResponse, clock pollClock) (*TokenResponse, error) {
	interval := time.Duration(dc.Interval) * time.Second
	if interval <= 0 {
		interval = defaultDevicePollInterval
	}
	var deadline time.Time
	if dc.ExpiresIn > 0 {
		deadline = clock.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	}
	expired := fmt.Errorf("the login request expired before it was approved; run 'jh auth login' again")

	for {
		clock.Sleep(interval)
		if !deadline.IsZero() && clock.Now().After(deadline) {
			return nil, expired
		}

		tokenData := url.Values{}
		tokenData.Set("client_id", "device")
		tokenData.Set("device_code", dc.DeviceCode)
		tokenData.Set("scope", "openid email profile offline_access")
		tokenData.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")

		tokenResp, err := client.PostForm(tokenURL, tokenData)
		if err != nil {
			return nil, fmt.Errorf("failed to request token: %w", err)
		}
		tokenBody, err := io.ReadAll(tokenResp.Body)
		tokenResp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read token response: %w", err)
		}

		var token TokenResponse
		if err := json.Unmarshal(tokenBody, &token); err != nil {
			return nil, fmt.Errorf("failed to parse token response: %w", err)
		}

		switch token.Error {
		case "":
			if token.AccessToken == "" {
				return nil, fmt.Errorf("no access token in token response")
			}
			return &token, nil
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

func refreshToken(server string, refreshToken string) (*TokenResponse, error) {
	authServer := authServerFor(server)

	tokenURL := fmt.Sprintf("https://%s/dex/token", authServer)

	data := url.Values{}
	data.Set("client_id", "device")
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", refreshToken)

	resp, err := http.PostForm(tokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("failed to refresh token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read refresh token response: %w", err)
	}

	var token TokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return nil, fmt.Errorf("failed to parse refresh token response: %w", err)
	}

	if token.Error != "" {
		return nil, fmt.Errorf("failed to refresh token: %s", token.Error)
	}

	if token.AccessToken == "" {
		return nil, fmt.Errorf("no access token in refresh response")
	}

	return &token, nil
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

	rememberAuthServerFromToken(storedToken.Server, storedToken.AccessToken)
	refreshedToken, err := refreshToken(storedToken.Server, storedToken.RefreshToken)
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
