package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
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
	th, yes, no := 0.42, true, false
	req, err := buildDocsSearchRequest("join tables", []string{"u1", "u2"}, nil, &th, &yes, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := json.Marshal(req)
	want := `{"pattern":"join tables","package":["u1","u2"],"maxresults":3,"threshold":0.42,"strictphrase":true}`
	if string(got) != want {
		t.Errorf("docs request JSON\n got %s\nwant %s", got, want)
	}

	// A nil threshold / strictphrase is omitted (server default); explicit
	// values, zero and false included, are sent.
	req, _ = buildDocsSearchRequest("q", nil, nil, nil, nil, 0)
	got, _ = json.Marshal(req)
	if string(got) != `{"pattern":"q"}` {
		t.Errorf("minimal docs request JSON = %s", got)
	}
	zero := 0.0
	req, _ = buildDocsSearchRequest("q", nil, nil, &zero, nil, 0)
	got, _ = json.Marshal(req)
	if string(got) != `{"pattern":"q","threshold":0}` {
		t.Errorf("zero-threshold docs request JSON = %s", got)
	}

	req, _ = buildDocsSearchRequest("q", nil, nil, nil, &no, 0)
	got, _ = json.Marshal(req)
	if string(got) != `{"pattern":"q","strictphrase":false}` {
		t.Errorf("strictphrase=false docs request JSON = %s", got)
	}

	if _, err := buildDocsSearchRequest("", nil, nil, nil, nil, 0); err == nil {
		t.Error("expected error for empty query")
	}
}

// --- response decoding ---

func TestDecodeSearchResponse(t *testing.T) {
	resp, err := decodeSearchResponse(200, []byte(`{"results":[{"file":"a.jl"}],"truncated":true,"unavailable":["General","JuliaSimRegistry"]}`))
	if err != nil {
		t.Fatalf("200: unexpected error: %v", err)
	}
	if !resp.Truncated {
		t.Error("200: truncated should be true")
	}
	if string(resp.Results) != `[{"file":"a.jl"}]` {
		t.Errorf("200: results = %s", resp.Results)
	}
	if !reflect.DeepEqual(resp.Unavailable, []string{"General", "JuliaSimRegistry"}) {
		t.Errorf("200: unavailable = %v", resp.Unavailable)
	}

	resp, err = decodeSearchResponse(200, []byte(`{"results":[],"truncated":false}`))
	if err != nil {
		t.Fatalf("200 without unavailable: unexpected error: %v", err)
	}
	if resp.Unavailable != nil {
		t.Errorf("200 without unavailable: got %v, want nil", resp.Unavailable)
	}

	// Missing/null results normalize to an empty array.
	resp, err = decodeSearchResponse(200, []byte(`{"results":null,"truncated":false}`))
	if err != nil || string(resp.Results) != "[]" {
		t.Errorf("200 null results: got %s, %v; want [], nil", resp.Results, err)
	}

	// Structured error body.
	_, err = decodeSearchResponse(400, []byte(`{"code":"invalid_pattern","message":"unterminated character class","details":{"pattern":"["}}`))
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
	_, err = decodeSearchResponse(500, []byte("  upstream exploded\n"))
	if !errors.As(err, &se) {
		t.Fatalf("500: expected *searchError, got %T", err)
	}
	if se.Code != "" || se.Status != 500 || se.Error() != "HTTP 500: upstream exploded" {
		t.Errorf("500: Error() = %q (%+v)", se.Error(), se)
	}

	// 2xx with a garbage body is a parse error, not a searchError.
	if _, err := decodeSearchResponse(200, []byte("not json")); err == nil || errors.As(err, &se) {
		t.Errorf("200 garbage: got %v, want a plain parse error", err)
	}
}

func TestDecodeLegacySearchResponse(t *testing.T) {
	resp, err := decodeLegacySearchResponse([]byte(`{"success":true,"data":[{"file":"b.jl","line":3}]}`))
	if err != nil {
		t.Fatalf("success: unexpected error: %v", err)
	}
	if resp.Truncated {
		t.Error("legacy never reports truncation")
	}
	if string(resp.Results) != `[{"file":"b.jl","line":3}]` {
		t.Errorf("success: results = %s", resp.Results)
	}
	if resp.Unavailable != nil {
		t.Errorf("legacy unavailable = %v, want nil", resp.Unavailable)
	}

	_, err = decodeLegacySearchResponse([]byte(`{"success":false,"data":"invalid pattern: missing ]"}`))
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

	if _, err := decodeLegacySearchResponse([]byte("<html>")); err == nil {
		t.Error("expected parse error for non-JSON legacy body")
	}
}

