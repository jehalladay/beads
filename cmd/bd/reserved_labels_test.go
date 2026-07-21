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

// TestProvidesLabelError verifies the beads-o70m1 write-time reservation of the
// 'provides:' cross-project capability family at the create authoring seams.
// `bd ship` is the only sanctioned way to apply provides:<cap> (it enforces the
// closed-requirement + single-provider invariants); the helper rejects a
// hand-set provides: label so `bd create --labels`/`--graph` cannot route
// around ship. Non-provides labels (including the gt identity family, which has
// its own guard) are unaffected.
//
// MUTATION-VERIFIED: reverting the providesLabelError call in create.go /
// graph_apply.go makes the seam tests go RED (a provides: label mints an OPEN
// bead at RC=0).
func TestProvidesLabelError(t *testing.T) {
	t.Run("provides labels rejected", func(t *testing.T) {
		for _, label := range []string{"provides:api", "provides:auth-service", "provides:x"} {
			msg := providesLabelError(label)
			if msg == "" {
				t.Errorf("providesLabelError(%q) = \"\" (allowed), want a rejection", label)
				continue
			}
			if !strings.Contains(msg, "provides:") || !strings.Contains(msg, "bd ship") {
				t.Errorf("providesLabelError(%q) = %q, want it to mention 'provides:' and the 'bd ship' hint", label, msg)
			}
			// The hint should name the bare capability (label minus the prefix).
			cap := strings.TrimPrefix(label, "provides:")
			if !strings.Contains(msg, cap) {
				t.Errorf("providesLabelError(%q) = %q, want it to name the capability %q", label, msg, cap)
			}
		}
	})

	t.Run("surrounding whitespace is trimmed before matching", func(t *testing.T) {
		if msg := providesLabelError("  provides:api  "); msg == "" {
			t.Error("padded provides: label should still be rejected (trimmed match)")
		}
	})

	t.Run("non-provides labels always allowed", func(t *testing.T) {
		for _, label := range []string{
			"provides",     // no colon — not the reserved family
			"gt:agent",     // reserved by a DIFFERENT guard, not this one
			"area:backend", // ordinary user label
			"priority:p0",
			"export:api", // ship's INPUT label — must stay settable by hand
			"",           // empty
		} {
			if msg := providesLabelError(label); msg != "" {
				t.Errorf("providesLabelError(%q) = %q, want allowed (not the provides: family)", label, msg)
			}
		}
	})

	// NOT gated on GT_INTERNAL: ship applies provides: via the storage layer,
	// not these CLI seams, so there is no privileged-write escape hatch to honor.
	t.Run("GT_INTERNAL does not bypass the provides reservation", func(t *testing.T) {
		t.Setenv(gtInternalEnv, "1")
		if msg := providesLabelError("provides:api"); msg == "" {
			t.Error("GT_INTERNAL must NOT bypass the provides: reservation (ship stamps it via storage, not this seam)")
		}
	})
}
