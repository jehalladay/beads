//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// beads-32m5l: the doctor top-level RunE error legs must honor the --json error
// contract — a parseable JSON error object on STDOUT (via the *RespectJSON
// helpers), matching the already-swept doctor SUBpaths (beads-5vist
// runPollutionCheck/runArtifactsCheck, beads-jngyh runMigrationValidation).
//
// The unknown-`--check` leg used a bare HandleErrorWithHint, which under --json
// emits JSON to STDERR (jsonStderrError) with an EMPTY stdout — unparseable by a
// `bd doctor --check=bogus --json | jq` consumer. --check is an unvalidated
// string flag (doctor_pollution.go), so this default branch is reachable.
//
// Hermetic: dispatchDoctorCheck's default (unknown-check) branch fires before any
// store/Dolt use, so a bogus check name against a temp path is enough.
func TestDoctorUnknownCheckJSONErrorContract_32m5l(t *testing.T) {
	prevJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = prevJSON })

	out, err := captureStdoutExpectErr(t, func() error {
		return dispatchDoctorCheck("bogus-check", t.TempDir())
	})
	if err == nil {
		t.Fatalf("expected a non-nil error from an unknown --check, got nil (stdout=%q)", out)
	}
	s := strings.TrimSpace(out)
	if s == "" {
		t.Fatalf("stdout empty on a --json doctor unknown-check error — must emit a JSON error object (beads-32m5l)")
	}
	var obj map[string]any
	if jerr := json.Unmarshal([]byte(s), &obj); jerr != nil {
		t.Fatalf("stdout is not a JSON object on --json error: %v\nstdout:\n%s", jerr, s)
	}
	if _, ok := obj["error"]; !ok {
		t.Errorf("expected an \"error\" field in the --json stdout object, got: %s", s)
	}
}
