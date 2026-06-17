package main

import "testing"

func TestParseAdminGroups(t *testing.T) {
	t.Run("flattens categories and sorts by id", func(t *testing.T) {
		// Two buckets; ids deliberately out of order to prove the sort.
		body := []byte(`{
			"juliahub": {"groups": [{"name":"admins","id":3},{"name":"users","id":1}]},
			"site": {"groups": [{"name":"ops","id":2}]}
		}`)
		groups, err := parseAdminGroups(body)
		if err != nil {
			t.Fatalf("parseAdminGroups: %v", err)
		}
		wantOrder := []struct {
			name string
			id   int64
		}{{"users", 1}, {"ops", 2}, {"admins", 3}}
		if len(groups) != len(wantOrder) {
			t.Fatalf("got %d groups, want %d: %+v", len(groups), len(wantOrder), groups)
		}
		for i, w := range wantOrder {
			if groups[i].Name != w.name || groups[i].ID != w.id {
				t.Errorf("groups[%d] = %+v, want {%s %d}", i, groups[i], w.name, w.id)
			}
		}
	})

	t.Run("empty object yields no groups", func(t *testing.T) {
		groups, err := parseAdminGroups([]byte(`{}`))
		if err != nil {
			t.Fatalf("parseAdminGroups: %v", err)
		}
		if len(groups) != 0 {
			t.Errorf("got %+v, want no groups", groups)
		}
	})

	t.Run("invalid JSON errors", func(t *testing.T) {
		if _, err := parseAdminGroups([]byte(`[`)); err == nil {
			t.Error("expected error for invalid JSON")
		}
	})
}
