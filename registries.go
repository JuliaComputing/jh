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

type Registry struct {
	UUID         string     `json:"uuid"`
	Name         string     `json:"name"`
	RegistryID   int        `json:"registry_id"`
	Owner        *string    `json:"owner"`
	Register     bool       `json:"register"`
	CreationDate CustomTime `json:"creation_date"`
	PackageCount int        `json:"package_count"`
	Description  string     `json:"description"`
}

func pluralize(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

// apiGet performs a GET request with up to 3 attempts, retrying on transient errors.
func apiGet(url, idToken string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", idToken))
		req.Header.Set("Accept", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("failed to read response: %w", readErr)
			continue
		}
		if resp.StatusCode == http.StatusInternalServerError {
			lastErr = fmt.Errorf("API request failed (status %d): %s", resp.StatusCode, string(body))
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("API request failed (status %d): %s", resp.StatusCode, string(body))
		}
		return body, nil
	}
	return nil, lastErr
}

func listRegistries(server string, verbose bool) error {
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	body, err := apiGet(fmt.Sprintf("https://%s/api/v1/registry/registries/descriptions", server), token.IDToken)
	if err != nil {
		return err
	}

	var registries []Registry
	if err := json.Unmarshal(body, &registries); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if len(registries) == 0 {
		fmt.Println("No registries found")
		return nil
	}

	fmt.Printf("Found %d registr%s:\n\n", len(registries), pluralize(len(registries), "y", "ies"))

	for _, registry := range registries {
		if !verbose {
			fmt.Printf("%s (%s)\n", registry.Name, registry.UUID)
			continue
		}
		fmt.Printf("UUID: %s\n", registry.UUID)
		fmt.Printf("Name: %s\n", registry.Name)
		if registry.Owner != nil {
			fmt.Printf("Owner: %s\n", *registry.Owner)
		} else {
			fmt.Printf("Owner: (none)\n")
		}
		fmt.Printf("Register: %t\n", registry.Register)
		fmt.Printf("Creation Date: %s\n", registry.CreationDate.Time.Format(time.RFC3339))
		fmt.Printf("Package Count: %d\n", registry.PackageCount)
		if registry.Description != "" {
			fmt.Printf("Description: %s\n", registry.Description)
		}
		fmt.Println()
	}

	return nil
}

func getRegistryConfig(server, name string) error {
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	body, err := apiGet(fmt.Sprintf("https://%s/api/v1/registry/config/registry/%s", server, name), token.IDToken)
	if err != nil {
		return err
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, body, "", "  "); err != nil {
		fmt.Println(string(body))
		return nil
	}
	fmt.Println(pretty.String())
	return nil
}

func readRegistryPayload(filePath string) (map[string]interface{}, error) {
	var data []byte
	var err error

	if filePath != "" {
		data, err = os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read file %q: %w", filePath, err)
		}
	} else {
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) != 0 {
			return nil, fmt.Errorf("no JSON payload provided — pipe JSON via stdin or use --file")
		}
		data, err = io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("failed to read stdin: %w", err)
		}
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	nameVal, ok := payload["name"]
	if !ok || nameVal == "" {
		return nil, fmt.Errorf(`JSON payload must include a non-empty "name"`)
	}

	providers, ok := payload["download_providers"]
	if !ok {
		return nil, fmt.Errorf(`JSON payload must include "download_providers"`)
	}
	if provList, ok := providers.([]interface{}); !ok || len(provList) == 0 {
		return nil, fmt.Errorf(`"download_providers" must be a non-empty array`)
	}

	return payload, nil
}

func submitRegistry(server string, payload map[string]interface{}, operation string) error {
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	name, _ := payload["name"].(string)

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	apiURL := fmt.Sprintf("https://%s/api/v1/registry/config/registry/%s", server, name)
	client := &http.Client{Timeout: 30 * time.Second}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		req, err := http.NewRequest("POST", apiURL, bytes.NewReader(payloadBytes))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.IDToken))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-JuliaHub-Ensure-Js", "true")

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			continue
		}
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusInternalServerError {
			lastErr = fmt.Errorf("API request failed (status %d): %s", resp.StatusCode, string(bodyBytes))
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("API request failed (status %d): %s", resp.StatusCode, string(bodyBytes))
		}
		lastErr = nil
		break
	}
	if lastErr != nil {
		return lastErr
	}

	fmt.Printf("Registry '%s' %s submitted, validating configuration...\n", name, operation)
	return pollRegistrySaveStatus(server, token.IDToken, name, operation)
}

func createRegistry(server string, payload map[string]interface{}) error {
	return submitRegistry(server, payload, "creation")
}

func updateRegistry(server string, payload map[string]interface{}) error {
	return submitRegistry(server, payload, "update")
}

type saveStatusResponse struct {
	Status string `json:"status"`
	Result *struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	} `json:"result"`
}

func pollRegistrySaveStatus(server, idToken, registryName, operation string) error {
	apiURL := fmt.Sprintf("https://%s/api/v1/registry/config/registry/%s/savestatus", server, registryName)
	client := &http.Client{Timeout: 30 * time.Second}
	deadline := time.Now().Add(2 * time.Minute)

	for time.Now().Before(deadline) {
		req, err := http.NewRequest("GET", apiURL, nil)
		if err != nil {
			return fmt.Errorf("failed to create status request: %w", err)
		}
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", idToken))
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-JuliaHub-Ensure-Js", "true")

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to check status: %w", err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("failed to read status response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			if resp.StatusCode == http.StatusInternalServerError {
				time.Sleep(3 * time.Second)
				continue
			}
			return fmt.Errorf("status check failed (status %d): %s", resp.StatusCode, string(body))
		}

		var status saveStatusResponse
		if err := json.Unmarshal(body, &status); err != nil {
			return fmt.Errorf("failed to parse status response: %w", err)
		}

		if status.Status == "done" {
			if status.Result != nil && status.Result.Success {
				fmt.Printf("Registry '%s' %s completed successfully!\n", registryName, operation)
				return nil
			} else if status.Result != nil {
				return fmt.Errorf("registry %s failed: %s", operation, status.Result.Message)
			}
			return fmt.Errorf("registry %s failed: unknown error", operation)
		}

		time.Sleep(3 * time.Second)
	}

	return fmt.Errorf("timed out waiting for registry %s to complete", operation)
}
