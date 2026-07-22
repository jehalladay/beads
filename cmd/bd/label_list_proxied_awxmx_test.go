//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerLabelList is the teeth for beads-awxmx: `bd label list <id>`
// and `bd label list-all` must WORK in proxied-server mode. Before the fix,
// `label list` resolved+read through the direct nil global `store` (storage is
// nil), and `label list-all` called store.SearchIssues on the nil interface
// (panic) — the READ siblings of the aocj/ouxlo label sweep-misses (add/remove/
// propagate were routed). Both are now routed via usesProxiedServer().
func TestProxiedServerLabelList(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("list_shows_labels", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "lll1")
		issue := bdProxiedCreate(t, bd, p.dir, "Labeled issue", "--type", "task")
		if _, err := bdProxiedRun(t, bd, p.dir, "label", "add", issue.ID, "backend"); err != nil {
			t.Fatalf("label add setup failed: %v", err)
		}

		out, err := bdProxiedRun(t, bd, p.dir, "label", "list", issue.ID)
		s := string(out)
		if err != nil {
			t.Fatalf("proxied label list failed: %v\n%s", err, s)
		}
		if strings.Contains(s, "storage is nil") {
			t.Fatalf("proxied label list hit the nil-store path (beads-awxmx): %s", s)
		}
		if !strings.Contains(s, "backend") {
			t.Errorf("expected 'backend' label in output, got: %s", s)
		}
	})

	t.Run("list_json", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "lll2")
		issue := bdProxiedCreate(t, bd, p.dir, "JSON labeled", "--type", "task")
		if _, err := bdProxiedRun(t, bd, p.dir, "label", "add", issue.ID, "team:core"); err != nil {
			t.Fatalf("label add setup failed: %v", err)
		}

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "label", "list", issue.ID, "--json")
		if err != nil {
			t.Fatalf("proxied label list --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout, "storage is nil") || strings.Contains(stderr, "storage is nil") {
			t.Fatalf("proxied label list --json hit the nil-store path (beads-awxmx)\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, "team:core") {
			t.Errorf("expected 'team:core' in JSON output, got:\n%s", stdout)
		}
	})

	t.Run("list_no_labels_is_clean", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "lll3")
		bare := bdProxiedCreate(t, bd, p.dir, "No labels", "--type", "task")

		out, err := bdProxiedRun(t, bd, p.dir, "label", "list", bare.ID)
		s := string(out)
		if err != nil {
			t.Fatalf("proxied label list on unlabeled issue should succeed: %v\n%s", err, s)
		}
		if strings.Contains(s, "storage is nil") {
			t.Fatalf("proxied label list hit the nil-store path (beads-awxmx): %s", s)
		}
		if !strings.Contains(s, "has no labels") {
			t.Errorf("expected 'has no labels' for an unlabeled issue, got: %s", s)
		}
	})

	t.Run("list_all_aggregates", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "lll4")
		a := bdProxiedCreate(t, bd, p.dir, "Issue A", "--type", "task")
		b := bdProxiedCreate(t, bd, p.dir, "Issue B", "--type", "task")
		if _, err := bdProxiedRun(t, bd, p.dir, "label", "add", a.ID, "shared"); err != nil {
			t.Fatalf("label add setup a failed: %v", err)
		}
		if _, err := bdProxiedRun(t, bd, p.dir, "label", "add", b.ID, "shared"); err != nil {
			t.Fatalf("label add setup b failed: %v", err)
		}
		if _, err := bdProxiedRun(t, bd, p.dir, "label", "add", b.ID, "solo"); err != nil {
			t.Fatalf("label add setup solo failed: %v", err)
		}

		out, err := bdProxiedRun(t, bd, p.dir, "label", "list-all")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied label list-all failed (nil-store panic pre-fix): %v\n%s", err, s)
		}
		if strings.Contains(s, "storage is nil") || strings.Contains(s, "panic") {
			t.Fatalf("proxied label list-all hit the nil-store path (beads-awxmx): %s", s)
		}
		if !strings.Contains(s, "shared") || !strings.Contains(s, "solo") {
			t.Errorf("expected both 'shared' and 'solo' labels aggregated, got: %s", s)
		}
	})

	t.Run("list_all_json", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "lll5")
		issue := bdProxiedCreate(t, bd, p.dir, "JSON all", "--type", "task")
		if _, err := bdProxiedRun(t, bd, p.dir, "label", "add", issue.ID, "counted"); err != nil {
			t.Fatalf("label add setup failed: %v", err)
		}

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "label", "list-all", "--json")
		if err != nil {
			t.Fatalf("proxied label list-all --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout, "storage is nil") || strings.Contains(stderr, "storage is nil") {
			t.Fatalf("proxied label list-all --json hit the nil-store path (beads-awxmx)\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, "counted") || !strings.Contains(stdout, "\"count\"") {
			t.Errorf("expected label 'counted' with a count field in JSON, got:\n%s", stdout)
		}
	})
}
