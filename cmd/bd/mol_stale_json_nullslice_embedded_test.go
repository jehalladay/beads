//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestEmbeddedMolStaleJSONEmptyIsArray_bvsm pins the json-ARRAY contract for
// `bd mol stale --json`: on a fresh database with no stale molecules the
// stale_molecules field must marshal to an empty JSON array `[]`, never
// `null`. Before the fix, findStaleMolecules declared its accumulator as
// `var staleMolecules []*StaleMolecule` (a nil slice), so an empty result
// serialized `"stale_molecules": null` (mol_stale.go) — unlike `bd mol
// ready`, which correctly emits `[]`. A --json consumer that iterates the
// array breaks on null.
//
// This exercises the real findStaleMolecules path end-to-end (embedded dolt),
// so it fails on the pre-fix nil slice and passes after initializing the
// slice to []*StaleMolecule{}.
func TestEmbeddedMolStaleJSONEmptyIsArray_bvsm(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ms")

	cmd := exec.Command(bd, "mol", "stale", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd mol stale --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}

	s := strings.TrimSpace(stdout.String())
	start := strings.IndexByte(s, '{')
	if start < 0 {
		t.Fatalf("no JSON object in mol stale --json output: %q", s)
	}

	var decoded struct {
		StaleMolecules json.RawMessage `json:"stale_molecules"`
	}
	if err := json.Unmarshal([]byte(s[start:]), &decoded); err != nil {
		t.Fatalf("mol stale --json output is not valid JSON: %v\ngot: %q", err, s)
	}

	field := strings.TrimSpace(string(decoded.StaleMolecules))
	if field == "null" {
		t.Fatalf("stale_molecules marshaled to `null` on an empty result; a --json consumer expects `[]`\ngot: %q", s)
	}

	var arr []json.RawMessage
	if err := json.Unmarshal(decoded.StaleMolecules, &arr); err != nil {
		t.Fatalf("stale_molecules is not a JSON array: %v\ngot: %q", err, s)
	}
	if len(arr) != 0 {
		t.Errorf("expected empty stale_molecules array on a fresh db, got %d elements: %q", len(arr), s)
	}
}
