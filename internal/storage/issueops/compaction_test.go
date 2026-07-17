package issueops

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/types"
)

// These tests cover the compaction.go tx helpers using sqlmock — hermetic, no
// live Dolt. getCompactDaysInTx reads a config row (with default fallbacks);
// CheckEligibilityInTx reads the issue row and, when it reaches the age gate,
// calls getCompactDaysInTx (a second config query); ApplyCompactionInTx runs a
// single UPDATE. The default sqlmock QueryMatcher is regexp/partial.

// expectConfigDays queues the config read getCompactDaysInTx issues, returning
// the given value string (use "" via WillReturnError(sql.ErrNoRows) for the
// missing-row path in dedicated tests).
func expectConfigDays(mock sqlmock.Sqlmock, value string) {
	mock.ExpectQuery("SELECT value FROM config WHERE").
		WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(value))
}

// eligIssueRow builds the 3-column row CheckEligibilityInTx scans.
func eligIssueRow(status string, closedAt any, compactionLevel int) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"status", "closed_at", "compaction_level"}).
		AddRow(status, closedAt, compactionLevel)
}

func TestGetCompactDaysInTx_Defaults(t *testing.T) {
	t.Parallel()

	t.Run("tier1 missing row → default 30", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery("SELECT value FROM config WHERE").
			WillReturnError(errors.New("no row"))
		if got := getCompactDaysInTx(context.Background(), tx, 1); got != 30 {
			t.Errorf("got %d, want default 30", got)
		}
	})

	t.Run("tier2 empty value → default 90", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectConfigDays(mock, "")
		if got := getCompactDaysInTx(context.Background(), tx, 2); got != 90 {
			t.Errorf("got %d, want default 90", got)
		}
	})

	t.Run("non-numeric value → default", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectConfigDays(mock, "notanumber")
		if got := getCompactDaysInTx(context.Background(), tx, 1); got != 30 {
			t.Errorf("got %d, want default 30 on parse failure", got)
		}
	})

	t.Run("non-positive value → default", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectConfigDays(mock, "0")
		if got := getCompactDaysInTx(context.Background(), tx, 1); got != 30 {
			t.Errorf("got %d, want default 30 on non-positive", got)
		}
	})

	t.Run("valid override", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectConfigDays(mock, "7")
		if got := getCompactDaysInTx(context.Background(), tx, 1); got != 7 {
			t.Errorf("got %d, want 7", got)
		}
	})
}

func TestCheckEligibilityInTx_EarlyExits(t *testing.T) {
	t.Parallel()

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`FROM issues WHERE id = \?`).
			WithArgs("bd-x").
			WillReturnRows(sqlmock.NewRows([]string{"status", "closed_at", "compaction_level"}))
		ok, reason, err := CheckEligibilityInTx(context.Background(), tx, "bd-x", 1)
		if err != nil || ok {
			t.Fatalf("ok=%v err=%v, want false/nil", ok, err)
		}
		if reason == "" {
			t.Error("want a not-found reason")
		}
	})

	t.Run("query error", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`FROM issues WHERE id = \?`).
			WithArgs("bd-e").
			WillReturnError(errors.New("boom"))
		_, _, err := CheckEligibilityInTx(context.Background(), tx, "bd-e", 1)
		if err == nil {
			t.Fatal("want a query error")
		}
	})

	t.Run("not closed", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`FROM issues WHERE id = \?`).
			WithArgs("bd-o").
			WillReturnRows(eligIssueRow("open", nil, 0))
		ok, reason, _ := CheckEligibilityInTx(context.Background(), tx, "bd-o", 1)
		if ok || reason == "" {
			t.Errorf("ok=%v reason=%q, want false + reason", ok, reason)
		}
	})

	t.Run("closed but no closed_at", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`FROM issues WHERE id = \?`).
			WithArgs("bd-n").
			WillReturnRows(eligIssueRow("closed", nil, 0))
		ok, reason, _ := CheckEligibilityInTx(context.Background(), tx, "bd-n", 1)
		if ok || reason != "issue has no closed_at timestamp" {
			t.Errorf("ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("unsupported tier", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`FROM issues WHERE id = \?`).
			WithArgs("bd-u").
			WillReturnRows(eligIssueRow("closed", time.Now().Add(-1000*time.Hour), 0))
		expectConfigDays(mock, "30")
		ok, reason, _ := CheckEligibilityInTx(context.Background(), tx, "bd-u", 3)
		if ok || reason != "unsupported tier: 3" {
			t.Errorf("ok=%v reason=%q", ok, reason)
		}
	})
}

