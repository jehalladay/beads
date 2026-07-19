package kvkeys

import "testing"

// TestMemoryConfigKeyPrefix pins the canonical config-table prefix for
// persistent memories. The merge resolver auto-resolves config conflicts only
// when every conflicted key carries this prefix (GH#2474); if it ever drifts
// from what cmd/bd actually writes, memory conflicts silently fall back to the
// operator and the pull/sync config wedge returns for the renamed keys. This
// test makes such a rename a conscious, caught change rather than a silent one.
func TestMemoryConfigKeyPrefix(t *testing.T) {
	if got, want := MemoryConfigKeyPrefix, "kv.memory."; got != want {
		t.Fatalf("MemoryConfigKeyPrefix = %q, want %q (renaming it re-wedges memory merge conflicts)", got, want)
	}
	if MemoryConfigKeyPrefix != Prefix+MemoryPrefix {
		t.Fatalf("MemoryConfigKeyPrefix %q must equal Prefix %q + MemoryPrefix %q",
			MemoryConfigKeyPrefix, Prefix, MemoryPrefix)
	}
}

// TestIsReservedJSONKey pins the reserved --json envelope keys (beads-z0fe):
// schema_version and data are what cmd/bd's wrapWithSchemaVersion injects, so a
// user memory/kv key equal to one would be silently clobbered on a flat --json
// read. This is the single source of truth the write-guards + config warn share.
func TestIsReservedJSONKey(t *testing.T) {
	for _, k := range []string{"schema_version", "data"} {
		if !IsReservedJSONKey(k) {
			t.Errorf("IsReservedJSONKey(%q) = false, want true (reserved --json envelope key)", k)
		}
	}
	for _, k := range []string{"", "schema", "version", "data_", "mydata", "feature_flag", "SCHEMA_VERSION"} {
		if IsReservedJSONKey(k) {
			t.Errorf("IsReservedJSONKey(%q) = true, want false (not a reserved key)", k)
		}
	}
}
