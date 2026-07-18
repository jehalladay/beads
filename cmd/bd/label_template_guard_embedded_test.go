//go:build cgo

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// minimalTemplateFormula is a tiny workflow formula. `bd cook --persist` on it
// creates a proto MOLECULE with IsTemplate=true (id "tguard-tmpl"), which is
// exactly the read-only artifact the NotTemplate guard protects.
const minimalTemplateFormula = `formula = "tguard-tmpl"
description = "template-guard parity test proto"
version = 1
type = "workflow"

[[steps]]
id = "only"
title = "only step"
description = "single step so the proto is valid"
`

// cookTemplate writes the formula to dir and persists it, returning the proto
// (template) id. It fails the test if cook does not succeed.
func cookTemplate(t *testing.T, bd, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "tguard.formula.toml")
	if err := os.WriteFile(path, []byte(minimalTemplateFormula), 0o644); err != nil {
		t.Fatalf("write formula: %v", err)
	}
	cmd := exec.Command(bd, "cook", path, "--persist", "--force")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bd cook --persist failed: %v\n%s", err, out)
	}
	return "tguard-tmpl"
}

// TestEmbeddedLabelTemplateGuard asserts that `bd label add`/`bd label remove`
// refuse to mutate a read-only template molecule, matching the NotTemplate
// guard that `bd tag` already enforces (beads-dwlg). Before the fix, tag
// rejected a template while label add/remove silently mutated it — a
// shorthand-stricter-than-full-command guard-parity gap.
func TestEmbeddedLabelTemplateGuard(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "tg")

	tmpl := cookTemplate(t, bd, dir)

	// Baseline parity anchor: bd tag already rejects a template.
	tagOut := bdLabelTagFail(t, bd, dir, tmpl, "sneaky")
	if !strings.Contains(tagOut, "template") {
		t.Errorf("bd tag on a template should mention 'template': %s", tagOut)
	}

	t.Run("label_add_rejects_template", func(t *testing.T) {
		out := bdLabelFail(t, bd, dir, "add", tmpl, "sneaky")
		if !strings.Contains(out, "template") {
			t.Errorf("expected a 'template' read-only error, got: %s", out)
		}
	})

	t.Run("label_remove_rejects_template", func(t *testing.T) {
		out := bdLabelFail(t, bd, dir, "remove", tmpl, "sneaky")
		if !strings.Contains(out, "template") {
			t.Errorf("expected a 'template' read-only error, got: %s", out)
		}
	})

	// A normal (non-template) issue in the same command still works — the guard
	// is template-specific, not a blanket block.
	t.Run("label_add_normal_issue_still_works", func(t *testing.T) {
		issue := bdCreate(t, bd, dir, "normal", "--type", "task")
		bdLabel(t, bd, dir, "add", issue.ID, "ok")
		labels := bdLabelListJSON(t, bd, dir, issue.ID)
		found := false
		for _, l := range labels {
			if l == "ok" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected 'ok' label on a normal issue: %v", labels)
		}
	})
}

// bdLabelTagFail runs `bd tag <id> <label>` and asserts it fails, returning
// combined output. Anchors the parity baseline for the label-guard test.
func bdLabelTagFail(t *testing.T, bd, dir, id, label string) string {
	t.Helper()
	cmd := exec.Command(bd, "tag", id, label)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected bd tag %s %s to fail, but succeeded:\n%s", id, label, out)
	}
	return string(out)
}
