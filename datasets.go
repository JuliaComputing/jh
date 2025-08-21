package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Dataset struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Description    string     `json:"description"`
	Visibility     string     `json:"visibility"`
	Groups         []string   `json:"groups"`
	Format         *string    `json:"format"`
	CredentialsURL string     `json:"credentials_url"`
	Owner          Owner      `json:"owner"`
	Storage        Storage    `json:"storage"`
	Version        string     `json:"version"`
	Versions       []Version  `json:"versions"`
	Size           int64      `json:"size"`
	DownloadURL    string     `json:"downloadURL"`
	Tags           []string   `json:"tags"`
	License        License    `json:"license"`
	Type           string     `json:"type"`
	LastModified   CustomTime `json:"lastModified"`
}

type Owner struct {
	Username string `json:"username"`
	Type     string `json:"type"`
}

type Storage struct {
	BucketRegion string `json:"bucket_region"`
	Bucket       string `json:"bucket"`
	Prefix       string `json:"prefix"`
	Vendor       string `json:"vendor"`
}

type Version struct {
	Project       *string    `json:"project"`
	Uploader      Uploader   `json:"uploader"`
	BlobstorePath string     `json:"blobstore_path"`
	Date          CustomTime `json:"date"`
	Size          int64      `json:"size"`
	Version       int        `json:"version"`
}

type Uploader struct {
	Username string `json:"username"`
}

type License struct {
	URL    *string `json:"url"`
	Name   string  `json:"name"`
	Text   string  `json:"text"`
	SPDXID string  `json:"spdx_id"`
}

type CustomTime struct {
	time.Time
}

func (ct *CustomTime) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), "\"")
	if s == "null" {
		ct.Time = time.Time{}
		return nil
	}

	// Try parsing with timezone first
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		ct.Time = t
		return nil
	}

	// Try parsing without timezone (assume UTC)
	if t, err := time.Parse("2006-01-02T15:04:05.999", s); err == nil {
		ct.Time = t.UTC()
		return nil
	}

	// Try parsing without milliseconds and timezone
	if t, err := time.Parse("2006-01-02T15:04:05", s); err == nil {
		ct.Time = t.UTC()
		return nil
	}

	return fmt.Errorf("cannot parse %s as time", s)
}

func listDatasets(server string) error {
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	url := fmt.Sprintf("https://%s/datasets", server)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.AccessToken))
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API request failed (status %d): %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var datasets []Dataset
	if err := json.Unmarshal(body, &datasets); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if len(datasets) == 0 {
		fmt.Println("No datasets found")
		return nil
	}

	fmt.Printf("Found %d dataset(s):\n\n", len(datasets))
	for _, dataset := range datasets {
		fmt.Printf("ID: %s\n", dataset.ID)
		fmt.Printf("Name: %s\n", dataset.Name)
		fmt.Printf("Owner: %s (%s)\n", dataset.Owner.Username, dataset.Owner.Type)
		if dataset.Description != "" {
			fmt.Printf("Description: %s\n", dataset.Description)
		}
		fmt.Printf("Size: %d bytes\n", dataset.Size)
		fmt.Printf("Visibility: %s\n", dataset.Visibility)
		fmt.Printf("Type: %s\n", dataset.Type)
		fmt.Printf("Version: %s\n", dataset.Version)
		fmt.Printf("Last Modified: %s\n", dataset.LastModified.Time.Format(time.RFC3339))
		if len(dataset.Tags) > 0 {
			fmt.Printf("Tags: %v\n", dataset.Tags)
		}
		if dataset.License.Name != "" {
			fmt.Printf("License: %s\n", dataset.License.Name)
		}
		fmt.Println()
	}

	return nil
}

type DatasetDownloadURL struct {
	DatasetID string `json:"dataset_id"`
	Version   string `json:"version"`
	Dataset   string `json:"dataset"`
	URL       string `json:"url"`
}

func getDatasetDownloadURL(server, datasetID, version string, token *StoredToken) (*DatasetDownloadURL, error) {
	url := fmt.Sprintf("https://%s/datasets/%s/url/v%s", server, datasetID, version)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.IDToken))
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get download URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get download URL (status %d): %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read download URL response: %w", err)
	}

	var downloadURL DatasetDownloadURL
	if err := json.Unmarshal(body, &downloadURL); err != nil {
		return nil, fmt.Errorf("failed to parse download URL: %w", err)
	}

	return &downloadURL, nil
}

