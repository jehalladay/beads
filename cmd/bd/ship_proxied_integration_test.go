//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerShip proves bd ship is proxied-server-aware (beads-kjda):
// ship reads issues by the export:<cap> label then adds provides:<cap>. Before
// the fix it used the nil global `store` in proxiedServerMode → "storage is
// nil". The by-label read GetIssuesByLabel was NOT on any UOW use-case (only
// GetLabels/AddLabel were), so the fix is an interface-extension leg —
// GetIssuesByLabel added to IssueUseCase (backed by issueops.GetIssuesByLabelInTx
// widened *sql.Tx→DBTX) + proxied CLI routing.
func TestProxiedServerShip(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("ship_adds_provides_label", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "kjs")
		issue := bdProxiedCreate(t, bd, p.dir, "Ship target", "--type", "task")
		// Tag it with the export label and close it (ship requires closed).
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--add-label", "export:mycap")
		bdProxiedClose(t, bd, p.dir, issue.ID)

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "ship", "mycap")
		if err != nil {
			t.Fatalf("bd ship failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd ship hit 'storage is nil' in proxied mode:\n%s\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, "Shipped") && !strings.Contains(stdout, "provides:mycap") {
			t.Errorf("expected ship success output:\n%s", stdout)
		}
		// The provides label must now be present.
		shown := bdProxiedShowRaw(t, bd, p.dir, issue.ID)
		if !strings.Contains(shown, "provides:mycap") {
			t.Errorf("expected provides:mycap label after ship:\n%s", shown)
		}
	})

	t.Run("ship_json", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "kjj")
		issue := bdProxiedCreate(t, bd, p.dir, "Ship json", "--type", "task")
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--add-label", "export:jcap")
		bdProxiedClose(t, bd, p.dir, issue.ID)

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "ship", "jcap", "--json")
		if err != nil {
			t.Fatalf("bd ship --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd ship --json hit 'storage is nil':\n%s\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, `"shipped"`) || !strings.Contains(stdout, "jcap") {
			t.Errorf("expected shipped JSON payload:\n%s", stdout)
		}
	})

	t.Run("ship_dry_run", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "kjd")
		issue := bdProxiedCreate(t, bd, p.dir, "Ship dry", "--type", "task")
		bdProxiedUpdateOne(t, bd, p.dir, issue.ID, "--add-label", "export:dcap")
		bdProxiedClose(t, bd, p.dir, issue.ID)

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "ship", "dcap", "--dry-run")
		if err != nil {
			t.Fatalf("bd ship --dry-run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd ship --dry-run hit 'storage is nil':\n%s\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, "dry run") {
			t.Errorf("expected dry-run output:\n%s", stdout)
		}
		// Dry-run must NOT have added the label.
		shown := bdProxiedShowRaw(t, bd, p.dir, issue.ID)
		if strings.Contains(shown, "provides:dcap") {
			t.Errorf("dry-run should not have added provides:dcap:\n%s", shown)
		}
	})

	t.Run("ship_no_export_label_fails", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "kjn")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "ship", "nonexistent-cap")
		if err == nil {
			t.Fatalf("expected ship with no matching export label to fail; got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("no-export-label path hit 'storage is nil' rather than a clean not-found:\n%s\n%s", stdout, stderr)
		}
	})
}
