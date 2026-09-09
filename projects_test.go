package main

import (
	"reflect"
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
