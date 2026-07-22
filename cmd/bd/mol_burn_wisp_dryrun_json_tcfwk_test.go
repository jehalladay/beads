//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestMolBurnWispDryRunJSONtcfwk is the beads-tcfwk teeth (8lqh --json-contract
// family, direct continuation of beads-51w8c which covered mol pour/bond/squash/
// distill).
//
// The dry-run legs 51w8c did NOT cover still printed a plaintext preview then
// returned nil BEFORE any jsonOutput check, so `--dry-run --json` leaked human
// text (or, for batch burn, emitted NOTHING) → `| jq` parse failure:
//   - cmd/bd/mol_burn.go  burnWispMolecule        (`bd mol burn <wisp> --dry-run`)
//   - cmd/bd/mol_burn.go  burnPersistentMolecule  (`bd mol burn <mol>  --dry-run`)
//   - cmd/bd/mol_burn.go  burnMultipleMolecules   (`bd mol burn a b   --dry-run`)
//   - cmd/bd/wisp.go      runWispCreate           (`bd mol wisp <proto> --dry-run`)
//
// The fix gates each dry-run branch under jsonOutput → a parseable envelope
// (dry_run:true + the same fields); the plaintext path is unchanged.
//
// These teeth drive the real embedded bd subprocess for each leg and assert:
// (a) `--dry-run --json` stdout is a single parseable JSON object carrying
// "dry_run":true, and (b) a positive control that WITHOUT --json the plaintext
// preview still prints (proving the dry-run branch is genuinely reached, so (a)
// can't false-green on a never-executed branch).
//
// Mutation-verify: drop any one `if jsonOutput { return output*DryRunJSON… }`
// guard (restore the bare plaintext + return nil) and that leg's *_json_parses
// subtest goes RED (stdout is plaintext → json.Unmarshal fails). Reuses the
// 51w8c shared helpers (assertDryRunJSON/bdRunOK/cookDryRunProto/dryRunIssueID).
func TestMolBurnWispDryRunJSONtcfwk(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// ---- burn wisp (ephemeral root) ------------------------------------------
	t.Run("burn_wisp_dryrun_json_parses", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "tbw")
		proto := cookDryRunProto(t, bd, dir, "tbw-proto")
		wisp := wispMoleculeRoot(t, bd, dir, proto)

		out := bdRunOK(t, bd, dir, "mol", "burn", wisp, "--dry-run", "--json")
		assertDryRunJSON(t, "burn wisp", out)
	})
	t.Run("burn_wisp_dryrun_plain_keeps_preview", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "tbwp")
		proto := cookDryRunProto(t, bd, dir, "tbwp-proto")
		wisp := wispMoleculeRoot(t, bd, dir, proto)

		out := bdRunOK(t, bd, dir, "mol", "burn", wisp, "--dry-run")
		if !strings.Contains(out, "Dry run: would burn wisp") {
			t.Errorf("beads-tcfwk: plain `mol burn <wisp> --dry-run` must still print the "+
				"plaintext preview (fix gates only under --json); got:\n%s", out)
		}
	})

	// ---- burn persistent mol -------------------------------------------------
	t.Run("burn_mol_dryrun_json_parses", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "tbm")
		proto := cookDryRunProto(t, bd, dir, "tbm-proto")
		mol := pourMoleculeRoot(t, bd, dir, proto)

		out := bdRunOK(t, bd, dir, "mol", "burn", mol, "--dry-run", "--json")
		assertDryRunJSON(t, "burn mol", out)
	})
	t.Run("burn_mol_dryrun_plain_keeps_preview", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "tbmp")
		proto := cookDryRunProto(t, bd, dir, "tbmp-proto")
		mol := pourMoleculeRoot(t, bd, dir, proto)

		out := bdRunOK(t, bd, dir, "mol", "burn", mol, "--dry-run")
		if !strings.Contains(out, "Dry run: would burn mol") {
			t.Errorf("beads-tcfwk: plain `mol burn <mol> --dry-run` must still print the "+
				"plaintext preview; got:\n%s", out)
		}
	})

	// ---- batch burn (two molecules) ------------------------------------------
	t.Run("burn_batch_dryrun_json_parses", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "tbb")
		p1 := cookDryRunProto(t, bd, dir, "tbb-p1")
		p2 := cookDryRunProto(t, bd, dir, "tbb-p2")
		m1 := pourMoleculeRoot(t, bd, dir, p1)
		m2 := pourMoleculeRoot(t, bd, dir, p2)

		out := bdRunOK(t, bd, dir, "mol", "burn", m1, m2, "--dry-run", "--json")
		assertDryRunJSON(t, "burn batch", out)
		// the batch envelope must carry a non-null id slice, not just dry_run
		var obj map[string]interface{}
		_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &obj)
		if _, ok := obj["would_burn"]; !ok {
			t.Errorf("beads-tcfwk: batch burn dry-run json lacks would_burn; got:\n%s", out)
		}
	})
	t.Run("burn_batch_dryrun_plain_keeps_preview", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "tbbp")
		p1 := cookDryRunProto(t, bd, dir, "tbbp-p1")
		p2 := cookDryRunProto(t, bd, dir, "tbbp-p2")
		m1 := pourMoleculeRoot(t, bd, dir, p1)
		m2 := pourMoleculeRoot(t, bd, dir, p2)

		out := bdRunOK(t, bd, dir, "mol", "burn", m1, m2, "--dry-run")
		if !strings.Contains(out, "Dry run: would burn") {
			t.Errorf("beads-tcfwk: plain batch `mol burn a b --dry-run` must still print the "+
				"plaintext preview; got:\n%s", out)
		}
	})

	// ---- wisp create ---------------------------------------------------------
	t.Run("wisp_create_dryrun_json_parses", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "twc")
		proto := cookDryRunProto(t, bd, dir, "twc-proto")

		out := bdRunOK(t, bd, dir, "mol", "wisp", proto, "--dry-run", "--json")
		assertDryRunJSON(t, "wisp create", out)
	})
	t.Run("wisp_create_dryrun_plain_keeps_preview", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "twcp")
		proto := cookDryRunProto(t, bd, dir, "twcp-proto")

		out := bdRunOK(t, bd, dir, "mol", "wisp", proto, "--dry-run")
		if !strings.Contains(out, "Dry run: would create wisp") {
			t.Errorf("beads-tcfwk: plain `mol wisp <proto> --dry-run` must still print the "+
				"plaintext preview; got:\n%s", out)
		}
	})
}

// wispMoleculeRoot creates a wisp (ephemeral) molecule from a proto and returns
// its root id — the input `bd mol burn <wisp>` routes through burnWispMolecule
// (root.Ephemeral == true). Distinct from pourMoleculeRoot, which yields a
// persistent (liquid) root that routes through burnPersistentMolecule.
func wispMoleculeRoot(t *testing.T, bd, dir, proto string) string {
	t.Helper()
	out := bdRunOK(t, bd, dir, "mol", "wisp", proto, "--json")
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &obj); err != nil {
		t.Fatalf("beads-tcfwk: `mol wisp --json` did not return JSON: %v\n%s", err, out)
	}
	root, _ := obj["new_epic_id"].(string)
	if root == "" {
		t.Fatalf("beads-tcfwk: `mol wisp --json` lacked new_epic_id; got:\n%s", out)
	}
	return root
}