func TestCheckEligibilityInTx_Tier1(t *testing.T) {
	t.Parallel()

	t.Run("already compacted", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`FROM issues WHERE id = \?`).
			WithArgs("bd-1").
			WillReturnRows(eligIssueRow("closed", time.Now().Add(-1000*time.Hour), 1))
		expectConfigDays(mock, "30")
		ok, reason, _ := CheckEligibilityInTx(context.Background(), tx, "bd-1", 1)
		if ok || reason != "already compacted at tier 1 or higher" {
			t.Errorf("ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("too recent", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`FROM issues WHERE id = \?`).
			WithArgs("bd-2").
			WillReturnRows(eligIssueRow("closed", time.Now().Add(-24*time.Hour), 0))
		expectConfigDays(mock, "30")
		ok, reason, _ := CheckEligibilityInTx(context.Background(), tx, "bd-2", 1)
		if ok || reason == "" {
			t.Errorf("ok=%v reason=%q, want false + too-recent reason", ok, reason)
		}
	})

	t.Run("eligible", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`FROM issues WHERE id = \?`).
			WithArgs("bd-3").
			WillReturnRows(eligIssueRow("closed", time.Now().Add(-1000*time.Hour), 0))
		expectConfigDays(mock, "30")
		ok, reason, err := CheckEligibilityInTx(context.Background(), tx, "bd-3", 1)
		if err != nil || !ok || reason != "" {
			t.Errorf("ok=%v reason=%q err=%v, want true/empty/nil", ok, reason, err)
		}
	})
}

func TestCheckEligibilityInTx_Tier2(t *testing.T) {
	t.Parallel()

	old := time.Now().Add(-3000 * time.Hour)

	t.Run("already tier2", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`FROM issues WHERE id = \?`).
			WithArgs("bd-a").WillReturnRows(eligIssueRow("closed", old, 2))
		expectConfigDays(mock, "90")
		ok, reason, _ := CheckEligibilityInTx(context.Background(), tx, "bd-a", 2)
		if ok || reason != "already compacted at tier 2" {
			t.Errorf("ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("must be tier1 first", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`FROM issues WHERE id = \?`).
			WithArgs("bd-b").WillReturnRows(eligIssueRow("closed", old, 0))
		expectConfigDays(mock, "90")
		ok, reason, _ := CheckEligibilityInTx(context.Background(), tx, "bd-b", 2)
		if ok || reason != "must be tier 1 compacted first" {
			t.Errorf("ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("too recent", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`FROM issues WHERE id = \?`).
			WithArgs("bd-c").WillReturnRows(eligIssueRow("closed", time.Now().Add(-24*time.Hour), 1))
		expectConfigDays(mock, "90")
		ok, reason, _ := CheckEligibilityInTx(context.Background(), tx, "bd-c", 2)
		if ok || reason == "" {
			t.Errorf("ok=%v reason=%q, want false + too-recent", ok, reason)
		}
	})

	t.Run("eligible", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`FROM issues WHERE id = \?`).
			WithArgs("bd-d").WillReturnRows(eligIssueRow("closed", old, 1))
		expectConfigDays(mock, "90")
		ok, reason, err := CheckEligibilityInTx(context.Background(), tx, "bd-d", 2)
		if err != nil || !ok || reason != "" {
			t.Errorf("ok=%v reason=%q err=%v, want true/empty/nil", ok, reason, err)
		}
	})
}

