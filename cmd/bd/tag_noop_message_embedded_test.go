//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestTagNoOpHonestMessage_huu7 is the end-to-end regression for beads-huu7:
// `bd tag <id> <label>` is idempotent (AddLabel no-ops when the label is already
// present), but the CLI printed "✓ Added label" regardless — a false success a
// CI/agent gate reads as proof the label was newly applied. The second tag of
// the SAME label must report a no-op ("no change", info glyph, NOT the "✓ Added"
// success line) while staying rc=0 (a present-label tag is not an error).
// Mirrors the landed label-add no-op fix (beads-qi8t) + label-remove (beads-yaux).
func TestTagNoOpHonestMessage_huu7(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "hu")

	created := bdCreate(t, bd, dir, "tag noop target", "--type", "task")

	// First tag: a genuinely new label → success line.
	out1, err := bdRunWithFlockRetry(t, bd, dir, "tag", created.ID, "urgent")
	if err != nil {
		t.Fatalf("first bd tag failed: %v\n%s", err, out1)
	}
	if !strings.Contains(string(out1), "Added label") {
		t.Errorf("first tag of a new label should report '✓ Added label': %s", out1)
	}

	// Second tag of the SAME label: a no-op. Must NOT claim "Added", must report
	// "no change", and must still exit 0.
	out2, err := bdRunWithFlockRetry(t, bd, dir, "tag", created.ID, "urgent")
	if err != nil {
		t.Fatalf("second (no-op) bd tag should exit 0, got err: %v\n%s", err, out2)
	}
	if strings.Contains(string(out2), "Added label") {
		t.Errorf("second tag of an already-present label must NOT print a false '✓ Added label': %s", out2)
	}
	if !strings.Contains(string(out2), "no change") {
		t.Errorf("second tag of an already-present label should report 'no change': %s", out2)
	}
}
