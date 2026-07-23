//go:build cgo

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEmbeddedImportMetadataKeyDrop_85nml is the beads-85nml teeth: the
// metadata twin-divergence between `bd update --metadata` and `bd import`.
//
// `bd update --metadata {k}` routes through MergeMetadataWithCAS (a shallow
// top-level MERGE — unlisted keys survive), but `bd import` applies the incoming
// metadata object VERBATIM (a full-state REPLACE — unlisted top-level keys are
// silently dropped). So an export -> edit (drop a key) -> import round-trip
// drops the omitted keys at RC=0 with a success line and NO warning.
//
// The fix (detectImportMetadataKeyDrops in import_shared.go) does NOT revert the
// drop — import is a full-state REPLACE by design, and flipping it to a merge
// would be a design-gated behavior change. Instead it makes the drop LOUD: it
// reports the ids whose incoming metadata omits top-level keys the local issue
// has (mirroring the InvalidMetadataIDs / SkippedDependencies skip-and-report
// idiom), so the otherwise-silent key loss is visible.
//
// Driven END-TO-END through the real `bd import` subprocess so the teeth
// exercise the actual upsert + detection plumbing. MUTATION-VERIFIED: neuter
// detectImportMetadataKeyDrops (e.g. `return nil, nil` at its top) ->
// round_trip_key_loss_is_reported + allow_stale_key_loss_is_reported go RED (the
// keys still drop — the bug — but no warning is emitted, so the assertions on
// the "Dropped metadata keys" report + JSON field fail).
func TestEmbeddedImportMetadataKeyDrop_85nml(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// A far-future updated_at makes the import line strictly newer than the
	// just-created local row, so the default stale guard imports it (the
	// upsert — the thing under test — actually runs).
	const newerTS = "2027-06-01T00:00:00Z"

	// metaKeys returns the sorted top-level keys of an issue's metadata blob.
	metaKeys := func(t *testing.T, raw json.RawMessage) []string {
		t.Helper()
		if len(raw) == 0 {
			return nil
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			t.Fatalf("metadata is not a JSON object: %s", raw)
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		return keys
	}

	// (1) The exact ts7vq-sibling repro: create {a,b}, import a newer line
	//     carrying only {c}. The verbatim REPLACE drops a+b (import's intended
	//     full-state semantics) — the fix must REPORT the drop, not silently
	//     eat it. This is the round-trip data-loss the bead documents.
	t.Run("round_trip_key_loss_is_reported", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "mkd")
		iss := bdCreate(t, bd, dir, "meta issue", "-t", "task", "--metadata", `{"a":"1","b":"2"}`)

		jsonl := filepath.Join(t.TempDir(), "drop.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"meta issue","issue_type":"task","metadata":{"c":"3"},"updated_at":%q}`, iss.ID, newerTS))
		out := bdImport(t, bd, dir, jsonl)

		// The drop must be REPORTED (the whole point — otherwise it is silent).
		if !strings.Contains(out, iss.ID) || !strings.Contains(out, "Dropped metadata keys") {
			t.Errorf("beads-85nml: import that drops metadata keys must report the id %s; got:\n%s", iss.ID, out)
		}
		// The import still REPLACES (design: import is full-state sync). Assert
		// the intended REPLACE actually happened so the report reflects reality.
		got := bdShow(t, bd, dir, iss.ID)
		keys := metaKeys(t, got.Metadata)
		if len(keys) != 1 || keys[0] != "c" {
			t.Errorf("beads-85nml: import applies metadata verbatim (REPLACE); expected only {c}, got keys %v (metadata=%s)", keys, got.Metadata)
		}
	})

	// (2) JSON output surfaces the dropped ids in metadata_keys_dropped.
	t.Run("json_reports_dropped_ids", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "mkj")
		iss := bdCreate(t, bd, dir, "meta issue", "-t", "task", "--metadata", `{"a":"1","b":"2"}`)

		jsonl := filepath.Join(t.TempDir(), "drop.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"meta issue","issue_type":"task","metadata":{"a":"1"},"updated_at":%q}`, iss.ID, newerTS))
		out := bdImport(t, bd, dir, jsonl, "--json")

		var res struct {
			MetadataKeysDropped []string `json:"metadata_keys_dropped"`
		}
		// --json returns the structured result on stdout and suppresses the
		// stderr advisories, so the drop info lives in metadata_keys_dropped.
		start := strings.Index(out, "{")
		if start < 0 {
			t.Fatalf("beads-85nml: no JSON object in --json import output:\n%s", out)
		}
		if err := json.Unmarshal([]byte(out[start:]), &res); err != nil {
			t.Fatalf("beads-85nml: parse --json import output failed: %v\n%s", err, out)
		}
		found := false
		for _, id := range res.MetadataKeysDropped {
			if id == iss.ID {
				found = true
			}
		}
		if !found {
			t.Errorf("beads-85nml: --json import must list %s in metadata_keys_dropped, got %v\n%s", iss.ID, res.MetadataKeysDropped, out)
		}
	})

	// (3) --allow-stale (older row, no --force) drops keys too — the detection
	//     runs on the post-filter landed set, which --allow-stale still writes.
	t.Run("allow_stale_key_loss_is_reported", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "mks")
		iss := bdCreate(t, bd, dir, "meta issue", "-t", "task", "--metadata", `{"a":"1","b":"2"}`)

		const olderTS = "2000-01-01T00:00:00Z"
		jsonl := filepath.Join(t.TempDir(), "stale-drop.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"meta issue","issue_type":"task","metadata":{"c":"3"},"updated_at":%q}`, iss.ID, olderTS))
		out := bdImport(t, bd, dir, jsonl, "--allow-stale")

		if !strings.Contains(out, iss.ID) || !strings.Contains(out, "Dropped metadata keys") {
			t.Errorf("beads-85nml: --allow-stale import that drops metadata keys must report the id %s; got:\n%s", iss.ID, out)
		}
	})

	// (4) CONTROL / regression: an import line carrying the SAME keys (superset
	//     or equal) drops nothing — no warning, no false positive.
	t.Run("no_drop_when_all_keys_present", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "mkn")
		iss := bdCreate(t, bd, dir, "meta issue", "-t", "task", "--metadata", `{"a":"1","b":"2"}`)

		jsonl := filepath.Join(t.TempDir(), "keep.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"meta issue","issue_type":"task","metadata":{"a":"1","b":"2","c":"3"},"updated_at":%q}`, iss.ID, newerTS))
		out := bdImport(t, bd, dir, jsonl)

		if strings.Contains(out, "Dropped metadata keys") {
			t.Errorf("beads-85nml: import that keeps all local keys (adds c) must NOT warn about dropped keys; got:\n%s", out)
		}
		got := bdShow(t, bd, dir, iss.ID)
		if keys := metaKeys(t, got.Metadata); len(keys) != 3 {
			t.Errorf("beads-85nml: expected 3 keys after superset import, got %v", keys)
		}
	})

	// (5) CONTROL / regression: an import line that OMITS the metadata field
	//     entirely preserves local metadata (restoreAbsentFieldsFromLocal) and
	//     must NOT warn — the drop guard only fires on a present-but-fewer-keys
	//     metadata object, not on absence.
	t.Run("no_drop_when_metadata_field_absent", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "mka")
		iss := bdCreate(t, bd, dir, "meta issue", "-t", "task", "--metadata", `{"a":"1","b":"2"}`)

		jsonl := filepath.Join(t.TempDir(), "no-meta.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"meta issue","issue_type":"task","updated_at":%q}`, iss.ID, newerTS))
		out := bdImport(t, bd, dir, jsonl)

		if strings.Contains(out, "Dropped metadata keys") {
			t.Errorf("beads-85nml: import omitting the metadata field must NOT warn (preserve-on-absent); got:\n%s", out)
		}
		got := bdShow(t, bd, dir, iss.ID)
		if keys := metaKeys(t, got.Metadata); len(keys) != 2 {
			t.Errorf("beads-85nml: metadata-absent import must preserve local {a,b}, got %v (metadata=%s)", keys, got.Metadata)
		}
	})

	// (6) CONTROL / regression: a genuinely-new issue with metadata (no local
	//     row) drops nothing — the guard only acts over an existing local issue.
	t.Run("no_drop_for_new_issue", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "mkc")
		jsonl := filepath.Join(t.TempDir(), "new.jsonl")
		writeRawJSONL(t, jsonl,
			`{"id":"mkc-9001","title":"brand new","issue_type":"task","metadata":{"x":"1"},"updated_at":"2027-06-01T00:00:00Z"}`)
		out := bdImport(t, bd, dir, jsonl)
		if strings.Contains(out, "Dropped metadata keys") {
			t.Errorf("beads-85nml: a genuinely-new issue import must not warn about dropped keys; got:\n%s", out)
		}
	})
}
