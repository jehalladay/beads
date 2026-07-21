package main

import (
	"os"
	"strings"
)

// gtInternalEnv is the environment variable the gt orchestrator sets (to
// gtInternalValue) on its own `bd` shell-outs (agent/rig/role/mail
// registration). gt strips-then-sets it so it cannot be spoofed by an inherited
// environment. When it equals gtInternalValue the CLI treats the write as a
// privileged system write and does NOT reserve the gt: identity family — so
// gt's own registration path keeps working. Human/script writes never set it,
// so they are rejected (beads-3c4g; gt-fork half myosw @bc04e5fc).
const gtInternalEnv = "GT_INTERNAL"

// gtInternalValue is the exact value gt stamps. Gate on equality (not merely
// non-empty) so a stray/inherited GT_INTERNAL=0 or garbage does not bypass the
// reservation.
const gtInternalValue = "1"

// reservedIdentityLabels are the identity/registration labels that the ready
// discriminator (beads-wqs) treats as system-controlled and always hides from
// `bd ready`. They MUST stay in sync with
// internal/storage/sqlbuild.readyWorkExcludeLabels — that package is the
// canonical read-side list; this is the write-side reservation. Kept as a small
// local copy rather than importing storage/sqlbuild into the CLI layer (which
// has no other dependency on it).
var reservedIdentityLabels = map[string]bool{
	"gt:agent": true,
	"gt:role":  true,
	"gt:rig":   true,
}

// gtInternalWrite reports whether the current process is a privileged gt
// orchestrator write (GT_INTERNAL == gtInternalValue).
func gtInternalWrite() bool {
	return strings.TrimSpace(os.Getenv(gtInternalEnv)) == gtInternalValue
}

// reservedIdentityLabelError returns a non-nil error message if label is a
// reserved identity label and this is not a privileged gt-internal write.
// Callers surface it via HandleErrorRespectJSON. Returns "" when the label is
// allowed.
//
// Trust boundary (beads-3c4g): the ready discriminator hides any bead carrying
// gt:agent/gt:role/gt:rig, but nothing stopped a human from `bd label add
// <bead> gt:agent` and silently hiding real work. Reserving the family at
// write-time closes that foot-gun/spoof vector. gt still stamps these labels
// via its own bd shell-outs, so the guard is gated on GT_INTERNAL (which gt
// sets and humans do not) to avoid breaking town-wide agent/rig/mail
// registration — see the gt-fork half (routed to gt_sr_pm) that sets the env.
func reservedIdentityLabelError(label string) string {
	if gtInternalWrite() {
		return ""
	}
	if reservedIdentityLabels[strings.TrimSpace(label)] {
		return "'" + strings.TrimSpace(label) + "' is a reserved gt identity label (system-controlled: it hides the bead from 'bd ready'). It is stamped by the gt orchestrator, not set by hand."
	}
	return ""
}

// providesLabelError returns a non-nil error message if label is a reserved
// 'provides:' capability label. Callers surface it via HandleErrorRespectJSON.
// Returns "" when the label is allowed.
//
// Trust boundary (beads-o70m1): 'provides:<cap>' marks a cross-project
// capability and enforces two invariants — the exporting issue must be CLOSED
// and only one issue may carry a given capability (single-provider). `bd ship`
// is the only sanctioned way to apply it (ship.go stamps the label via the
// storage layer AFTER checking those invariants). `bd label add` already
// rejects a hand-set provides: (label.go:274, label_proxied_server.go:32), but
// the create authoring seams (`bd create --labels`, `bd create --graph`) did
// not — a node/flag carrying provides: minted an OPEN bead with the label at
// RC=0, bypassing both invariants. This mirrors that same reservation into the
// authoring seams (same 3c4g write-reserve pattern as reservedIdentityLabelError).
// NOT gated on GT_INTERNAL: ship applies the label via store.AddLabel / the
// proxied UOW, not these CLI seams, so no privileged-write escape hatch is
// needed here. `bd import` + bulk restore stay UNGUARDED (they restore
// already-created legit beads), matching the f8fvh family ruling.
func providesLabelError(label string) string {
	trimmed := strings.TrimSpace(label)
	if strings.HasPrefix(trimmed, "provides:") {
		return "'provides:' labels are reserved for cross-project capabilities. Hint: use 'bd ship " + strings.TrimPrefix(trimmed, "provides:") + "' instead"
	}
	return ""
}
