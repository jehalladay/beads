package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// beads-exw0r (PUSH/PULL twin of lster/mfmcf): every push/pull handler in
// sync_push_pull.go (ado, jira, linear, github, gitlab, notion — 12 total) sets
// engine.OnWarning and routes its result through outputSyncResult, which emits
// outputJSON(result) under --json. tracker.SyncResult.Warnings (types.go:131,
// json:"warnings") already carries every engine.warn() INDEPENDENTLY of the
// callback — so under --json a raw `Fprintf(os.Stderr, "Warning: %s\n", msg)`
// callback DOUBLE-reports: the raw non-JSON line on stderr PLUS the same string
// in the machine-readable warnings[] envelope, interleaving noise into a --json
// consumer's captured stderr.
//
// The fix routes all 12 OnWarning callbacks through the shared, json-guarded
// emitSyncWarningStderr (sync_warning_json_lster.go:25) — the same helper the
// jira/linear/notion sync verbs use (lster) and the ado twin (mfmcf). KEY DIFF
// from lster's sync exclusion: the github/gitlab push/pull handlers DO call the
// envelope-backed outputSyncResult, so they too are routed here (suppression is
// safe precisely because the warning is envelope-backed).
//
// The behavior of emitSyncWarningStderr itself (suppressed under --json,
// emitted in human mode) is mutation-verified by the lster/mfmcf helper tests.
// This guard is the load-bearing teeth for the *routing*: it fails if any
// OnWarning callback in sync_push_pull.go reverts to a raw stderr Fprintf
// instead of the shared helper.
//
// MUTATION-VERIFIED: reverting any of the 12 routings back to
// `engine.OnWarning = func(msg string) { fmt.Fprintf(os.Stderr, "Warning: %s\n", msg) }`
// → this guard goes RED (rawOnWarning matches, routedOnWarning count drops
// below 12).
func TestSyncPushPullOnWarningRoutedThroughSharedHelper_exw0r(t *testing.T) {
	const src = "sync_push_pull.go"
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	content := string(data)

	// No OnWarning callback may write the raw "Warning:" line to stderr
	// directly — that is the double-report bug under --json.
	rawOnWarning := regexp.MustCompile(`engine\.OnWarning\s*=\s*func\(msg string\)\s*\{\s*fmt\.Fprintf\(os\.Stderr,\s*"Warning:`)
	if loc := rawOnWarning.FindStringIndex(content); loc != nil {
		t.Errorf("%s: an engine.OnWarning callback still writes a raw \"Warning:\" line to stderr (beads-exw0r double-report under --json); route it through emitSyncWarningStderr instead. First offender near byte %d:\n\t%s",
			src, loc[0], strings.TrimSpace(content[loc[0]:min(loc[0]+90, len(content))]))
	}

	// All 12 handlers (ado/jira/linear/github/gitlab × {push,pull} + notion ×
	// {push,pull}) must route OnWarning through the shared json-guarded helper.
	routed := strings.Count(content, "engine.OnWarning = func(msg string) { emitSyncWarningStderr(os.Stderr, msg) }")
	if routed != 12 {
		t.Errorf("%s: expected all 12 push/pull OnWarning callbacks routed through emitSyncWarningStderr, found %d (beads-exw0r)", src, routed)
	}

	// The two "failed to release sync lock" Fprintf lines (linear push/pull) are
	// NOT engine warnings (not in result.Warnings) and must stay unguarded on
	// stderr — guard against accidentally suppressing them.
	if lock := strings.Count(content, "Warning: failed to release sync lock"); lock != 2 {
		t.Errorf("%s: expected 2 unguarded 'failed to release sync lock' stderr lines preserved, found %d (beads-exw0r must not touch these)", src, lock)
	}
}
