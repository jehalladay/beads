package dolt

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/steveyegge/beads/internal/storage/issueops"
)

// SlotSet sets a key-value pair in the issue's metadata JSON.
// If the issue has no metadata, a new JSON object is created.
// If the key already exists, its value is overwritten.
//
// The write is a single server-side JSON_SET (issueops.ApplyMetadataKeyEditsInTx)
// rather than a read-modify-write of the whole blob, so two concurrent slot
// edits to DIFFERENT keys on the same issue both survive instead of the second
// client-rebuilt blob clobbering the first (beads-fnp6).
func (s *DoltStore) SlotSet(ctx context.Context, issueID, key, value, actor string) error {
	// Preserve prior behavior: the slot value is stored as a JSON string.
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshaling slot value for %s: %w", issueID, err)
	}
	return s.withWriteTx(ctx, func(tx *sql.Tx) error {
		isWisp := issueops.IsActiveWispInTx(ctx, tx, issueID)
		issueTable, _, _, _ := issueops.WispTableRouting(isWisp)
		return issueops.ApplyMetadataKeyEditsInTx(ctx, tx, issueTable, issueID,
			map[string]json.RawMessage{key: encoded}, nil)
	})
}

// SlotGet retrieves the value of a metadata key from an issue.
// Returns an error if the issue has no metadata or the key is not found.
func (s *DoltStore) SlotGet(ctx context.Context, issueID, key string) (string, error) {
	issue, err := s.GetIssue(ctx, issueID)
	if err != nil {
		return "", fmt.Errorf("getting issue %s: %w", issueID, err)
	}

	if len(issue.Metadata) == 0 {
		return "", fmt.Errorf("no slot %q on %s: no metadata", key, issueID)
	}

	metadata := make(map[string]interface{})
	if err := json.Unmarshal(issue.Metadata, &metadata); err != nil {
		return "", fmt.Errorf("parsing metadata for %s: %w", issueID, err)
	}

	val, ok := metadata[key]
	if !ok {
		return "", fmt.Errorf("no slot %q on %s: key not found", key, issueID)
	}

	switch v := val.(type) {
	case string:
		return v, nil
	default:
		// Non-string values are returned as JSON
		raw, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("marshaling slot value for %s.%s: %w", issueID, key, err)
		}
		return string(raw), nil
	}
}

// SlotClear removes a metadata key from an issue.
// It is not an error to clear a key that doesn't exist.
func (s *DoltStore) SlotClear(ctx context.Context, issueID, key, actor string) error {
	// Single server-side JSON_REMOVE (idempotent: removing an absent key is a
	// no-op), atomic against concurrent edits to other keys (beads-fnp6).
	return s.withWriteTx(ctx, func(tx *sql.Tx) error {
		isWisp := issueops.IsActiveWispInTx(ctx, tx, issueID)
		issueTable, _, _, _ := issueops.WispTableRouting(isWisp)
		return issueops.ApplyMetadataKeyEditsInTx(ctx, tx, issueTable, issueID,
			nil, []string{key})
	})
}

// UpdateMetadataFields applies per-key metadata edits atomically (beads-fnp6).
func (s *DoltStore) UpdateMetadataFields(ctx context.Context, issueID string, sets map[string]json.RawMessage, unsets []string, actor string) error {
	if len(sets) == 0 && len(unsets) == 0 {
		return nil
	}
	return s.withWriteTx(ctx, func(tx *sql.Tx) error {
		isWisp := issueops.IsActiveWispInTx(ctx, tx, issueID)
		issueTable, _, _, _ := issueops.WispTableRouting(isWisp)
		return issueops.ApplyMetadataKeyEditsInTx(ctx, tx, issueTable, issueID, sets, unsets)
	})
}

// MergeMetadataWithCAS merges incoming metadata via a read-merge-write in one
// serializable transaction; withRetryTx replays on a commit-time conflict so
// concurrent --metadata merges don't clobber each other (beads-fnp6).
func (s *DoltStore) MergeMetadataWithCAS(ctx context.Context, issueID string, incoming json.RawMessage, actor string) error {
	return s.withRetryTx(ctx, func(tx *sql.Tx) error {
		isWisp := issueops.IsActiveWispInTx(ctx, tx, issueID)
		issueTable, _, _, _ := issueops.WispTableRouting(isWisp)
		return issueops.MergeMetadataInTx(ctx, tx, issueTable, issueID, incoming)
	})
}

// AppendNotes atomically appends text to an issue's notes at the DB in one
// transaction, so concurrent `bd update --append-notes` don't lose an update via
// a client-side read-modify-write (beads-jscve, notes twin of beads-jibd). Wisp-
// aware (routes to the wisps table for an active wisp).
func (s *DoltStore) AppendNotes(ctx context.Context, issueID, text, actor string) error {
	return s.withRetryTx(ctx, func(tx *sql.Tx) error {
		isWisp := issueops.IsActiveWispInTx(ctx, tx, issueID)
		issueTable, _, _, _ := issueops.WispTableRouting(isWisp)
		return issueops.AppendNotesInTx(ctx, tx, issueTable, issueID, text)
	})
}
