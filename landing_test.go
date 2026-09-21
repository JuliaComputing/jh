package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A layout in the shape GET /api/v1/ui/layout/homepage returns, including a
// field the CLI does not model (`color`) to prove round-tripping keeps it.
const sampleLayout = `[
  {"order":0,"title":"Welcome","type":"greeter",
   "metadata":{"subType":null,"content":"### Hi","renderedContent":"<h3>Hi</h3>\n"},
   "x":0,"y":0,"w":2,"h":1,"color":"blue"},
  {"order":1,"title":"Applications","type":"applications",
   "metadata":{"subType":null,"content":"","renderedContent":""},
   "x":0,"y":1,"w":2,"h":1},
  {"order":2,"title":"My Projects","type":"projects",
   "metadata":{"subType":{"name":"my","id":null},"content":"","renderedContent":""},
   "x":0,"y":2,"w":1,"h":1}
]`

func parseLayout(t *testing.T, s string) []layoutWidget {
	t.Helper()
	var ws []layoutWidget
	if err := json.Unmarshal([]byte(s), &ws); err != nil {
		t.Fatalf("unmarshal layout: %v", err)
	}
	return ws
}

func TestLayoutWidgetRoundTrip(t *testing.T) {
	ws := parseLayout(t, sampleLayout)
	if len(ws) != 3 {
		t.Fatalf("got %d widgets, want 3", len(ws))
	}
	out, err := json.Marshal(ws)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got, want []map[string]interface{}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if err := json.Unmarshal([]byte(sampleLayout), &want); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d widgets after round trip, want %d", len(got), len(want))
	}
	for i := range want {
		g, _ := json.Marshal(got[i])
		w, _ := json.Marshal(want[i])
		if string(g) != string(w) {
			t.Errorf("widget %d changed on round trip:\n got  %s\n want %s", i, g, w)
		}
	}
	// The unknown field must survive as-is, not be dropped or mangled.
	if got[0]["color"] != "blue" {
		t.Errorf("unknown field color = %v, want blue", got[0]["color"])
	}
	// subType: {"name":"my","id":null} must keep its null id (the frontend
	// null-checks it), which is why metadata is carried as raw JSON.
	sub := got[2]["metadata"].(map[string]interface{})["subType"].(map[string]interface{})
	if v, ok := sub["id"]; !ok || v != nil {
		t.Errorf("subType.id = %v (present=%v), want present null", v, ok)
	}
}

func TestGreeterContent(t *testing.T) {
	if c, ok := greeterContent(parseLayout(t, sampleLayout)); !ok || c != "### Hi" {
		t.Errorf("greeterContent = %q, %v; want \"### Hi\", true", c, ok)
	}
	if _, ok := greeterContent(parseLayout(t, `[{"order":0,"title":"Apps","type":"applications","x":0,"y":0,"w":2,"h":1}]`)); ok {
		t.Error("greeterContent found a greeter in a layout without one")
	}
	if _, ok := greeterContent(nil); ok {
		t.Error("greeterContent found a greeter in an empty layout")
	}
}

func TestSetGreeterContentReplaces(t *testing.T) {
	ws, added, err := setGreeterContent(parseLayout(t, sampleLayout), "# New")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if added {
		t.Error("added = true, want false when a greeter already exists")
	}
	if len(ws) != 3 {
		t.Fatalf("got %d widgets, want 3", len(ws))
	}
	var md map[string]interface{}
	if err := json.Unmarshal(ws[0].Metadata, &md); err != nil {
		t.Fatalf("metadata: %v", err)
	}
	if md["content"] != "# New" {
		t.Errorf("content = %v, want # New", md["content"])
	}
	// Stale rendered HTML must not be sent back; the server recomputes it.
	if _, ok := md["renderedContent"]; ok {
		t.Errorf("renderedContent still present: %v", md["renderedContent"])
	}
	if v, ok := md["subType"]; !ok || v != nil {
		t.Errorf("subType = %v (present=%v), want present null", v, ok)
	}
	// Other widgets and geometry untouched.
	if ws[0].Y != 0 || ws[1].Y != 1 || ws[2].Y != 2 || ws[1].Order != 1 {
		t.Errorf("geometry/order changed: %+v", ws)
	}
	if string(ws[0].Extra["color"]) != `"blue"` {
		t.Errorf("unknown field lost: %s", ws[0].Extra["color"])
	}
}

