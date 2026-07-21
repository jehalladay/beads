//go:build cgo

package embeddeddolt_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/steveyegge/beads/internal/types"
	"golang.org/x/sync/errgroup"
)

// TestAppendNotesConcurrent (beads-jscve) exercises the atomic server-side
// AppendNotes under N concurrent writers each adding a distinct marker: ALL
// markers survive and the pre-existing note is preserved. NOTE ON TEETH: the
// embedded store serializes+retries store-level txns (withConn/withRetryTx), so
// this test does NOT by itself discriminate the atomic CONCAT_WS from a
// retry-wrapped in-tx read-modify-write — both converge here. Its value is
// proving the atomic path is correct under concurrency (no deadlock, no lost
// marker, separator intact). The jscve bug the fix actually removes is the
// CMD-LAYER read-modify-write (bd update reads issue.Notes at resolve time, then
// writes a full combined blob via a SEPARATE UpdateIssue with no retry linking
// the two) — worst on a shared hub sql-server where two `bd update
// --append-notes` genuinely interleave. That is eliminated structurally by
// routing the cmd path through this atomic AppendNotes (no client-side snapshot
// exists to go stale). The discriminating separator behavior is covered by
// TestAppendNotesFromEmpty below (mutation-verified).
func TestAppendNotesConcurrent(t *testing.T) {
	skipUnlessEmbeddedDolt(t)

	te := newTestEnv(t, "an")
	ctx := t.Context()

	issue := &types.Issue{
		Title:     "concurrent append target",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		Notes:     "seed",
	}
	if err := te.store.CreateIssue(ctx, issue, "tester"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	id := issue.ID

	const writers = 8
	var start sync.WaitGroup
	start.Add(1)
	g, gctx := errgroup.WithContext(ctx)
	for i := 0; i < writers; i++ {
		marker := fmt.Sprintf("append-marker-%d", i)
		g.Go(func() error {
			start.Wait()
			return te.store.AppendNotes(gctx, id, marker, "tester")
		})
	}
	start.Done()
	if err := g.Wait(); err != nil {
		t.Fatalf("concurrent AppendNotes: %v", err)
	}

	got, err := te.store.GetIssue(ctx, id)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if !strings.Contains(got.Notes, "seed") {
		t.Errorf("pre-existing notes %q clobbered; notes=%q", "seed", got.Notes)
	}
	for i := 0; i < writers; i++ {
		marker := fmt.Sprintf("append-marker-%d", i)
		if !strings.Contains(got.Notes, marker) {
			t.Errorf("append %q lost to concurrent clobber; notes=%q", marker, got.Notes)
		}
	}
}

// TestAppendNotesFromEmpty verifies AppendNotes on an issue with NO existing
// notes does not prepend a blank line (CONCAT_WS + NULLIF drops the separator),
// matching the prior client-side "if notes != '' { += \n }" behavior.
func TestAppendNotesFromEmpty(t *testing.T) {
	skipUnlessEmbeddedDolt(t)

	te := newTestEnv(t, "ae")
	ctx := t.Context()

	issue := &types.Issue{
		Title:     "empty notes target",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
	}
	if err := te.store.CreateIssue(ctx, issue, "tester"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := te.store.AppendNotes(ctx, issue.ID, "first line", "tester"); err != nil {
		t.Fatalf("AppendNotes: %v", err)
	}
	got, err := te.store.GetIssue(ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.Notes != "first line" {
		t.Errorf("first append onto empty notes should have no leading newline; got %q", got.Notes)
	}
	// A second append then separates with a single newline.
	if err := te.store.AppendNotes(ctx, issue.ID, "second line", "tester"); err != nil {
		t.Fatalf("AppendNotes 2: %v", err)
	}
	got, err = te.store.GetIssue(ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue 2: %v", err)
	}
	if got.Notes != "first line\nsecond line" {
		t.Errorf("second append should newline-separate; got %q", got.Notes)
	}
}
