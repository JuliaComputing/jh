package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifestNameRe(t *testing.T) {
	match := []string{
		"Manifest.toml",
		"JuliaManifest.toml",
		"Manifest-v1.11.toml",
		"JuliaManifest-v1.10.toml",
		"Manifest-v2.0.toml",
	}
	for _, name := range match {
		if !manifestNameRe.MatchString(name) {
			t.Errorf("manifestNameRe should match %q", name)
		}
	}
	reject := []string{
		"manifest.toml",      // lowercase
		"Manifest.tomlx",     // trailing chars
		"Project.toml",       // project, not manifest
		"Manifest-v1.toml",   // missing minor
		"Manifest-1.11.toml", // missing 'v'
		"NotAManifest.toml",
	}
	for _, name := range reject {
		if manifestNameRe.MatchString(name) {
			t.Errorf("manifestNameRe should NOT match %q", name)
		}
	}
}

func TestFindManifestCandidatesOrdering(t *testing.T) {
	dir := t.TempDir()
	names := []string{
		"Manifest.toml",
		"JuliaManifest.toml",
		"Manifest-v1.10.toml",
		"Manifest-v1.11.toml",
		"NotAManifest.toml", // ignored
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x = 1\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}

	cands, err := findManifestCandidates(dir)
	if err != nil {
		t.Fatalf("findManifestCandidates: %v", err)
	}
	// Expected precedence: versioned (newest first), then unversioned with the
	// JuliaManifest* spelling before plain Manifest*.
	want := []string{"Manifest-v1.11.toml", "Manifest-v1.10.toml", "JuliaManifest.toml", "Manifest.toml"}
	if len(cands) != len(want) {
		t.Fatalf("got %d candidates, want %d: %v", len(cands), len(want), cands)
	}
	for i, w := range want {
		if cands[i].name != w {
			t.Errorf("candidate[%d] = %q, want %q (order: %v)", i, cands[i].name, w, names)
		}
	}
}

func TestFindManifestCandidatesNone(t *testing.T) {
	cands, err := findManifestCandidates(t.TempDir())
	if err != nil {
		t.Fatalf("findManifestCandidates: %v", err)
	}
	if len(cands) != 0 {
		t.Errorf("expected no candidates in empty dir, got %v", cands)
	}
}

func TestFindProjectFile(t *testing.T) {
	// Neither present.
	dir := t.TempDir()
	if got := findProjectFile(dir); got != "" {
		t.Errorf("findProjectFile(empty) = %q, want \"\"", got)
	}

	// Project.toml only.
	dir = t.TempDir()
	proj := filepath.Join(dir, "Project.toml")
	os.WriteFile(proj, []byte("name=\"x\"\n"), 0o644)
	if got := findProjectFile(dir); got != proj {
		t.Errorf("findProjectFile = %q, want %q", got, proj)
	}

	// JuliaProject.toml takes precedence over Project.toml.
	dir = t.TempDir()
	jproj := filepath.Join(dir, "JuliaProject.toml")
	os.WriteFile(jproj, []byte("name=\"x\"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "Project.toml"), []byte("name=\"x\"\n"), 0o644)
	if got := findProjectFile(dir); got != jproj {
		t.Errorf("findProjectFile preferred = %q, want %q", got, jproj)
	}
}

