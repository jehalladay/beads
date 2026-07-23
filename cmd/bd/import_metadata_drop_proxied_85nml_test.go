//go:build cgo

package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// TestProxiedImportMetadataKeyDrop_85nml is the proxied-server twin of
// TestEmbeddedImportMetadataKeyDrop_85nml (beads-85nml): `bd import` under a
// proxied-server backend flows through the SAME importIssuesCore/upsert +
// detectImportMetadataKeyDrops path as the embedded backend (import has no
// separate proxied handler — the global store is proxied-backed), so the
// metadata round-trip key-loss report must hold on the proxied path too.
//
// MUTATION-VERIFIED with the embedded test (they share detectImportMetadataKeyDrops):
// neuter the detector -> the keys still drop (the bug) but no warning is emitted
// on either backend (RED).
func TestProxiedImportMetadataKeyDrop_85nml(t *testing.T) {
	requireProxiedServerEnv(t)
	t.Parallel()

	bd := buildEmbeddedBD(t)

	const newerTS = "2027-06-01T00:00:00Z"

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

	// (1) Round-trip key loss is REPORTED on the proxied path, and the verbatim
	//     REPLACE still happened.
	t.Run("round_trip_key_loss_is_reported", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pmd")
		iss := bdProxiedCreate(t, bd, p.dir, "meta issue", "-t", "task", "--metadata", `{"a":"1","b":"2"}`)

		jsonl := filepath.Join(t.TempDir(), "drop.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"meta issue","issue_type":"task","metadata":{"c":"3"},"updated_at":%q}`, iss.ID, newerTS))
		// Use *Buffers so the stderr advisory ("Dropped metadata keys ...") is
		// captured — bdProxiedRun drops stderr on success.
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "import", jsonl)
		if err != nil {
			t.Fatalf("proxied bd import failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		out := stdout + stderr

		if !strings.Contains(out, iss.ID) || !strings.Contains(out, "Dropped metadata keys") {
			t.Errorf("beads-85nml (proxied): import that drops metadata keys must report the id %s; got:\n%s", iss.ID, out)
		}
		got := bdProxiedShow(t, bd, p.dir, iss.ID)
		if keys := metaKeys(t, got.Metadata); len(keys) != 1 || keys[0] != "c" {
			t.Errorf("beads-85nml (proxied): import applies metadata verbatim (REPLACE); expected only {c}, got %v (metadata=%s)", keys, got.Metadata)
		}
	})

	// (2) CONTROL / regression: a superset import (all local keys present) does
	//     NOT warn on the proxied path.
	t.Run("no_drop_when_all_keys_present", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pmn")
		iss := bdProxiedCreate(t, bd, p.dir, "meta issue", "-t", "task", "--metadata", `{"a":"1","b":"2"}`)

		jsonl := filepath.Join(t.TempDir(), "keep.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"meta issue","issue_type":"task","metadata":{"a":"1","b":"2","c":"3"},"updated_at":%q}`, iss.ID, newerTS))
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "import", jsonl)
		if err != nil {
			t.Fatalf("proxied bd import failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "Dropped metadata keys") {
			t.Errorf("beads-85nml (proxied): superset import must NOT warn about dropped keys; got:\n%s%s", stdout, stderr)
		}
	})

	// (3) CONTROL / regression: an import that OMITS the metadata field
	//     preserves local metadata and does NOT warn on the proxied path.
	t.Run("no_drop_when_metadata_field_absent", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "pma")
		iss := bdProxiedCreate(t, bd, p.dir, "meta issue", "-t", "task", "--metadata", `{"a":"1","b":"2"}`)

		jsonl := filepath.Join(t.TempDir(), "no-meta.jsonl")
		writeRawJSONL(t, jsonl, fmt.Sprintf(
			`{"id":%q,"title":"meta issue","issue_type":"task","updated_at":%q}`, iss.ID, newerTS))
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "import", jsonl)
		if err != nil {
			t.Fatalf("proxied bd import failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "Dropped metadata keys") {
			t.Errorf("beads-85nml (proxied): metadata-absent import must NOT warn; got:\n%s%s", stdout, stderr)
		}
		got := bdProxiedShow(t, bd, p.dir, iss.ID)
		if keys := metaKeys(t, got.Metadata); len(keys) != 2 {
			t.Errorf("beads-85nml (proxied): metadata-absent import must preserve local {a,b}, got %v", keys)
		}
	})
}
