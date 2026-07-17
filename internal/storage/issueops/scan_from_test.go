package issueops

import (
	"database/sql"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// fakeScanner implements IssueScanner without a database. It assigns values
// by inspecting each dest pointer's concrete type, so tests exercise
// ScanIssueFrom's column->field mapping and null handling deterministically.
type fakeScanner struct {
	id           string
	title        string
	assignee     string
	createdAtStr string
	updatedAtStr string
}

func (f *fakeScanner) Scan(dest ...any) error {
	// Assign only the fields the test asserts on; leave the rest zero-valued.
	// Dest order comes from ScanIssueFrom's dests slice. The plain *string
	// dests in order are: issue.ID, issue.Title, issue.Description, ... — we
	// fill the first two (ID, Title). The *sql.NullString dests in order are:
	// contentHash(0), assignee(1), createdAtStr(2), createdBy(3), owner(4),
	// updatedAtStr(5), ...
	strSeen := 0
	nullStringSeen := 0
	for _, d := range dest {
		switch p := d.(type) {
		case *string:
			switch strSeen {
			case 0:
				*p = f.id
			case 1:
				*p = f.title
			}
			strSeen++
		case *sql.NullString:
			switch nullStringSeen {
			case 1:
				if f.assignee != "" {
					*p = sql.NullString{Valid: true, String: f.assignee}
				}
			case 2:
				*p = sql.NullString{Valid: true, String: f.createdAtStr}
			case 5:
				*p = sql.NullString{Valid: true, String: f.updatedAtStr}
			}
			nullStringSeen++
		}
	}
	return nil
}

func TestScanIssueFromMapsCoreFields(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	updated := created.Add(2 * time.Hour)
	fs := &fakeScanner{
		id:           "bd-scan-1",
		title:        "Scanned Issue",
		assignee:     "alice",
		createdAtStr: created.Format(time.RFC3339),
		updatedAtStr: updated.Format(time.RFC3339),
	}

	issue, err := ScanIssueFrom(fs)
	if err != nil {
		t.Fatalf("ScanIssueFrom: %v", err)
	}
	if issue.ID != "bd-scan-1" {
		t.Errorf("ID = %q, want bd-scan-1", issue.ID)
	}
	if issue.Title != "Scanned Issue" {
		t.Errorf("Title = %q, want \"Scanned Issue\"", issue.Title)
	}
	if !issue.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v (parsed from TEXT)", issue.CreatedAt, created)
	}
	if !issue.UpdatedAt.Equal(updated) {
		t.Errorf("UpdatedAt = %v, want %v (parsed from TEXT)", issue.UpdatedAt, updated)
	}
	if issue.Assignee != "alice" {
		t.Errorf("Assignee = %q, want alice (nullable field mapping)", issue.Assignee)
	}
}

// errScanner returns an error from Scan so ScanIssueFrom's error path runs.
type errScanner struct{ err error }

func (e errScanner) Scan(dest ...any) error { return e.err }

func TestScanIssueFromPropagatesScanError(t *testing.T) {
	t.Parallel()

	wantErr := sql.ErrNoRows
	_, err := ScanIssueFrom(errScanner{err: wantErr})
	if err != wantErr {
		t.Fatalf("ScanIssueFrom error = %v, want %v", err, wantErr)
	}
}

// capturingScanner records how many dests it was handed so the extra
// passthrough can be asserted.
type capturingScanner struct{ n int }

func (c *capturingScanner) Scan(dest ...any) error {
	c.n = len(dest)
	return nil
}

func TestScanIssueFromAppendsExtraDests(t *testing.T) {
	t.Parallel()

	base := &capturingScanner{}
	if _, err := ScanIssueFrom(base); err != nil {
		t.Fatalf("ScanIssueFrom(no extra): %v", err)
	}
	baseN := base.n

	withExtra := &capturingScanner{}
	var extra1, extra2 sql.NullString
	if _, err := ScanIssueFrom(withExtra, &extra1, &extra2); err != nil {
		t.Fatalf("ScanIssueFrom(2 extra): %v", err)
	}
	if withExtra.n != baseN+2 {
		t.Fatalf("extra dests: got %d, want base %d + 2", withExtra.n, baseN)
	}
}

func TestRecordSkippedDependency(t *testing.T) {
	t.Parallel()

	t.Run("nil dep is a no-op", func(t *testing.T) {
		t.Parallel()
		called := false
		opts := storage.BatchCreateOptions{OnSkippedDependency: func(_, _, _ string) { called = true }}
		recordSkippedDependency(opts, nil, "reason")
		if called {
			t.Fatal("callback should not fire for nil dep")
		}
	})

	t.Run("forwards dep fields to the callback", func(t *testing.T) {
		t.Parallel()
		var gotIssue, gotDep, gotReason string
		opts := storage.BatchCreateOptions{OnSkippedDependency: func(i, d, r string) {
			gotIssue, gotDep, gotReason = i, d, r
		}}
		dep := &types.Dependency{IssueID: "bd-1", DependsOnID: "bd-2"}
		recordSkippedDependency(opts, dep, "cross-bucket")
		if gotIssue != "bd-1" || gotDep != "bd-2" || gotReason != "cross-bucket" {
			t.Fatalf("callback got (%q,%q,%q), want (bd-1,bd-2,cross-bucket)", gotIssue, gotDep, gotReason)
		}
	})
}

func TestRecordSkippedDependencyEdge(t *testing.T) {
	t.Parallel()

	t.Run("nil callback is a no-op (no panic)", func(t *testing.T) {
		t.Parallel()
		recordSkippedDependencyEdge(storage.BatchCreateOptions{}, "bd-1", "bd-2", "r")
	})

	t.Run("invokes callback with the given edge", func(t *testing.T) {
		t.Parallel()
		var got [3]string
		opts := storage.BatchCreateOptions{OnSkippedDependency: func(i, d, r string) {
			got = [3]string{i, d, r}
		}}
		recordSkippedDependencyEdge(opts, "bd-a", "bd-b", "why")
		if got != [3]string{"bd-a", "bd-b", "why"} {
			t.Fatalf("callback got %v, want [bd-a bd-b why]", got)
		}
	})
}
