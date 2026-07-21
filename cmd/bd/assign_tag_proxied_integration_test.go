//go:build cgo

package main

import (
	"strings"
	"testing"
	"time"
)

// TestProxiedServerAssignTag is the teeth for beads-aocj: bd assign / bd tag
// must WORK in proxied-server mode. Before the fix, both used the direct nil
// `store` in proxiedServerMode with no usesProxiedServer() routing, so they
// failed "storage is nil" — unlike their long forms (`bd update --assignee` /
// `bd update --add-label`) which route to a proxied handler. Mirrors the
// beads-1zuh relate/unrelate routing fix.
func TestProxiedServerAssignTag(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("assign_happy_path", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "asg1")
		a := bdProxiedCreate(t, bd, p.dir, "Assign me", "--type", "task")

		out, err := bdProxiedRun(t, bd, p.dir, "assign", a.ID, "alice")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied assign failed: %v\n%s", err, s)
		}
		if strings.Contains(s, "storage is nil") {
			t.Fatalf("proxied assign hit the nil-store path (beads-aocj regression): %s", s)
		}
		if !strings.Contains(s, "Assigned") {
			t.Errorf("expected '✓ Assigned' from proxied assign, got: %s", s)
		}
		// Verify the mutation actually persisted through the proxied path.
		got := bdProxiedShow(t, bd, p.dir, a.ID)
		if got.Assignee != "alice" {
			t.Errorf("assignee after proxied assign = %q, want alice", got.Assignee)
		}
	})

	t.Run("assign_none_unassigns", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "asg2")
		a := bdProxiedCreate(t, bd, p.dir, "Assign then clear", "--type", "task", "--assignee", "bob")

		out, err := bdProxiedRun(t, bd, p.dir, "assign", a.ID, "none")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied assign none failed: %v\n%s", err, s)
		}
		if !strings.Contains(s, "Unassigned") {
			t.Errorf("expected '✓ Unassigned' from proxied assign none, got: %s", s)
		}
		got := bdProxiedShow(t, bd, p.dir, a.ID)
		if got.Assignee != "" {
			t.Errorf("assignee after proxied assign none = %q, want empty", got.Assignee)
		}
	})

	t.Run("tag_happy_path", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "tag1")
		a := bdProxiedCreate(t, bd, p.dir, "Tag me", "--type", "task")

		out, err := bdProxiedRun(t, bd, p.dir, "tag", a.ID, "needs-review")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied tag failed: %v\n%s", err, s)
		}
		if strings.Contains(s, "storage is nil") {
			t.Fatalf("proxied tag hit the nil-store path (beads-aocj regression): %s", s)
		}
		if !strings.Contains(s, "Added label") {
			t.Errorf("expected '✓ Added label' from proxied tag, got: %s", s)
		}
		got := bdProxiedShow(t, bd, p.dir, a.ID)
		found := false
		for _, l := range got.Labels {
			if l == "needs-review" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("label after proxied tag = %v, want to contain needs-review", got.Labels)
		}
	})

	// beads-mpkza: proxied `bd assign <id> <same-assignee>` is an idempotent no-op
	// — it must report an honest "no change" (not a fake "✓ Assigned") AND skip the
	// write so it does not bump updated_at, matching the direct path (xqsy) and the
	// proxied priority twin (helt4). The shared applyUpdateProxiedOne core runs
	// ApplyUpdate+Commit unconditionally, so before the fix a no-op re-assign both
	// printed a fake ✓ and bumped updated_at (the full xqsy defect). The updated_at
	// assertion is the load-bearing teeth.
	t.Run("assign_noop_reports_no_change_and_does_not_bump_updated_at", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "asgnp")
		a := bdProxiedCreate(t, bd, p.dir, "Assign no-op", "--type", "task", "--assignee", "alice")

		before := bdProxiedShow(t, bd, p.dir, a.ID)
		if before.Assignee != "alice" {
			t.Fatalf("setup: assignee = %q, want alice", before.Assignee)
		}
		time.Sleep(1100 * time.Millisecond)

		out, err := bdProxiedRun(t, bd, p.dir, "assign", a.ID, "alice")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied no-op assign failed: %v\n%s", err, s)
		}
		if strings.Contains(s, "✓") && strings.Contains(s, "Assigned") {
			t.Errorf("false success: re-assigning to the current owner printed '✓ Assigned': %s", s)
		}
		if !strings.Contains(s, "no change") {
			t.Errorf("expected an 'already assigned to alice, no change' message, got: %s", s)
		}

		after := bdProxiedShow(t, bd, p.dir, a.ID)
		if !after.UpdatedAt.Equal(before.UpdatedAt) {
			t.Errorf("no-op proxied assign bumped updated_at (spurious write/commit, beads-mpkza): before=%v after=%v", before.UpdatedAt, after.UpdatedAt)
		}
		if after.Assignee != "alice" {
			t.Errorf("no-op assign changed the assignee: want alice, got %q", after.Assignee)
		}
	})

	// beads-mpkza: proxied `bd assign <id> none` on an already-unassigned issue is a
	// no-op too — honest "already unassigned, no change", no updated_at bump.
	t.Run("assign_none_noop_on_unassigned_reports_no_change", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "asgnu")
		a := bdProxiedCreate(t, bd, p.dir, "Unassigned no-op", "--type", "task")

		before := bdProxiedShow(t, bd, p.dir, a.ID)
		if before.Assignee != "" {
			t.Fatalf("setup: assignee = %q, want empty", before.Assignee)
		}
		time.Sleep(1100 * time.Millisecond)

		out, err := bdProxiedRun(t, bd, p.dir, "assign", a.ID, "none")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied no-op assign none failed: %v\n%s", err, s)
		}
		if strings.Contains(s, "✓") && strings.Contains(s, "Unassigned") {
			t.Errorf("false success: unassigning an already-unassigned issue printed '✓ Unassigned': %s", s)
		}
		if !strings.Contains(s, "no change") {
			t.Errorf("expected an 'already unassigned, no change' message, got: %s", s)
		}

		after := bdProxiedShow(t, bd, p.dir, a.ID)
		if !after.UpdatedAt.Equal(before.UpdatedAt) {
			t.Errorf("no-op proxied assign none bumped updated_at (beads-mpkza): before=%v after=%v", before.UpdatedAt, after.UpdatedAt)
		}
	})

	// beads-mpkza: proxied `bd assign <id> <different>` must still report a genuine
	// success and persist — the no-op guard must not swallow real changes.
	t.Run("assign_real_change_still_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "asgnr")
		a := bdProxiedCreate(t, bd, p.dir, "Assign real", "--type", "task", "--assignee", "alice")

		out, err := bdProxiedRun(t, bd, p.dir, "assign", a.ID, "bob")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied real assign failed: %v\n%s", err, s)
		}
		if !strings.Contains(s, "Assigned") || strings.Contains(s, "no change") {
			t.Errorf("real assign change should report a genuine 'Assigned', got: %s", s)
		}
		got := bdProxiedShow(t, bd, p.dir, a.ID)
		if got.Assignee != "bob" {
			t.Errorf("assignee after real proxied assign = %q, want bob", got.Assignee)
		}
	})

	// beads-mpkza: proxied `bd tag <id> <existing-label>` is a no-op — AddLabelInTx
	// is idempotent (so no spurious write), but the proxied handler printed a fake
	// "✓ Added label" instead of the direct path's honest "label already present ...
	// (no change)". Assert the honest message; the label set stays unchanged.
	t.Run("tag_noop_existing_label_reports_no_change", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "tagnp")
		a := bdProxiedCreate(t, bd, p.dir, "Tag no-op", "--type", "task")

		if _, err := bdProxiedRun(t, bd, p.dir, "tag", a.ID, "keep"); err != nil {
			t.Fatalf("setup tag failed: %v", err)
		}

		out, err := bdProxiedRun(t, bd, p.dir, "tag", a.ID, "keep")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied no-op tag failed: %v\n%s", err, s)
		}
		if strings.Contains(s, "✓") && strings.Contains(s, "Added label") {
			t.Errorf("false success: re-adding an existing label printed '✓ Added label': %s", s)
		}
		if !strings.Contains(s, "no change") {
			t.Errorf("expected a 'label already present ... (no change)' message, got: %s", s)
		}

		got := bdProxiedShow(t, bd, p.dir, a.ID)
		count := 0
		for _, l := range got.Labels {
			if l == "keep" {
				count++
			}
		}
		if count != 1 {
			t.Errorf("label 'keep' present %d times after no-op tag, want exactly 1: %v", count, got.Labels)
		}
	})

	// beads-mpkza: proxied `bd tag <id> <new-label>` must still add and report ✓.
	t.Run("tag_real_new_label_still_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "tagnr")
		a := bdProxiedCreate(t, bd, p.dir, "Tag real", "--type", "task")

		out, err := bdProxiedRun(t, bd, p.dir, "tag", a.ID, "fresh")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied real tag failed: %v\n%s", err, s)
		}
		if !strings.Contains(s, "Added label") || strings.Contains(s, "no change") {
			t.Errorf("real tag should report a genuine 'Added label', got: %s", s)
		}
		got := bdProxiedShow(t, bd, p.dir, a.ID)
		found := false
		for _, l := range got.Labels {
			if l == "fresh" {
				found = true
			}
		}
		if !found {
			t.Errorf("label after real proxied tag = %v, want to contain fresh", got.Labels)
		}
	})

	// beads-qxu4: a label containing a comma/newline must be REJECTED over the
	// proxied server too, matching the direct-path AddLabelInTx guard (beads-pqzx).
	// The proxied AddLabels path goes through the domain use-case (addMany ->
	// labelRepo.Insert), which previously skipped the delimiter check — so a
	// comma-bearing label was stored verbatim and the markdown "### Labels"
	// round-trip re-split it into multiple labels (identity corruption).
	t.Run("tag_comma_label_rejected", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "tagcx")
		a := bdProxiedCreate(t, bd, p.dir, "Tag comma", "--type", "task")

		out, err := bdProxiedRun(t, bd, p.dir, "tag", a.ID, "a,b")
		s := string(out)
		if err == nil {
			t.Fatalf("proxied tag with a comma label should fail; got success:\n%s", s)
		}
		if strings.Contains(s, "storage is nil") {
			t.Fatalf("proxied tag hit the nil-store path (beads-aocj regression): %s", s)
		}
		if !strings.Contains(s, "comma or newline") {
			t.Errorf("expected a 'comma or newline' delimiter-reject error, got: %s", s)
		}
		// The corrupt label must NOT have been stored.
		got := bdProxiedShow(t, bd, p.dir, a.ID)
		for _, l := range got.Labels {
			if strings.Contains(l, ",") {
				t.Errorf("comma-bearing label leaked into storage: %v", got.Labels)
			}
		}
	})

	// beads-qxu4: a label exceeding the VARCHAR(255) column width must be
	// rejected with a clean length error over the proxied server too, matching
	// the direct AddLabelInTx guard — not left to fail as a raw DB truncation/
	// insert error. Mirrors the domain validateLabelValue maxDomainLabelLen bound.
	t.Run("tag_overlong_label_rejected", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "tagln")
		a := bdProxiedCreate(t, bd, p.dir, "Tag long", "--type", "task")

		long := strings.Repeat("x", 256)
		out, err := bdProxiedRun(t, bd, p.dir, "tag", a.ID, long)
		s := string(out)
		if err == nil {
			t.Fatalf("proxied tag with a 256-char label should fail; got success:\n%s", s)
		}
		if strings.Contains(s, "storage is nil") {
			t.Fatalf("proxied tag hit the nil-store path (beads-aocj regression): %s", s)
		}
		if !strings.Contains(s, "characters or less") {
			t.Errorf("expected a 'characters or less' length error, got: %s", s)
		}
	})
}