func TestApplyCompactionInTx(t *testing.T) {
	t.Parallel()

	t.Run("happy", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectExec(`UPDATE issues SET compaction_level`).
			WithArgs(1, sqlmock.AnyArg(), "abc123", 4096, sqlmock.AnyArg(), "bd-1").
			WillReturnResult(sqlmock.NewResult(0, 1))
		if err := ApplyCompactionInTx(context.Background(), tx, "bd-1", 1, 4096, "abc123"); err != nil {
			t.Fatalf("ApplyCompactionInTx: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("update error", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectExec(`UPDATE issues SET compaction_level`).
			WillReturnError(errors.New("update boom"))
		if err := ApplyCompactionInTx(context.Background(), tx, "bd-2", 1, 0, "x"); err == nil {
			t.Fatal("err = nil, want an update error")
		}
	})
}

func TestSnapshotIssueInTx(t *testing.T) {
	t.Parallel()

	t.Run("happy", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`SELECT title, description, design, notes, acceptance_criteria FROM issues WHERE id = \?`).
			WithArgs("bd-1").
			WillReturnRows(sqlmock.NewRows([]string{"title", "description", "design", "notes", "acceptance_criteria"}).
				AddRow("t", "d", "des", "n", "ac"))
		mock.ExpectExec(`INSERT INTO compaction_snapshots`).
			WillReturnResult(sqlmock.NewResult(1, 1))
		if err := SnapshotIssueInTx(context.Background(), tx, "bd-1", 1); err != nil {
			t.Fatalf("SnapshotIssueInTx: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`FROM issues WHERE id = \?`).
			WithArgs("bd-x").
			WillReturnRows(sqlmock.NewRows([]string{"title", "description", "design", "notes", "acceptance_criteria"}))
		if err := SnapshotIssueInTx(context.Background(), tx, "bd-x", 1); err == nil {
			t.Fatal("err = nil, want not-found error")
		}
	})

	t.Run("read error", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`FROM issues WHERE id = \?`).
			WithArgs("bd-e").WillReturnError(errors.New("read boom"))
		if err := SnapshotIssueInTx(context.Background(), tx, "bd-e", 1); err == nil {
			t.Fatal("err = nil, want read error")
		}
	})

	t.Run("insert error", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`FROM issues WHERE id = \?`).
			WithArgs("bd-i").
			WillReturnRows(sqlmock.NewRows([]string{"title", "description", "design", "notes", "acceptance_criteria"}).
				AddRow("t", "d", "des", "n", "ac"))
		mock.ExpectExec(`INSERT INTO compaction_snapshots`).
			WillReturnError(errors.New("insert boom"))
		if err := SnapshotIssueInTx(context.Background(), tx, "bd-i", 2); err == nil {
			t.Fatal("err = nil, want insert error")
		}
	})
}

func TestGetLatestSnapshotInTx(t *testing.T) {
	t.Parallel()

	t.Run("found", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		payload, _ := json.Marshal(&types.IssueSnapshot{Title: "t", Description: "d"})
		mock.ExpectQuery(`FROM compaction_snapshots WHERE issue_id = \?`).
			WithArgs("bd-1").
			WillReturnRows(sqlmock.NewRows([]string{"compaction_level", "snapshot_json"}).AddRow(2, payload))
		snap, err := GetLatestSnapshotInTx(context.Background(), tx, "bd-1")
		if err != nil {
			t.Fatalf("GetLatestSnapshotInTx: %v", err)
		}
		if snap == nil || snap.CompactionLevel != 2 || snap.Description != "d" {
			t.Errorf("snap = %+v, want level 2 / desc d", snap)
		}
	})

	t.Run("none", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`FROM compaction_snapshots WHERE issue_id = \?`).
			WithArgs("bd-none").
			WillReturnRows(sqlmock.NewRows([]string{"compaction_level", "snapshot_json"}))
		snap, err := GetLatestSnapshotInTx(context.Background(), tx, "bd-none")
		if err != nil || snap != nil {
			t.Errorf("snap=%v err=%v, want nil/nil", snap, err)
		}
	})

	t.Run("query error", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`FROM compaction_snapshots WHERE issue_id = \?`).
			WithArgs("bd-e").WillReturnError(errors.New("boom"))
		if _, err := GetLatestSnapshotInTx(context.Background(), tx, "bd-e"); err == nil {
			t.Fatal("err = nil, want query error")
		}
	})

	t.Run("bad json", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`FROM compaction_snapshots WHERE issue_id = \?`).
			WithArgs("bd-j").
			WillReturnRows(sqlmock.NewRows([]string{"compaction_level", "snapshot_json"}).AddRow(1, []byte("{bad")))
		if _, err := GetLatestSnapshotInTx(context.Background(), tx, "bd-j"); err == nil {
			t.Fatal("err = nil, want unmarshal error")
		}
	})
}

