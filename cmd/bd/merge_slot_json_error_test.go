package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// beads-hut2: `bd merge-slot acquire --json` with no holder (neither --holder
// nor a resolvable actor) must honor the --json error contract — a parseable
// JSON error object on stdout, not plain text via HandleError. The command has
// json success paths and the adjacent store-error return already used
// HandleErrorRespectJSON; only the no-holder guard was a plain HandleError.
//
// This guard fires before any store access, so the test needs no DB.
func TestMergeSlotAcquireNoHolderJSONError(t *testing.T) {
	prevJSON := jsonOutput
	prevActor := actor
	prevHolder := mergeSlotHolder
	t.Cleanup(func() {
		jsonOutput = prevJSON
		actor = prevActor
		mergeSlotHolder = prevHolder
	})

	jsonOutput = true
	actor = ""
	mergeSlotHolder = ""

	out, err := captureStdoutExpectErr(t, func() error {
		return runMergeSlotAcquire(mergeSlotAcquireCmd, nil)
	})
	if err == nil {
		t.Fatalf("expected a non-nil error when no holder is specified, got nil (stdout=%q)", out)
	}
	s := strings.TrimSpace(out)
	if s == "" {
		t.Fatalf("stdout empty on a --json error — must emit a JSON error object (beads-hut2), err=%v", err)
	}
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(s), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on --json error: %v\nstdout:\n%s", jerr, s)
	}
	if _, ok := obj["error"]; !ok {
		t.Errorf("expected an \"error\" field in the --json stdout object, got: %s", s)
	}
}
