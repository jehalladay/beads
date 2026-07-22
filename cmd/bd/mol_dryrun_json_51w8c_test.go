//go:build cgo

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMolDryRunJSON51w8c is the beads-51w8c teeth (8lqh --json-contract family,
// sibling of the beads-8zed6 pour vapor-advisory fix).
//
// The four `bd mol …` dry-run paths (pour / bond / squash / distill) each
// printed a plaintext preview via fmt.Printf and returned nil BEFORE reaching
// their `if jsonOutput { outputJSON(...) }` block, so `--dry-run --json`
// silently emitted human text with rc=0. `bd mol pour <proto> --dry-run --json
// | jq` therefore got a parse error, not the intended machine result — and
// --dry-run is exactly the SAFE preview path scripts/agents use before a real
// mutating pour/bond/squash/distill.
//
// The fix gates each dry-run branch: under --json emit a parseable preview
// envelope (dry_run:true + the same fields as the plaintext preview); otherwise
// keep the plaintext unchanged.
//
// These teeth drive the real embedded `bd mol …` subprocess for each leg and
// assert: (a) `--dry-run --json` stdout is a single parseable JSON object
// carrying "dry_run":true, and (b) a positive control that WITHOUT --json the
// plaintext preview still prints (proving the dry-run branch is genuinely
// reached, so (a) can't false-green on a never-executed branch).
//
// Mutation-verify: drop any one `if jsonOutput { return outputMol*DryRunJSON… }`
// guard (restore the bare printMol*DryRun + return nil) and that leg's
// *_json_parses subtest goes RED (stdout is plaintext → json.Unmarshal fails).
func TestMolDryRunJSON51w8c(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// ---- pour ----------------------------------------------------------------
	t.Run("pour_dryrun_json_parses", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "dpo")
		proto := cookDryRunProto(t, bd, dir, "dpo-proto")

		out := bdRunOK(t, bd, dir, "mol", "pour", proto, "--dry-run", "--json")
		assertDryRunJSON(t, "pour", out)
	})
	t.Run("pour_dryrun_plain_keeps_preview", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "dpp")
		proto := cookDryRunProto(t, bd, dir, "dpp-proto")

		out := bdRunOK(t, bd, dir, "mol", "pour", proto, "--dry-run")
		if !strings.Contains(out, "Dry run: would pour") {
			t.Errorf("beads-51w8c: plain `mol pour --dry-run` must still print the plaintext "+
				"preview (fix gates only under --json); got:\n%s", out)
		}
	})

	// ---- bond ----------------------------------------------------------------
	t.Run("bond_dryrun_json_parses", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "dbo")
		a := cookDryRunProto(t, bd, dir, "dbo-a")
		b := cookDryRunProto(t, bd, dir, "dbo-b")

		out := bdRunOK(t, bd, dir, "mol", "bond", a, b, "--dry-run", "--json")
		assertDryRunJSON(t, "bond", out)
	})
	t.Run("bond_dryrun_plain_keeps_preview", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "dbp")
		a := cookDryRunProto(t, bd, dir, "dbp-a")
		b := cookDryRunProto(t, bd, dir, "dbp-b")

		out := bdRunOK(t, bd, dir, "mol", "bond", a, b, "--dry-run")
		if !strings.Contains(out, "Dry run: bond") {
			t.Errorf("beads-51w8c: plain `mol bond --dry-run` must still print the plaintext "+
				"preview; got:\n%s", out)
		}
	})

	// ---- distill (reverse of pour: needs a real molecule to distill) ---------
	t.Run("distill_dryrun_json_parses", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "ddi")
		proto := cookDryRunProto(t, bd, dir, "ddi-proto")
		epic := pourMoleculeRoot(t, bd, dir, proto)

		out := bdRunOK(t, bd, dir, "mol", "distill", epic, "ddi-formula", "--dry-run", "--json")
		assertDryRunJSON(t, "distill", out)
	})
	t.Run("distill_dryrun_plain_keeps_preview", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "ddp")
		proto := cookDryRunProto(t, bd, dir, "ddp-proto")
		epic := pourMoleculeRoot(t, bd, dir, proto)

		out := bdRunOK(t, bd, dir, "mol", "distill", epic, "ddp-formula", "--dry-run")
		if !strings.Contains(out, "Dry run: would distill") {
			t.Errorf("beads-51w8c: plain `mol distill --dry-run` must still print the plaintext "+
				"preview; got:\n%s", out)
		}
	})

	// ---- squash (needs a molecule with ephemeral wisp children) --------------
	t.Run("squash_dryrun_json_parses", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "dsq")
		epic := squashableMolecule(t, bd, dir, "dsq")

		out := bdRunOK(t, bd, dir, "mol", "squash", epic, "--dry-run", "--json")
		assertDryRunJSON(t, "squash", out)
	})
	t.Run("squash_dryrun_plain_keeps_preview", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "dsp")
		epic := squashableMolecule(t, bd, dir, "dsp")

		out := bdRunOK(t, bd, dir, "mol", "squash", epic, "--dry-run")
		if !strings.Contains(out, "Dry run: would squash") {
			t.Errorf("beads-51w8c: plain `mol squash --dry-run` must still print the plaintext "+
				"preview; got:\n%s", out)
		}
	})
}

