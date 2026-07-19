// Package kvkeys defines the config-table key prefixes that the cmd/bd KV and
// memory commands write and that the storage-layer merge resolver reads, so the
// "kv.memory.* config rows are convergent persistent memories" contract has a
// single source of truth.
//
// cmd/bd is package main and cannot be imported, so before this package the
// prefix lived in three structurally-forced copies (cmd/bd/kv.go,
// cmd/bd/memory.go, and internal/storage/versioncontrolops). A rename of any
// one silently drifted from the others: the merge resolver would stop matching
// real memory keys and the pull/sync config wedge would quietly return for the
// renamed keys with no compile error. Centralizing the prefix here makes that a
// single edit guarded by a contract test.
package kvkeys

const (
	// Prefix namespaces every user key under the synced config table, keeping
	// user data out of internal config keys such as issue_prefix.
	Prefix = "kv."

	// MemoryPrefix namespaces persistent `bd remember` memories within Prefix.
	MemoryPrefix = "memory."

	// MemoryConfigKeyPrefix is the full config-table key prefix for persistent
	// memories (Prefix + MemoryPrefix == "kv.memory."). The merge resolver
	// treats a config conflict as a convergent memory conflict only when every
	// conflicted key carries this prefix; cmd/bd reserves it so generic
	// `bd kv set` keys cannot collide with the `bd remember` namespace.
	MemoryConfigKeyPrefix = Prefix + MemoryPrefix
)

// ReservedJSONKeys are the top-level keys that cmd/bd's outputJSON /
// wrapWithSchemaVersion inject into a --json response. A user key equal to one
// of these silently collides with the injected key when a user-keyed map is
// emitted flat (bd memories / bd kv list / bd config list --json), losing the
// user value on read (beads-z0fe). bd remember / bd kv set reject such a user
// key at WRITE time; bd config list (whose keys are built-in and cannot be
// rejected) warns on the collision instead. Single source of truth shared by
// the write guards and cmd/bd/output.go's injection.
var ReservedJSONKeys = map[string]struct{}{
	"schema_version": {}, // always injected by wrapWithSchemaVersion
	"data":           {}, // the payload wrapper injected under BD_JSON_ENVELOPE=1
}

// IsReservedJSONKey reports whether key collides with a reserved --json
// envelope key (see ReservedJSONKeys).
func IsReservedJSONKey(key string) bool {
	_, ok := ReservedJSONKeys[key]
	return ok
}