func TestSetGreeterContentAddsWhenMissing(t *testing.T) {
	noGreeter := parseLayout(t, sampleLayout)[1:]
	ws, added, err := setGreeterContent(noGreeter, "# Hello")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !added {
		t.Error("added = false, want true")
	}
	if len(ws) != 3 {
		t.Fatalf("got %d widgets, want 3", len(ws))
	}
	g := ws[0]
	if g.Type != greeterWidgetType || g.Title != greeterDefaultTitle || g.Order != 0 ||
		g.X != 0 || g.Y != 0 || g.W != greeterDefaultW || g.H != greeterDefaultH {
		t.Errorf("greeter widget = %+v", g)
	}
	var md widgetMetadata
	if err := json.Unmarshal(g.Metadata, &md); err != nil || md.Content != "# Hello" {
		t.Errorf("greeter metadata = %s (err %v)", g.Metadata, err)
	}
	// Everything else shifted down one row and re-ordered after the greeter.
	if ws[1].Title != "Applications" || ws[1].Y != 2 || ws[1].Order != 1 {
		t.Errorf("Applications = %+v, want y=2 order=1", ws[1])
	}
	if ws[2].Title != "My Projects" || ws[2].Y != 3 || ws[2].Order != 2 {
		t.Errorf("My Projects = %+v, want y=3 order=2", ws[2])
	}
	// The payload must serialize with the fields the server requires.
	b, err := json.Marshal(ws[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"order"`, `"title"`, `"type"`, `"x"`, `"y"`, `"w"`, `"h"`, `"metadata"`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("serialized greeter lacks %s: %s", k, b)
		}
	}
}

func TestRemoveGreeter(t *testing.T) {
	ws, n := removeGreeter(parseLayout(t, sampleLayout))
	if n != 1 {
		t.Errorf("removed = %d, want 1", n)
	}
	if len(ws) != 2 || ws[0].Title != "Applications" || ws[1].Title != "My Projects" {
		t.Fatalf("widgets = %+v", ws)
	}
	if ws[0].Order != 0 || ws[1].Order != 1 {
		t.Errorf("order not renumbered: %d, %d", ws[0].Order, ws[1].Order)
	}
	again, n := removeGreeter(ws)
	if n != 0 || len(again) != 2 {
		t.Errorf("second remove: removed=%d len=%d", n, len(again))
	}
}

func TestApiErrorMessage(t *testing.T) {
	cases := map[string]string{
		`{"message":"Widget 1 is missing required fields!"}`: "Widget 1 is missing required fields!",
		`{"error":"boom"}`:      "boom",
		`<html>502 Bad Gateway`: "",
		`{"success":false}`:     "",
		``:                      "",
	}
	for in, want := range cases {
		if got := apiErrorMessage([]byte(in)); got != want {
			t.Errorf("apiErrorMessage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReadContentFromFileOrArgOrStdin(t *testing.T) {
	t.Run("file wins", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "landing.md")
		os.WriteFile(path, []byte("from file"), 0o644)
		got, err := readContentFromFileOrArgOrStdin(path, "from arg")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got != "from file" {
			t.Errorf("got %q, want %q", got, "from file")
		}
	})

	t.Run("arg used when no file", func(t *testing.T) {
		got, err := readContentFromFileOrArgOrStdin("", "from arg")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got != "from arg" {
			t.Errorf("got %q, want %q", got, "from arg")
		}
	})

	t.Run("missing file errors", func(t *testing.T) {
		_, err := readContentFromFileOrArgOrStdin(filepath.Join(t.TempDir(), "nope.md"), "")
		if err == nil {
			t.Error("expected error for missing file")
		}
	})
}
