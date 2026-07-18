//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"
)

// TestEmbeddedDepTreeFormatValidation is the beads-n95d regression: `bd dep
// tree --format <invalid>` must error (exit != 0) instead of silently falling
// back to the default text tree render. Before the fix, dep tree only
// special-cased --format json and --format mermaid; any other value fell
// straight through to the default output with no error — the same silent-accept
// false-green the sort-field (y04n) and status (p330) validation closed on the
// sibling read commands. --direction and --max-depth on this same command
// already fail loud on invalid input; --format did not.
func TestEmbeddedDepTreeFormatValidation(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "dtf")

	epic := bdCreate(t, bd, dir, "n95d epic", "--type", "epic")
	child := bdCreate(t, bd, dir, "n95d child", "--type", "task")
	// parent-child edge so the tree is non-trivial (proves the invalid-format
	// path aborts BEFORE rendering, not just because the tree is empty).
	bdDep(t, bd, dir, "add", child.ID, epic.ID, "--type", "parent-child")

	t.Run("invalid_format_errors", func(t *testing.T) {
		out := bdDepFail(t, bd, dir, "tree", epic.ID, "--format", "bogusxyz")
		if !strings.Contains(out, "invalid") || !strings.Contains(out, "format") {
			t.Errorf("expected 'invalid ... format' error, got: %s", out)
		}
	})

	t.Run("dot_not_valid_for_dep_tree", func(t *testing.T) {
		// 'dot'/'digraph' ARE valid for `bd list --format` but NOT for `bd dep
		// tree` — a user carrying the list mental model must get a hard error,
		// not silent default text.
		out := bdDepFail(t, bd, dir, "tree", epic.ID, "--format", "dot")
		if !strings.Contains(out, "invalid") || !strings.Contains(out, "format") {
			t.Errorf("expected 'invalid ... format' error for dot, got: %s", out)
		}
	})

	t.Run("mermaid_still_succeeds", func(t *testing.T) {
		out := bdDep(t, bd, dir, "tree", epic.ID, "--format", "mermaid")
		// mermaid flowchart output uses a "flowchart"/"graph" header.
		if !strings.Contains(out, "flowchart") && !strings.Contains(out, "graph") {
			t.Errorf("expected mermaid flowchart output, got: %s", out)
		}
	})

	t.Run("json_still_succeeds", func(t *testing.T) {
		out := bdDep(t, bd, dir, "tree", epic.ID, "--format", "json")
		if !strings.Contains(out, epic.ID) {
			t.Errorf("expected epic id in json output, got: %s", out)
		}
	})

	t.Run("no_format_default_text_succeeds", func(t *testing.T) {
		out := bdDep(t, bd, dir, "tree", epic.ID)
		if !strings.Contains(out, epic.ID) {
			t.Errorf("expected epic id in default text tree, got: %s", out)
		}
	})
}
