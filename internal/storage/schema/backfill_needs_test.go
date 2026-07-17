package schema

import (
	"context"
	"database/sql"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

// TestNeedsCustomTypesBackfill covers every branch of the custom-types backfill
// predicate: an already-populated table (skip), no legacy config row (skip), a
// legacy config row with types (backfill needed), and an empty legacy value.
func TestNeedsCustomTypesBackfill(t *testing.T) {
	t.Parallel()

	t.Run("already populated table -> no backfill", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		defer db.Close()
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM custom_types").
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(3))
		got, err := needsCustomTypesBackfill(context.Background(), db)
		if err != nil || got {
			t.Fatalf("populated table: got (%v,%v), want (false,nil)", got, err)
		}
	})

	t.Run("no legacy config row -> no backfill", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		defer db.Close()
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM custom_types").
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))
		mock.ExpectQuery("SELECT .value. FROM config WHERE").
			WillReturnError(sql.ErrNoRows)
		got, err := needsCustomTypesBackfill(context.Background(), db)
		if err != nil || got {
			t.Fatalf("no config: got (%v,%v), want (false,nil)", got, err)
		}
	})

	t.Run("legacy config with types -> backfill needed", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		defer db.Close()
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM custom_types").
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))
		mock.ExpectQuery("SELECT .value. FROM config WHERE").
			WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(`["design","spike"]`))
		got, err := needsCustomTypesBackfill(context.Background(), db)
		if err != nil || !got {
			t.Fatalf("config with types: got (%v,%v), want (true,nil)", got, err)
		}
	})

	t.Run("legacy config empty -> no backfill", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		defer db.Close()
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM custom_types").
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))
		mock.ExpectQuery("SELECT .value. FROM config WHERE").
			WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(""))
		got, err := needsCustomTypesBackfill(context.Background(), db)
		if err != nil || got {
			t.Fatalf("empty config: got (%v,%v), want (false,nil)", got, err)
		}
	})
}

// TestNeedsCustomStatusesBackfill covers the parallel status-backfill predicate:
// populated table (skip), no config row (skip), blank value (skip), and a valid
// custom-status config (backfill needed).
func TestNeedsCustomStatusesBackfill(t *testing.T) {
	t.Parallel()

	t.Run("already populated table -> no backfill", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		defer db.Close()
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM custom_statuses").
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(2))
		got, err := needsCustomStatusesBackfill(context.Background(), db)
		if err != nil || got {
			t.Fatalf("populated: got (%v,%v), want (false,nil)", got, err)
		}
	})

	t.Run("no config row -> no backfill", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		defer db.Close()
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM custom_statuses").
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))
		mock.ExpectQuery("SELECT .value. FROM config WHERE").
			WillReturnError(sql.ErrNoRows)
		got, err := needsCustomStatusesBackfill(context.Background(), db)
		if err != nil || got {
			t.Fatalf("no config: got (%v,%v), want (false,nil)", got, err)
		}
	})

	t.Run("blank config value -> no backfill", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		defer db.Close()
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM custom_statuses").
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))
		mock.ExpectQuery("SELECT .value. FROM config WHERE").
			WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("   "))
		got, err := needsCustomStatusesBackfill(context.Background(), db)
		if err != nil || got {
			t.Fatalf("blank config: got (%v,%v), want (false,nil)", got, err)
		}
	})

	t.Run("unparseable config value -> no backfill (parse error swallowed)", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		defer db.Close()
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM custom_statuses").
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))
		// A value that is non-blank but not valid custom-status config: the
		// parse error must be swallowed into "no backfill", not surfaced.
		mock.ExpectQuery("SELECT .value. FROM config WHERE").
			WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("{not valid status config"))
		got, err := needsCustomStatusesBackfill(context.Background(), db)
		if err != nil || got {
			t.Fatalf("unparseable config: got (%v,%v), want (false,nil)", got, err)
		}
	})

	t.Run("valid custom-status config -> backfill needed", func(t *testing.T) {
		t.Parallel()
		db, mock, _ := sqlmock.New()
		defer db.Close()
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM custom_statuses").
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))
		mock.ExpectQuery("SELECT .value. FROM config WHERE").
			WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow("review:wip"))
		got, err := needsCustomStatusesBackfill(context.Background(), db)
		if err != nil {
			t.Fatalf("valid config: unexpected error %v", err)
		}
		if !got {
			t.Fatal("valid custom-status config should require backfill")
		}
	})
}
