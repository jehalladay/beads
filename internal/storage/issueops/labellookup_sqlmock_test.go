package issueops

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestGetIssuesByLabelInTx(t *testing.T) {
	t.Parallel()

	issuesQ := regexp.QuoteMeta("SELECT i.id FROM issues i")
	wispQ := regexp.QuoteMeta("SELECT issue_id FROM wisp_labels WHERE label = ?")

	t.Run("returns issue IDs from both issues and wisp_labels", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(issuesQ).WithArgs("bug").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("bd-1").AddRow("bd-2"))
		mock.ExpectQuery(wispQ).WithArgs("bug").
			WillReturnRows(sqlmock.NewRows([]string{"issue_id"}).AddRow("bd-w"))
		got, err := GetIssuesByLabelInTx(context.Background(), tx, "bug")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(got) != 3 || got[0] != "bd-1" || got[2] != "bd-w" {
			t.Fatalf("ids = %v, want [bd-1 bd-2 bd-w]", got)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("issues query error is wrapped", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(issuesQ).WithArgs("bug").WillReturnError(errors.New("boom"))
		if _, err := GetIssuesByLabelInTx(context.Background(), tx, "bug"); err == nil {
			t.Fatal("expected wrapped issues-query error")
		}
	})

	t.Run("wisp_labels query error is wrapped", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(issuesQ).WithArgs("bug").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("bd-1"))
		mock.ExpectQuery(wispQ).WithArgs("bug").WillReturnError(errors.New("boom"))
		if _, err := GetIssuesByLabelInTx(context.Background(), tx, "bug"); err == nil {
			t.Fatal("expected wrapped wisp-query error")
		}
	})
}

func TestGetLabelsForIssuesInTx(t *testing.T) {
	t.Parallel()

	t.Run("empty ids returns empty map without querying", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		got, err := GetLabelsForIssuesInTx(context.Background(), tx, nil)
		if err != nil || len(got) != 0 {
			t.Fatalf("got (%v,%v), want (empty,nil)", got, err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unexpected queries: %v", err)
		}
	})

	t.Run("wisp-set option short-circuits the DB partition and routes per bucket", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		// bd-w is in the provided wisp set -> wisp_labels; bd-1 -> labels.
		// The two source SELECTs run (order: wisp first, then perm).
		mock.ExpectQuery(regexp.QuoteMeta("SELECT issue_id, label FROM wisp_labels WHERE issue_id IN (?)")).
			WithArgs("bd-w").
			WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label"}).AddRow("bd-w", "ephem"))
		mock.ExpectQuery(regexp.QuoteMeta("SELECT issue_id, label FROM labels WHERE issue_id IN (?)")).
			WithArgs("bd-1").
			WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label"}).AddRow("bd-1", "bug"))

		wispSet := map[string]struct{}{"bd-w": {}}
		got, err := GetLabelsForIssuesInTx(context.Background(), tx, []string{"bd-1", "bd-w"}, wispSet)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(got["bd-w"]) != 1 || got["bd-w"][0] != "ephem" {
			t.Fatalf("bd-w labels = %v, want [ephem]", got["bd-w"])
		}
		if len(got["bd-1"]) != 1 || got["bd-1"][0] != "bug" {
			t.Fatalf("bd-1 labels = %v, want [bug]", got["bd-1"])
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("all-perm wisp set only queries the labels table", func(t *testing.T) {
		t.Parallel()
		_, mock, tx := beginMockTx(t)
		mock.ExpectQuery(regexp.QuoteMeta("SELECT issue_id, label FROM labels WHERE issue_id IN (?)")).
			WithArgs("bd-1").
			WillReturnRows(sqlmock.NewRows([]string{"issue_id", "label"}).AddRow("bd-1", "bug"))
		// Empty wisp set: partitionByWispSet routes everything to perm.
		got, err := GetLabelsForIssuesInTx(context.Background(), tx, []string{"bd-1"}, map[string]struct{}{})
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(got["bd-1"]) != 1 || got["bd-1"][0] != "bug" {
			t.Fatalf("bd-1 labels = %v, want [bug]", got["bd-1"])
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})
}
