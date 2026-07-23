//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-b3k8s: `bd ready` and `bd blocked` must default-exclude persisted
// template molecule protos, at parity with bd list/count/export (which already
// exclude via the filter.go:280 IsTemplate predicate). A persisted proto is the
// is_template COLUMN (formula-cooked, e.g. `bd cook --persist` steps) OR the
// `template` LABEL (canonical `bd create --label template`, is_template=NULL) —
// the fix delegates to the same column-OR-label predicate on both surfaces and
// adds a `--include-templates` opt-in. Before the fix, cooked proto STEPS
// (type=task, is_template=1) and label-template issues leaked into ready/blocked
// (the type-exclusion drops molecule/epic roots but not the task-typed steps).
//
// These teeth assert exclusion-by-default + opt-in on BOTH representations and
// BOTH commands. The mutation check (feature disabled -> template leaks) was
// verified manually against a RED build during development.

// readyIDsJSON runs `bd ready <extra...> --json` in dir and returns the result
// issue IDs. Fatals on error or unparseable JSON.
func readyIDsJSON(t *testing.T, bd, dir string, extra ...string) []string {
	t.Helper()
	args := append([]string{"ready", "--json"}, extra...)
	cmd := exec.Command(bd, args...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd %v failed: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
	}
	return parseIssueIDs(t, stdout.String())
}

// blockedIDsJSON runs `bd blocked <extra...> --json` in dir and returns the
// result issue IDs. Fatals on error or unparseable JSON.
func blockedIDsJSON(t *testing.T, bd, dir string, extra ...string) []string {
	t.Helper()
	args := append([]string{"blocked", "--json"}, extra...)
	cmd := exec.Command(bd, args...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd %v failed: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
	}
	var blocked []*types.BlockedIssue
	s := strings.TrimSpace(stdout.String())
	start := strings.IndexAny(s, "[{")
	if start < 0 {
		return nil
	}
	if err := json.Unmarshal([]byte(s[start:]), &blocked); err != nil {
		t.Fatalf("invalid JSON in blocked --json: %v\n%s", err, s[start:])
	}
	ids := make([]string, 0, len(blocked))
	for _, b := range blocked {
		ids = append(ids, b.ID)
	}
	return ids
}

