package schema

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

const versionQuery = "SELECT COALESCE(MAX(version), 0) FROM schema_migrations"

// clearSkewEnv makes a test hermetic against a crew/refinery shell that exports
// BD_IGNORE_SCHEMA_SKEW=1 (which would otherwise downgrade the drift errors to
// warnings). Same hermeticity class as the actor-env tests.
func clearSkewEnv(t *testing.T) {
	t.Helper()
	t.Setenv("BD_IGNORE_SCHEMA_SKEW", "")
}

// -- CheckForwardDrift (delegates to checkSchemaSkew on a *sql.DB) --

func TestCheckForwardDrift_EqualVersion_NoError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(regexpQuoteVersion()).
		WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(LatestVersion()))

	if err := CheckForwardDrift(context.Background(), db); err != nil {
		t.Fatalf("CheckForwardDrift: unexpected error %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestCheckForwardDrift_Ahead_ReturnsSchemaSkewError(t *testing.T) {
	clearSkewEnv(t)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	ahead := LatestVersion() + 2
	mock.ExpectQuery(regexpQuoteVersion()).
		WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(ahead))

	err = CheckForwardDrift(context.Background(), db)
	var skew *SchemaSkewError
	if !errors.As(err, &skew) {
		t.Fatalf("CheckForwardDrift: want *SchemaSkewError, got %v", err)
	}
	if skew.DBVersion != ahead {
		t.Errorf("DBVersion = %d, want %d", skew.DBVersion, ahead)
	}
}

func TestCheckForwardDrift_QueryError_Wrapped(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(regexpQuoteVersion()).
		WillReturnError(fmt.Errorf("boom"))

	err = CheckForwardDrift(context.Background(), db)
	if err == nil || !strings.Contains(err.Error(), "schema skew check") {
		t.Fatalf("want wrapped skew-check error, got %v", err)
	}
}

// -- CheckBehindDrift --

func TestCheckBehindDrift_Current_NoError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(regexpQuoteVersion()).
		WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(LatestVersion()))

	if err := CheckBehindDrift(context.Background(), db); err != nil {
		t.Fatalf("CheckBehindDrift: unexpected error %v", err)
	}
}

func TestCheckBehindDrift_Behind_ReturnsSchemaBehindError(t *testing.T) {
	clearSkewEnv(t)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	behind := LatestVersion() - 3
	mock.ExpectQuery(regexpQuoteVersion()).
		WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(behind))

	err = CheckBehindDrift(context.Background(), db)
	var be *SchemaBehindError
	if !errors.As(err, &be) {
		t.Fatalf("CheckBehindDrift: want *SchemaBehindError, got %v", err)
	}
	if be.DBVersion != behind {
		t.Errorf("DBVersion = %d, want %d", be.DBVersion, behind)
	}
	if be.BinaryVersion != LatestVersion() {
		t.Errorf("BinaryVersion = %d, want %d", be.BinaryVersion, LatestVersion())
	}
}

// A fresh DB (version 0) is reported as behind — it has no readable schema.
func TestCheckBehindDrift_FreshDB_ReturnsBehindError(t *testing.T) {
	clearSkewEnv(t)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(regexpQuoteVersion()).
		WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(0))

	err = CheckBehindDrift(context.Background(), db)
	if !IsSchemaBehindError(err) {
		t.Fatalf("fresh DB: want *SchemaBehindError, got %v", err)
	}
}

func TestCheckBehindDrift_EscapeHatch_ReturnsNilAndWarns(t *testing.T) {
	t.Setenv("BD_IGNORE_SCHEMA_SKEW", "1")

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(regexpQuoteVersion()).
		WillReturnRows(sqlmock.NewRows([]string{"version"}).AddRow(LatestVersion() - 1))

	if err := CheckBehindDrift(context.Background(), db); err != nil {
		t.Fatalf("with escape hatch, want nil, got %v", err)
	}
}

func TestCheckBehindDrift_QueryError_Wrapped(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(regexpQuoteVersion()).
		WillReturnError(fmt.Errorf("boom"))

	err = CheckBehindDrift(context.Background(), db)
	if err == nil || !strings.Contains(err.Error(), "schema behind-drift check") {
		t.Fatalf("want wrapped behind-drift error, got %v", err)
	}
}

