package issueops

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/storage"
)

// ApplyMetadataKeyEditsInTx applies per-key metadata edits (--set-metadata /
// --unset-metadata, and SlotSet/SlotClear) using a single server-side
// JSON_SET / JSON_REMOVE statement, so two concurrent edits to DIFFERENT keys
// on the same issue both survive instead of the last client-rebuilt blob
// clobbering the other (beads-fnp6). The read-merge-write is atomic at the DB;
// there is no client-side snapshot to go stale.
//
// sets maps metadata keys to already-encoded JSON values (use toJSONValue at
// the CLI, or json.Marshal); unsets lists keys to remove. Keys are validated
// and dot-quoted via storage.JSONMetadataPath. updated_at is bumped. A missing
// issue yields no error (rowsAffected 0) so callers can distinguish via a prior
// existence check if needed; a no-op (empty sets+unsets) returns nil.
//
//nolint:gosec // G201: table is a WispTableRouting constant; JSON paths are placeholders/validated keys.
func ApplyMetadataKeyEditsInTx(ctx context.Context, tx DBTX, table, id string, sets map[string]json.RawMessage, unsets []string) error {
	if len(sets) == 0 && len(unsets) == 0 {
		return nil
	}

	// Build a single expression that nests JSON_REMOVE(...) around
	// JSON_SET(...) around COALESCE(metadata,'{}'), so all edits apply in one
	// atomic UPDATE. JSON_SET takes (doc, path, val, path, val, ...); JSON_REMOVE
	// takes (doc, path, ...). Order: set first, then remove (a key in both sets
	// and unsets ends up removed — matches the client-side applyMetadataEdits
	// which deletes after setting).
	expr := "COALESCE(metadata, '{}')"
	var args []interface{}

	if len(sets) > 0 {
		var setPairs []string
		// Deterministic order isn't required for correctness (distinct keys),
		// but keep it stable for readable SQL/tests.
		for _, key := range sortedMetadataKeys(sets) {
			if err := storage.ValidateMetadataKey(key); err != nil {
				return err
			}
			setPairs = append(setPairs, "?, CAST(? AS JSON)")
			args = append(args, storage.JSONMetadataPath(key), string(sets[key]))
		}
		expr = fmt.Sprintf("JSON_SET(%s, %s)", expr, strings.Join(setPairs, ", "))
	}

	if len(unsets) > 0 {
		var removePaths []string
		for _, key := range unsets {
			if err := storage.ValidateMetadataKey(key); err != nil {
				return err
			}
			removePaths = append(removePaths, "?")
			args = append(args, storage.JSONMetadataPath(key))
		}
		expr = fmt.Sprintf("JSON_REMOVE(%s, %s)", expr, strings.Join(removePaths, ", "))
	}

	query := fmt.Sprintf("UPDATE %s SET metadata = %s, updated_at = UTC_TIMESTAMP() WHERE id = ?", table, expr)
	args = append(args, id)

	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("apply metadata key edits for %s: %w", id, err)
	}
	return nil
}

// MergeMetadataInTx performs a --metadata whole-blob MERGE (incoming shallow
// top-level keys overwrite existing, other existing keys preserved) as a
// read-merge-write WITHIN one transaction. The read and write happen in the
// same serializable transaction, so a concurrent writer that commits between
// them makes THIS commit fail with a serialization conflict (Dolt 1213/1205),
// which the store's withRetryTx wrapper replays with a fresh read — the same
// commit-time-conflict + retry model the merge-slot acquire uses (beads-ynj8).
// This eliminates the old client-side read-modify-write clobber where two
// concurrent --metadata merges silently lost one side (beads-fnp6).
//
// incoming must be a JSON object (arbitrary keys allowed — unlike the per-key
// path, --metadata keys are not restricted to the JSON_SET-path charset). A
// missing issue returns ErrNotFound.
//
//nolint:gosec // G201: table is a WispTableRouting constant.
func MergeMetadataInTx(ctx context.Context, tx DBTX, table, id string, incoming json.RawMessage) error {
	var current sql.NullString
	err := tx.QueryRowContext(ctx,
		fmt.Sprintf("SELECT metadata FROM %s WHERE id = ?", table), id).Scan(&current)
	if err == sql.ErrNoRows {
		return fmt.Errorf("%w: %s", storage.ErrNotFound, id)
	}
	if err != nil {
		return fmt.Errorf("read metadata for merge %s: %w", id, err)
	}

	base := make(map[string]json.RawMessage)
	if current.Valid {
		trimmed := strings.TrimSpace(current.String)
		if trimmed != "" && trimmed != "null" {
			if uerr := json.Unmarshal([]byte(current.String), &base); uerr != nil {
				return fmt.Errorf("existing metadata for %s is not a JSON object: %w", id, uerr)
			}
		}
	}

	incomingMap := make(map[string]json.RawMessage)
	if uerr := json.Unmarshal(incoming, &incomingMap); uerr != nil {
		return fmt.Errorf("new metadata is not a JSON object: %w", uerr)
	}
	for k, v := range incomingMap {
		base[k] = v
	}

	merged, merr := json.Marshal(base)
	if merr != nil {
		return fmt.Errorf("marshal merged metadata for %s: %w", id, merr)
	}

	if _, eerr := tx.ExecContext(ctx,
		fmt.Sprintf("UPDATE %s SET metadata = ?, updated_at = UTC_TIMESTAMP() WHERE id = ?", table),
		string(merged), id); eerr != nil {
		return fmt.Errorf("write merged metadata for %s: %w", id, eerr)
	}
	return nil
}

func sortedMetadataKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// simple insertion sort to avoid importing sort for a tiny map
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
