package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- request builders ---

func TestBuildCodeSearchRequest(t *testing.T) {
	req, err := buildCodeSearchRequest("foo.*bar", []string{"u1"}, []string{"General"}, `src/.*\.jl$`, true, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := json.Marshal(req)
	want := `{"pattern":"foo.*bar","package":["u1"],"registry":["General"],"maxresults":20,"pathfilter":"src/.*\\.jl$","ignorecase":true}`
	if string(got) != want {
		t.Errorf("code request JSON\n got %s\nwant %s", got, want)
	}

	// Optional fields are omitted when unset.
	req, err = buildCodeSearchRequest("x", nil, nil, "", false, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ = json.Marshal(req)
	if string(got) != `{"pattern":"x"}` {
		t.Errorf("minimal code request JSON = %s, want {\"pattern\":\"x\"}", got)
	}

	if _, err := buildCodeSearchRequest("   ", nil, nil, "", false, 0); err == nil {
		t.Error("expected error for blank pattern")
	}
	if _, err := buildCodeSearchRequest("x", nil, nil, "", false, -1); err == nil {
		t.Error("expected error for negative limit")
	}
}

func TestBuildSymbolSearchRequest(t *testing.T) {
	req, err := buildSymbolSearchRequest("DataFrame", nil, nil, []string{"type", "function"}, []string{"define"}, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := json.Marshal(req)
	want := `{"pattern":"DataFrame","maxresults":5,"types":["type","function"],"usages":["define"]}`
	if string(got) != want {
		t.Errorf("symbol request JSON\n got %s\nwant %s", got, want)
	}

	_, err = buildSymbolSearchRequest("x", nil, nil, []string{"struct"}, nil, 0)
	if err == nil || !strings.Contains(err.Error(), `invalid --type "struct"`) || !strings.Contains(err.Error(), "function, type, macro, module") {
		t.Errorf("bad --type: got %v, want usage error listing the valid kinds", err)
	}
	_, err = buildSymbolSearchRequest("x", nil, nil, nil, []string{"call"}, 0)
	if err == nil || !strings.Contains(err.Error(), `invalid --usage "call"`) || !strings.Contains(err.Error(), "define, use") {
		t.Errorf("bad --usage: got %v, want usage error listing the valid kinds", err)
	}
	// Case-sensitive: the server only knows the lowercase spellings.
	if _, err := buildSymbolSearchRequest("x", nil, nil, []string{"Function"}, nil, 0); err == nil {
		t.Error("expected error for capitalized --type")
	}
}

func TestBuildDocsSearchRequest(t *testing.T) {
	th := 0.42
	req, err := buildDocsSearchRequest("join tables", []string{"u1", "u2"}, nil, &th, true, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := json.Marshal(req)
	want := `{"pattern":"join tables","package":["u1","u2"],"maxresults":3,"threshold":0.42,"strictphrase":true}`
	if string(got) != want {
		t.Errorf("docs request JSON\n got %s\nwant %s", got, want)
	}

	// A nil threshold is omitted; an explicit zero is sent.
	req, _ = buildDocsSearchRequest("q", nil, nil, nil, false, 0)
	got, _ = json.Marshal(req)
	if string(got) != `{"pattern":"q"}` {
		t.Errorf("minimal docs request JSON = %s", got)
	}
	zero := 0.0
	req, _ = buildDocsSearchRequest("q", nil, nil, &zero, false, 0)
	got, _ = json.Marshal(req)
	if string(got) != `{"pattern":"q","threshold":0}` {
		t.Errorf("zero-threshold docs request JSON = %s", got)
	}

	if _, err := buildDocsSearchRequest("", nil, nil, nil, false, 0); err == nil {
		t.Error("expected error for empty query")
	}
}

// --- response decoding ---

func TestDecodeSearchResponse(t *testing.T) {
	results, truncated, err := decodeSearchResponse(200, []byte(`{"results":[{"file":"a.jl"}],"truncated":true}`))
	if err != nil {
		t.Fatalf("200: unexpected error: %v", err)
	}
	if !truncated {
		t.Error("200: truncated should be true")
	}
	if string(results) != `[{"file":"a.jl"}]` {
		t.Errorf("200: results = %s", results)
	}

	// Missing/null results normalize to an empty array.
	results, _, err = decodeSearchResponse(200, []byte(`{"results":null,"truncated":false}`))
	if err != nil || string(results) != "[]" {
		t.Errorf("200 null results: got %s, %v; want [], nil", results, err)
	}

	// Structured error body.
	_, _, err = decodeSearchResponse(400, []byte(`{"code":"invalid_pattern","message":"unterminated character class","details":{"pattern":"["}}`))
	var se *searchError
	if !errors.As(err, &se) {
		t.Fatalf("400: expected *searchError, got %T (%v)", err, err)
	}
	if se.Status != 400 || se.Code != "invalid_pattern" || se.Message != "unterminated character class" {
		t.Errorf("400: searchError = %+v", se)
	}
	if got := se.Error(); got != "invalid_pattern: unterminated character class" {
		t.Errorf("400: Error() = %q", got)
	}
	if se.Details == nil {
		t.Error("400: details should be retained")
	}

	// Undecodable body: HTTP <status>: <trimmed body>.
	_, _, err = decodeSearchResponse(500, []byte("  upstream exploded\n"))
	if !errors.As(err, &se) {
		t.Fatalf("500: expected *searchError, got %T", err)
	}
	if se.Code != "" || se.Status != 500 || se.Error() != "HTTP 500: upstream exploded" {
		t.Errorf("500: Error() = %q (%+v)", se.Error(), se)
	}

	// 2xx with a garbage body is a parse error, not a searchError.
	if _, _, err := decodeSearchResponse(200, []byte("not json")); err == nil || errors.As(err, &se) {
		t.Errorf("200 garbage: got %v, want a plain parse error", err)
	}
}

func TestDecodeLegacySearchResponse(t *testing.T) {
	results, truncated, err := decodeLegacySearchResponse([]byte(`{"success":true,"data":[{"file":"b.jl","line":3}]}`))
	if err != nil {
		t.Fatalf("success: unexpected error: %v", err)
	}
	if truncated {
		t.Error("legacy never reports truncation")
	}
	if string(results) != `[{"file":"b.jl","line":3}]` {
		t.Errorf("success: results = %s", results)
	}

	_, _, err = decodeLegacySearchResponse([]byte(`{"success":false,"data":"invalid pattern: missing ]"}`))
	var se *searchError
	if !errors.As(err, &se) {
		t.Fatalf("failure: expected *searchError, got %T (%v)", err, err)
	}
	if se.Code != "legacy_error" || se.Message != "invalid pattern: missing ]" {
		t.Errorf("failure: searchError = %+v", se)
	}
	if se.Error() != "legacy_error: invalid pattern: missing ]" {
		t.Errorf("failure: Error() = %q", se.Error())
	}

	if _, _, err := decodeLegacySearchResponse([]byte("<html>")); err == nil {
		t.Error("expected parse error for non-JSON legacy body")
	}
}

func TestSearchErrorHint(t *testing.T) {
	_, _, err := decodeSearchResponse(400, []byte(`{"code":"invalid_registry","message":"unknown registry \"Genral\"","details":{"known":["General","JuliaSimRegistry"]}}`))
	var se *searchError
	if !errors.As(err, &se) {
		t.Fatalf("expected *searchError, got %T", err)
	}
	if got := se.Hint(); got != "known registries: General, JuliaSimRegistry" {
		t.Errorf("Hint() = %q", got)
	}
	var buf bytes.Buffer
	printSearchError(&buf, err)
	if buf.String() != "invalid_registry: unknown registry \"Genral\"\nknown registries: General, JuliaSimRegistry\n" {
		t.Errorf("printSearchError output = %q", buf.String())
	}

	// No details: no hint, single line.
	buf.Reset()
	printSearchError(&buf, &searchError{Status: 400, Code: "invalid_request", Message: "pattern required"})
	if buf.String() != "invalid_request: pattern required\n" {
		t.Errorf("printSearchError without details = %q", buf.String())
	}
	if (&searchError{Details: map[string]any{"known": []any{}}}).Hint() != "" {
		t.Error("empty known list should give no hint")
	}
}

// --- renderers ---

const codeFixture = `[
  {"file":"src/dataframe.jl","line":42,"text":"  mutable struct DataFrame <: AbstractDataFrame\n","package":"a93c6f00-e57d-5684-b7b6-d8193f3e46c0","registry":"General","registries":["General"],"packagename":"DataFrames"}
]`

const symbolFixture = `[
  {"file":"src/abstractdataframe/join.jl","line":7,"package":"a93c6f00-e57d-5684-b7b6-d8193f3e46c0","registry":"General","registries":["General"],"packagename":"DataFrames","usage":"define","type":"function","text":"function innerjoin(df1::AbstractDataFrame, df2::AbstractDataFrame)"}
]`

const docsFixture = `[
  {"package":"a93c6f00-e57d-5684-b7b6-d8193f3e46c0","packagename":"DataFrames","version":"1.6.1","registry":"General","registries":["General"],"score":0.8123,
   "sections":[{"page":"man/joins.html","title":"Database-Style Joins","category":"manual","docname":"DataFrames"},{"page":"lib/functions.html","title":"Joining","category":"reference","docname":"DataFrames"}]}
]`

func TestRenderCodeResults(t *testing.T) {
	var buf bytes.Buffer
	if err := renderCodeResults(&buf, json.RawMessage(codeFixture)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Found 1 result(s)", "PACKAGE", "FILE:LINE", "DataFrames", "General", "src/dataframe.jl:42", "mutable struct DataFrame <: AbstractDataFrame"} {
		if !strings.Contains(out, want) {
			t.Errorf("code table missing %q:\n%s", want, out)
		}
	}
	// The match text is collapsed onto the row: no embedded newline survives.
	if strings.Count(out, "\n") != 5 {
		t.Errorf("code table should be count line, blank, header, rule and 1 row (5 lines), got:\n%s", out)
	}

	buf.Reset()
	if err := renderCodeResults(&buf, json.RawMessage(`[]`)); err != nil || buf.String() != "No results found\n" {
		t.Errorf("empty code results: %q, %v", buf.String(), err)
	}
	if err := renderCodeResults(&buf, json.RawMessage(`{"not":"an array"}`)); err == nil {
		t.Error("expected error for non-array results")
	}
}

func TestRenderSymbolResults(t *testing.T) {
	var buf bytes.Buffer
	if err := renderSymbolResults(&buf, json.RawMessage(symbolFixture)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"PACKAGE", "TYPE", "USAGE", "DataFrames", "General", "function", "define", "src/abstractdataframe/join.jl:7", "function innerjoin("} {
		if !strings.Contains(out, want) {
			t.Errorf("symbol table missing %q:\n%s", want, out)
		}
	}
	buf.Reset()
	if err := renderSymbolResults(&buf, json.RawMessage(`[]`)); err != nil || buf.String() != "No results found\n" {
		t.Errorf("empty symbol results: %q, %v", buf.String(), err)
	}
}

func TestRenderDocsResults(t *testing.T) {
	var buf bytes.Buffer
	if err := renderDocsResults(&buf, json.RawMessage(docsFixture)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"PACKAGE", "VERSION", "SCORE", "SECTION", "DataFrames", "1.6.1", "General", "0.812", "Database-Style Joins", "(+1 more)"} {
		if !strings.Contains(out, want) {
			t.Errorf("docs table missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Joining") {
		t.Errorf("docs table should show only the first section title:\n%s", out)
	}
	buf.Reset()
	if err := renderDocsResults(&buf, json.RawMessage(`[]`)); err != nil || buf.String() != "No results found\n" {
		t.Errorf("empty docs results: %q, %v", buf.String(), err)
	}
}

func TestWriteJSONResults(t *testing.T) {
	var buf bytes.Buffer
	if err := writeJSONResults(&buf, json.RawMessage(`[{"file":"a.jl","line":1}]`), true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "{\n  \"results\": [\n    {\n      \"file\": \"a.jl\",\n      \"line\": 1\n    }\n  ],\n  \"truncated\": true\n}\n"
	if buf.String() != want {
		t.Errorf("writeJSONResults output:\n%s\nwant:\n%s", buf.String(), want)
	}

	buf.Reset()
	if err := writeJSONResults(&buf, nil, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.String() != "{\n  \"results\": [],\n  \"truncated\": false\n}\n" {
		t.Errorf("empty writeJSONResults output:\n%s", buf.String())
	}
}

func TestWritePackagesJSON(t *testing.T) {
	var buf bytes.Buffer
	pkgs := []packageInfo{{Name: "DataFrames", UUID: "a93c6f00-e57d-5684-b7b6-d8193f3e46c0", Registry: "General", Version: "1.6.1"}}
	if err := writePackagesJSON(&buf, pkgs, 7); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var doc struct {
		Results []map[string]any `json:"results"`
		Total   int              `json:"total"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, buf.String())
	}
	if doc.Total != 7 || len(doc.Results) != 1 {
		t.Errorf("total/results = %d/%d", doc.Total, len(doc.Results))
	}
	r := doc.Results[0]
	for _, key := range []string{"name", "uuid", "owner", "registry", "version", "description", "source_url", "tags", "stars", "docs_url", "license", "is_app", "score", "status"} {
		if _, ok := r[key]; !ok {
			t.Errorf("results[0] missing key %q", key)
		}
	}
	if r["name"] != "DataFrames" {
		t.Errorf("results[0].name = %v", r["name"])
	}
	if tags, ok := r["tags"].([]any); !ok || len(tags) != 0 {
		t.Errorf("nil tags should serialize as an empty array, got %v", r["tags"])
	}
	if !strings.HasPrefix(buf.String(), "{\n  \"results\": [\n") || !strings.HasSuffix(buf.String(), "\n}\n") {
		t.Errorf("expected 2-space indentation with trailing newline:\n%s", buf.String())
	}
}

// --- package resolution helpers ---

func TestIsUUID(t *testing.T) {
	yes := []string{"a93c6f00-e57d-5684-b7b6-d8193f3e46c0", "A93C6F00-E57D-5684-B7B6-D8193F3E46C0"}
	no := []string{"DataFrames", "a93c6f00e57d5684b7b6d8193f3e46c0", "a93c6f00-e57d-5684-b7b6-d8193f3e46c", "a93c6f00-e57d-5684-b7b6-d8193f3e46c0x", ""}
	for _, s := range yes {
		if !isUUID(s) {
			t.Errorf("isUUID(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if isUUID(s) {
			t.Errorf("isUUID(%q) = true, want false", s)
		}
	}
}

func TestPackageUUIDsByName(t *testing.T) {
	pkgs := []RESTPackage{
		{Name: "Foo", UUID: "u-foo", Registry: "General"},
		{Name: "Foo", UUID: "u-foo", Registry: "Private"}, // same package, second registry
		{Name: "foo", UUID: "u-foo2", Registry: "Other"},  // distinct package sharing the name
		{Name: "FooBar", UUID: "u-foobar", Registry: "General"},
	}
	got := packageUUIDsByName(pkgs, "foo")
	want := []string{"u-foo", "u-foo2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("packageUUIDsByName = %v, want %v", got, want)
	}
	if got := packageUUIDsByName(pkgs, "Nope"); len(got) != 0 {
		t.Errorf("no match should give no UUIDs, got %v", got)
	}
}

func TestDedupeStrings(t *testing.T) {
	got := dedupeStrings([]string{"a", "b", "a", "c", "b"})
	if strings.Join(got, ",") != "a,b,c" {
		t.Errorf("dedupeStrings = %v", got)
	}
	if got := dedupeStrings(nil); got != nil {
		t.Errorf("dedupeStrings(nil) = %v, want nil", got)
	}
}

func TestResolveSearchPackagesUUIDsOnly(t *testing.T) {
	// UUIDs pass through (lower-cased, deduplicated) without any network call.
	got, err := resolveSearchPackages("unused", []string{"A93C6F00-E57D-5684-B7B6-D8193F3E46C0", " a93c6f00-e57d-5684-b7b6-d8193f3e46c0 ", ""}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "a93c6f00-e57d-5684-b7b6-d8193f3e46c0" {
		t.Errorf("resolveSearchPackages = %v", got)
	}
	// Against a local http:// service names cannot be resolved.
	_, err = resolveSearchPackages("localhost:4446", []string{"DataFrames"}, true)
	if err == nil || !strings.Contains(err.Error(), "UUID") {
		t.Errorf("local name resolution: got %v, want an error asking for the UUID", err)
	}
}

func TestSearchBaseURL(t *testing.T) {
	cases := map[string]string{
		"juliahub":               "https://juliahub.com",
		"foo":                    "https://foo.juliahub.com",
		"foo.dev":                "https://foo.dev",
		"http://localhost:4446/": "http://localhost:4446",
		"https://x.example.com":  "https://x.example.com",
	}
	for in, want := range cases {
		if got := searchBaseURL(in); got != want {
			t.Errorf("searchBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
	if !isLocalSearchBaseURL("http://localhost:4446") || isLocalSearchBaseURL("https://juliahub.com") {
		t.Error("isLocalSearchBaseURL should be true only for http://")
	}
}

// --- HTTP: v1 → legacy fallback ---

// searchTestServer serves v1 and legacy routes from handler maps keyed by path.
func searchTestServer(t *testing.T, handlers map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, h := range handlers {
		mux.HandleFunc(path, h)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestPostSearchFallsBackToLegacy(t *testing.T) {
	var v1Hits, legacyHits int
	var legacyBody []byte
	var legacyAuth string
	srv := searchTestServer(t, map[string]http.HandlerFunc{
		"/search/v1/code": func(w http.ResponseWriter, r *http.Request) {
			v1Hits++
			http.NotFound(w, r)
		},
		"/search/code": func(w http.ResponseWriter, r *http.Request) {
			legacyHits++
			legacyAuth = r.Header.Get("Authorization")
			legacyBody, _ = readAll(r)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"success":true,"data":[{"file":"legacy.jl","line":1,"text":"x"}]}`))
		},
	})

	body, _ := buildCodeSearchRequest("x", nil, nil, "", false, 0)
	results, truncated, err := postSearchWithToken(srv.Client(), srv.URL, searchCode, "tok", body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v1Hits != 1 || legacyHits != 1 {
		t.Errorf("hits v1=%d legacy=%d, want 1/1", v1Hits, legacyHits)
	}
	if truncated {
		t.Error("legacy result must not be truncated")
	}
	if string(results) != `[{"file":"legacy.jl","line":1,"text":"x"}]` {
		t.Errorf("results = %s", results)
	}
	if legacyAuth != "Bearer tok" {
		t.Errorf("legacy Authorization = %q", legacyAuth)
	}
	if string(legacyBody) != `{"pattern":"x"}` {
		t.Errorf("legacy request body = %s", legacyBody)
	}
}

// TestPostSearchV1StructuredNotFoundIsNotAFallback: a 404 whose body is a v1
// {code, message} error came from the search API, not from a web server
// lacking the route, so the legacy route must not be tried.
func TestPostSearchV1StructuredNotFoundIsNotAFallback(t *testing.T) {
	var legacyHits int
	srv := searchTestServer(t, map[string]http.HandlerFunc{
		"/search/v1/code": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"code":"package_not_found","message":"no such package"}`))
		},
		"/search/code": func(w http.ResponseWriter, r *http.Request) {
			legacyHits++
			w.Write([]byte(`{"success":true,"data":[]}`))
		},
	})

	body, _ := buildCodeSearchRequest("x", nil, nil, "", false, 0)
	_, _, err := postSearchWithToken(srv.Client(), srv.URL, searchCode, "tok", body)
	var se *searchError
	if !errors.As(err, &se) || se.Code != "package_not_found" || se.Status != http.StatusNotFound {
		t.Fatalf("err = %v, want the structured 404 error", err)
	}
	if legacyHits != 0 {
		t.Errorf("legacy route was tried %d time(s) on a structured v1 404", legacyHits)
	}
}

func TestSearchTokenAllowed(t *testing.T) {
	cases := map[string]bool{
		"https://juliahub.com":     true,
		"https://some-host:8443":   true,
		"http://localhost:4446":    true,
		"http://127.0.0.1:4446":    true,
		"http://[::1]:4446":        true,
		"http://some-host:4446":    false,
		"http://192.168.1.10:4446": false,
		"http://juliahub.com":      false,
		"ftp://localhost":          false,
		"://bad":                   false,
	}
	for base, want := range cases {
		if got := searchTokenAllowed(base); got != want {
			t.Errorf("searchTokenAllowed(%q) = %v, want %v", base, got, want)
		}
	}
}

func TestPostSearchLegacyFailureEnvelope(t *testing.T) {
	srv := searchTestServer(t, map[string]http.HandlerFunc{
		"/search/v1/sym": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusMethodNotAllowed)
		},
		"/search/sym": func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"success":false,"data":"invalid pattern"}`))
		},
	})
	_, _, err := postSearchWithToken(srv.Client(), srv.URL, searchSym, "tok", map[string]string{"pattern": "["})
	var se *searchError
	if !errors.As(err, &se) || se.Code != "legacy_error" || se.Message != "invalid pattern" {
		t.Errorf("got %v (%T), want legacy_error: invalid pattern", err, err)
	}
}

func TestPostSearchV1StructuredError(t *testing.T) {
	legacyHit := false
	srv := searchTestServer(t, map[string]http.HandlerFunc{
		"/search/v1/code": func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer tok" {
				t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
			}
			if r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"code":"invalid_pattern","message":"missing closing ]"}`))
		},
		"/search/code": func(w http.ResponseWriter, r *http.Request) {
			legacyHit = true
		},
	})
	_, _, err := postSearchWithToken(srv.Client(), srv.URL, searchCode, "tok", map[string]string{"pattern": "["})
	var se *searchError
	if !errors.As(err, &se) || se.Status != 400 || se.Code != "invalid_pattern" {
		t.Errorf("got %v, want invalid_pattern searchError", err)
	}
	if legacyHit {
		t.Error("a 400 from v1 must not trigger the legacy fallback")
	}
}

func TestPostSearchV1Success(t *testing.T) {
	srv := searchTestServer(t, map[string]http.HandlerFunc{
		"/search/v1/docs": func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"results":[{"packagename":"DataFrames"}],"truncated":true}`))
		},
	})
	results, truncated, err := postSearchWithToken(srv.Client(), srv.URL, searchDocs, "", map[string]string{"pattern": "q"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !truncated || string(results) != `[{"packagename":"DataFrames"}]` {
		t.Errorf("results=%s truncated=%v", results, truncated)
	}
}

func TestPostSearchUnavailableOnBothRoutes(t *testing.T) {
	srv := searchTestServer(t, map[string]http.HandlerFunc{}) // everything 404s
	_, _, err := postSearchWithToken(srv.Client(), srv.URL, searchCode, "tok", map[string]string{"pattern": "x"})
	var se *searchError
	if !errors.As(err, &se) || se.Code != "search_unavailable" {
		t.Errorf("got %v, want search_unavailable", err)
	}
	if !strings.Contains(err.Error(), "/search/v1/code") || !strings.Contains(err.Error(), "/search/code") {
		t.Errorf("error should name both routes: %v", err)
	}
}

func TestDoSearchRequestOmitsAuthWithoutToken(t *testing.T) {
	var auth string
	var hadAuth bool
	srv := searchTestServer(t, map[string]http.HandlerFunc{
		"/search/v1/code": func(w http.ResponseWriter, r *http.Request) {
			auth, hadAuth = r.Header.Get("Authorization"), r.Header.Values("Authorization") != nil
			w.Write([]byte(`{"results":[],"truncated":false}`))
		},
	})
	status, body, err := doSearchRequest(srv.Client(), srv.URL, "/search/v1/code", "", []byte(`{"pattern":"x"}`))
	if err != nil || status != 200 {
		t.Fatalf("status=%d err=%v", status, err)
	}
	if hadAuth || auth != "" {
		t.Errorf("no token should mean no Authorization header, got %q", auth)
	}
	if string(body) != `{"results":[],"truncated":false}` {
		t.Errorf("body = %s", body)
	}
}

// readAll drains a request body for assertions.
func readAll(r *http.Request) ([]byte, error) {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(r.Body)
	return buf.Bytes(), err
}

func TestPackageUUIDsByNameGQL(t *testing.T) {
	pkgs := []Package{{Name: "Foo", UUID: "u1"}, {Name: "foo", UUID: "u1"}, {Name: "Bar", UUID: "u2"}, {Name: "Foo", UUID: ""}}
	got := packageUUIDsByNameGQL(pkgs, "FOO")
	if len(got) != 1 || got[0] != "u1" {
		t.Errorf("got %v, want [u1]", got)
	}
}
