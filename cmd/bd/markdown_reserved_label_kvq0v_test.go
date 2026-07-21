package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCreateIssuesFromMarkdown_RejectsReservedIdentityLabel verifies beads-kvq0v:
// `bd create --file <f>` rejects a node whose untrusted "### Labels" section
// carries a reserved gt identity label (gt:agent/gt:role/gt:rig) on a non-gt-
// internal write, matching single `bd create` (create.go:200), `bd label add`,
// and graph create (beads-f8fvh). These labels hide a bead from `bd ready`, so
// a hand-authored markdown label would silently hide real work — the same
// spoof vector beads-3c4g closed at the write time. The guard runs before the
// dry-run branch, so --dry-run (which needs no store) exercises it directly.
func TestCreateIssuesFromMarkdown_RejectsReservedIdentityLabel(t *testing.T) {
	t.Setenv(gtInternalEnv, "")
	for _, label := range []string{"gt:agent", "gt:role", "gt:rig"} {
		tmpDir := t.TempDir()
		md := "## Spoof Task\n\nBody.\n\n### Labels\n\n" + label + "\n"
		mdPath := filepath.Join(tmpDir, "spoof.md")
		if err := os.WriteFile(mdPath, []byte(md), 0644); err != nil {
			t.Fatal(err)
		}
		// Dry-run: no store needed; the guard fires before the preview.
		// HandleErrorRespectJSON prints "Error: <msg>" to stderr and returns a
		// bare exitError, so assert the rejection via a non-nil error AND the
		// label named in the captured stderr message.
		var err error
		stderr := captureHookStderr(t, func() {
			err = createIssuesFromMarkdown(nil, mdPath, true)
		})
		if err == nil {
			t.Errorf("createIssuesFromMarkdown should reject reserved identity label %q (spoof vector), got nil", label)
			continue
		}
		if !strings.Contains(stderr, label) {
			t.Errorf("rejection for %q should name the reserved label; stderr = %q", label, stderr)
		}
	}
}

// TestCreateIssuesFromMarkdown_ReservedLabelAllowedWithGTInternal verifies the
// fix does not break gt's own registration: with GT_INTERNAL set, a markdown
// batch may carry identity labels, matching single/graph create gating.
func TestCreateIssuesFromMarkdown_ReservedLabelAllowedWithGTInternal(t *testing.T) {
	t.Setenv(gtInternalEnv, gtInternalValue)
	tmpDir := t.TempDir()
	md := "## GT Registration\n\nBody.\n\n### Labels\n\ngt:agent, gt:role\n"
	mdPath := filepath.Join(tmpDir, "gt.md")
	if err := os.WriteFile(mdPath, []byte(md), 0644); err != nil {
		t.Fatal(err)
	}
	// Dry-run so no store is required; the reserved-label guard must NOT fire.
	if err := createIssuesFromMarkdown(nil, mdPath, true); err != nil {
		t.Errorf("createIssuesFromMarkdown with GT_INTERNAL set should allow reserved identity labels, got %v", err)
	}
}

// TestCreateIssuesFromMarkdown_NonReservedLabelsUnaffected verifies the fix
// does not over-reach: ordinary markdown labels still pass the guard.
func TestCreateIssuesFromMarkdown_NonReservedLabelsUnaffected(t *testing.T) {
	t.Setenv(gtInternalEnv, "")
	tmpDir := t.TempDir()
	md := "## Normal Task\n\nBody.\n\n### Labels\n\narea:cli, needs review\n"
	mdPath := filepath.Join(tmpDir, "normal.md")
	if err := os.WriteFile(mdPath, []byte(md), 0644); err != nil {
		t.Fatal(err)
	}
	if err := createIssuesFromMarkdown(nil, mdPath, true); err != nil {
		t.Errorf("createIssuesFromMarkdown should accept non-reserved labels, got %v", err)
	}
}
