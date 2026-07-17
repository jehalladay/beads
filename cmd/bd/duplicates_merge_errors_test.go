package main

import "testing"

// TestCollectMergeErrors verifies beads-tg0f: per-merge errors recorded by
// performMerge in result["errors"] must be collected so the caller can both
// surface them (text mode) and set a non-zero exit code. Previously the text
// path printed only "Merged N group(s)" and returned nil, so a merge that
// failed to close/link/reparent an issue was reported as a clean success.
func TestCollectMergeErrors(t *testing.T) {
	t.Run("no errors -> empty", func(t *testing.T) {
		results := []map[string]interface{}{
			{"target": "bd-1", "closed": []string{"bd-2"}, "errors": []string{}},
			{"target": "bd-3", "closed": []string{"bd-4"}, "errors": []string{}},
		}
		if got := collectMergeErrors(results); len(got) != 0 {
			t.Errorf("expected no errors, got %d: %v", len(got), got)
		}
	})

	t.Run("errors across groups are flattened", func(t *testing.T) {
		results := []map[string]interface{}{
			{"target": "bd-1", "errors": []string{"failed to close bd-2: boom"}},
			{"target": "bd-3", "errors": []string{}},
			{"target": "bd-5", "errors": []string{"failed to link bd-6 to bd-5: nope", "failed to reparent bd-7: bad"}},
		}
		got := collectMergeErrors(results)
		if len(got) != 3 {
			t.Fatalf("expected 3 flattened errors, got %d: %v", len(got), got)
		}
		want := "failed to close bd-2: boom"
		if got[0] != want {
			t.Errorf("first error = %q, want %q", got[0], want)
		}
	})

	t.Run("missing or non-slice errors key is ignored safely", func(t *testing.T) {
		results := []map[string]interface{}{
			{"target": "bd-1"},                 // no errors key
			{"target": "bd-3", "errors": nil},  // nil
			{"target": "bd-5", "errors": 42},   // wrong type
			{"target": "bd-7", "errors": []string{"real error"}},
		}
		got := collectMergeErrors(results)
		if len(got) != 1 || got[0] != "real error" {
			t.Errorf("expected exactly the one real error, got %v", got)
		}
	})
}
