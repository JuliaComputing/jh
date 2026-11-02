package main

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestEscapeLikePattern(t *testing.T) {
	cases := map[string]string{
		"john":        "john",
		"a_b":         `a\_b`,
		"100%":        `100\%`,
		`back\slash`:  `back\\slash`,
		"mix_%\\done": `mix\_\%\\done`,
	}
	for in, want := range cases {
		if got := escapeLikePattern(in); got != want {
			t.Errorf("escapeLikePattern(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildProjectsFilter(t *testing.T) {
	t.Run("no filter fetches everything visible", func(t *testing.T) {
		got := buildProjectsFilter("", false, 42)
		if !reflect.DeepEqual(got, map[string]interface{}{}) {
			t.Errorf("got %v, want empty filter", got)
		}
	})

	t.Run("--user with no value filters by the current user's id", func(t *testing.T) {
		got := buildProjectsFilter("", true, 42)
		want := map[string]interface{}{
			"owner_id": map[string]interface{}{"_eq": int64(42)},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("--user <name> filters by owner username, case-insensitively and literally", func(t *testing.T) {
		got := buildProjectsFilter("Jo_hn", true, 42)
		want := map[string]interface{}{
			"owner": map[string]interface{}{
				"username": map[string]interface{}{"_ilike": `Jo\_hn`},
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

func TestTotalPages(t *testing.T) {
	cases := map[int]int{
		0:                    0,
		1:                    1,
		projectsPageSize:     1,
		projectsPageSize + 1: 2,
		2 * projectsPageSize: 2,
		57508:                576,
	}
	for total, want := range cases {
		if got := totalPages(total); got != want {
			t.Errorf("totalPages(%d) = %d, want %d", total, got, want)
		}
	}
}

// normalizeGraphQL drops comments and insignificant whitespace, so the hash below
// changes only when the document itself does.
func normalizeGraphQL(doc string) string {
	doc = regexp.MustCompile(`(?m)#.*$`).ReplaceAllString(doc, "")
	doc = strings.Join(strings.Fields(doc), " ")
	return regexp.MustCompile(` ?([{}():,!\[\]=$]) ?`).ReplaceAllString(doc, "$1")
}

// The Projects documents are sent verbatim and the server answers only
// allowlisted text, so an edit to either file breaks `jh project list` and
// `jh clone` on every server that doesn't carry the edited text. These hashes
// only detect such an edit; this repo can't see the server's allowlist. Update
// them only after checking the new text against JuliaHub's allowlist
// (graphqlsrvr/internal/allowlist Lookup) for every release the CLI supports.
var projectsDocumentSHA256 = map[string]string{
	"projects.gql":          "3c5c5d098cbe397de6fe6247e229a0cc7b0ed914ce1fdcc1b6c10876812fa0e3",
	"projects_juliasim.gql": "187e0b35c14cd505720d20fa3f07d34cae4951a2184379507a8a96037cf01ead",
}

func TestProjectsDocumentsUnchanged(t *testing.T) {
	for name, doc := range map[string]string{"projects.gql": projectsQuery, "projects_juliasim.gql": projectsJuliaSimQuery} {
		sum := sha256.Sum256([]byte(normalizeGraphQL(doc)))
		if got := hex.EncodeToString(sum[:]); got != projectsDocumentSHA256[name] {
			t.Errorf("%s changed (normalized sha256 %s, pinned %s); it must stay the server's allowlisted document", name, got, projectsDocumentSHA256[name])
		}
	}
	if strings.Contains(projectsQuery, "resources") {
		t.Error("projects.gql selects `resources`, which costs the server about a second per row")
	}
}

// fakeProjectsServer answers each Projects document from bodies by file name
// and records which documents were sent.
func fakeProjectsServer(bodies map[string]string, sent *[]string) func(GraphQLRequest) ([]byte, error) {
	return func(req GraphQLRequest) ([]byte, error) {
		name := "projects.gql"
		if req.Query == projectsJuliaSimQuery {
			name = "projects_juliasim.gql"
		}
		*sent = append(*sent, name)
		return []byte(bodies[name]), nil
	}
}

const (
	notAllowedBody = `{"errors":[{"message":"query is not allowed","extensions":{"code":"validation-failed"}}]}`
	oneProjectBody = `{"data":{"aggregate":{"aggregate":{"count":1}},"projects":[{"id":"p1","name":"proj","owner":{"username":"alice"}}]}}`
)

func TestFetchProjectsPagePrefersTheWebUIDocument(t *testing.T) {
	var sent []string
	projects, total, err := fetchProjectsPageWith(fakeProjectsServer(map[string]string{"projects.gql": oneProjectBody}, &sent), 1, map[string]interface{}{}, 1)
	if err != nil || total != 1 || len(projects) != 1 {
		t.Fatalf("got %v, %d, %v", projects, total, err)
	}
	if !reflect.DeepEqual(sent, []string{"projects.gql"}) {
		t.Fatalf("sent %v, want only projects.gql", sent)
	}
}

func TestFetchProjectsPageFallsBackWhenNotAllowlisted(t *testing.T) {
	var sent []string
	projects, _, err := fetchProjectsPageWith(fakeProjectsServer(map[string]string{
		"projects.gql":          notAllowedBody,
		"projects_juliasim.gql": oneProjectBody,
	}, &sent), 1, map[string]interface{}{}, 1)
	if err != nil || len(projects) != 1 {
		t.Fatalf("got %v, %v", projects, err)
	}
	if !reflect.DeepEqual(sent, []string{"projects.gql", "projects_juliasim.gql"}) {
		t.Fatalf("sent %v", sent)
	}
}

func TestFetchProjectsPageDoesNotFallBackOnOtherErrors(t *testing.T) {
	var sent []string
	_, _, err := fetchProjectsPageWith(fakeProjectsServer(map[string]string{
		"projects.gql": `{"errors":[{"message":"permission denied"}]}`,
	}, &sent), 1, map[string]interface{}{}, 1)
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("err = %v", err)
	}
	if len(sent) != 1 {
		t.Fatalf("sent %v, want one request", sent)
	}
}

func TestFetchProjectsPageReportsWhenNoDocumentIsAllowlisted(t *testing.T) {
	var sent []string
	_, _, err := fetchProjectsPageWith(fakeProjectsServer(map[string]string{
		"projects.gql":          notAllowedBody,
		"projects_juliasim.gql": notAllowedBody,
	}, &sent), 1, map[string]interface{}{}, 1)
	if err == nil || !strings.Contains(err.Error(), "query is not allowed") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchProjectsPageFallsBackOnSchemaValidationFailure(t *testing.T) {
	var sent []string
	projects, _, err := fetchProjectsPageWith(fakeProjectsServer(map[string]string{
		"projects.gql":          `{"errors":[{"message":"field 'editing_mode' not found in type: 'projects'","extensions":{"code":"validation-failed"}}]}`,
		"projects_juliasim.gql": oneProjectBody,
	}, &sent), 1, map[string]interface{}{}, 1)
	if err != nil || len(projects) != 1 {
		t.Fatalf("got %v, %v", projects, err)
	}
}

// The JuliaSim fallback is fetched in projectsFallbackChunk-row requests that
// together cover the same page as one web UI request.
func TestFetchProjectsPageFallbackChunks(t *testing.T) {
	type call struct{ limit, offset int }
	var calls []call
	rows := func(n int) string {
		items := make([]string, n)
		for i := range items {
			items[i] = `{"id":"p","name":"n","owner":{"username":"u"}}`
		}
		return `{"data":{"aggregate":{"aggregate":{"count":225}},"projects":[` + strings.Join(items, ",") + `]}}`
	}
	execute := func(req GraphQLRequest) ([]byte, error) {
		if req.Query == projectsQuery {
			return []byte(notAllowedBody), nil
		}
		c := call{req.Variables["limit"].(int), req.Variables["offset"].(int)}
		calls = append(calls, c)
		// 225 projects in all: page 3 (offset 200) has 25.
		left := 225 - c.offset
		if left > c.limit {
			left = c.limit
		}
		return []byte(rows(left)), nil
	}

	projects, total, err := fetchProjectsPageWith(execute, 1, map[string]interface{}{}, 2)
	if err != nil || total != 225 || len(projects) != projectsPageSize {
		t.Fatalf("page 2: %d rows, total %d, err %v", len(projects), total, err)
	}
	if len(calls) != projectsPageSize/projectsFallbackChunk || calls[0] != (call{projectsFallbackChunk, 100}) || calls[len(calls)-1] != (call{projectsFallbackChunk, 190}) {
		t.Fatalf("page 2 requests: %v", calls)
	}

	calls = nil
	projects, _, err = fetchProjectsPageWith(execute, 1, map[string]interface{}{}, 3)
	if err != nil || len(projects) != 25 {
		t.Fatalf("page 3: %d rows, err %v", len(projects), err)
	}
	if len(calls) != 3 {
		t.Fatalf("page 3 should stop after the short chunk: %v", calls)
	}
}

func TestPickProject(t *testing.T) {
	p := func(owner, name string) Project {
		var pr Project
		pr.Owner.Username, pr.Name, pr.ID = owner, name, owner+"/"+name
		return pr
	}
	rows := []Project{p("alice", "projX"), p("alice", "proj")}
	if got := pickProject(rows, "ALICE", "PROJ"); got == nil || got.ID != "alice/proj" {
		t.Fatalf("exact case-insensitive match: got %v", got)
	}
	if got := pickProject([]Project{p("Łukasz", "p")}, "łukasz", "P"); got == nil {
		t.Fatal("a single server-side match should be used even when EqualFold disagrees")
	}
	if got := pickProject(rows, "bob", "proj"); got != nil {
		t.Fatalf("no match among several rows: got %v", got)
	}
	if got := pickProject(nil, "alice", "proj"); got != nil {
		t.Fatalf("empty: got %v", got)
	}
}

func TestProjectLookupFilter(t *testing.T) {
	got := projectLookupFilter("al_ice", "my%proj")
	want := map[string]interface{}{
		"owner": map[string]interface{}{
			"username": map[string]interface{}{"_ilike": `al\_ice`},
		},
		"name": map[string]interface{}{"_ilike": `my\%proj`},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("projectLookupFilter = %#v, want %#v", got, want)
	}
}
