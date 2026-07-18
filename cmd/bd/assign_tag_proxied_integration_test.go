//go:build cgo

package main

import (
	"strings"
	"testing"
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
