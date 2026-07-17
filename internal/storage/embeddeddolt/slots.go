//go:build cgo

package embeddeddolt

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/steveyegge/beads/internal/storage/issueops"
)

// SlotSet sets a key-value pair in the issue's metadata JSON.
//
// Single server-side JSON_SET (issueops.ApplyMetadataKeyEditsInTx) rather than
// a whole-blob read-modify-write, so concurrent slot edits to different keys
// don't clobber each other (beads-fnp6).
func (s *EmbeddedDoltStore) SlotSet(ctx context.Context, issueID, key, value, actor string) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshaling slot value for %s: %w", issueID, err)
	}
	return s.withConn(ctx, true, func(tx *sql.Tx) error {
		isWisp := issueops.IsActiveWispInTx(ctx, tx, issueID)
		issueTable, _, _, _ := issueops.WispTableRouting(isWisp)
		return issueops.ApplyMetadataKeyEditsInTx(ctx, tx, issueTable, issueID,
			map[string]json.RawMessage{key: encoded}, nil)
	})
}

// SlotGet retrieves the value of a metadata key from an issue.
func (s *EmbeddedDoltStore) SlotGet(ctx context.Context, issueID, key string) (string, error) {
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
		raw, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("marshaling slot value for %s.%s: %w", issueID, key, err)
		}
		return string(raw), nil
	}
}

// SlotClear removes a metadata key from an issue.
func (s *EmbeddedDoltStore) SlotClear(ctx context.Context, issueID, key, actor string) error {
	// Single server-side JSON_REMOVE (idempotent), atomic against concurrent
	// edits to other keys (beads-fnp6).
	return s.withConn(ctx, true, func(tx *sql.Tx) error {
		isWisp := issueops.IsActiveWispInTx(ctx, tx, issueID)
		issueTable, _, _, _ := issueops.WispTableRouting(isWisp)
		return issueops.ApplyMetadataKeyEditsInTx(ctx, tx, issueTable, issueID,
			nil, []string{key})
	})
}

// UpdateMetadataFields applies per-key metadata edits atomically (beads-fnp6).
func (s *EmbeddedDoltStore) UpdateMetadataFields(ctx context.Context, issueID string, sets map[string]json.RawMessage, unsets []string, actor string) error {
	if len(sets) == 0 && len(unsets) == 0 {
		return nil
	}
	return s.withConn(ctx, true, func(tx *sql.Tx) error {
		isWisp := issueops.IsActiveWispInTx(ctx, tx, issueID)
		issueTable, _, _, _ := issueops.WispTableRouting(isWisp)
		return issueops.ApplyMetadataKeyEditsInTx(ctx, tx, issueTable, issueID, sets, unsets)
	})
}

// MergeMetadataWithCAS merges incoming metadata via a read-merge-write in one
// transaction so concurrent --metadata merges don't clobber each other
// (beads-fnp6).
func (s *EmbeddedDoltStore) MergeMetadataWithCAS(ctx context.Context, issueID string, incoming json.RawMessage, actor string) error {
	return s.withConn(ctx, true, func(tx *sql.Tx) error {
		isWisp := issueops.IsActiveWispInTx(ctx, tx, issueID)
		issueTable, _, _, _ := issueops.WispTableRouting(isWisp)
		return issueops.MergeMetadataInTx(ctx, tx, issueTable, issueID, incoming)
	})
}