func TestRestoreFromSnapshotInTx(t *testing.T) {
	t.Parallel()

	// expectSnapshot queues the GetLatestSnapshotInTx read with the given level.
	expectSnapshot := func(mock sqlmock.Sqlmock, id string, level int) {
		payload, _ := json.Marshal(&types.IssueSnapshot{Description: "restored"})
		mock.ExpectQuery(`FROM compaction_snapshots WHERE issue_id = \?`).
			WithArgs(id).
			WillReturnRows(sqlmock.NewRows([]string{"compaction_level", "snapshot_json"}).AddRow(level, payload))
	}

	t.Run("no snapshot", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`FROM compaction_snapshots WHERE issue_id = \?`).
			WithArgs("bd-0").
			WillReturnRows(sqlmock.NewRows([]string{"compaction_level", "snapshot_json"}))
		snap, err := RestoreFromSnapshotInTx(context.Background(), tx, "bd-0")
		if err != nil || snap != nil {
			t.Errorf("snap=%v err=%v, want nil/nil", snap, err)
		}
	})

	t.Run("get error", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(`FROM compaction_snapshots WHERE issue_id = \?`).
			WithArgs("bd-g").WillReturnError(errors.New("boom"))
		if _, err := RestoreFromSnapshotInTx(context.Background(), tx, "bd-g"); err == nil {
			t.Fatal("err = nil, want get error")
		}
	})

	t.Run("full restore (level 1 → 0 clears bookkeeping)", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectSnapshot(mock, "bd-1", 1)
		mock.ExpectExec(`compaction_level = 0, compacted_at = NULL`).
			WillReturnResult(sqlmock.NewResult(0, 1))
		snap, err := RestoreFromSnapshotInTx(context.Background(), tx, "bd-1")
		if err != nil || snap == nil {
			t.Fatalf("snap=%v err=%v, want snapshot/nil", snap, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("step down (level 2 → 1)", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectSnapshot(mock, "bd-2", 2)
		mock.ExpectExec(`SET description = \?, design = \?, notes = \?, acceptance_criteria = \?,\s+compaction_level = \?`).
			WillReturnResult(sqlmock.NewResult(0, 1))
		snap, err := RestoreFromSnapshotInTx(context.Background(), tx, "bd-2")
		if err != nil || snap == nil {
			t.Fatalf("snap=%v err=%v, want snapshot/nil", snap, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("update error", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectSnapshot(mock, "bd-u", 1)
		mock.ExpectExec(`UPDATE issues`).WillReturnError(errors.New("update boom"))
		if _, err := RestoreFromSnapshotInTx(context.Background(), tx, "bd-u"); err == nil {
			t.Fatal("err = nil, want update error")
		}
	})
}

func TestGetCandidatesInTx(t *testing.T) {
	t.Parallel()

	candidateRows := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"id", "closed_at", "original_size", "dependent_count"}).
			AddRow("bd-1", time.Now(), 100, 2).
			AddRow("bd-2", time.Now(), 200, 0)
	}

	t.Run("tier1 happy + estimated size", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectConfigDays(mock, "30") // getCompactDaysInTx(tier1)
		mock.ExpectQuery(`FROM issues i`).WillReturnRows(candidateRows())
		got, err := GetTier1CandidatesInTx(context.Background(), tx)
		if err != nil {
			t.Fatalf("GetTier1CandidatesInTx: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d candidates, want 2", len(got))
		}
		if got[0].EstimatedSize != 100*3/10 {
			t.Errorf("estimated size = %d, want %d", got[0].EstimatedSize, 100*3/10)
		}
	})

	t.Run("tier1 query error", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectConfigDays(mock, "30")
		mock.ExpectQuery(`FROM issues i`).WillReturnError(errors.New("boom"))
		if _, err := GetTier1CandidatesInTx(context.Background(), tx); err == nil {
			t.Fatal("err = nil, want query error")
		}
	})

	t.Run("tier2 happy", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectConfigDays(mock, "90") // getCompactDaysInTx(tier2)
		mock.ExpectQuery(`FROM issues i`).WillReturnRows(candidateRows())
		got, err := GetTier2CandidatesInTx(context.Background(), tx)
		if err != nil {
			t.Fatalf("GetTier2CandidatesInTx: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d candidates, want 2", len(got))
		}
	})

	t.Run("tier2 query error", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectConfigDays(mock, "90")
		mock.ExpectQuery(`FROM issues i`).WillReturnError(errors.New("boom"))
		if _, err := GetTier2CandidatesInTx(context.Background(), tx); err == nil {
			t.Fatal("err = nil, want query error")
		}
	})

	t.Run("scan error", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		expectConfigDays(mock, "30")
		// Too few columns → Scan fails inside scanCompactionCandidates.
		mock.ExpectQuery(`FROM issues i`).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("bd-1"))
		if _, err := GetTier1CandidatesInTx(context.Background(), tx); err == nil {
			t.Fatal("err = nil, want scan error")
		}
	})
}
