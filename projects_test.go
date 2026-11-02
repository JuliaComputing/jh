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

// projects.gql is sent verbatim and the server answers only allowlisted
// documents. This hash pins it to the JuliaHub web UI's Projects document
// (frontend/src/components/Projects/queries/projects.gql). Change it only
// together with that file, after checking the new text is on the server's
// allowlist; an edit here alone breaks `jh project list` and `jh clone`.
const projectsQuerySHA256 = "3c5c5d098cbe397de6fe6247e229a0cc7b0ed914ce1fdcc1b6c10876812fa0e3"

func TestProjectsQueryIsTheAllowlistedDocument(t *testing.T) {
	sum := sha256.Sum256([]byte(normalizeGraphQL(projectsQuery)))
	if got := hex.EncodeToString(sum[:]); got != projectsQuerySHA256 {
		t.Fatalf("projects.gql changed (normalized sha256 %s, pinned %s); it must stay the server's allowlisted Projects document", got, projectsQuerySHA256)
	}
	if strings.Contains(projectsQuery, "resources") {
		t.Fatal("projects.gql selects `resources`, which costs the server about a second per row")
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
