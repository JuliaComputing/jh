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
// allowlisted text, so an edit to any of them breaks `jh project list` and
// `jh clone` on every server that doesn't carry the edited text. These hashes
// only detect such an edit; this repo can't see the server's allowlist. Update
// them only after checking the new text against JuliaHub's allowlist
// (graphqlsrvr/internal/allowlist Lookup) for every release the CLI supports.
// The listing documents may not select `resources`, which costs the server about
// a second per row.
var projectsDocumentSHA256 = map[string]string{
	"projects.gql":          "3c5c5d098cbe397de6fe6247e229a0cc7b0ed914ce1fdcc1b6c10876812fa0e3",
	"projects_pre264.gql":   "185121c1abf0e32c1f6a75d7dbda7ad4013e46cf7464b9e3d37e0acd2a8f3604",
	"projects_juliasim.gql": "187e0b35c14cd505720d20fa3f07d34cae4951a2184379507a8a96037cf01ead",
}

func TestProjectsDocumentsUnchanged(t *testing.T) {
	for name, doc := range map[string]string{"projects.gql": projectsQuery, "projects_pre264.gql": projectsPre264Query, "projects_juliasim.gql": projectsJuliaSimQuery} {
		sum := sha256.Sum256([]byte(normalizeGraphQL(doc)))
		if got := hex.EncodeToString(sum[:]); got != projectsDocumentSHA256[name] {
			t.Errorf("%s changed (normalized sha256 %s, pinned %s); it must stay the server's allowlisted document", name, got, projectsDocumentSHA256[name])
		}
	}
	for _, d := range listDocuments {
		if strings.Contains(d.query, "resources") {
			t.Errorf("the %s listing document selects `resources`, which costs the server about a second per row", d.name)
		}
	}
}

// fakeProjectsServer answers each Projects document from bodies by file name
// and records which documents were sent.
func fakeProjectsServer(bodies map[string]string, sent *[]string) func(GraphQLRequest) ([]byte, error) {
	return func(req GraphQLRequest) ([]byte, error) {
		name := "projects.gql"
		switch req.Query {
		case projectsPre264Query:
			name = "projects_pre264.gql"
		case projectsJuliaSimQuery:
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

func TestFetchProjectsPagePrefersThe264Document(t *testing.T) {
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
		"projects.gql":        notAllowedBody,
		"projects_pre264.gql": oneProjectBody,
	}, &sent), 1, map[string]interface{}{}, 1)
	if err != nil || len(projects) != 1 {
		t.Fatalf("got %v, %v", projects, err)
	}
	if !reflect.DeepEqual(sent, []string{"projects.gql", "projects_pre264.gql"}) {
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
		"projects.gql":        notAllowedBody,
		"projects_pre264.gql": notAllowedBody,
	}, &sent), 1, map[string]interface{}{}, 1)
	if err == nil || !strings.Contains(err.Error(), "query is not allowed") {
		t.Fatalf("err = %v", err)
	}
	if !reflect.DeepEqual(sent, []string{"projects.gql", "projects_pre264.gql"}) {
		t.Fatalf("sent %v", sent)
	}
}

func TestFetchProjectsPageDoesNotFallBackOnFilterValidationErrors(t *testing.T) {
	var sent []string
	_, _, err := fetchProjectsPageWith(fakeProjectsServer(map[string]string{
		"projects.gql": `{"errors":[{"message":"field 'nope' not found in type: 'projects_bool_exp'","extensions":{"code":"validation-failed"}}]}`,
	}, &sent), 1, map[string]interface{}{}, 1)
	if err == nil || !strings.Contains(err.Error(), "projects_bool_exp") {
		t.Fatalf("err = %v", err)
	}
	if len(sent) != 1 {
		t.Fatalf("sent %v, want one request", sent)
	}
}

func TestFetchProjectsPageKeepsTheFirstErrorWhenTheFallbackFails(t *testing.T) {
	var sent []string
	_, _, err := fetchProjectsPageWith(fakeProjectsServer(map[string]string{
		"projects.gql":        `{"errors":[{"message":"field 'requires_publish_request' not found in type: 'projects'","extensions":{"code":"validation-failed"}}]}`,
		"projects_pre264.gql": notAllowedBody,
	}, &sent), 1, map[string]interface{}{}, 1)
	want := "GraphQL errors: [{field 'requires_publish_request' not found in type: 'projects'}]; with the 6.9–26.3 document: GraphQL errors: [{query is not allowed}]"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant  %s", err, want)
	}
}

