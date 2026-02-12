package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Token represents a JuliaHub API token
type Token struct {
	CreatedBy           string `json:"created_by"`
	IsExpired           bool   `json:"is_expired"`
	CreatedAt           string `json:"created_at"`
	ExpiresAt           string `json:"expires_at"`
	ExpiresAtIsEstimate bool   `json:"expires_at_is_estimate,omitempty"`
	Subject             string `json:"subject"`
	Signature           string `json:"signature"`
}

// TokensResponse represents the response from /app/token/activelist
type TokensResponse struct {
	Tokens  []Token `json:"tokens"`
	Message string  `json:"message"`
	Success bool    `json:"success"`
}

// formatTokenDate parses and formats a token date string into a readable format
func formatTokenDate(dateStr string) string {
	// Try parsing as RFC3339 with fractional seconds
	t, err := time.Parse(time.RFC3339, dateStr)
	if err != nil {
		// If parsing fails, return the original string
		return dateStr
	}
	// Convert to local timezone
	localTime := t.Local()
	// Format with timezone offset: "Jan 02, 2006 15:04:05 -0700"
	return localTime.Format("Jan 02, 2006 15:04:05 -0700")
}

func listTokens(server string, verbose bool) error {
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	url := fmt.Sprintf("https://%s/app/token/activelist", server)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.IDToken))
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("request failed (status %d): %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var response TokensResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if !response.Success {
		return fmt.Errorf("API request failed: %s", response.Message)
	}

	// Display tokens
	fmt.Printf("Tokens (%d total):\n\n", len(response.Tokens))

	if verbose {
		// Verbose mode: show all details
		for _, tok := range response.Tokens {
			fmt.Printf("Subject: %s\n", tok.Subject)
			fmt.Printf("Signature: %s\n", tok.Signature)
			fmt.Printf("Created By: %s\n", tok.CreatedBy)
			fmt.Printf("Created At: %s\n", formatTokenDate(tok.CreatedAt))
			fmt.Printf("Expires At: %s", formatTokenDate(tok.ExpiresAt))
			if tok.ExpiresAtIsEstimate {
				fmt.Printf(" (estimate)")
			}
			fmt.Printf("\n")
			fmt.Printf("Expired: %t\n", tok.IsExpired)
			fmt.Println()
		}
	} else {
		// Default mode: show only Subject, Created By, and Expired status
		for _, tok := range response.Tokens {
			fmt.Printf("Subject: %s\n", tok.Subject)
			fmt.Printf("Created By: %s\n", tok.CreatedBy)
			fmt.Printf("Expired: %t\n", tok.IsExpired)
			fmt.Println()
		}
	}

	return nil
}