func TestResolveScanInputs(t *testing.T) {
	const manifestBody = "julia_version = \"1.10.0\"\n"
	const projectBody = "name = \"Demo\"\n"

	t.Run("directory discovery picks up manifest and sibling project", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "Manifest.toml"), []byte(manifestBody), 0o644)
		os.WriteFile(filepath.Join(dir, "Project.toml"), []byte(projectBody), 0o644)

		in, err := resolveScanInputs(dir, "", false)
		if err != nil {
			t.Fatalf("resolveScanInputs: %v", err)
		}
		if in.ManifestBody != manifestBody {
			t.Errorf("ManifestBody = %q, want %q", in.ManifestBody, manifestBody)
		}
		if in.ProjectBody != projectBody {
			t.Errorf("ProjectBody = %q, want %q", in.ProjectBody, projectBody)
		}
	})

	t.Run("explicit manifest file with sibling project", func(t *testing.T) {
		dir := t.TempDir()
		manifest := filepath.Join(dir, "Manifest.toml")
		os.WriteFile(manifest, []byte(manifestBody), 0o644)
		os.WriteFile(filepath.Join(dir, "Project.toml"), []byte(projectBody), 0o644)

		in, err := resolveScanInputs(manifest, "", false)
		if err != nil {
			t.Fatalf("resolveScanInputs: %v", err)
		}
		if in.ManifestPath != manifest || in.ProjectBody != projectBody {
			t.Errorf("unexpected inputs: %+v", in)
		}
	})

	t.Run("noProject skips the sibling project", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "Manifest.toml"), []byte(manifestBody), 0o644)
		os.WriteFile(filepath.Join(dir, "Project.toml"), []byte(projectBody), 0o644)

		in, err := resolveScanInputs(dir, "", true)
		if err != nil {
			t.Fatalf("resolveScanInputs: %v", err)
		}
		if in.ProjectBody != "" || in.ProjectPath != "" {
			t.Errorf("expected no project with noProject=true, got %+v", in)
		}
	})

	t.Run("empty project file is skipped", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "Manifest.toml"), []byte(manifestBody), 0o644)
		os.WriteFile(filepath.Join(dir, "Project.toml"), []byte("   \n"), 0o644)

		in, err := resolveScanInputs(dir, "", false)
		if err != nil {
			t.Fatalf("resolveScanInputs: %v", err)
		}
		if in.ProjectBody != "" || in.ProjectPath != "" {
			t.Errorf("expected empty project to be skipped, got %+v", in)
		}
	})

	t.Run("missing path errors", func(t *testing.T) {
		_, err := resolveScanInputs(filepath.Join(t.TempDir(), "nope"), "", false)
		if err == nil {
			t.Fatal("expected error for missing path")
		}
	})

	t.Run("empty manifest errors", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "Manifest.toml"), []byte("  \n\t\n"), 0o644)
		_, err := resolveScanInputs(dir, "", false)
		if err == nil {
			t.Fatal("expected error for empty manifest")
		}
	})
}

func TestWriteResultsOutput(t *testing.T) {
	t.Run("JSON is pretty-printed to a file", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "results.json")
		if err := writeResultsOutput([]byte(`{"a":1,"b":2}`), out, false); err != nil {
			t.Fatalf("writeResultsOutput: %v", err)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("read output: %v", err)
		}
		// Indented output spans multiple lines and ends with a newline.
		if !strings.Contains(string(data), "\n  \"a\": 1") {
			t.Errorf("expected indented JSON, got:\n%s", data)
		}
		if !strings.HasSuffix(string(data), "\n") {
			t.Error("expected trailing newline on JSON output")
		}
	})

	t.Run("CSV is written verbatim", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "results.csv")
		csv := "pkg,severity\nFoo,HIGH\n"
		if err := writeResultsOutput([]byte(csv), out, true); err != nil {
			t.Fatalf("writeResultsOutput: %v", err)
		}
		data, _ := os.ReadFile(out)
		if string(data) != csv {
			t.Errorf("CSV not written verbatim: got %q, want %q", data, csv)
		}
	})

	t.Run("invalid JSON falls back to raw bytes", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "raw.json")
		raw := "not json at all"
		if err := writeResultsOutput([]byte(raw), out, false); err != nil {
			t.Fatalf("writeResultsOutput: %v", err)
		}
		data, _ := os.ReadFile(out)
		if string(data) != raw {
			t.Errorf("expected raw passthrough on unindentable JSON, got %q", data)
		}
	})
}
