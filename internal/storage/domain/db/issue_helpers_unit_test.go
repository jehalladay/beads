package db

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestFormatJSONStringArray covers the pure JSON-array formatter that stores
// TEXT-backed string slices (e.g. waiters). All three branches: empty→"",
// non-empty→marshaled JSON, and the shape of a multi-element result.
func TestFormatJSONStringArray(t *testing.T) {
	t.Parallel()

	if got := formatJSONStringArray(nil); got != "" {
		t.Errorf("nil slice: got %q, want empty string", got)
	}
	if got := formatJSONStringArray([]string{}); got != "" {
		t.Errorf("empty slice: got %q, want empty string", got)
	}

	got := formatJSONStringArray([]string{"a"})
	if got != `["a"]` {
		t.Errorf("single element: got %q, want %q", got, `["a"]`)
	}

	got = formatJSONStringArray([]string{"a", "b", "c"})
	// Round-trip to assert it is valid JSON preserving order and elements,
	// independent of exact spacing.
	var back []string
	if err := json.Unmarshal([]byte(got), &back); err != nil {
		t.Fatalf("result %q is not valid JSON: %v", got, err)
	}
	if len(back) != 3 || back[0] != "a" || back[1] != "b" || back[2] != "c" {
		t.Errorf("round-trip mismatch: got %v", back)
	}

	// A value containing characters that require JSON escaping must still
	// round-trip cleanly.
	got = formatJSONStringArray([]string{`quote"inside`, "new\nline"})
	back = nil
	if err := json.Unmarshal([]byte(got), &back); err != nil {
		t.Fatalf("escaped result %q is not valid JSON: %v", got, err)
	}
	if len(back) != 2 || back[0] != `quote"inside` || back[1] != "new\nline" {
		t.Errorf("escaped round-trip mismatch: got %v", back)
	}
}

// TestNormalizeUpdateValue covers every branch of the per-column update-value
// normalizer: timestamp fields (time.Time / *time.Time / nil-pointer / other),
// enum coercion (status, issue_type), metadata coercion (json.RawMessage /
// []byte / other), and the untouched-passthrough default.
func TestNormalizeUpdateValue(t *testing.T) {
	t.Parallel()

	// --- timestamp fields: time.Time is coerced to UTC ---
	loc := time.FixedZone("test+5", 5*3600)
	local := time.Date(2026, 7, 17, 12, 0, 0, 0, loc)
	for _, key := range []string{"started_at", "closed_at", "due_at", "defer_until"} {
		got := normalizeUpdateValue(key, local)
		gt, ok := got.(time.Time)
		if !ok {
			t.Fatalf("%s: got %T, want time.Time", key, got)
		}
		if gt.Location() != time.UTC {
			t.Errorf("%s: time not normalized to UTC: %v", key, gt.Location())
		}
		if !gt.Equal(local) {
			t.Errorf("%s: instant changed by normalization: got %v want %v", key, gt, local)
		}
	}

	// --- timestamp field: *time.Time is dereferenced + UTC-normalized ---
	got := normalizeUpdateValue("closed_at", &local)
	gt, ok := got.(time.Time)
	if !ok {
		t.Fatalf("*time.Time: got %T, want time.Time", got)
	}
	if gt.Location() != time.UTC || !gt.Equal(local) {
		t.Errorf("*time.Time not normalized: got %v", gt)
	}

	// --- timestamp field: nil *time.Time returns nil ---
	var nilTime *time.Time
	if got := normalizeUpdateValue("due_at", nilTime); got != nil {
		t.Errorf("nil *time.Time: got %v, want nil", got)
	}

	// --- timestamp field: a non-time value falls through unchanged ---
	if got := normalizeUpdateValue("started_at", "not-a-time"); got != "not-a-time" {
		t.Errorf("timestamp field non-time value: got %v, want passthrough", got)
	}

	// --- status: types.Status coerces to its string; other passes through ---
	if got := normalizeUpdateValue("status", types.StatusClosed); got != string(types.StatusClosed) {
		t.Errorf("status coercion: got %v (%T), want string %q", got, got, string(types.StatusClosed))
	}
	if got := normalizeUpdateValue("status", "already-a-string"); got != "already-a-string" {
		t.Errorf("status non-Status value: got %v, want passthrough", got)
	}

	// --- issue_type: types.IssueType coerces to its string; other passes through ---
	if got := normalizeUpdateValue("issue_type", types.TypeTask); got != string(types.TypeTask) {
		t.Errorf("issue_type coercion: got %v (%T), want string %q", got, got, string(types.TypeTask))
	}
	if got := normalizeUpdateValue("issue_type", 42); got != 42 {
		t.Errorf("issue_type non-IssueType value: got %v, want passthrough", got)
	}

	// --- metadata: json.RawMessage and []byte both coerce to string ---
	raw := json.RawMessage(`{"k":"v"}`)
	if got := normalizeUpdateValue("metadata", raw); got != `{"k":"v"}` {
		t.Errorf("metadata json.RawMessage: got %v, want string", got)
	}
	if got := normalizeUpdateValue("metadata", []byte(`{"a":1}`)); got != `{"a":1}` {
		t.Errorf("metadata []byte: got %v, want string", got)
	}
	if got := normalizeUpdateValue("metadata", `{"already":"string"}`); got != `{"already":"string"}` {
		t.Errorf("metadata string: got %v, want passthrough", got)
	}

	// --- default: an unrecognized column passes its value through untouched ---
	if got := normalizeUpdateValue("title", "hello"); got != "hello" {
		t.Errorf("default passthrough: got %v, want %q", got, "hello")
	}
	if got := normalizeUpdateValue("priority", 3); got != 3 {
		t.Errorf("default passthrough int: got %v, want 3", got)
	}
}
