package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// beads-exw0r: the 12 push/pull handlers in sync_push_pull.go (ado/jira/linear/
// github/gitlab {push,pull}) all route result through outputSyncResult, which
// emits outputJSON(result) under --json — and SyncResult.Warnings already
// carries every engine.warn() independently of the OnWarning callback. So a raw
// `engine.OnWarning = func(msg){ Fprintf(os.Stderr, "Warning: ...") }` echo
// double-reports under --json (raw non-JSON stderr line PLUS the warnings[]
// envelope), the exact defect beads-lster fixed for the sync verb and mfmcf for
// ado. The fix routes every push/pull OnWarning through the shared, json-guarded
// emitSyncWarningStderr helper.
//
// This is a wiring guard (the helper's own suppression is proven by
// TestSyncWarning_SuppressedInJSONMode_lster): it asserts NO push/pull
// engine.OnWarning callback re-introduces a raw stderr echo. Unlike lster's
// github/gitlab SYNC exclusion (those emit no json result), the push/pull
// github/gitlab handlers DO call outputSyncResult, so they are envelope-backed
// and MUST be routed too.
//
// MUTATION-VERIFIED: reverting any handler to
// `engine.OnWarning = func(msg string) { fmt.Fprintf(os.Stderr, "Warning: %s\n", msg) }`
// turns this test RED.
func TestSyncPushPull_OnWarning_RoutedThroughHelper_exw0r(t *testing.T) {
	src, err := os.ReadFile("sync_push_pull.go")
	if err != nil {
		t.Fatalf("reading sync_push_pull.go: %v", err)
	}
	text := string(src)

	// Every engine.OnWarning assignment must go through the json-guarded helper.
	onWarnAssign := regexp.MustCompile(`engine\.OnWarning\s*=\s*func\(msg string\)\s*\{([^}]*)\}`)
	matches := onWarnAssign.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		t.Fatalf("expected engine.OnWarning assignments in sync_push_pull.go, found none")
	}

	routed := 0
	for _, m := range matches {
		body := m[1]
		if strings.Contains(body, "emitSyncWarningStderr(") {
			routed++
			continue
		}
		if strings.Contains(body, "os.Stderr") || strings.Contains(body, `"Warning:`) {
			t.Errorf("beads-exw0r: a push/pull engine.OnWarning still writes a raw stderr echo (%q) — route it through emitSyncWarningStderr so it is suppressed under --json (the warning already travels in result.Warnings)", strings.TrimSpace(body))
		}
	}

	// All 12 handlers (ado/jira/linear/github/gitlab {push,pull}) set OnWarning.
	if routed < 12 {
		t.Errorf("beads-exw0r: expected all 12 push/pull OnWarning callbacks routed through emitSyncWarningStderr, got %d", routed)
	}
}
