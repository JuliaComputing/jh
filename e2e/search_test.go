//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// `jh search` — code/symbols/docs against the search service, plus `search
// packages` (the package catalogue search under the new group). The search
// service is not present on every instance: v1 (`/search/v1/*`) and the legacy
// routes (`/search/*`) may both be absent, which the CLI reports as
// `search_unavailable`; that, and the usual permission/5xx markers, are
// backend gaps and skip. When the service answers, the --json document must
// parse into the documented shape.

// searchGap reports whether a failed search is a backend gap rather than a CLI
// defect: the service is missing on the instance, or metadata is not synced.
func searchGap(out string) bool {
	low := strings.ToLower(out)
	return backendGap(out) ||
		strings.Contains(low, "search_unavailable") ||
		strings.Contains(low, "metadata_unavailable") ||
		strings.Contains(low, "search_failed")
}

// searchDoc is the --json document of `search code|symbols|docs`.
type searchDoc struct {
	Results   []map[string]any `json:"results"`
	Truncated *bool            `json:"truncated"`
}

func parseSearchDoc(t *testing.T, res result) searchDoc {
	t.Helper()
	var doc searchDoc
	if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil {
		t.Fatalf("--json stdout is not the {results, truncated} document: %v\nstdout:\n%s", err, truncate(res.stdout))
	}
	if doc.Results == nil {
		t.Errorf("--json document has no \"results\" array:\n%s", truncate(res.stdout))
	}
	if doc.Truncated == nil {
		t.Errorf("--json document has no \"truncated\" field:\n%s", truncate(res.stdout))
	}
	return doc
}

// runSearchOrSkip runs a search command and skips on a backend gap; any other
// non-zero exit is a failure.
func runSearchOrSkip(t *testing.T, args ...string) result {
	t.Helper()
	res := runJH(t, args...)
	if res.exitCode != 0 {
		if searchGap(res.combined()) {
			t.Skipf("search unavailable on this instance: %s", errorLine(res.combined()))
		}
		skipIfUnsupported(t, res)
		t.Fatalf("jh %s exited %d\nstderr: %s", strings.Join(args, " "), res.exitCode, res.stderr)
	}
	return res
}

func TestSearchPackagesJSON(t *testing.T) {
	requireCreds(t)
	res := runJH(t, "search", "packages", canaryPackage, "--json")
	if res.exitCode != 0 || backendGap(res.combined()) {
		skipIfUnsupported(t, res)
		t.Skipf("package search unavailable on this instance: %s", errorLine(res.combined()))
	}
	var doc struct {
		Results []struct {
			Name string `json:"name"`
			UUID string `json:"uuid"`
		} `json:"results"`
		Total *int `json:"total"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil {
		t.Fatalf("--json stdout is not the {results, total} document: %v\nstdout:\n%s", err, truncate(res.stdout))
	}
	if doc.Total == nil {
		t.Errorf("--json document has no \"total\":\n%s", truncate(res.stdout))
	}
	if len(doc.Results) == 0 {
		t.Skip("no packages indexed on this instance")
	}
	if doc.Results[0].Name == "" {
		t.Errorf("results[0].name is empty:\n%s", truncate(res.stdout))
	}
}

func TestSearchSymbolsJSON(t *testing.T) {
	requireCreds(t)
	res := runSearchOrSkip(t, "search", "symbols", canaryPackage, "--limit", "5", "--json")
	parseSearchDoc(t, res)
}

func TestSearchCodeJSON(t *testing.T) {
	requireCreds(t)
	res := runSearchOrSkip(t, "search", "code", "function", "--limit", "3", "--json")
	doc := parseSearchDoc(t, res)
	if len(doc.Results) > 3 {
		t.Errorf("--limit 3 returned %d results", len(doc.Results))
	}
}

func TestSearchDocsJSON(t *testing.T) {
	requireCreds(t)
	res := runSearchOrSkip(t, "search", "docs", "data frame", "--limit", "3", "--json")
	parseSearchDoc(t, res)
}

// TestSearchCodeInvalidPattern verifies a malformed regular expression is
// rejected with exit 1 and the server's stable error code (invalid_pattern) —
// or, on an install with only the legacy routes, its "invalid pattern" text.
func TestSearchCodeInvalidPattern(t *testing.T) {
	requireCreds(t)
	res := runJH(t, "search", "code", "[")
	if res.exitCode == 0 {
		t.Fatalf("expected exit 1 for an invalid pattern, got 0:\n%s", truncate(res.combined()))
	}
	if searchGap(res.combined()) {
		t.Skipf("search unavailable on this instance: %s", errorLine(res.combined()))
	}
	skipIfUnsupported(t, res)
	low := strings.ToLower(res.stderr)
	if !strings.Contains(low, "invalid_pattern") && !strings.Contains(low, "invalid pattern") {
		t.Errorf("stderr should name the invalid pattern:\n%s", truncate(res.stderr))
	}
	if strings.TrimSpace(res.stdout) != "" {
		t.Errorf("error output must go to stderr, stdout was:\n%s", truncate(res.stdout))
	}
}

// TestSearchSymbolsRejectsBadType is client-side validation: no network call.
func TestSearchSymbolsRejectsBadType(t *testing.T) {
	res := runJH(t, "search", "symbols", "Foo", "--type", "struct")
	if res.exitCode == 0 {
		t.Fatalf("expected non-zero exit for --type struct, got 0:\n%s", truncate(res.combined()))
	}
	assertContains(t, res.stderr, "invalid --type")
	assertContains(t, res.stderr, "function, type, macro, module")
}
