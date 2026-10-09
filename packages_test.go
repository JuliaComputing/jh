package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestRestToInfo(t *testing.T) {
	p := RESTPackage{
		Name:                   "DataFrames",
		UUID:                   "a93c6f00-e57d-5684-b7b6-d8193f3e46c0",
		Registry:               "General",
		Description:            "in-memory tabular data",
		StargazersCount:        1700,
		SourceURL:              "https://github.com/JuliaData/DataFrames.jl",
		JHubDocsURL:            "https://docs/x",
		LatestStableVersion:    "1.6.1",
		DetectedSourceLicenses: []string{"MIT", "Apache-2.0"},
		Tags:                   []string{"data"},
	}
	got := restToInfo(p)

	if got.Name != p.Name || got.UUID != p.UUID || got.Registry != p.Registry {
		t.Errorf("identity fields not carried: %+v", got)
	}
	if got.Version != "1.6.1" {
		t.Errorf("Version = %q, want 1.6.1 (from LatestStableVersion)", got.Version)
	}
	if got.Stars != 1700 {
		t.Errorf("Stars = %d, want 1700", got.Stars)
	}
	if got.License != "MIT, Apache-2.0" {
		t.Errorf("License = %q, want joined licenses", got.License)
	}
}

func TestGqlToInfo(t *testing.T) {
	idToName := map[int]string{7: "General"}

	t.Run("full package", func(t *testing.T) {
		p := Package{
			Name: "Plots", UUID: "u-1", Owner: " JuliaPlots", License: "MIT",
			IsApp: true, Score: 9.5,
			Metadata:    &PackageMetadata{Description: "viz", Repo: "r", Tags: []string{"plot"}, StarCount: 42, DocsLink: "d"},
			RegistryMap: &PackageRegistryMap{Version: "1.0.0", RegistryID: 7, Status: true},
		}
		got := gqlToInfo(p, idToName)
		if got.Registry != "General" {
			t.Errorf("Registry = %q, want General (resolved from id)", got.Registry)
		}
		if got.Version != "1.0.0" || got.Status != "Active" || got.Stars != 42 || !got.IsApp {
			t.Errorf("fields not mapped: %+v", got)
		}
	})

	t.Run("inactive status", func(t *testing.T) {
		p := Package{Name: "X", RegistryMap: &PackageRegistryMap{RegistryID: 7, Status: false}}
		if got := gqlToInfo(p, idToName); got.Status != "Inactive" {
			t.Errorf("Status = %q, want Inactive", got.Status)
		}
	})

	t.Run("nil metadata and registrymap are safe", func(t *testing.T) {
		got := gqlToInfo(Package{Name: "Bare", UUID: "u"}, idToName)
		if got.Name != "Bare" || got.Registry != "" || got.Description != "" || got.Status != "" {
			t.Errorf("nil sub-structs should leave fields empty: %+v", got)
		}
	})
}

