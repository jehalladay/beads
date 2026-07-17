//go:build cgo

package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/testutil"
)

// TestCompactApplyArchivesSnapshotSharedServer is a teeth test for beads-zh1r.
//
// `bd admin compact --apply` (the documented "Agent-driven workflow
// (recommended)"; requires SQL-server mode via requireServerMode) destructively
// overwrites description/design/notes/acceptance_criteria and MUST archive a
// pre-compaction snapshot first — exactly like the --auto path
// (internal/compact/compactor.go CompactTier1) — so the advertised
// `bd restore --apply` undo can recover the original content.
//
// RED before the fix: runCompactApply omitted store.SnapshotIssue, so after
// --apply, `bd restore --apply` hard-failed "no archived snapshot for X; cannot
// safely restore content" and the original design/notes/acceptance text was
// silently destroyed (wiped to "") on a live, documented path.
// GREEN after the fix: --apply snapshots first, restore recovers the originals.
//
// Gated by BEADS_TEST_SHARED_SERVER=1 (starts a real Dolt container) since the
// --apply path is unreachable in embedded mode.
func TestCompactApplyArchivesSnapshotSharedServer(t *testing.T) {
	if os.Getenv("BEADS_TEST_SHARED_SERVER") == "" {
		t.Skip("skipping: set BEADS_TEST_SHARED_SERVER=1 to run")
	}
	if runtime.GOOS == "windows" {
		t.Skip("not supported on Windows")
	}

	bdBinary := buildSharedServerTestBinary(t)

	cp, err := testutil.NewContainerProvider()
	if err != nil {
		t.Skipf("cannot start Dolt container: %v", err)
	}
	containerPort := cp.Port()
	t.Cleanup(func() { _ = cp.Stop() })

	sharedDir := t.TempDir()
	if err := cp.WritePortFile(sharedDir); err != nil {
		t.Fatalf("write port file: %v", err)
	}

	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"GOPATH=" + os.Getenv("GOPATH"),
		"GOROOT=" + os.Getenv("GOROOT"),
		"BEADS_SHARED_SERVER_DIR=" + sharedDir,
		"BEADS_DOLT_SHARED_SERVER=1",
		"BEADS_DOLT_SERVER_PORT=" + strconv.Itoa(containerPort),
		"BEADS_DOLT_AUTO_START=0",
		"BEADS_TEST_MODE=1",
		"BD_DISABLE_METRICS=1",
		"BD_DISABLE_EVENT_FLUSH=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		"GT_ROOT=",
	}

	ctx := context.Background()
	if dl, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, dl)
		defer cancel()
	}

	dir := filepath.Join(t.TempDir(), "compactproj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := gitInit(ctx, dir); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if out, err := ssExec(ctx, bdBinary, dir, env,
		"init", "--shared-server", "--external", "--prefix", "ca", "--quiet", "--non-interactive"); err != nil {
		t.Fatalf("bd init failed: %s: %v", out, err)
	}

	const (
		origDesign = "ORIGINAL DESIGN: layered MVC with a repository seam"
		origNotes  = "ORIGINAL NOTES: watch the shared-worktree race on /fsx"
		origAccept = "ORIGINAL ACCEPTANCE: restore --apply recovers every text field"
	)

	// A closed issue with all four compactible text fields populated, sized so
	// the summary is genuinely shorter (the size-reduction gate passes).
	createOut, err := ssExec(ctx, bdBinary, dir, env, "create",
		"Compact-apply snapshot reversibility",
		"--type", "task",
		"--description", strings.Repeat("original description body. ", 20),
		"--design", origDesign,
		"--notes", origNotes,
		"--acceptance", origAccept,
		"--json",
	)
	if err != nil {
		t.Fatalf("bd create failed: %s: %v", createOut, err)
	}
	issueID, err := ssJSONField(createOut, "id")
	if err != nil {
		t.Fatalf("parse create id: %v", err)
	}

	if out, err := ssExec(ctx, bdBinary, dir, env, "close", issueID); err != nil {
		t.Fatalf("bd close failed: %s: %v", out, err)
	}

	summaryPath := filepath.Join(dir, "summary.txt")
	if err := os.WriteFile(summaryPath, []byte("compacted summary."), 0o600); err != nil {
		t.Fatalf("write summary file: %v", err)
	}

	// Documented recommended path: bd admin compact --apply. --force bypasses the
	// age eligibility gate (the issue was just closed) but NOT the reversibility
	// contract — the snapshot must still be archived.
	applyOut, err := ssExec(ctx, bdBinary, dir, env,
		"admin", "compact", "--apply", "--id", issueID, "--summary", summaryPath, "--force")
	if err != nil {
		t.Fatalf("bd admin compact --apply failed: %s: %v", applyOut, err)
	}
	if !strings.Contains(applyOut, "Compacted") {
		t.Fatalf("expected --apply to report compaction, got:\n%s", applyOut)
	}

	// The destructive overwrite happened (fields cleared).
	compacted, err := ssShowIssue(ctx, bdBinary, dir, env, issueID)
	if err != nil {
		t.Fatalf("show after compact: %v", err)
	}
	if ssStr(compacted, "design") != "" || ssStr(compacted, "notes") != "" || ssStr(compacted, "acceptance_criteria") != "" {
		t.Fatalf("expected --apply to clear design/notes/acceptance; got design=%q notes=%q accept=%q",
			ssStr(compacted, "design"), ssStr(compacted, "notes"), ssStr(compacted, "acceptance_criteria"))
	}

	// TEETH: the advertised undo must recover the ORIGINAL text. Before the fix
	// this fails with "no archived snapshot ... cannot safely restore" and the
	// originals are lost — silent data loss on the recommended path.
	restoreOut, err := ssExec(ctx, bdBinary, dir, env, "restore", issueID, "--apply")
	if err != nil {
		t.Fatalf("bd restore --apply failed (--apply never archived a snapshot — beads-zh1r): %s: %v", restoreOut, err)
	}

	restored, err := ssShowIssue(ctx, bdBinary, dir, env, issueID)
	if err != nil {
		t.Fatalf("show after restore: %v", err)
	}
	if got := ssStr(restored, "design"); got != origDesign {
		t.Errorf("design not restored: got %q, want %q", got, origDesign)
	}
	if got := ssStr(restored, "notes"); got != origNotes {
		t.Errorf("notes not restored: got %q, want %q", got, origNotes)
	}
	if got := ssStr(restored, "acceptance_criteria"); got != origAccept {
		t.Errorf("acceptance_criteria not restored: got %q, want %q", got, origAccept)
	}
}

// ssShowIssue runs `bd show <id> --json` against the shared server and parses it.
func ssShowIssue(ctx context.Context, binary, dir string, env []string, id string) (map[string]any, error) {
	out, err := ssExec(ctx, binary, dir, env, "show", id, "--json")
	if err != nil {
		return nil, err
	}
	return ssParseShowJSON(out)
}

// ssStr reads a string field from a parsed show-JSON object (missing/omitempty → "").
func ssStr(m map[string]any, field string) string {
	if v, ok := m[field].(string); ok {
		return v
	}
	return ""
}
