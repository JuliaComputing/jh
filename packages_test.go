package main

import (
	"reflect"
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
			IsApp:       true,
			Metadata:    &PackageMetadata{Description: "viz", Repo: "r", Tags: []string{"plot"}, StarCount: 42, DocsLink: "d"},
			RegistryMap: &PackageRegistryMap{Version: "1.0.0", RegistryID: 7},
		}
		got := gqlToInfo(p, idToName)
		if got.Registry != "General" {
			t.Errorf("Registry = %q, want General (resolved from id)", got.Registry)
		}
		if got.Version != "1.0.0" || got.Stars != 42 || !got.IsApp {
			t.Errorf("fields not mapped: %+v", got)
		}
	})

	t.Run("nil metadata and registrymap are safe", func(t *testing.T) {
		got := gqlToInfo(Package{Name: "Bare", UUID: "u"}, idToName)
		if got.Name != "Bare" || got.Registry != "" || got.Description != "" || got.Version != "" {
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
