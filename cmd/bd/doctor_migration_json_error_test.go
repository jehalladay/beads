//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// beads-jngyh: `bd doctor --migration=<phase> --json` emits a JSON object on the
// valid pre/post phases, but the default (invalid-phase) branch at doctor.go:1198
// used a bare HandleError → plaintext "Error: ..." on stderr with an EMPTY stdout,
// unparseable by a --json consumer. Sibling of beads-5vist (doctor --check),
// beads-51m50 (notion), beads-uc71 (ado). The fix routes the invalid-phase return
// through HandleErrorRespectJSON.
//
// Hermetic: the invalid-phase branch fires from the switch default before any
// store/Dolt use, so a bogus phase string against a temp dir is enough.
func TestDoctorMigrationInvalidPhaseJSONErrorContract_jngyh(t *testing.T) {
	prevJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = prevJSON })

	out, err := captureStdoutExpectErr(t, func() error {
		return runMigrationValidation(t.TempDir(), "bogus-phase")
	})
	if err == nil {
		t.Fatalf("expected a non-nil error from an invalid migration phase, got nil (stdout=%q)", out)
	}
	s := strings.TrimSpace(out)
	if s == "" {
		t.Fatalf("stdout empty on a --json doctor migration error — must emit a JSON error object (beads-jngyh)")
	}
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(s), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on --json error: %v\nstdout:\n%s", jerr, s)
	}
	if _, ok := obj["error"]; !ok {
		t.Errorf("expected an \"error\" field in the --json stdout object, got: %s", s)
	}
}