func TestFetchProjectsPageFallsBackOnSchemaValidationFailure(t *testing.T) {
	var sent []string
	projects, _, err := fetchProjectsPageWith(fakeProjectsServer(map[string]string{
		"projects.gql":        `{"errors":[{"message":"field 'editing_mode' not found in type: 'projects'","extensions":{"code":"validation-failed"}}]}`,
		"projects_pre264.gql": oneProjectBody,
	}, &sent), 1, map[string]interface{}{}, 1)
	if err != nil || len(projects) != 1 {
		t.Fatalf("got %v, %v", projects, err)
	}
	if !reflect.DeepEqual(sent, []string{"projects.gql", "projects_pre264.gql"}) {
		t.Fatalf("sent %v", sent)
	}
}

func TestLookupProjectsFallsBackToJuliaSim(t *testing.T) {
	var sent []string
	var limits []interface{}
	execute := fakeProjectsServer(map[string]string{
		"projects.gql":          notAllowedBody,
		"projects_pre264.gql":   notAllowedBody,
		"projects_juliasim.gql": oneProjectBody,
	}, &sent)
	projects, err := lookupProjectsWith(func(req GraphQLRequest) ([]byte, error) {
		limits = append(limits, req.Variables["limit"])
		return execute(req)
	}, 1, projectLookupFilter("alice", "proj"))
	if err != nil || len(projects) != 1 {
		t.Fatalf("got %v, %v", projects, err)
	}
	if !reflect.DeepEqual(sent, []string{"projects.gql", "projects_pre264.gql", "projects_juliasim.gql"}) {
		t.Fatalf("sent %v", sent)
	}
	for _, l := range limits {
		if l != projectLookupLimit {
			t.Fatalf("lookup sent limit %v, want %d", l, projectLookupLimit)
		}
	}
}

func TestLookupProjectsReportsEveryRejection(t *testing.T) {
	var sent []string
	_, err := lookupProjectsWith(fakeProjectsServer(map[string]string{
		"projects.gql":          notAllowedBody,
		"projects_pre264.gql":   notAllowedBody,
		"projects_juliasim.gql": notAllowedBody,
	}, &sent), 1, projectLookupFilter("alice", "proj"))
	want := "GraphQL errors: [{query is not allowed}]; with the 6.9–26.3 document: GraphQL errors: [{query is not allowed}]; with the JuliaSim document: GraphQL errors: [{query is not allowed}]"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant  %s", err, want)
	}
}

func TestPickProject(t *testing.T) {
	p := func(owner, name string) Project {
		var pr Project
		pr.Owner.Username, pr.Name, pr.ID = owner, name, owner+"/"+name
		return pr
	}
	rows := []Project{p("alice", "projX"), p("alice", "proj")}
	if got, err := pickProject(rows, "ALICE", "PROJ"); err != nil || got.ID != "alice/proj" {
		t.Fatalf("case-insensitive match: got %v, %v", got, err)
	}
	if got, err := pickProject([]Project{p("alice", "Proj"), p("alice", "proj")}, "alice", "proj"); err != nil || got.ID != "alice/proj" {
		t.Fatalf("exact match should beat a case-insensitive one: got %v, %v", got, err)
	}
	if got, err := pickProject([]Project{p("alice", "Proj"), p("alice", "PROJ")}, "alice", "proj"); err == nil || !strings.Contains(err.Error(), "alice/Proj, alice/PROJ") {
		t.Fatalf("several case-insensitive matches without an exact one should be ambiguous: got %v, %v", got, err)
	}
	if got, err := pickProject([]Project{p("bob", "other")}, "alice", "proj"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("a single row that does not match must not be picked: got %v, %v", got, err)
	}
	if got, err := pickProject(rows, "bob", "proj"); err == nil {
		t.Fatalf("no match among several rows: got %v", got)
	}
	if got, err := pickProject(nil, "alice", "proj"); err == nil {
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
