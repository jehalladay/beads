package issueops

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestPrepareIssueForInsert_RejectsControlCharMetadata_nc639 guards the wire
// from PrepareIssueForInsert to storage.ValidateMetadataReadable (beads-nc639).
// A control byte in metadata is accepted by the Dolt JSON column but re-emits
// unreadable JSON on readback, bricking every subsequent list/show/export
// repo-wide — so it must be rejected at the shared create+import write seam.
func TestPrepareIssueForInsert_RejectsControlCharMetadata_nc639(t *testing.T) {
	// metadata value carrying a raw ESC (OSC-52 injection lead byte).
	meta, err := json.Marshal(map[string]any{"k": "x\x1bz"})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	issue := &types.Issue{
		ID:        "bd-1",
		Title:     "ok",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		Metadata:  json.RawMessage(meta),
	}
	err = PrepareIssueForInsert(issue, nil, nil)
	if err == nil {
		t.Fatal("expected PrepareIssueForInsert to reject control-char metadata")
	}
	if !strings.Contains(err.Error(), "control character") {
		t.Errorf("error should name the control character; got: %v", err)
	}
}

// TestPrepareIssueForInsert_AllowsBenignMetadata_nc639 confirms the guard does
// not over-reject legitimate metadata (unicode, emoji, punctuation).
func TestPrepareIssueForInsert_AllowsBenignMetadata_nc639(t *testing.T) {
	meta, err := json.Marshal(map[string]any{"k": "café 🚀 \"quoted\" value/slash"})
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	issue := &types.Issue{
		ID:        "bd-2",
		Title:     "ok",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		Metadata:  json.RawMessage(meta),
	}
	if err := PrepareIssueForInsert(issue, nil, nil); err != nil {
		t.Fatalf("benign metadata should pass, got: %v", err)
	}
}