func getDatasetVersions(server, datasetID string, token *StoredToken) ([]Version, error) {
	// Get all datasets
	datasets, err := getDatasets(server, token)
	if err != nil {
		return nil, fmt.Errorf("failed to get datasets: %w", err)
	}

	// Find the dataset with the matching ID
	var targetDataset *Dataset
	for i := range datasets {
		if datasets[i].ID == datasetID {
			targetDataset = &datasets[i]
			break
		}
	}

	if targetDataset == nil {
		return nil, fmt.Errorf("dataset with ID %s not found", datasetID)
	}

	fmt.Printf("DEBUG: Found dataset: %s\n", targetDataset.Name)
	fmt.Printf("DEBUG: Dataset versions:\n")
	for i, version := range targetDataset.Versions {
		fmt.Printf("  [%d] Version %d, Size: %d, Date: %s, BlobstorePath: %s\n",
			i, version.Version, version.Size, version.Date.Time.Format(time.RFC3339), version.BlobstorePath)
	}

	return targetDataset.Versions, nil
}

func getDatasets(server string, token *StoredToken) ([]Dataset, error) {
	url := fmt.Sprintf("https://%s/datasets", server)

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

	var datasets []Dataset
	if err := json.Unmarshal(body, &datasets); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return datasets, nil
}

func resolveDatasetIdentifier(server, identifier string, token *StoredToken) (string, error) {
	// If identifier contains dashes and looks like a UUID, use it directly
	if strings.Contains(identifier, "-") && len(identifier) >= 32 {
		return identifier, nil
	}

	// Parse user/name format
	var targetUser, targetName string
	if strings.Contains(identifier, "/") {
		parts := strings.SplitN(identifier, "/", 2)
		targetUser = parts[0]
		targetName = parts[1]
	} else {
		targetName = identifier
	}

	fmt.Printf("Searching for dataset: name='%s', user='%s'\n", targetName, targetUser)

	// Get all datasets and search by name/user
	datasets, err := getDatasets(server, token)
	if err != nil {
		return "", fmt.Errorf("failed to get datasets: %w", err)
	}

	var matches []Dataset
	for _, dataset := range datasets {
		nameMatch := dataset.Name == targetName
		userMatch := targetUser == "" || dataset.Owner.Username == targetUser

		if nameMatch && userMatch {
			matches = append(matches, dataset)
		}
	}

	if len(matches) == 0 {
		if targetUser != "" {
			return "", fmt.Errorf("no dataset found with name '%s' by user '%s'", targetName, targetUser)
		} else {
			return "", fmt.Errorf("no dataset found with name '%s'", targetName)
		}
	}

	if len(matches) > 1 {
		fmt.Printf("Multiple datasets found with name '%s':\n", targetName)
		for _, match := range matches {
			fmt.Printf("  - %s by %s (ID: %s)\n", match.Name, match.Owner.Username, match.ID)
		}
		return "", fmt.Errorf("multiple datasets found, please specify user as 'user/name' or use dataset ID")
	}

	// Single match found
	match := matches[0]
	fmt.Printf("Found dataset: %s by %s (ID: %s)\n", match.Name, match.Owner.Username, match.ID)
	return match.ID, nil
}