// parseIssueIDs extracts issue IDs from a `--json` list payload that may be a
// bare array or an object with an "issues" key.
func parseIssueIDs(t *testing.T, out string) []string {
	t.Helper()
	s := strings.TrimSpace(out)
	start := strings.IndexAny(s, "[{")
	if start < 0 {
		return nil
	}
	body := s[start:]
	var arr []map[string]any
	if err := json.Unmarshal([]byte(body), &arr); err != nil {
		var obj struct {
			Issues []map[string]any `json:"issues"`
		}
		if err2 := json.Unmarshal([]byte(body), &obj); err2 != nil {
			t.Fatalf("invalid JSON in ready --json: %v / %v\n%s", err, err2, body)
		}
		arr = obj.Issues
	}
	ids := make([]string, 0, len(arr))
	for _, m := range arr {
		if id, ok := m["id"].(string); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

func b3k8sHasID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// b3k8sTemplateFormula cooks to a proto molecule whose single step persists as
// a task-typed row with the is_template COLUMN set — the column-based proto
// representation (distinct from the `template` LABEL case).
const b3k8sTemplateFormula = `formula = "rbt-tmpl"
description = "b3k8s ready/blocked template exclusion proto"
version = 1
type = "workflow"

[[steps]]
id = "s1"
title = "proto step one"
description = "single step so the proto is valid"
`

// TestEmbeddedReadyTemplateExclusion asserts `bd ready` default-excludes both
// representations of a persisted template proto (the is_template COLUMN via a
// cooked proto step, and the `template` LABEL via bd create) and that
// --include-templates opts them back in.
func TestEmbeddedReadyTemplateExclusion(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "rbt")

	// A plain ready task (the control — always present).
	regular := bdCreate(t, bd, dir, "regular ready task", "--type", "task")

	// LABEL representation: a task carrying the `template` label, is_template NULL.
	labelTmpl := bdCreate(t, bd, dir, "label template task", "--type", "task", "--label", "template")

	// COLUMN representation: cook+persist a proto so its step row has is_template=1.
	path := filepath.Join(dir, "rbt.formula.toml")
	if err := os.WriteFile(path, []byte(b3k8sTemplateFormula), 0o644); err != nil {
		t.Fatalf("write formula: %v", err)
	}
	cmd := exec.Command(bd, "cook", path, "--persist", "--force")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bd cook --persist failed: %v\n%s", err, out)
	}
	const cookedStep = "rbt-tmpl.s1"

	// Default: the regular task is present; NEITHER template representation is.
	ids := readyIDsJSON(t, bd, dir)
	if !b3k8sHasID(ids, regular.ID) {
		t.Errorf("regular task %s missing from bd ready: %v", regular.ID, ids)
	}
	if b3k8sHasID(ids, labelTmpl.ID) {
		t.Errorf("label-template %s leaked into bd ready (should be excluded by default): %v", labelTmpl.ID, ids)
	}
	if b3k8sHasID(ids, cookedStep) {
		t.Errorf("cooked proto step %s leaked into bd ready (should be excluded by default): %v", cookedStep, ids)
	}

	// --include-templates: BOTH template representations reappear.
	incIDs := readyIDsJSON(t, bd, dir, "--include-templates")
	if !b3k8sHasID(incIDs, regular.ID) {
		t.Errorf("regular task %s missing from bd ready --include-templates: %v", regular.ID, incIDs)
	}
	if !b3k8sHasID(incIDs, labelTmpl.ID) {
		t.Errorf("label-template %s missing from bd ready --include-templates: %v", labelTmpl.ID, incIDs)
	}
	if !b3k8sHasID(incIDs, cookedStep) {
		t.Errorf("cooked proto step %s missing from bd ready --include-templates: %v", cookedStep, incIDs)
	}
}

// TestEmbeddedBlockedTemplateExclusion asserts `bd blocked` default-excludes a
// blocked template-labeled proto and that --include-templates opts it back in,
// at parity with the bd ready surface above and bd list/count/export.
func TestEmbeddedBlockedTemplateExclusion(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "bte")

	blocker := bdCreate(t, bd, dir, "shared blocker", "--type", "task")
	blockedRegular := bdCreate(t, bd, dir, "regular blocked", "--type", "task")
	blockedTmpl := bdCreate(t, bd, dir, "template blocked", "--type", "task", "--label", "template")

	for _, dep := range [][]string{
		{"dep", "add", blockedRegular.ID, blocker.ID},
		{"dep", "add", blockedTmpl.ID, blocker.ID},
	} {
		cmd := exec.Command(bd, dep...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("dep add %v failed: %v\n%s", dep, err, out)
		}
	}

	// Default: the regular blocked issue is present; the template one is not.
	ids := blockedIDsJSON(t, bd, dir)
	if !b3k8sHasID(ids, blockedRegular.ID) {
		t.Errorf("regular blocked %s missing from bd blocked: %v", blockedRegular.ID, ids)
	}
	if b3k8sHasID(ids, blockedTmpl.ID) {
		t.Errorf("template blocked %s leaked into bd blocked (should be excluded by default): %v", blockedTmpl.ID, ids)
	}

	// --include-templates: the template blocked issue reappears alongside the regular one.
	incIDs := blockedIDsJSON(t, bd, dir, "--include-templates")
	if !b3k8sHasID(incIDs, blockedRegular.ID) {
		t.Errorf("regular blocked %s missing from bd blocked --include-templates: %v", blockedRegular.ID, incIDs)
	}
	if !b3k8sHasID(incIDs, blockedTmpl.ID) {
		t.Errorf("template blocked %s missing from bd blocked --include-templates: %v", blockedTmpl.ID, incIDs)
	}
}
