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
