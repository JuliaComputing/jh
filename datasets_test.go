package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCustomTimeUnmarshalJSON(t *testing.T) {
	t.Run("RFC3339 with timezone", func(t *testing.T) {
		var ct CustomTime
		if err := json.Unmarshal([]byte(`"2025-01-02T03:04:05Z"`), &ct); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		want := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
		if !ct.Time.Equal(want) {
			t.Errorf("got %v, want %v", ct.Time, want)
		}
	})

	t.Run("no timezone with milliseconds (assumed UTC)", func(t *testing.T) {
		var ct CustomTime
		if err := json.Unmarshal([]byte(`"2025-01-02T03:04:05.5"`), &ct); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		want := time.Date(2025, 1, 2, 3, 4, 5, int(500*time.Millisecond), time.UTC)
		if !ct.Time.Equal(want) {
			t.Errorf("got %v, want %v", ct.Time, want)
		}
		if ct.Time.Location() != time.UTC {
			t.Errorf("expected UTC location, got %v", ct.Time.Location())
		}
	})

	t.Run("no timezone, no milliseconds (assumed UTC)", func(t *testing.T) {
		var ct CustomTime
		if err := json.Unmarshal([]byte(`"2025-01-02T03:04:05"`), &ct); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		want := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
		if !ct.Time.Equal(want) {
			t.Errorf("got %v, want %v", ct.Time, want)
		}
	})

	t.Run("null yields zero time", func(t *testing.T) {
		var ct CustomTime
		if err := json.Unmarshal([]byte(`null`), &ct); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !ct.Time.IsZero() {
			t.Errorf("expected zero time for null, got %v", ct.Time)
		}
	})

	t.Run("unparseable value errors", func(t *testing.T) {
		var ct CustomTime
		if err := json.Unmarshal([]byte(`"not-a-date"`), &ct); err == nil {
			t.Error("expected error for unparseable time")
		}
	})
}

// resolveDatasetIdentifier short-circuits on a UUID-shaped identifier before any
// network call, so that branch is unit-testable with a nil token.
func TestResolveDatasetIdentifierUUIDPassthrough(t *testing.T) {
	uuid := "12345678-1234-1234-1234-123456789abc"
	got, err := resolveDatasetIdentifier("", uuid, nil)
	if err != nil {
		t.Fatalf("resolveDatasetIdentifier: %v", err)
	}
	if got != uuid {
		t.Errorf("got %q, want the UUID passed through unchanged", got)
	}
}
