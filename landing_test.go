package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestHomepageResponseUnmarshal(t *testing.T) {
	t.Run("object message", func(t *testing.T) {
		var r homepageResponse
		body := `{"success":true,"message":{"md":"# Hi","updated_at":"2025-01-02T03:04:05Z"}}`
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !r.Success {
			t.Error("Success = false, want true")
		}
		if r.Message == nil || r.Message.Md != "# Hi" {
			t.Errorf("Message = %+v, want md=# Hi", r.Message)
		}
	})

	t.Run("string message leaves struct nil", func(t *testing.T) {
		var r homepageResponse
		body := `{"success":false,"message":"permission denied"}`
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if r.Success {
			t.Error("Success = true, want false")
		}
		if r.Message != nil {
			t.Errorf("Message = %+v, want nil for a string message", r.Message)
		}
		var msg string
		if err := json.Unmarshal(r.RawMsg, &msg); err != nil || msg != "permission denied" {
			t.Errorf("RawMsg = %q (err %v), want \"permission denied\"", msg, err)
		}
	})

	t.Run("success with null message", func(t *testing.T) {
		var r homepageResponse
		if err := json.Unmarshal([]byte(`{"success":true,"message":null}`), &r); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !r.Success || r.Message != nil {
			t.Errorf("got success=%v message=%+v, want success=true message=nil", r.Success, r.Message)
		}
	})
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
