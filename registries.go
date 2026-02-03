package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// fetchRegistries retrieves all registries from the API and returns them
func fetchRegistries(server string) ([]Registry, error) {
	token, err := ensureValidToken()
	if err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}

	url := fmt.Sprintf("https://%s/api/v1/registry/registries/descriptions", server)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.IDToken))
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed (status %d): %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var registries []Registry
	if err := json.Unmarshal(body, &registries); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return registries, nil
}

func listRegistries(server string, verbose bool) error {
	registries, err := fetchRegistries(server)
	if err != nil {
		return err
	}

	if len(registries) == 0 {
		fmt.Println("No registries found")
		return nil
	}

	fmt.Printf("Found %d registr%s:\n\n", len(registries), pluralize(len(registries), "y", "ies"))

	if verbose {
		// Verbose mode: show all details
		for _, registry := range registries {
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
	} else {
		// Default mode: show UUID, Name, and Registry ID (needed for package search filtering)
		for _, registry := range registries {
			fmt.Printf("UUID: %s\n", registry.UUID)
			fmt.Printf("Name: %s\n", registry.Name)
			fmt.Println()
		}
	}

	return nil
}

func pluralize(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}
