package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type SeverityScore struct {
	Type  string `json:"type"`
	Score string `json:"score"`
}

type RangeEvent struct {
	EventType string `json:"event_type"`
	Version   string `json:"version"`
}

type PackageVulnerability struct {
	AdvisoryID       string          `json:"advisory_id"`
	Summary          string          `json:"summary"`
	Details          string          `json:"details"`
	Published        *time.Time      `json:"published"`
	Modified         *time.Time      `json:"modified"`
	SeverityScores   []SeverityScore `json:"severity_scores"`
	Aliases          []string        `json:"aliases"`
	References       []string        `json:"references"`
	AffectedVersions []string        `json:"affected_versions"`
	RangesType       string          `json:"ranges_type"`
	RangeEvents      []RangeEvent    `json:"range_events"`
	IsAffected       *bool           `json:"is_affected"`
}

func fetchVulnerabilities(server, packageName, version string) ([]PackageVulnerability, error) {
	token, err := ensureValidToken()
	if err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}

	endpoint := fmt.Sprintf("https://%s/api/v1/ui/vulnerabilities/packages/%s",
		server, url.PathEscape(packageName))
	if version != "" {
		endpoint += "?version=" + url.QueryEscape(version)
	}

	body, err := apiGet(endpoint, token.IDToken)
	if err != nil {
		return nil, err
	}

	var vulns []PackageVulnerability
	if err := json.Unmarshal(body, &vulns); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return vulns, nil
}

func fetchLatestVersion(server, registry, packageName string) (string, error) {
	token, err := ensureValidToken()
	if err != nil {
		return "", fmt.Errorf("authentication required: %w", err)
	}

	endpoint := fmt.Sprintf("https://%s/docs/%s/%s/versions.json", server, url.PathEscape(registry), url.PathEscape(packageName))
	body, err := apiGet(endpoint, token.IDToken)
	if err != nil {
		return "", err
	}

	var versions []string
	if err := json.Unmarshal(body, &versions); err != nil {
		return "", fmt.Errorf("failed to parse versions: %w", err)
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("no versions found for %s", packageName)
	}
	return versions[0], nil
}

// topSeverity returns the highest numeric CVSS_V3 score, or the first available, or "N/A".
func topSeverity(scores []SeverityScore) string {
	bestScore := ""
	bestValue := -1.0
	for _, s := range scores {
		if !strings.HasPrefix(s.Type, "CVSS_V3") || s.Score == "" {
			continue
		}
		v, err := strconv.ParseFloat(s.Score, 64)
		if err != nil {
			if bestScore == "" {
				bestScore = s.Score
			}
			continue
		}
		if v > bestValue {
			bestValue = v
			bestScore = s.Score
		}
	}
	if bestScore != "" {
		return bestScore
	}
	if len(scores) > 0 && scores[0].Score != "" {
		return scores[0].Score
	}
	return "N/A"
}

func advisoryLink(v *PackageVulnerability) string {
	year := "unknown"
	if v.Published != nil {
		year = fmt.Sprintf("%d", v.Published.Year())
	}
	url := fmt.Sprintf("https://github.com/JuliaLang/SecurityAdvisories.jl/blob/main/advisories/published/%s/%s.md", year, v.AdvisoryID)
	return fmt.Sprintf("\033]8;;%s\033\\%s\033]8;;\033\\", url, v.AdvisoryID)
}

func printAdvisory(v *PackageVulnerability, verbose bool) {
	fmt.Printf("Advisory: %s\n", advisoryLink(v))
	if v.IsAffected != nil {
		if *v.IsAffected {
			fmt.Println("Affected: Yes")
		} else {
			fmt.Println("Affected: No")
		}
	}
	if len(v.SeverityScores) > 0 {
		parts := make([]string, len(v.SeverityScores))
		for i, s := range v.SeverityScores {
			parts[i] = s.Type + ": " + s.Score
		}
		fmt.Printf("Severity: %s\n", strings.Join(parts, ", "))
	}
	if v.Summary != "" {
		fmt.Printf("Summary:  %s\n", v.Summary)
	}
	if len(v.AffectedVersions) > 0 {
		fmt.Printf("Affected versions: %s\n", strings.Join(v.AffectedVersions, ", "))
	}
	if len(v.RangeEvents) > 0 {
		parts := make([]string, len(v.RangeEvents))
		for i, re := range v.RangeEvents {
			parts[i] = re.EventType + ": " + re.Version
		}
		rangeLabel := "Version ranges"
		if v.RangesType != "" {
			rangeLabel += fmt.Sprintf(" (%s)", v.RangesType)
		}
		fmt.Printf("%s: %s\n", rangeLabel, strings.Join(parts, ", "))
	}
	if verbose {
		if len(v.Aliases) > 0 {
			fmt.Printf("Aliases:  %s\n", strings.Join(v.Aliases, ", "))
		}
		if v.Published != nil {
			fmt.Printf("Published: %s\n", v.Published.Format("2006-01-02"))
		}
		if v.Modified != nil {
			fmt.Printf("Modified:  %s\n", v.Modified.Format("2006-01-02"))
		}
		if len(v.References) > 0 {
			fmt.Println("References:")
			for _, r := range v.References {
				fmt.Printf("  %s\n", r)
			}
		}
	}
}
