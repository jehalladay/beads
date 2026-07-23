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

// TestEmbeddedImportSummaryLabelsDeps_dn438 is the beads-dn438 teeth:
// importRowChangeSummary (import_shared.go) drives the import's created /
// updated / unchanged partition — a row with an empty summary is classified
// "Unchanged (already up to date)", a non-empty one is "Updated". The summary
// diffed only scalar/text columns (status, priority, type, title, ...,
// metadata) and OMITTED the two RELATIONAL fields the import apply path DOES
// mutate: labels (INSERT IGNORE union-add, PersistLabels) and dependencies
// (deterministic-PK additive INSERT, #4259). So a label-only or dep-only
// upsert had an empty summary -> was reported "Unchanged" while the row was
// really changed, corrupting the partition the beads-06x87/fkzvk/grmih family
// keeps honest and hiding the mutation from the "Updated ..." detail line.
//
// The fix extends importRowChangeSummary to diff labels (one-way set: an
// incoming label the local lacks) and dependencies (one-way set keyed on the
// edge target, since import is additive-only and never drops a local edge), and
// hydrates local.Dependencies at the call sites (GetIssuesByIDs leaves them
// nil). Both apply paths are additive, so the diff is one-way: an incoming
// member local already has is not reported, and a member only local has cannot
// be dropped by import so it is not reported either.
//
// Driven END-TO-END through the real `bd import` subprocess. MUTATION-VERIFIED:
// drop the labels leg (`if importLabelsAdded(...)`) or deps leg from
// importRowChangeSummary -> the label_only / dep_only / json / allow_stale
// report subtests go RED (the mutation still applies the label/dep — the bug —
// but the row is misreported "Unchanged").
func TestEmbeddedImportSummaryLabelsDeps_dn438(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// A far-future updated_at makes the import line strictly newer than the
	// just-created local row, so the default stale guard imports it (the
	// upsert being classified — the thing under test — actually runs).
	const newerTS = "2027-06-01T00:00:00Z"
	// A far-past updated_at exercises the planAllowStaleChanges path (an older
	// incoming row only lands under --allow-stale).
	const olderTS = "2000-01-01T00:00:00Z"

	depAdd := func(t *testing.T, dir, from, to string) {
		t.Helper()
		cmd := exec.Command(bd, "dep", "add", from, to)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("dep add %s %s failed: %v\n%s", from, to, err, out)
		}
	}
	labelAdd := func(t *testing.T, dir, id, label string) {
		t.Helper()
		cmd := exec.Command(bd, "label", "add", id, label)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("label add %s %s failed: %v\n%s", id, label, err, out)
		}
	}

	// (1) LABEL axis: local carries {x1}; a newer line carrying {x1,x2} with
	//     otherwise-identical scalar fields must classify as Updated / "labels",
	//     NOT the silent "Unchanged", and the new label must land.
	t.Run("label_only_upsert_reported_as_updated", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "dnl")
		iss := bdCreate(t, bd, dir, "label issue", "-t", "task")
		labelAdd(t, dir, iss.ID, "x1")

		jsonl := filepath.Join(t.TempDir(), "label.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"label issue","issue_type":"task","labels":["x1","x2"],"updated_at":%q}`, iss.ID, newerTS))
		out := bdImport(t, bd, dir, jsonl)

		if strings.Contains(out, "Unchanged (already up to date)") && strings.Contains(out, iss.ID) {
			t.Errorf("beads-dn438: a label-only upsert must NOT be reported Unchanged; got:\n%s", out)
		}
		if !strings.Contains(out, "labels") {
			t.Errorf("beads-dn438: a label-only upsert must report a 'labels' change for %s; got:\n%s", iss.ID, out)
		}
		got := bdShow(t, bd, dir, iss.ID)
		if !hasLabel(got, "x2") {
			t.Errorf("beads-dn438: import must union-add label x2; got labels %v", got.Labels)
		}
	})

	// (2) DEPENDENCY axis: local A has no deps; a newer A-line adding a blocks
	//     edge to B must classify as Updated / "dependencies".
	t.Run("dep_only_upsert_reported_as_updated", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "dnd")
		a := bdCreate(t, bd, dir, "A", "-t", "task")
		b := bdCreate(t, bd, dir, "B", "-t", "task")

		jsonl := filepath.Join(t.TempDir(), "dep.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"A","issue_type":"task","dependencies":[{"depends_on_id":%q,"type":"blocks"}],"updated_at":%q}`, a.ID, b.ID, newerTS))
		out := bdImport(t, bd, dir, jsonl)

		if strings.Contains(out, "Unchanged (already up to date)") && strings.Contains(out, a.ID) {
			t.Errorf("beads-dn438: a dep-only upsert must NOT be reported Unchanged; got:\n%s", out)
		}
		if !strings.Contains(out, "dependencies") {
			t.Errorf("beads-dn438: a dep-only upsert must report a 'dependencies' change for %s; got:\n%s", a.ID, out)
		}
	})

	// (3) JSON: metadata drift is surfaced in updated_issues[].changes and the
	//     id must NOT appear in unchanged_ids.
	t.Run("json_reports_labels_change", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "dnj")
		iss := bdCreate(t, bd, dir, "json issue", "-t", "task")
		labelAdd(t, dir, iss.ID, "x1")

		jsonl := filepath.Join(t.TempDir(), "label.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"json issue","issue_type":"task","labels":["x1","x2"],"updated_at":%q}`, iss.ID, newerTS))
		out := bdImport(t, bd, dir, jsonl, "--json")

		res := parseImportJSON(t, out)
		if res.Updated < 1 {
			t.Errorf("beads-dn438: --json label-only upsert must count as updated>=1; got updated=%d\n%s", res.Updated, out)
		}
		found := false
		for _, u := range res.UpdatedIssues {
			if u.ID == iss.ID && strings.Contains(u.Changes, "labels") {
				found = true
			}
		}
		if !found {
			t.Errorf("beads-dn438: --json must list %s in updated_issues with a 'labels' change; got %+v", iss.ID, res.UpdatedIssues)
		}
		for _, id := range res.UnchangedIDs {
			if id == iss.ID {
				t.Errorf("beads-dn438: %s changed a label, must NOT appear in unchanged_ids; got %v", iss.ID, res.UnchangedIDs)
			}
		}
	})

	// (4) --allow-stale path (planAllowStaleChanges): an OLDER row that adds a
	//     label still lands under --allow-stale and must be reported Updated.
	t.Run("allow_stale_label_upsert_reported", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "dns")
		iss := bdCreate(t, bd, dir, "stale issue", "-t", "task")
		labelAdd(t, dir, iss.ID, "x1")

		jsonl := filepath.Join(t.TempDir(), "stale.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"stale issue","issue_type":"task","labels":["x1","x2"],"updated_at":%q}`, iss.ID, olderTS))
		out := bdImport(t, bd, dir, "--allow-stale", jsonl)

		if strings.Contains(out, "Unchanged (already up to date)") && strings.Contains(out, iss.ID) {
			t.Errorf("beads-dn438: --allow-stale label upsert must NOT be reported Unchanged; got:\n%s", out)
		}
		if !strings.Contains(out, "labels") {
			t.Errorf("beads-dn438: --allow-stale label upsert must report a 'labels' change for %s; got:\n%s", iss.ID, out)
		}
	})

	// CONTROL (5): a newer line whose label set matches local exactly (and no
	//     other field differs) is a genuine no-op -> must stay Unchanged. Guards
	//     against the fix over-reporting a label that is already present.
	t.Run("no_label_change_stays_unchanged", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "dnc")
		iss := bdCreate(t, bd, dir, "control issue", "-t", "task")
		labelAdd(t, dir, iss.ID, "x1")

		jsonl := filepath.Join(t.TempDir(), "same.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"control issue","issue_type":"task","labels":["x1"],"updated_at":%q}`, iss.ID, newerTS))
		out := bdImport(t, bd, dir, jsonl)

		if strings.Contains(out, "labels") {
			t.Errorf("beads-dn438 CONTROL: identical label set must NOT report a labels change; got:\n%s", out)
		}
		if !strings.Contains(out, "Unchanged (already up to date)") || !strings.Contains(out, iss.ID) {
			t.Errorf("beads-dn438 CONTROL: a true no-op label re-import must stay Unchanged; got:\n%s", out)
		}
	})

	// CONTROL (6): re-importing an edge that already exists locally is a no-op
	//     (import is additive-only) -> must stay Unchanged.
	t.Run("existing_dep_reimport_stays_unchanged", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "dne")
		a := bdCreate(t, bd, dir, "A", "-t", "task")
		b := bdCreate(t, bd, dir, "B", "-t", "task")
		depAdd(t, dir, a.ID, b.ID)

		jsonl := filepath.Join(t.TempDir(), "samedep.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"A","issue_type":"task","dependencies":[{"depends_on_id":%q,"type":"blocks"}],"updated_at":%q}`, a.ID, b.ID, newerTS))
		out := bdImport(t, bd, dir, jsonl)

		if strings.Contains(out, "dependencies") {
			t.Errorf("beads-dn438 CONTROL: re-importing an existing edge must NOT report a dependencies change; got:\n%s", out)
		}
		if !strings.Contains(out, "Unchanged (already up to date)") || !strings.Contains(out, a.ID) {
			t.Errorf("beads-dn438 CONTROL: a true no-op edge re-import must stay Unchanged; got:\n%s", out)
		}
	})
}

// importJSONResult is the subset of `bd import --json` output the dn438 teeth assert on.
type importJSONResult struct {
	Updated       int `json:"updated"`
	UpdatedIssues []struct {
		ID      string `json:"id"`
		Changes string `json:"changes"`
	} `json:"updated_issues"`
	UnchangedIDs []string `json:"unchanged_ids"`
}

// parseImportJSON extracts the JSON object from `bd import --json` output
// (which may carry non-JSON preamble) and unmarshals it.
func parseImportJSON(t *testing.T, out string) importJSONResult {
	t.Helper()
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("beads-dn438: no JSON object in import output:\n%s", out)
	}
	var res importJSONResult
	if err := json.Unmarshal([]byte(out[start:]), &res); err != nil {
		t.Fatalf("beads-dn438: parse import --json: %v\n%s", err, out[start:])
	}
	return res
}