func downloadDataset(server, datasetIdentifier, version, localPath string) error {
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	// Resolve the dataset identifier to a UUID
	datasetID, err := resolveDatasetIdentifier(server, datasetIdentifier, token)
	if err != nil {
		return err
	}

	var versionNumber string
	var datasetName string

	if version != "" {
		// Version was provided, strip the 'v' prefix
		versionNumber = strings.TrimPrefix(version, "v")
		fmt.Printf("Using specified version: %s\n", version)

		// We still need to get the dataset name for the filename
		versions, err := getDatasetVersions(server, datasetID, token)
		if err != nil {
			return fmt.Errorf("failed to get dataset versions: %w", err)
		}

		if len(versions) == 0 {
			return fmt.Errorf("no versions available for dataset")
		}

		// Get dataset name from the first version (all versions have the same dataset name)
		datasetName = datasetID // fallback to ID if we can't get the name
	} else {
		// No version provided, find the latest version
		versions, err := getDatasetVersions(server, datasetID, token)
		if err != nil {
			return fmt.Errorf("failed to get dataset versions: %w", err)
		}

		if len(versions) == 0 {
			return fmt.Errorf("no versions available for dataset")
		}

		// Find the latest version (highest version number)
		var latestVersion *Version
		for i := range versions {
			if latestVersion == nil || versions[i].Version > latestVersion.Version {
				latestVersion = &versions[i]
			}
		}

		fmt.Printf("DEBUG: Latest version found: %d\n", latestVersion.Version)
		versionNumber = fmt.Sprintf("%d", latestVersion.Version)
		datasetName = datasetID // fallback to ID if we can't get the name
	}

	// Get download URL
	downloadInfo, err := getDatasetDownloadURL(server, datasetID, versionNumber, token)
	if err != nil {
		return fmt.Errorf("failed to get download URL: %w", err)
	}

	fmt.Printf("Downloading dataset: %s\n", downloadInfo.Dataset)
	fmt.Printf("Version: %s\n", downloadInfo.Version)
	fmt.Printf("Download URL: %s\n", downloadInfo.URL)

	// Download the file
	resp, err := http.Get(downloadInfo.URL)
	if err != nil {
		return fmt.Errorf("failed to download dataset: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("download failed (status %d): %s", resp.StatusCode, string(body))
	}

	// Determine local file name
	if localPath == "" {
		if downloadInfo.Dataset != "" {
			localPath = fmt.Sprintf("%s.tar.gz", downloadInfo.Dataset)
		} else {
			localPath = fmt.Sprintf("%s.tar.gz", datasetName)
		}
	}

	// Create local file
	file, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("failed to create local file: %w", err)
	}
	defer file.Close()

	// Copy response body to file
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	fmt.Printf("Successfully downloaded dataset to: %s\n", localPath)
	return nil
}

func statusDataset(server, datasetIdentifier, version string) error {
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	// Resolve the dataset identifier to a UUID
	datasetID, err := resolveDatasetIdentifier(server, datasetIdentifier, token)
	if err != nil {
		return err
	}

	var versionNumber string

	if version != "" {
		// Version was provided, strip the 'v' prefix
		versionNumber = strings.TrimPrefix(version, "v")
		fmt.Printf("Using specified version: %s\n", version)

		// We still need to get the dataset name for the filename
		versions, err := getDatasetVersions(server, datasetID, token)
		if err != nil {
			return fmt.Errorf("failed to get dataset versions: %w", err)
		}

		if len(versions) == 0 {
			return fmt.Errorf("no versions available for dataset")
		}
	} else {
		// No version provided, find the latest version
		versions, err := getDatasetVersions(server, datasetID, token)
		if err != nil {
			return fmt.Errorf("failed to get dataset versions: %w", err)
		}

		if len(versions) == 0 {
			return fmt.Errorf("no versions available for dataset")
		}

		// Find the latest version (highest version number)
		var latestVersion *Version
		for i := range versions {
			if latestVersion == nil || versions[i].Version > latestVersion.Version {
				latestVersion = &versions[i]
			}
		}

		fmt.Printf("DEBUG: Latest version found: %d\n", latestVersion.Version)
		versionNumber = fmt.Sprintf("%d", latestVersion.Version)
	}

	// Get download URL (but don't download)
	downloadInfo, err := getDatasetDownloadURL(server, datasetID, versionNumber, token)
	if err != nil {
		return fmt.Errorf("failed to get download URL: %w", err)
	}

	fmt.Printf("Dataset: %s\n", downloadInfo.Dataset)
	fmt.Printf("Version: %s\n", downloadInfo.Version)
	fmt.Printf("Download URL: %s\n", downloadInfo.URL)
	fmt.Printf("Status: Ready for download\n")

	return nil
}

func uploadDataset(server, datasetID, filePath string, isNew bool) error {
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return fmt.Errorf("file does not exist: %s", filePath)
	}

	if isNew {
		// Create new dataset
		return createNewDataset(server, filePath, token)
	} else {
		// Upload new version to existing dataset
		return uploadToExistingDataset(server, datasetID, filePath, token)
	}
}