func TestSearchErrorHint(t *testing.T) {
	_, err := decodeSearchResponse(400, []byte(`{"code":"invalid_registry","message":"unknown registry \"Genral\"","details":{"known":["General","JuliaSimRegistry"]}}`))
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
	if err := writeJSONResults(&buf, searchResponse{Results: json.RawMessage(`[{"file":"a.jl","line":1}]`), Truncated: true}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "{\n  \"results\": [\n    {\n      \"file\": \"a.jl\",\n      \"line\": 1\n    }\n  ],\n  \"truncated\": true\n}\n"
	if buf.String() != want {
		t.Errorf("writeJSONResults output:\n%s\nwant:\n%s", buf.String(), want)
	}

	buf.Reset()
	if err := writeJSONResults(&buf, searchResponse{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.String() != "{\n  \"results\": [],\n  \"truncated\": false\n}\n" {
		t.Errorf("empty writeJSONResults output:\n%s", buf.String())
	}

	buf.Reset()
	if err := writeJSONResults(&buf, searchResponse{
		Results:     json.RawMessage("[]"),
		Truncated:   true,
		Unavailable: []string{"General", "JuliaSimRegistry"},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want = "{\n  \"results\": [],\n  \"truncated\": true,\n  \"unavailable\": [\n    \"General\",\n    \"JuliaSimRegistry\"\n  ]\n}\n"
	if buf.String() != want {
		t.Errorf("writeJSONResults with unavailable:\n%s\nwant:\n%s", buf.String(), want)
	}

	buf.Reset()
	if err := writeJSONResults(&buf, searchResponse{Results: json.RawMessage("[]"), Unavailable: []string{}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(buf.String(), `"unavailable"`) {
		t.Errorf("empty unavailable key should be omitted:\n%s", buf.String())
	}
}

func TestJoinNames(t *testing.T) {
	cases := []struct {
		names []string
		want  string
	}{
		{[]string{"A"}, "A"},
		{[]string{"A", "B"}, "A and B"},
		{[]string{"A", "B", "C"}, "A, B and C"},
	}
	for _, tc := range cases {
		if got := joinNames(tc.names); got != tc.want {
			t.Errorf("joinNames(%v) = %q, want %q", tc.names, got, tc.want)
		}
	}
}

func TestUnavailableNote(t *testing.T) {
	cases := []struct {
		names []string
		want  string
	}{
		{
			names: []string{"General"},
			want:  "note: results from General are missing: the service it is searched through could not be reached; try again later",
		},
		{
			names: []string{"A", "B", "C"},
			want:  "note: results from A, B and C are missing: the service they are searched through could not be reached; try again later",
		},
	}
	for _, tc := range cases {
		if got := unavailableNote(tc.names); got != tc.want {
			t.Errorf("unavailableNote(%v) = %q, want %q", tc.names, got, tc.want)
		}
	}
}

func TestWriteSearchNotes(t *testing.T) {
	cases := []struct {
		name string
		resp searchResponse
		want string
	}{
		{name: "neither"},
		{
			name: "truncated only",
			resp: searchResponse{Truncated: true},
			want: "note: results truncated (search stopped early)\n",
		},
		{
			name: "unavailable only",
			resp: searchResponse{Unavailable: []string{"General"}},
			want: "note: results from General are missing: the service it is searched through could not be reached; try again later\n",
		},
		{
			name: "both",
			resp: searchResponse{Truncated: true, Unavailable: []string{"General"}},
			want: "note: results truncated (search stopped early)\nnote: results from General are missing: the service it is searched through could not be reached; try again later\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			writeSearchNotes(&buf, tc.resp)
			if got := buf.String(); got != tc.want {
				t.Errorf("writeSearchNotes() = %q, want %q", got, tc.want)
			}
		})
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
	resp, err := postSearchWithToken(srv.Client(), srv.URL, searchCode, "tok", body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v1Hits != 1 || legacyHits != 1 {
		t.Errorf("hits v1=%d legacy=%d, want 1/1", v1Hits, legacyHits)
	}
	if resp.Truncated {
		t.Error("legacy result must not be truncated")
	}
	if resp.Unavailable != nil {
		t.Errorf("legacy unavailable = %v, want nil", resp.Unavailable)
	}
	if string(resp.Results) != `[{"file":"legacy.jl","line":1,"text":"x"}]` {
		t.Errorf("results = %s", resp.Results)
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
	_, err := postSearchWithToken(srv.Client(), srv.URL, searchCode, "tok", body)
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
	_, err := postSearchWithToken(srv.Client(), srv.URL, searchSym, "tok", map[string]string{"pattern": "["})
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
	_, err := postSearchWithToken(srv.Client(), srv.URL, searchCode, "tok", map[string]string{"pattern": "["})
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
			w.Write([]byte(`{"results":[{"packagename":"DataFrames"}],"truncated":true,"unavailable":["General"]}`))
		},
	})
	resp, err := postSearchWithToken(srv.Client(), srv.URL, searchDocs, "", map[string]string{"pattern": "q"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Truncated || string(resp.Results) != `[{"packagename":"DataFrames"}]` {
		t.Errorf("results=%s truncated=%v", resp.Results, resp.Truncated)
	}
	if !reflect.DeepEqual(resp.Unavailable, []string{"General"}) {
		t.Errorf("unavailable=%v, want [General]", resp.Unavailable)
	}
}

func TestPostSearchUnavailableOnBothRoutes(t *testing.T) {
	srv := searchTestServer(t, map[string]http.HandlerFunc{}) // everything 404s
	_, err := postSearchWithToken(srv.Client(), srv.URL, searchCode, "tok", map[string]string{"pattern": "x"})
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

func TestLoginMatchesServer(t *testing.T) {
	cases := []struct {
		login, host string
		want        bool
	}{
		{"nightly.juliahub.dev", "nightly.juliahub.dev", true},
		{"auth.nightly-juliahub.juliahub.dev", "nightly-juliahub.juliahub.dev", true},
		{"juliahub.com", "juliahub.com", true},
		{"tanmaylocal.juliahub.dev", "nightly.juliahub.dev", false},
		{"nightly.juliahub.dev", "auth.nightly.juliahub.dev", false},
		{"", "nightly.juliahub.dev", false},
		{"example.org.juliahub.com", "example.org", false},
		{"example.org", "example.org.juliahub.com", true},
	}
	for _, c := range cases {
		if got := loginMatchesServer(c.login, c.host); got != c.want {
			t.Errorf("loginMatchesServer(%q, %q) = %v, want %v", c.login, c.host, got, c.want)
		}
	}
}

func TestChooseToken(t *testing.T) {
	valid := func() (*StoredToken, error) { return &StoredToken{IDToken: "fresh"}, nil }
	broken := func() (*StoredToken, error) { return nil, errors.New("refresh failed") }
	never := func() (*StoredToken, error) {
		t.Error("ensureValid must not run for another server's login")
		return nil, nil
	}
	stored := &StoredToken{Server: "nightly.juliahub.dev", IDToken: "old"}

	if tok, note := chooseToken("nightly.juliahub.dev", stored, nil, valid); tok != "fresh" || note != "" {
		t.Errorf("own server: token %q note %q", tok, note)
	}
	if tok, note := chooseToken("other.juliahub.dev", stored, nil, never); tok != "" || !strings.Contains(note, "is for nightly.juliahub.dev, not other.juliahub.dev") {
		t.Errorf("other server: token %q note %q", tok, note)
	}
	if tok, note := chooseToken("nightly.juliahub.dev", stored, nil, broken); tok != "" || !strings.Contains(note, "could not be refreshed") {
		t.Errorf("refresh failure: token %q note %q", tok, note)
	}
	if tok, note := chooseToken("nightly.juliahub.dev", nil, errors.New("no config"), never); tok != "" || note != "" {
		t.Errorf("no login: token %q note %q (anonymous use should be silent)", tok, note)
	}
}

func TestAddLoginHint(t *testing.T) {
	err := error(&searchError{Status: http.StatusUnauthorized, Message: "<html>401</html>"})
	addLoginHint(err, "nightly.juliahub.dev")
	var buf bytes.Buffer
	printSearchError(&buf, err)
	if !strings.Contains(buf.String(), "run 'jh auth login -s nightly.juliahub.dev'") {
		t.Errorf("missing login hint:\n%s", buf.String())
	}
	other := &searchError{Status: http.StatusBadRequest, Code: "invalid_pattern"}
	addLoginHint(other, "x")
	if other.LoginHint != "" {
		t.Errorf("hint added to a non-auth error")
	}
}

func TestDecodeSearchErrorHTMLPage(t *testing.T) {
	se := decodeSearchError(http.StatusUnauthorized, []byte("<html>\n<head><title>401 Authorization Required</title></head>\n</html>\n"))
	if se.Message != "Unauthorized" || strings.Contains(se.Error(), "<") {
		t.Errorf("HTML error page should collapse to the status text, got %q", se.Error())
	}
	if se := decodeSearchError(http.StatusBadRequest, []byte("bad thing")); se.Message != "bad thing" {
		t.Errorf("plain-text bodies are kept, got %q", se.Message)
	}
}

func TestDecodeSearchErrorGateway(t *testing.T) {
	page := []byte("<html>\n<head><title>502 Bad Gateway</title></head>\n</html>\n")
	for _, c := range []struct {
		status int
		body   []byte
		want   string
	}{
		{http.StatusBadGateway, page, "search_unavailable: this search is not available on the server (HTTP 502 Bad Gateway): its backend is down or not enabled there"},
		{http.StatusServiceUnavailable, nil, "search_unavailable: this search is not available on the server (HTTP 503 Service Unavailable): its backend is down or not enabled there"},
		{http.StatusGatewayTimeout, []byte("upstream timed out"), "search_timeout: the search did not finish in time (HTTP 504 Gateway Timeout: upstream timed out); try a narrower pattern or limit it with --package/--registry"},
	} {
		if got := decodeSearchError(c.status, c.body).Error(); got != c.want {
			t.Errorf("decodeSearchError(%d) = %q, want %q", c.status, got, c.want)
		}
	}
	// A structured error from the search API itself is reported as sent.
	se := decodeSearchError(http.StatusServiceUnavailable, []byte(`{"code":"metadata_unavailable","message":"m"}`))
	if se.Code != "metadata_unavailable" {
		t.Errorf("structured 503 lost its code: %v", se)
	}
}