// -- SchemaBehindError.Error / IsSchemaBehindError --

func TestSchemaBehindError_Error_MentionsVersions(t *testing.T) {
	e := &SchemaBehindError{DBVersion: 40, BinaryVersion: 53}
	msg := e.Error()
	if !strings.Contains(msg, "v40") || !strings.Contains(msg, "v53") {
		t.Errorf("Error() = %q, want both v40 and v53", msg)
	}
}

func TestIsSchemaBehindError(t *testing.T) {
	t.Run("direct", func(t *testing.T) {
		if !IsSchemaBehindError(&SchemaBehindError{DBVersion: 1, BinaryVersion: 2}) {
			t.Error("want true for direct *SchemaBehindError")
		}
	})
	t.Run("wrapped", func(t *testing.T) {
		wrapped := fmt.Errorf("open failed: %w", &SchemaBehindError{DBVersion: 1, BinaryVersion: 2})
		if !IsSchemaBehindError(wrapped) {
			t.Error("want true for wrapped *SchemaBehindError")
		}
	})
	t.Run("other", func(t *testing.T) {
		if IsSchemaBehindError(errors.New("nope")) {
			t.Error("want false for unrelated error")
		}
	})
	t.Run("nil", func(t *testing.T) {
		if IsSchemaBehindError(nil) {
			t.Error("want false for nil error")
		}
	})
}

// -- RemoteMigrateGateError.EscapeHint --

func TestRemoteMigrateGateError_EscapeHint(t *testing.T) {
	e := &RemoteMigrateGateError{CurrentVersion: 50, LatestVersion: 53, Pending: 3}
	want := AllowRemoteMigrateEnv + "=1 bd migrate"
	if got := e.EscapeHint(); got != want {
		t.Errorf("EscapeHint() = %q, want %q", got, want)
	}
}

// -- parseTypesValue (pure) --

func TestParseTypesValue(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"whitespace-only", "   ", nil},
		{"json array", `["bug","task","epic"]`, []string{"bug", "task", "epic"}},
		{"json empty array", `[]`, []string{}},
		{"csv", "bug, task ,epic", []string{"bug", "task", "epic"}},
		{"csv with blanks", "bug,,task, ,epic", []string{"bug", "task", "epic"}},
		{"single value", "bug", []string{"bug"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTypesValue(tt.in)
			if !equalStrSlice(got, tt.want) {
				t.Errorf("parseTypesValue(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

// -- sqlStringLiteral (pure) --

func TestSQLStringLiteral(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"issues", "'issues'"},
		{"o'brien", "'o''brien'"},
		{`back\slash`, `'back\\slash'`},
		{`both'\`, `'both''\\'`},
		{"", "''"},
	}
	for _, tt := range tests {
		if got := sqlStringLiteral(tt.in); got != tt.want {
			t.Errorf("sqlStringLiteral(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// -- isDiffMetadataColumn (pure) --

func TestIsDiffMetadataColumn(t *testing.T) {
	meta := []string{"from_commit", "to_commit", "from_commit_date", "to_commit_date",
		"FROM_COMMIT", "To_Commit_Date"}
	for _, c := range meta {
		if !isDiffMetadataColumn(c) {
			t.Errorf("isDiffMetadataColumn(%q) = false, want true", c)
		}
	}
	data := []string{"id", "title", "status", "commit", "from_state", ""}
	for _, c := range data {
		if isDiffMetadataColumn(c) {
			t.Errorf("isDiffMetadataColumn(%q) = true, want false", c)
		}
	}
}

// -- writeSignatureValue (pure) --

func TestWriteSignatureValue(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"nil", nil, "<nil>"},
		{"bytes", []byte("abc"), "abc"},
		{"string", "hello", "hello"},
		{"int", 42, "42"},
		{"bool", true, "true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			writeSignatureValue(&b, tt.in)
			if got := b.String(); got != tt.want {
				t.Errorf("writeSignatureValue(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// -- helpers --

func regexpQuoteVersion() string {
	// The drift checks issue exactly versionQuery; QuoteMeta escapes the parens.
	return regexp.QuoteMeta(versionQuery)
}

func equalStrSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
