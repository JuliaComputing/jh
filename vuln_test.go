package main

import (
	"strings"
	"testing"
	"time"
)

func TestTopSeverity(t *testing.T) {
	tests := []struct {
		name   string
		scores []SeverityScore
		want   string
	}{
		{"none", nil, "N/A"},
		{"highest CVSS_V3", []SeverityScore{
			{Type: "CVSS_V3", Score: "5.5"},
			{Type: "CVSS_V3", Score: "9.1"},
			{Type: "CVSS_V3", Score: "7.0"},
		}, "9.1"},
		{"ignores non-v3 when v3 present", []SeverityScore{
			{Type: "CVSS_V2", Score: "10.0"},
			{Type: "CVSS_V3.1", Score: "6.4"},
		}, "6.4"},
		{"falls back to first when no parseable v3", []SeverityScore{
			{Type: "CVSS_V2", Score: "4.3"},
		}, "4.3"},
		{"non-numeric v3 falls back to that string", []SeverityScore{
			{Type: "CVSS_V3", Score: "HIGH"},
		}, "HIGH"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := topSeverity(tt.scores); got != tt.want {
				t.Errorf("topSeverity(%v) = %q, want %q", tt.scores, got, tt.want)
			}
		})
	}
}

func TestAdvisoryLink(t *testing.T) {
	published := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	v := &PackageVulnerability{AdvisoryID: "JLSEC-2025-232", Published: &published}
	link := advisoryLink(v)

	// OSC8 hyperlink sequence and the advisory id must be present.
	if !strings.Contains(link, "\033]8;;") {
		t.Errorf("advisoryLink missing OSC8 escape: %q", link)
	}
	if !strings.Contains(link, "JLSEC-2025-232") {
		t.Errorf("advisoryLink missing advisory id: %q", link)
	}
	if !strings.Contains(link, "/published/2025/JLSEC-2025-232.md") {
		t.Errorf("advisoryLink should embed the published year in the URL: %q", link)
	}

	// With no published date the URL uses the "unknown" year bucket.
	noDate := &PackageVulnerability{AdvisoryID: "JLSEC-0000-000"}
	if !strings.Contains(advisoryLink(noDate), "/published/unknown/") {
		t.Errorf("advisoryLink without date should use unknown year: %q", advisoryLink(noDate))
	}
}
