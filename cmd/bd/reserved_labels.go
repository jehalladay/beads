package main

import (
	"os"
	"strings"
)

// gtInternalEnv is the environment variable the gt orchestrator sets on its own
// `bd` shell-outs (agent/rig/mail registration). When it is present the CLI
// treats the write as a privileged system write and does NOT reserve the gt:
// identity family — so gt's own CLI-fallback registration path keeps working.
// Human/script writes never set it, so they are rejected.
const gtInternalEnv = "GT_INTERNAL"

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
// orchestrator write (GT_INTERNAL set to a non-empty value).
func gtInternalWrite() bool {
	return strings.TrimSpace(os.Getenv(gtInternalEnv)) != ""
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
