package main

import (
	"encoding/json"
	"fmt"
	"net/url"
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

// topSeverity returns the highest CVSS_V3 score string, or the first available, or "N/A".
func topSeverity(scores []SeverityScore) string {
	for _, s := range scores {
		if strings.HasPrefix(s.Type, "CVSS_V3") && s.Score != "" {
			return s.Score
		}
	}
	if len(scores) > 0 && scores[0].Score != "" {
		return scores[0].Score
	}
	return "N/A"
}

func affectedLabel(v *PackageVulnerability, versionQueried bool) string {
	if !versionQueried {
		return "-"
	}
	if v.IsAffected == nil {
		return "Unknown"
	}
	if *v.IsAffected {
		return "Yes"
	}
	return "No"
}

func printVersionsForAdvisory(packageName string, v *PackageVulnerability) {
	fmt.Printf("Package:  %s\n", packageName)
	fmt.Printf("Advisory: %s\n", v.AdvisoryID)
	if len(v.Aliases) > 0 {
		fmt.Printf("Aliases:  %s\n", strings.Join(v.Aliases, ", "))
	}
	if v.Summary != "" {
		fmt.Printf("Summary:  %s\n", v.Summary)
	}
	if v.Details != "" {
		fmt.Printf("Details:\n  %s\n", strings.ReplaceAll(strings.TrimSpace(v.Details), "\n", "\n  "))
	}
	fmt.Println()

	if len(v.AffectedVersions) > 0 {
		fmt.Println("Affected versions:")
		for _, av := range v.AffectedVersions {
			fmt.Printf("  %s\n", av)
		}
		fmt.Println()
	}

	if len(v.RangeEvents) > 0 {
		rangeLabel := "Version ranges"
		if v.RangesType != "" {
			rangeLabel += fmt.Sprintf(" (%s)", v.RangesType)
		}
		fmt.Printf("%s:\n", rangeLabel)
		for _, re := range v.RangeEvents {
			fmt.Printf("  %-16s %s\n", re.EventType+":", re.Version)
		}
		fmt.Println()
	}

	if len(v.AffectedVersions) == 0 && len(v.RangeEvents) == 0 {
		fmt.Println("No version information available for this advisory.")
	}
}

func printVulnerabilities(packageName, version string, vulns []PackageVulnerability, verbose bool) {
	versionQueried := version != ""

	header := fmt.Sprintf("Package: %s", packageName)
	if versionQueried {
		header += fmt.Sprintf(" (version: %s)", version)
	}
	fmt.Println(header)
	fmt.Println()

	if len(vulns) == 0 {
		fmt.Println("No vulnerabilities found.")
		return
	}

	count := len(vulns)
	suffix := "ies"
	if count == 1 {
		suffix = "y"
	}
	fmt.Printf("Found %d vulnerabilit%s:\n\n", count, suffix)

	if verbose {
		for i, v := range vulns {
			if i > 0 {
				fmt.Println(strings.Repeat("-", 60))
			}
			fmt.Printf("Advisory:  %s\n", v.AdvisoryID)
			if versionQueried {
				fmt.Printf("Affected:  %s\n", affectedLabel(&v, versionQueried))
			}
			if v.Summary != "" {
				fmt.Printf("Summary:   %s\n", v.Summary)
			}
			if len(v.Aliases) > 0 {
				fmt.Printf("Aliases:   %s\n", strings.Join(v.Aliases, ", "))
			}
			if len(v.SeverityScores) > 0 {
				parts := make([]string, len(v.SeverityScores))
				for i, s := range v.SeverityScores {
					parts[i] = fmt.Sprintf("%s: %s", s.Type, s.Score)
				}
				fmt.Printf("Severity:  %s\n", strings.Join(parts, ", "))
			}
			if v.Published != nil {
				fmt.Printf("Published: %s\n", v.Published.Local().Format("2006-01-02 15:04:05 MST"))
			}
			if v.Modified != nil {
				fmt.Printf("Modified:  %s\n", v.Modified.Local().Format("2006-01-02 15:04:05 MST"))
			}
			if len(v.AffectedVersions) > 0 {
				fmt.Printf("Affected Versions: %s\n", strings.Join(v.AffectedVersions, ", "))
			}
			if len(v.RangeEvents) > 0 {
				rangeLabel := "Version Ranges"
				if v.RangesType != "" {
					rangeLabel += fmt.Sprintf(" (%s)", v.RangesType)
				}
				fmt.Printf("%s:\n", rangeLabel)
				for _, re := range v.RangeEvents {
					fmt.Printf("  %-16s %s\n", re.EventType+":", re.Version)
				}
			}
			if v.Details != "" {
				fmt.Printf("Details:\n  %s\n", strings.ReplaceAll(strings.TrimSpace(v.Details), "\n", "\n  "))
			}
			if len(v.References) > 0 {
				fmt.Printf("References:\n")
				for _, ref := range v.References {
					fmt.Printf("  - %s\n", ref)
				}
			}
			fmt.Println()
		}
		return
	}

	// Concise table
	const (
		colAdvisory = 22
		colSeverity = 10
		colAffected = 10
		colAliases  = 30
		colSummary  = 50
	)

	fmt.Printf("%-*s %-*s %-*s %-*s %s\n",
		colAdvisory, "ADVISORY",
		colSeverity, "SEVERITY",
		colAffected, "AFFECTED",
		colAliases, "ALIASES",
		"SUMMARY")
	fmt.Printf("%-*s %-*s %-*s %-*s %s\n",
		colAdvisory, strings.Repeat("-", colAdvisory),
		colSeverity, strings.Repeat("-", colSeverity),
		colAffected, strings.Repeat("-", colAffected),
		colAliases, strings.Repeat("-", colAliases),
		strings.Repeat("-", colSummary))

	for _, v := range vulns {
		severity := topSeverity(v.SeverityScores)

		aliases := strings.Join(v.Aliases, ", ")
		if len(aliases) > colAliases {
			aliases = aliases[:colAliases-3] + "..."
		}

		summary := v.Summary
		if len(summary) > colSummary {
			summary = summary[:colSummary-3] + "..."
		}

		fmt.Printf("%-*s %-*s %-*s %-*s %s\n",
			colAdvisory, v.AdvisoryID,
			colSeverity, severity,
			colAffected, affectedLabel(&v, versionQueried),
			colAliases, aliases,
			summary)
	}
}
