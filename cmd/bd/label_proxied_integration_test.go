//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerLabel is the teeth for beads-aocj: bd label add / bd label
// remove must WORK in proxied-server mode. Before the fix, both used the direct
// nil `store` in proxiedServerMode with no usesProxiedServer() routing, so they
// failed "storage is nil" — unlike `bd update --add-label/--remove-label` which
// route to a proxied handler. Mirrors the beads-1zuh relate/unrelate and
// beads-qwez assign/tag routing fixes.
func TestProxiedServerLabel(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	hasLabel := func(labels []string, want string) bool {
		for _, l := range labels {
			if l == want {
				return true
			}
		}
		return false
	}

	t.Run("add_happy_path", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "lba1")
		a := bdProxiedCreate(t, bd, p.dir, "Label me", "--type", "task")

		out, err := bdProxiedRun(t, bd, p.dir, "label", "add", a.ID, "needs-review")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied label add failed: %v\n%s", err, s)
		}
		if strings.Contains(s, "storage is nil") {
			t.Fatalf("proxied label add hit the nil-store path (beads-aocj regression): %s", s)
		}
		if !strings.Contains(s, "Added label") {
			t.Errorf("expected '✓ Added label' from proxied label add, got: %s", s)
		}
		got := bdProxiedShow(t, bd, p.dir, a.ID)
		if !hasLabel(got.Labels, "needs-review") {
			t.Errorf("labels after proxied label add = %v, want to contain needs-review", got.Labels)
		}
	})

	t.Run("add_repeatable_label_flag", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "lba2")
		a := bdProxiedCreate(t, bd, p.dir, "Multi label", "--type", "task")

		out, err := bdProxiedRun(t, bd, p.dir, "label", "add", a.ID, "--label", "alpha", "--label", "beta")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied label add --label failed: %v\n%s", err, s)
		}
		got := bdProxiedShow(t, bd, p.dir, a.ID)
		if !hasLabel(got.Labels, "alpha") || !hasLabel(got.Labels, "beta") {
			t.Errorf("labels after proxied multi-label add = %v, want alpha+beta", got.Labels)
		}
	})

	t.Run("remove_happy_path", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "lbr1")
		a := bdProxiedCreate(t, bd, p.dir, "Unlabel me", "--type", "task")

		if _, err := bdProxiedRun(t, bd, p.dir, "label", "add", a.ID, "temp"); err != nil {
			t.Fatalf("setup add failed: %v", err)
		}
		out, err := bdProxiedRun(t, bd, p.dir, "label", "remove", a.ID, "temp")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied label remove failed: %v\n%s", err, s)
		}
		if strings.Contains(s, "storage is nil") {
			t.Fatalf("proxied label remove hit the nil-store path (beads-aocj regression): %s", s)
		}
		if !strings.Contains(s, "Removed label") {
			t.Errorf("expected '✓ Removed label' from proxied label remove, got: %s", s)
		}
		got := bdProxiedShow(t, bd, p.dir, a.ID)
		if hasLabel(got.Labels, "temp") {
			t.Errorf("labels after proxied label remove = %v, want NOT to contain temp", got.Labels)
		}
	})

	// beads-4zy65: proxied `bd label add <id> <existing-label>` is a no-op — it
	// must report an honest "already present ... (no change)" / JSON status
	// "unchanged", not a fake "✓ Added label" / status:"added". The batch handler
	// looped applyUpdateProxiedOne with a blanket "added" status. AddLabelInTx is
	// idempotent so no updated_at bump, but the fake ✓/wrong status is the CI/agent
	// false-success qi8t exists to kill. Mirrors the direct addLabelsHonoringNoChange.
	t.Run("add_noop_existing_label_reports_no_change", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "lbnp1")
		a := bdProxiedCreate(t, bd, p.dir, "Label no-op", "--type", "task")
		if _, err := bdProxiedRun(t, bd, p.dir, "label", "add", a.ID, "keep"); err != nil {
			t.Fatalf("setup add failed: %v", err)
		}

		out, err := bdProxiedRun(t, bd, p.dir, "label", "add", a.ID, "keep")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied no-op label add failed: %v\n%s", err, s)
		}
		if strings.Contains(s, "✓") && strings.Contains(s, "Added label") {
			t.Errorf("false success: re-adding an existing label printed '✓ Added label': %s", s)
		}
		if !strings.Contains(s, "no change") {
			t.Errorf("expected 'label ... already present ... (no change)', got: %s", s)
		}
		// JSON: status must be "unchanged", not "added".
		jout, jerr := bdProxiedRun(t, bd, p.dir, "label", "add", a.ID, "keep", "--json")
		if jerr != nil {
			t.Fatalf("proxied no-op label add --json failed: %v\n%s", jerr, jout)
		}
		js := string(jout)
		if !strings.Contains(js, `"unchanged"`) {
			t.Errorf("expected JSON status \"unchanged\" on a no-op label add, got: %s", js)
		}
		if strings.Contains(js, `"added"`) {
			t.Errorf("no-op label add JSON must NOT report status \"added\": %s", js)
		}
		// Label present exactly once (no duplication).
		got := bdProxiedShow(t, bd, p.dir, a.ID)
		count := 0
		for _, l := range got.Labels {
			if l == "keep" {
				count++
			}
		}
		if count != 1 {
			t.Errorf("label 'keep' present %d times after no-op add, want 1: %v", count, got.Labels)
		}
	})

	// beads-4zy65: a genuinely new label in the SAME batch as an existing one must
	// still be added and reported ✓, while the existing one reports "unchanged"
	// (mixed batch — proves the per-pair partition, not an all-or-nothing gate).
	t.Run("add_mixed_new_and_existing_reports_per_pair", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "lbnp2")
		a := bdProxiedCreate(t, bd, p.dir, "Mixed", "--type", "task")
		if _, err := bdProxiedRun(t, bd, p.dir, "label", "add", a.ID, "old"); err != nil {
			t.Fatalf("setup add failed: %v", err)
		}

		out, err := bdProxiedRun(t, bd, p.dir, "label", "add", a.ID, "--label", "old", "--label", "new", "--json")
		s := string(out)
		if err != nil {
			t.Fatalf("proxied mixed label add failed: %v\n%s", err, s)
		}
		if !strings.Contains(s, `"unchanged"`) {
			t.Errorf("expected status \"unchanged\" for the existing 'old' label: %s", s)
		}
		if !strings.Contains(s, `"added"`) {
			t.Errorf("expected status \"added\" for the new 'new' label: %s", s)
		}
		got := bdProxiedShow(t, bd, p.dir, a.ID)
		if !hasLabel(got.Labels, "old") || !hasLabel(got.Labels, "new") {
			t.Errorf("labels after mixed add = %v, want old+new", got.Labels)
		}
	})

	// beads-4zy65: proxied `bd label remove <id> <never-had-label>` must NOT print
	// a fake "✓ Removed" — the direct yaux path treats a never-present label as a
	// FAILURE (rc!=0, "no label ... to remove"). Twin-parity: match the direct
	// contract, not a swallowed in-band success.
	t.Run("remove_never_present_is_failure_not_fake_success", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "lbnr1")
		a := bdProxiedCreate(t, bd, p.dir, "No such label", "--type", "task")

		out, err := bdProxiedRun(t, bd, p.dir, "label", "remove", a.ID, "ghost")
		s := string(out)
		if err == nil {
			t.Fatalf("removing a never-present label should fail (yaux twin-parity); got success:\n%s", s)
		}
		if strings.Contains(s, "✓") && strings.Contains(s, "Removed label") {
			t.Errorf("false success: removing a never-present label printed '✓ Removed label': %s", s)
		}
		if !strings.Contains(s, "to remove") {
			t.Errorf("expected a 'no label ... to remove' failure message, got: %s", s)
		}
	})

	// beads-4zy65: removing a present label from one id while another id never had
	// it — the present removal succeeds AND is reported, the missing one is a
	// per-id failure (rc!=0), matching the direct present/missing split.
	t.Run("remove_partial_present_and_missing", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "lbnr2")
		a := bdProxiedCreate(t, bd, p.dir, "Has label", "--type", "task")
		b := bdProxiedCreate(t, bd, p.dir, "No label", "--type", "task")
		if _, err := bdProxiedRun(t, bd, p.dir, "label", "add", a.ID, "shared"); err != nil {
			t.Fatalf("setup add failed: %v", err)
		}

		out, err := bdProxiedRun(t, bd, p.dir, "label", "remove", a.ID, b.ID, "shared")
		s := string(out)
		if err == nil {
			t.Fatalf("a batch remove where one id never had the label should exit non-zero; got success:\n%s", s)
		}
		if !strings.Contains(s, "Removed label") {
			t.Errorf("expected the present removal to still report '✓ Removed label': %s", s)
		}
		// The present label was actually removed.
		gotA := bdProxiedShow(t, bd, p.dir, a.ID)
		if hasLabel(gotA.Labels, "shared") {
			t.Errorf("label 'shared' should have been removed from %s: %v", a.ID, gotA.Labels)
		}
	})

	t.Run("add_provides_label_rejected", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "lbp1")
		a := bdProxiedCreate(t, bd, p.dir, "No provides", "--type", "task")

		out, err := bdProxiedRun(t, bd, p.dir, "label", "add", a.ID, "provides:foo")
		s := string(out)
		if err == nil {
			t.Fatalf("expected nonzero exit for a 'provides:' label, got success:\n%s", s)
		}
		if !strings.Contains(s, "provides:") {
			t.Errorf("expected a 'provides:' rejection message, got: %s", s)
		}
	})

	t.Run("add_all_unresolvable_nonzero_exit", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "lbu1")
		out, err := bdProxiedRun(t, bd, p.dir, "label", "add", "no-such-id", "orphan")
		s := string(out)
		if err == nil {
			t.Fatalf("expected nonzero exit when no id resolves, got success:\n%s", s)
		}
		if strings.Contains(s, "storage is nil") {
			t.Fatalf("proxied label add hit the nil-store path (beads-aocj regression): %s", s)
		}
	})
}
