//go:build cgo

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage/uow"
)

// beads-a81t3 (8lqh/ag3ru residual): validateUpdateStatus (update_input.go),
// reached via the PROXIED update path (update_proxied_server.go →
// gatherUpdateInput → validateUpdateStatus), had its NewUOW-failure leg on a
// bare FatalError → under --json the {error} object went to STDERR with an
// EMPTY stdout, breaking scripted parsers. Its own sibling legs in the same
// function (read-status-set, invalid-status) already use FatalErrorRespectJSON,
// and it was the SOLE bare-FatalError outlier across all 33 "open unit of work"
// legs in cmd/bd. The fix routes it through FatalErrorRespectJSON (stdout
// {error} under --json; non-json plaintext-stderr + os.Exit(1) unchanged).
//
// validateUpdateStatus exits the process via FatalError* on failure and has no
// exit seam, and a NewUOW failure cannot be induced through any clean CLI input
// (it's a genuine runtime infra fault), so the contract is exercised via a
// subprocess re-exec + a fault uowProvider whose NewUOW returns an error (the
// 4yi7 idiom). The child sets jsonOutput=true and calls validateUpdateStatus;
// the natural os.Exit(1) fires. RED before the fix: the JSON error lands on
// STDERR and the child's STDOUT is empty. GREEN after: STDOUT carries a
// parseable {error} object.

// a81t3FaultUOWProvider satisfies uow.UnitOfWorkProvider and fails NewUOW,
// driving validateUpdateStatus into its :284 "open unit of work" leg.
type a81t3FaultUOWProvider struct{}

func (a81t3FaultUOWProvider) NewUOW(context.Context) (uow.UnitOfWork, error) {
	return nil, errors.New("injected NewUOW failure")
}

func (a81t3FaultUOWProvider) Close(context.Context) error { return nil }

func TestUpdateStatusUOWFailureJSONError_a81t3(t *testing.T) {
	if os.Getenv("BEADS_A81T3_CHILD") == "1" {
		runUpdateStatusUOWFailureChild()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run", "TestUpdateStatusUOWFailureJSONError_a81t3")
	cmd.Env = append(os.Environ(), "BEADS_A81T3_CHILD=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	// The command must still FAIL (os.Exit(1)) — only the --json stream placement
	// changes, never the terminal outcome.
	if err == nil {
		t.Fatalf("beads-a81t3: child unexpectedly exited 0; validateUpdateStatus must still os.Exit(1) on a NewUOW failure\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("beads-a81t3: expected child exit code 1, got %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("beads-a81t3: STDOUT is EMPTY on a --json NewUOW failure — the proxied `bd update --status` UOW-open error must emit a JSON {error} object on STDOUT (bare FatalError sent it to STDERR, breaking parsers)\nstderr:\n%s", stderr.String())
	}
	var obj map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
		t.Fatalf("beads-a81t3: STDOUT is not a JSON object on a --json NewUOW failure: %v\nstdout:\n%s", jerr, out)
	}
	msg, ok := obj["error"]
	if !ok {
		if data, dok := obj["data"].(map[string]interface{}); dok {
			msg, ok = data["error"]
		}
	}
	if s, _ := msg.(string); !ok || !strings.Contains(s, "open unit of work") {
		t.Errorf("beads-a81t3: expected an \"error\" field mentioning \"open unit of work\", got: %s", out)
	}
}

// runUpdateStatusUOWFailureChild installs a fault uowProvider + jsonOutput=true
// and drives validateUpdateStatus, which os.Exits via FatalError*. Guarded by
// BEADS_A81T3_CHILD=1 so it runs only in the re-exec child.
func runUpdateStatusUOWFailureChild() {
	uowProvider = a81t3FaultUOWProvider{}
	jsonOutput = true
	validateUpdateStatus(context.Background(), "open")
	// Reached only if validateUpdateStatus did NOT os.Exit (should never happen
	// on a NewUOW failure); exit 2 so the parent's exit-code assertion catches it.
	os.Exit(2)
}
