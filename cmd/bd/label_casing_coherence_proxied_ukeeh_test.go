//go:build cgo

package main

import (
	"testing"
)

// TestProxiedLabelCasingCoherence_ukeeh (beads-ukeeh) is the proxied/domain twin
// of the direct TestLabelCasingCoherence_9jjj8. It proves the label subsystem
// agrees with itself on casing on the DOMAIN write path used by a hub-connected
// (proxied-server) crew.
//
// beads-9jjj8 folded labels to lower-case at the DIRECT issueops chokepoints
// (AddLabelInTx / SetLabelsInTx / RemoveLabelInTx), matching the case-INSENSITIVE
// query side (LOWER(label)=LOWER(?) throughout sqlbuild). But the domain
// LabelSQLRepository sink (internal/storage/domain/db/label.go), which every
// proxied label write funnels through, was left uncovered: Insert stored the
// label verbatim (case-SENSITIVE) and Delete matched case-EXACT. So on the
// proxied path 'FOO' and 'foo' could coexist, an issue labelled 'FOO' matched
// `--label foo` (query folds) yet `label remove foo` no-op'd (case-exact DELETE)
// — the same find-then-cannot-remove trap 9jjj8 killed for direct crew.
//
// Fix: fold at the domain sink — Insert lower-cases before write, Delete
// lower-cases the input AND matches LOWER(label)=? (so legacy mixed-case rows
// clear too), and setMany folds its desired keys. Mirrors the direct fix.
//
// Drives a real proxied bd subprocess so the on-disk casing + the LOWER()
// query/DELETE SQL are validated for real against the proxied server.
// MUTATION-VERIFY: drop the `label = strings.ToLower(label)` fold from the domain
// Insert and "add_folds_stored_lower"/"no_coexisting_case_variants" go RED;
// revert Delete's fold/LOWER() and "remove_is_case_insensitive" goes RED.
func TestProxiedLabelCasingCoherence_ukeeh(t *testing.T) {
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

	t.Run("add_folds_stored_lower", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ukfold")
		a := bdProxiedCreate(t, bd, p.dir, "Mixed add", "--type", "task")

		if _, err := bdProxiedRun(t, bd, p.dir, "label", "add", a.ID, "FOO"); err != nil {
			t.Fatalf("proxied label add FOO failed: %v", err)
		}
		got := bdProxiedShow(t, bd, p.dir, a.ID)
		if len(got.Labels) != 1 || got.Labels[0] != "foo" {
			t.Errorf("REGRESSION (beads-ukeeh): proxied AddLabel(\"FOO\") stored %v, want [\"foo\"] (domain write must case-fold to match the case-insensitive query)", got.Labels)
		}
	})

	t.Run("no_coexisting_case_variants", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ukdup")
		a := bdProxiedCreate(t, bd, p.dir, "Case dup", "--type", "task")

		if _, err := bdProxiedRun(t, bd, p.dir, "label", "add", a.ID, "Bar"); err != nil {
			t.Fatalf("proxied label add Bar failed: %v", err)
		}
		if _, err := bdProxiedRun(t, bd, p.dir, "label", "add", a.ID, "BAR"); err != nil {
			t.Fatalf("proxied label add BAR failed: %v", err)
		}
		got := bdProxiedShow(t, bd, p.dir, a.ID)
		if len(got.Labels) != 1 || got.Labels[0] != "bar" {
			t.Errorf("REGRESSION (beads-ukeeh): proxied 'Bar' then 'BAR' produced %v, want a single [\"bar\"] (case variants must not coexist on the domain path)", got.Labels)
		}
	})

	t.Run("remove_is_case_insensitive", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ukrm")
		a := bdProxiedCreate(t, bd, p.dir, "Case remove", "--type", "task")

		// Add mixed-case, remove with a different case — must clear it.
		if _, err := bdProxiedRun(t, bd, p.dir, "label", "add", a.ID, "Needs-Review"); err != nil {
			t.Fatalf("proxied label add Needs-Review failed: %v", err)
		}
		// The query side finds it case-insensitively; a case-exact DELETE would
		// leave it. Remove with an all-lower spelling.
		if _, err := bdProxiedRun(t, bd, p.dir, "label", "remove", a.ID, "needs-review"); err != nil {
			t.Fatalf("proxied label remove needs-review (differently cased) failed: %v", err)
		}
		got := bdProxiedShow(t, bd, p.dir, a.ID)
		if hasLabel(got.Labels, "needs-review") || hasLabel(got.Labels, "Needs-Review") {
			t.Errorf("REGRESSION (beads-ukeeh): proxied remove of a differently-cased label left %v, want it cleared (DELETE must match LOWER(label))", got.Labels)
		}
	})
}
