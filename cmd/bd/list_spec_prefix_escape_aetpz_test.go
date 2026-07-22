//go:build cgo

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func skipUnlessEmbeddedDolt(t *testing.T) {
	t.Helper()
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
}

// TestListSpecPrefixEscape_aetpz is the end-to-end teeth for beads-aetpz: the
// `--spec <prefix>` filter (filter.SpecIDPrefix) and the `id=<prefix>*` query
// filter (filter.IDPrefix) build a `LIKE ?` clause with the RAW user prefix +
// "%", so a literal `_` or `%` in the prefix acted as a wildcard and over-matched
// (`--spec 'SPEC_A'` also returned `SPECXA`; `--spec '%'` returned every row).
// The fix escapes LIKE metachars in the prefix + adds `ESCAPE '\'` so the trailing
// `%` stays the only wildcard. Mutation-verify: revert the EscapeLikePattern calls
// in internal/storage/sqlbuild/filter.go and this test goes RED (SPECXA leaks in).
//
// Sibling of the b9ova/k3xye free-text LIKE-escape family.
func TestListSpecPrefixEscape_aetpz(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "sp")

	// Two issues whose spec_ids differ ONLY at the position of the literal '_':
	// SPEC_A (underscore) vs SPECXA (any-single-char in LIKE). Without escaping,
	// `spec_id LIKE 'SPEC_A%'` matches BOTH because '_' is a single-char wildcard.
	specA := bdCreate(t, bd, dir, "spec underscore literal", "--type", "task", "--spec-id", "SPEC_A")
	specX := bdCreate(t, bd, dir, "spec wildcard collision", "--type", "task", "--spec-id", "SPECXA")

	listSpec := func(t *testing.T, prefix string) []types.IssueWithCounts {
		t.Helper()
		cmd := exec.Command(bd, "list", "--json", "--spec", prefix)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd list --spec %q failed: %v\nstderr:\n%s", prefix, err, stderr.String())
		}
		var got []types.IssueWithCounts
		if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &got); err != nil {
			t.Fatalf("parse list JSON: %v\n%s", err, stdout.String())
		}
		return got
	}

	t.Run("underscore_matches_literally_not_as_wildcard", func(t *testing.T) {
		got := listSpec(t, "SPEC_A")
		if len(got) != 1 {
			ids := make([]string, len(got))
			for i, g := range got {
				ids[i] = g.ID + "(" + g.SpecID + ")"
			}
			t.Fatalf("--spec 'SPEC_A' returned %d issues %v, want exactly 1 (SPEC_A only; the '_' must not wildcard-match SPECXA)", len(got), ids)
		}
		if got[0].ID != specA.ID {
			t.Fatalf("--spec 'SPEC_A' returned %s, want %s (SPEC_A)", got[0].ID, specA.ID)
		}
	})

	t.Run("percent_matches_literally_not_all_rows", func(t *testing.T) {
		// A bare '%' prefix must match a LITERAL '%', not every row. Neither of our
		// two issues has a '%' in its spec_id, so the result must be empty.
		got := listSpec(t, "%")
		if len(got) != 0 {
			t.Fatalf("--spec '%%' returned %d issues, want 0 (a literal '%%' must not act as match-all)", len(got))
		}
		_ = specX // referenced for clarity: SPECXA must NOT leak into either query
	})
}

// TestQueryIDPrefixEscape_aetpz is the FAMILY sibling: the SAME unescaped
// `id LIKE ?` bug at filter.go IDPrefix, reached via `bd query "id=<prefix>*"`.
// Guards against the b9ova sibling-miss (fixing spec-id but not id-prefix).
func TestQueryIDPrefixEscape_aetpz(t *testing.T) {
	skipUnlessEmbeddedDolt(t)
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "iq")

	// Explicit IDs differing only at the '_' position: iq_a vs iqXa.
	idA := bdCreate(t, bd, dir, "id underscore literal", "--type", "task", "--id", "iq-a_b")
	bdCreate(t, bd, dir, "id wildcard collision", "--type", "task", "--id", "iq-aXb")

	cmd := exec.Command(bd, "query", "id=iq-a_b*", "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd query 'id=iq-a_b*' failed: %v\nstderr:\n%s", err, stderr.String())
	}
	var got []types.IssueWithCounts
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &got); err != nil {
		t.Fatalf("parse query JSON: %v\n%s", err, stdout.String())
	}
	if len(got) != 1 {
		ids := make([]string, len(got))
		for i, g := range got {
			ids[i] = g.ID
		}
		t.Fatalf("query 'id=iq-a_b*' returned %d issues %v, want exactly 1 (iq-a_b only; the '_' must not wildcard-match iq-aXb)", len(got), ids)
	}
	if got[0].ID != idA.ID {
		t.Fatalf("query 'id=iq-a_b*' returned %s, want %s (iq-a_b)", got[0].ID, idA.ID)
	}
}
