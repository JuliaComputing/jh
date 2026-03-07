package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type homepageConfig struct {
	Md        string `json:"md"`
	UpdatedAt string `json:"updated_at"`
}

// homepageResponse handles `message` being either an object or a string.
type homepageResponse struct {
	Success bool `json:"success"`
	Message *homepageConfig
	RawMsg  json.RawMessage `json:"message"`
}

func (r *homepageResponse) UnmarshalJSON(data []byte) error {
	type alias struct {
		Success bool            `json:"success"`
		Message json.RawMessage `json:"message"`
	}
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	r.Success = a.Success
	r.RawMsg = a.Message
	if len(a.Message) > 0 && a.Message[0] == '{' {
		var cfg homepageConfig
		if err := json.Unmarshal(a.Message, &cfg); err != nil {
			return err
		}
		r.Message = &cfg
	}
	return nil
}

func showLandingPage(server string) error {
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required — run 'jh auth login' first")
	}

	url := fmt.Sprintf("https://%s/app/homepage", server)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.IDToken))
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the server")
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("you do not have permission to view the landing page configuration")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("could not retrieve landing page (server returned %d)", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("could not read response from server")
	}

	var result homepageResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("unexpected response from server")
	}

	if !result.Success {
		var msg string
		_ = json.Unmarshal(result.RawMsg, &msg)
		return fmt.Errorf("%s", msg)
	}

	if result.Message == nil {
		fmt.Println("Currently using default landing screen content.")
		return nil
	}

	fmt.Printf("Last updated: %s\n\n", result.Message.UpdatedAt)
	fmt.Println(result.Message.Md)
	return nil
}

func setLandingPage(server, content string) error {
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required — run 'jh auth login' first")
	}

	payload, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		return fmt.Errorf("could not prepare request")
	}

	url := fmt.Sprintf("https://%s/app/config/homepage", server)
	req, err := http.NewRequest("POST", url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("could not prepare request")
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.IDToken))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the server")
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("you do not have permission to update the landing page")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("could not update landing page (server returned %d)", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("could not read response from server")
	}

	var result struct {
		Success bool            `json:"success"`
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(body, &result); err != nil || !result.Success {
		var msg string
		_ = json.Unmarshal(result.Message, &msg)
		return fmt.Errorf("%s", msg)
	}

	fmt.Println("Successfully updated the landing page.")
	return nil
}

func disableLandingPage(server string) error {
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required — run 'jh auth login' first")
	}

	url := fmt.Sprintf("https://%s/app/config/homepage", server)
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("could not prepare request")
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.IDToken))
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the server")
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("you do not have permission to disable the landing page")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("could not disable landing page (server returned %d)", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("could not read response from server")
	}

	var result struct {
		Success bool            `json:"success"`
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(body, &result); err != nil || !result.Success {
		var msg string
		_ = json.Unmarshal(result.Message, &msg)
		return fmt.Errorf("%s", msg)
	}

	fmt.Println("Successfully disabled the custom landing page.")
	return nil
}

func readContentFromFileOrArgOrStdin(filePath, contentArg string) (string, error) {
	if filePath != "" {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return "", fmt.Errorf("failed to read file %q: %w", filePath, err)
		}
		return string(data), nil
	}
	if contentArg != "" {
		return contentArg, nil
	}
	// Fall back to stdin
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("failed to read from stdin: %w", err)
	}
	return string(data), nil
}
