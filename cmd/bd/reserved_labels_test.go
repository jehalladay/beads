package main

import (
	"strings"
	"testing"
)

// TestReservedIdentityLabelError verifies the beads-3c4g write-time reservation
// of the gt identity family: gt:agent/gt:role/gt:rig are rejected for ordinary
// (human/script) writes but allowed for privileged gt-internal writes
// (GT_INTERNAL set), so gt's own CLI-fallback registration keeps working.
func TestReservedIdentityLabelError(t *testing.T) {
	// GT_INTERNAL absent: reserved labels are rejected.
	t.Run("reserved labels rejected without GT_INTERNAL", func(t *testing.T) {
		t.Setenv(gtInternalEnv, "")
		for _, label := range []string{"gt:agent", "gt:role", "gt:rig"} {
			if msg := reservedIdentityLabelError(label); msg == "" {
				t.Errorf("reservedIdentityLabelError(%q) = \"\" (allowed), want a rejection", label)
			} else if !strings.Contains(msg, label) {
				t.Errorf("reservedIdentityLabelError(%q) message %q should name the label", label, msg)
			}
		}
	})

	t.Run("reserved labels allowed with GT_INTERNAL set", func(t *testing.T) {
		t.Setenv(gtInternalEnv, "1")
		for _, label := range []string{"gt:agent", "gt:role", "gt:rig"} {
			if msg := reservedIdentityLabelError(label); msg != "" {
				t.Errorf("reservedIdentityLabelError(%q) = %q with GT_INTERNAL set, want allowed", label, msg)
			}
		}
	})

	t.Run("whitespace-only GT_INTERNAL does not count as internal", func(t *testing.T) {
		t.Setenv(gtInternalEnv, "   ")
		if msg := reservedIdentityLabelError("gt:agent"); msg == "" {
			t.Error("whitespace-only GT_INTERNAL should NOT bypass the reservation")
		}
	})

	t.Run("GT_INTERNAL must equal the exact value, not just be non-empty", func(t *testing.T) {
		// A stray/inherited GT_INTERNAL=0 or garbage must NOT bypass the guard;
		// only the exact value gt stamps (gtInternalValue) counts as internal.
		for _, v := range []string{"0", "true", "yes", "2", "internal"} {
			t.Setenv(gtInternalEnv, v)
			if msg := reservedIdentityLabelError("gt:agent"); msg == "" {
				t.Errorf("GT_INTERNAL=%q should NOT bypass the reservation (only %q does)", v, gtInternalValue)
			}
		}
	})

	t.Run("non-reserved labels always allowed", func(t *testing.T) {
		t.Setenv(gtInternalEnv, "")
		for _, label := range []string{
			"gt:message",   // gt: prefix but NOT in the ready-hidden identity family
			"gt:convoy",    // ditto
			"priority:p0",  // ordinary user label
			"provides:cap", // reserved elsewhere, not by this guard
			"agent",        // no gt: prefix
			"",             // empty
		} {
			if msg := reservedIdentityLabelError(label); msg != "" {
				t.Errorf("reservedIdentityLabelError(%q) = %q, want allowed (not an identity label)", label, msg)
			}
		}
	})

	t.Run("surrounding whitespace on the label is trimmed before matching", func(t *testing.T) {
		t.Setenv(gtInternalEnv, "")
		if msg := reservedIdentityLabelError("  gt:agent  "); msg == "" {
			t.Error("padded reserved label should still be rejected (trimmed match)")
		}
	})
}
