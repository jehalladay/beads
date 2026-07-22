package main

import (
	"fmt"
	"os"
	"strings"
)

type doltAutoCommitMode string

const (
	doltAutoCommitOff   doltAutoCommitMode = "off"
	doltAutoCommitOn    doltAutoCommitMode = "on"
	doltAutoCommitBatch doltAutoCommitMode = "batch"
)

func getDoltAutoCommitMode() (doltAutoCommitMode, error) {
	mode := strings.TrimSpace(strings.ToLower(doltAutoCommit))
	if mode == "" {
		// Default resolved at store-creation time in main.go based on server mode.
		// If still empty here, fall back to off (safe default).
		mode = string(doltAutoCommitOff)
	}
	switch doltAutoCommitMode(mode) {
	case doltAutoCommitOff:
		return doltAutoCommitOff, nil
	case doltAutoCommitOn:
		return doltAutoCommitOn, nil
	case doltAutoCommitBatch:
		return doltAutoCommitBatch, nil
	default:
		// beads-m83zh: LOAD-PATH RESILIENCE. An out-of-domain value can reach
		// here from two sources: an explicit CLI flag (user-supplied this
		// invocation) or a persisted config value (dolt.auto-commit in
		// config.yaml). This is called from PersistentPreRunE on EVERY command,
		// so hard-failing on a bad PERSISTED value bricks the whole workspace —
		// every read/write, including the `bd config unset`/`set` that would fix
		// it, errors before its RunE runs. A bad persisted value must therefore
		// degrade to the safe default (off) with a warning, so the CLI can always
		// self-recover. An explicit flag is user-supplied per-invocation and
		// SHOULD hard-fail loudly (the fix is to re-run without the bad flag).
		if doltAutoCommitFlagChanged() {
			return "", fmt.Errorf("invalid --dolt-auto-commit=%q (valid: off, on, batch)", doltAutoCommit)
		}
		fmt.Fprintf(os.Stderr, "Warning: invalid dolt.auto-commit=%q in config (valid: off, on, batch); using default 'off'. Fix with: bd config set dolt.auto-commit off\n", doltAutoCommit)
		return doltAutoCommitOff, nil
	}
}

// doltAutoCommitFromFlag records whether doltAutoCommit was supplied by an
// explicit --dolt-auto-commit CLI flag (true) rather than resolved from the
// persisted dolt.auto-commit config value (false). Set in PersistentPreRunE
// during flag/config resolution. A flag error hard-fails; a persisted-config
// error degrades to the safe default so a bad config can never brick the
// workspace. (beads-m83zh) It is NOT read from rootCmd's flag set directly
// because getDoltAutoCommitMode is reachable from rootCmd's initializer and a
// back-reference to rootCmd would form an initialization cycle.
var doltAutoCommitFromFlag bool

// doltAutoCommitFlagChanged reports whether the value in doltAutoCommit came
// from an explicit --dolt-auto-commit CLI flag (vs. a persisted config value).
func doltAutoCommitFlagChanged() bool {
	return doltAutoCommitFromFlag
}