func TestBuildRegistryIDToName(t *testing.T) {
	got := buildRegistryIDToName([]int{1, 2, 3}, []string{"General", "JuliaSim"})
	// Third id has no matching name and must be skipped (no panic, no empty key).
	want := map[int]string{1: "General", 2: "JuliaSim"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBuildGraphQLPackageVariables(t *testing.T) {
	t.Run("no limit, no registries", func(t *testing.T) {
		v := buildGraphQLPackageVariables("plots", 0, 5, nil)
		if v["search"] != "plots" || v["offset"] != 5 {
			t.Errorf("search/offset not set: %v", v)
		}
		if _, ok := v["limit"]; ok {
			t.Error("limit should be omitted when <= 0")
		}
		if _, ok := v["registries"]; ok {
			t.Error("registries should be omitted when none given")
		}
	})

	t.Run("with limit and registries", func(t *testing.T) {
		v := buildGraphQLPackageVariables("x", 10, 0, []int{3, 7})
		if v["limit"] != 10 {
			t.Errorf("limit = %v, want 10", v["limit"])
		}
		if v["registries"] != "{3,7}" {
			t.Errorf("registries = %v, want {3,7}", v["registries"])
		}
	})
}

func TestPlanRESTPages(t *testing.T) {
	cases := []struct {
		limit, offset int
		pages         []restPage
		skip          int
	}{
		{10, 0, []restPage{{1, 10}}, 0},
		{10, 20, []restPage{{3, 10}}, 0},
		{10, 15, []restPage{{2, 10}, {3, 10}}, 5},
		{250, 0, []restPage{{1, 100}, {2, 100}, {3, 100}}, 0},
		{0, 0, []restPage{{1, 10}}, 0},
	}
	for _, c := range cases {
		pages, skip := planRESTPages(c.limit, c.offset)
		if !reflect.DeepEqual(pages, c.pages) || skip != c.skip {
			t.Errorf("planRESTPages(%d, %d) = %v, %d; want %v, %d", c.limit, c.offset, pages, skip, c.pages, c.skip)
		}
	}
}

func TestBuildPackagesInfoQuery(t *testing.T) {
	q := buildPackagesInfoQuery("plots", []string{"General", "MyReg"}, restPage{Page: 2, PerPage: 25})
	want := "name=plots&pagination%5Bpage%5D=2&pagination%5Bper_page%5D=25&pagination%5Btype%5D=offset&registries=General%2CMyReg&sorts%5B0%5D=-score&sorts%5B1%5D=-name"
	if got := q.Encode(); got != want {
		t.Errorf("query = %s\nwant    %s", got, want)
	}
	// Without a search term: most-starred first, still with the name tiebreak.
	q = buildPackagesInfoQuery("", nil, restPage{1, 10})
	if q.Has("name") || q.Get("sorts[0]") != "-stargazers_count" || q.Get("sorts[1]") != "-name" {
		t.Errorf("unexpected params without a search term: %s", q.Encode())
	}
	// The API's Sorts schema requires a direction sign on every key.
	signed := regexp.MustCompile(`^[+-].+$`)
	for _, search := range []string{"plots", ""} {
		q = buildPackagesInfoQuery(search, nil, restPage{1, 10})
		for key, vals := range q {
			if !strings.HasPrefix(key, "sorts[") {
				continue
			}
			for _, v := range vals {
				if !signed.MatchString(v) {
					t.Errorf("search %q: %s=%q does not match the API's sort pattern ^[+-].+$", search, key, v)
				}
			}
		}
	}
}

func TestFetchRESTPackagesFromWindow(t *testing.T) {
	// 23 packages P00..P22 served in pages; ask for 10 starting at offset 15.
	var gotAuth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != packagesInfoPath {
			http.NotFound(w, r)
			return
		}
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
		page, _ := strconv.Atoi(r.URL.Query().Get("pagination[page]"))
		per, _ := strconv.Atoi(r.URL.Query().Get("pagination[per_page]"))
		var pkgs []RESTPackage
		for i := (page - 1) * per; i < page*per && i < 23; i++ {
			pkgs = append(pkgs, RESTPackage{Name: fmt.Sprintf("P%02d", i)})
		}
		json.NewEncoder(w).Encode(PackageRESTListResponse{Packages: pkgs, Meta: struct {
			Total int `json:"total"`
		}{23}})
	}))
	defer srv.Close()

	pkgs, total, err := fetchRESTPackagesFrom(srv.Client(), srv.URL, "", "", 10, 15, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range pkgs {
		names = append(names, p.Name)
	}
	want := []string{"P15", "P16", "P17", "P18", "P19", "P20", "P21", "P22"}
	if !reflect.DeepEqual(names, want) || total != 23 {
		t.Errorf("got %v (total %d), want %v (total 23)", names, total, want)
	}
	for _, a := range gotAuth {
		if a != "" {
			t.Errorf("empty token should send no Authorization header, got %q", a)
		}
	}
}

func TestFetchRESTPackagesFromHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("<html><head><title>401 Authorization Required</title></head></html>"))
	}))
	defer srv.Close()
	_, _, err := fetchRESTPackagesFrom(srv.Client(), srv.URL, "t", "x", 10, 0, nil)
	if err == nil || err.Error() != "API request failed (status 401): Unauthorized" {
		t.Errorf("want a one-line status 401 error, got %v", err)
	}
}