func createNewDataset(server, filePath string, token *StoredToken) error {
	// Extract filename from path for dataset name
	fileName := filepath.Base(filePath)

	fmt.Printf("Creating new dataset from file: %s\n", filePath)
	fmt.Printf("Dataset name: %s\n", fileName)
	fmt.Printf("Server: %s\n", server)

	// Create form data
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add name parameter
	if err := writer.WriteField("name", fileName); err != nil {
		return fmt.Errorf("failed to write name field: %w", err)
	}

	writer.Close()

	// Create request
	url := fmt.Sprintf("https://%s/user/datasets", server)
	req, err := http.NewRequest("POST", url, &buf)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.IDToken))
	req.Header.Set("Content-Type", writer.FormDataContentType())

	// Send request
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to create dataset: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	fmt.Printf("DEBUG: Create dataset response status: %d\n", resp.StatusCode)
	fmt.Printf("DEBUG: Create dataset response body: %s\n", string(body))

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("failed to create dataset (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse response to get dataset UUID
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract UUID from response
	var datasetID string
	if id, ok := result["repo_id"]; ok {
		datasetID = fmt.Sprintf("%v", id)
	} else {
		return fmt.Errorf("could not find dataset ID in response")
	}

	fmt.Printf("Created dataset with ID: %s\n", datasetID)

	// Now upload the file to the new dataset
	fmt.Printf("Uploading file to new dataset...\n")
	return uploadToExistingDataset(server, datasetID, filePath, token)
}

func uploadToExistingDataset(server, datasetID, filePath string, token *StoredToken) error {
	fmt.Printf("Uploading file to existing dataset: %s\n", datasetID)
	fmt.Printf("File: %s\n", filePath)
	fmt.Printf("Server: %s\n", server)

	// Request presigned URL
	presignedData := map[string]bool{"_presigned": true}
	jsonData, err := json.Marshal(presignedData)
	if err != nil {
		return fmt.Errorf("failed to marshal presigned request: %w", err)
	}

	url := fmt.Sprintf("https://%s/datasets/%s/versions", server, datasetID)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create presigned request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.IDToken))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to get presigned URL: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read presigned response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("failed to get presigned URL (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse presigned response
	var presignedResponse map[string]interface{}
	if err := json.Unmarshal(body, &presignedResponse); err != nil {
		return fmt.Errorf("failed to parse presigned response: %w", err)
	}

	presignedURL, ok := presignedResponse["presigned_url"].(string)
	if !ok {
		return fmt.Errorf("presigned_url not found in response")
	}

	uploadID, ok := presignedResponse["upload_id"].(string)
	if !ok {
		return fmt.Errorf("upload_id not found in response")
	}

	// Upload file to presigned URL
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Create form data for file upload
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	fileWriter, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return fmt.Errorf("failed to create form file: %w", err)
	}

	_, err = io.Copy(fileWriter, file)
	if err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}

	writer.Close()

	// PUT to presigned URL
	putReq, err := http.NewRequest("PUT", presignedURL, &buf)
	if err != nil {
		return fmt.Errorf("failed to create upload request: %w", err)
	}

	putReq.Header.Set("Content-Type", writer.FormDataContentType())

	uploadClient := &http.Client{Timeout: 300 * time.Second}
	putResp, err := uploadClient.Do(putReq)
	if err != nil {
		return fmt.Errorf("failed to upload file: %w", err)
	}
	defer putResp.Body.Close()

	putBody, err := io.ReadAll(putResp.Body)
	if err != nil {
		return fmt.Errorf("failed to read upload response: %w", err)
	}

	if putResp.StatusCode != http.StatusOK && putResp.StatusCode != http.StatusCreated {
		return fmt.Errorf("failed to upload file (status %d): %s", putResp.StatusCode, string(putBody))
	}

	// Close the upload
	closeData := map[string]interface{}{
		"action":    "close",
		"upload_id": uploadID,
	}

	closeJsonData, err := json.Marshal(closeData)
	if err != nil {
		return fmt.Errorf("failed to marshal close request: %w", err)
	}

	closeReq, err := http.NewRequest("POST", url, bytes.NewBuffer(closeJsonData))
	if err != nil {
		return fmt.Errorf("failed to create close request: %w", err)
	}

	closeReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.IDToken))
	closeReq.Header.Set("Content-Type", "application/json")

	closeResp, err := client.Do(closeReq)
	if err != nil {
		return fmt.Errorf("failed to close upload: %w", err)
	}
	defer closeResp.Body.Close()

	closeBody, err := io.ReadAll(closeResp.Body)
	if err != nil {
		return fmt.Errorf("failed to read close response: %w", err)
	}

	if closeResp.StatusCode != http.StatusOK && closeResp.StatusCode != http.StatusCreated {
		return fmt.Errorf("failed to close upload (status %d): %s", closeResp.StatusCode, string(closeBody))
	}

	fmt.Printf("Successfully uploaded file to dataset %s\n", datasetID)
	return nil
}