// assertDryRunJSON asserts stdout is a single parseable JSON object carrying
// "dry_run":true (the shared 51w8c envelope marker).
func assertDryRunJSON(t *testing.T, leg, stdout string) {
	t.Helper()
	trimmed := strings.TrimSpace(stdout)
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		t.Fatalf("beads-51w8c: `mol %s --dry-run --json` stdout is NOT a single JSON object "+
			"(plaintext preview leaked): %v\nstdout:\n%s", leg, err, stdout)
	}
	if dr, ok := obj["dry_run"].(bool); !ok || !dr {
		t.Errorf("beads-51w8c: `mol %s --dry-run --json` JSON lacks \"dry_run\":true; got: %s",
			leg, trimmed)
	}
}

// bdRunOK runs a bd subprocess (stdout captured separately) with embedded-flock
// retry and fails the test if it errors. Returns stdout only — the 51w8c
// envelope must land on STDOUT (the machine channel), so asserting on stdout
// alone is what proves the contract (a plaintext leak on stdout is the defect).
func bdRunOK(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	for attempt := 0; attempt < 10; attempt++ {
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		so, se, err := runCommandBuffers(t, cmd)
		stdout, stderr := so.String(), se.String()
		if err == nil {
			return stdout
		}
		if !isEmbeddedLockOutput(stdout + stderr) {
			t.Fatalf("beads-51w8c: `bd %s` failed: %v\nstdout:\n%s\nstderr:\n%s",
				strings.Join(args, " "), err, stdout, stderr)
		}
		t.Logf("bd %s: flock contention (attempt %d/10), retrying...", args[0], attempt+1)
	}
	t.Fatalf("beads-51w8c: `bd %s` still flock-contended after 10 attempts", strings.Join(args, " "))
	return ""
}

// cookDryRunProto writes a minimal single-step workflow formula and persists it
// as a proto (template label), returning the proto id — the attachable/pourable
// artifact the pour/bond/distill dry-run legs consume.
func cookDryRunProto(t *testing.T, bd, dir, name string) string {
	t.Helper()
	body := fmt.Sprintf(`formula = %q
description = "beads-51w8c dry-run json teeth proto"
version = 1
type = "workflow"

[[steps]]
id = "only"
title = "only step"
description = "single step so the proto is valid"
`, name)
	path := filepath.Join(dir, name+".formula.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write formula %s: %v", name, err)
	}
	out := bdRunOK(t, bd, dir, "cook", path, "--persist", "--force")
	// `bd cook --persist` prints the minted proto id; recover it from the output.
	id := dryRunIssueID(out)
	if id == "" {
		t.Fatalf("beads-51w8c: could not parse proto id from `bd cook --persist %s` output:\n%s", name, out)
	}
	return id
}

// pourMoleculeRoot pours a proto into a real (liquid) molecule and returns its
// root epic id — the input `bd mol distill` requires.
func pourMoleculeRoot(t *testing.T, bd, dir, proto string) string {
	t.Helper()
	out := bdRunOK(t, bd, dir, "mol", "pour", proto, "--json")
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &obj); err != nil {
		t.Fatalf("beads-51w8c: `mol pour --json` did not return JSON: %v\n%s", err, out)
	}
	root, _ := obj["new_epic_id"].(string)
	if root == "" {
		t.Fatalf("beads-51w8c: `mol pour --json` lacked new_epic_id; got:\n%s", out)
	}
	return root
}

// squashableMolecule builds a molecule with an ephemeral (wisp) child so
// `bd mol squash <epic> --dry-run` reaches its preview branch (a molecule with
// zero wisp children short-circuits before the dry-run block).
func squashableMolecule(t *testing.T, bd, dir, prefix string) string {
	t.Helper()
	epic := dryRunIssueID(bdRunOK(t, bd, dir, "create", "Squashable Root", "--type", "epic", "--json"))
	if epic == "" {
		t.Fatalf("beads-51w8c: could not create squashable root epic")
	}
	child := dryRunIssueID(bdRunOK(t, bd, dir, "create", "Wisp Step", "--type", "task",
		"--parent", epic, "--ephemeral", "--json"))
	if child == "" {
		t.Fatalf("beads-51w8c: could not create ephemeral wisp child")
	}
	return epic
}

// dryRunIssueID pulls an issue id out of a `--json` create/cook payload,
// unwrapping a possible {"data":{…}} envelope, else falls back to the first
// token that looks like <prefix>-<id> in plaintext (cook's success line).
func dryRunIssueID(out string) string {
	trimmed := strings.TrimSpace(out)
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
		if data, ok := obj["data"].(map[string]interface{}); ok {
			if _, hasID := obj["id"]; !hasID {
				obj = data
			}
		}
		for _, k := range []string{"id", "issue_id", "new_epic_id", "root_id"} {
			if v, ok := obj[k].(string); ok && v != "" {
				return v
			}
		}
	}
	for _, tok := range strings.Fields(trimmed) {
		tok = strings.Trim(tok, "():,")
		if i := strings.Index(tok, "-"); i > 0 && i < len(tok)-1 {
			return tok
		}
	}
	return ""
}
