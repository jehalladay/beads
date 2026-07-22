//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedServerLint proves bd lint is proxied-server-aware (beads-hquo8):
// lint.go hard-failed "database not initialized" for hub (proxiedServerMode)
// crew because it gated every read (GetIssue, SearchIssues, filter-config, the
// closed-epic-open-children scan) on the nil global `store`. Since `bd lint
// $IDS || fail` is a documented CI gate, that made lint unusable for hub crew.
// The fix routes all reads through a lintBackend over the proxied UOW when
// usesProxiedServer() (read-divergence sibling of info/6pjl6, orphans/ktlo,
// count, show). This exercises: the default full-scan template lint, the
// explicit-id path, --json exit parity, and the structural inconsistency scan
// (closed epic with an open child), all against a real proxied Dolt.
func TestProxiedServerLint(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("template_warnings_json", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "plw")
		// A bug with no description/AC is missing "Steps to Reproduce" and
		// "Acceptance Criteria" → LintIssue flags it. Proves lint READS the
		// populated proxied DB rather than hard-failing store-nil.
		bug := bdProxiedCreate(t, bd, p.dir, "bug no sections", "--type", "bug")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "lint", "--json")
		// beads-x3jo: lint returns rc=1 when there are warnings, so a non-nil err
		// with an exit status is expected here — what must NOT happen is a
		// store-nil hard-fail.
		combined := stdout + stderr
		if strings.Contains(combined, "database not initialized") ||
			strings.Contains(combined, "storage is nil") {
			t.Fatalf("bd lint hit a store-nil hard-fail in proxied mode:\nerr:%v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		start := strings.Index(stdout, "{")
		if start < 0 {
			t.Fatalf("no JSON object in lint output:\n%s", stdout)
		}
		var out struct {
			Total   int `json:"total"`
			Issues  int `json:"issues"`
			Results []struct {
				ID      string   `json:"id"`
				Missing []string `json:"missing"`
			} `json:"results"`
		}
		if err := json.Unmarshal([]byte(stdout[start:]), &out); err != nil {
			t.Fatalf("parse lint JSON: %v\nraw: %s", err, stdout[start:])
		}
		if out.Issues < 1 || out.Total < 1 {
			t.Fatalf("expected at least one template warning for the section-less bug, got issues=%d total=%d:\n%s", out.Issues, out.Total, stdout)
		}
		found := false
		for _, r := range out.Results {
			if r.ID == bug.ID && len(r.Missing) > 0 {
				found = true
			}
		}
		if !found {
			t.Errorf("expected bug %s in lint results with missing sections:\n%s", bug.ID, stdout)
		}
	})

	t.Run("explicit_ids_and_clean", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pei")
		// A well-formed task with acceptance criteria passes lint clean.
		clean := bdProxiedCreate(t, bd, p.dir, "clean task", "--type", "task",
			"--acceptance", "Given/When/Then it works")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "lint", clean.ID)
		if err != nil {
			t.Fatalf("bd lint <clean-id> should exit 0, got err=%v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		combined := stdout + stderr
		if strings.Contains(combined, "database not initialized") ||
			strings.Contains(combined, "storage is nil") {
			t.Fatalf("explicit-id lint hit a store-nil hard-fail:\n%s\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, "No template warnings") {
			t.Errorf("expected clean lint result for %s:\n%s", clean.ID, stdout)
		}

		// A non-existent id must make the gate exit non-zero (beads-p3y5),
		// reached via the proxied GetIssue path.
		_, _, missErr := bdProxiedRunBuffers(t, bd, p.dir, "lint", "bd-doesnotexist")
		if missErr == nil {
			t.Errorf("expected lint of a missing id to exit non-zero (p3y5 partial-failure)")
		}
	})

	t.Run("closed_epic_open_children_scan", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pcs")
		epic := bdProxiedCreate(t, bd, p.dir, "closed epic", "-t", "epic",
			"--acceptance", "Success criteria: done")
		child := bdProxiedCreate(t, bd, p.dir, "still open child", "--parent", epic.ID,
			"--acceptance", "Acceptance Criteria: ok")
		// --force lets the epic close while its child stays open, reaching the
		// closed_epic_with_open_children state the scan flags (beads-4u7d).
		bdProxiedClose(t, bd, p.dir, epic.ID, "--force")

		stdout, stderr, _ := bdProxiedRunBuffers(t, bd, p.dir, "lint", "--json")
		combined := stdout + stderr
		if strings.Contains(combined, "database not initialized") ||
			strings.Contains(combined, "storage is nil") {
			t.Fatalf("inconsistency-scan lint hit a store-nil hard-fail:\n%s\n%s", stdout, stderr)
		}
		start := strings.Index(stdout, "{")
		if start < 0 {
			t.Fatalf("no JSON object in lint output:\n%s", stdout)
		}
		var out struct {
			Inconsistencies []struct {
				ID           string   `json:"id"`
				Kind         string   `json:"kind"`
				OpenChildren []string `json:"open_children"`
			} `json:"inconsistencies"`
		}
		if err := json.Unmarshal([]byte(stdout[start:]), &out); err != nil {
			t.Fatalf("parse lint JSON: %v\nraw: %s", err, stdout[start:])
		}
		found := false
		for _, inc := range out.Inconsistencies {
			if inc.ID == epic.ID && inc.Kind == "closed_epic_with_open_children" {
				for _, oc := range inc.OpenChildren {
					if oc == child.ID {
						found = true
					}
				}
			}
		}
		if !found {
			t.Fatalf("expected proxied inconsistency scan to flag closed epic %s with open child %s:\n%s", epic.ID, child.ID, stdout)
		}
	})
}
