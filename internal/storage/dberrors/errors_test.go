package dberrors

import (
	"errors"
	"fmt"
	"testing"

	mysql "github.com/go-sql-driver/mysql"
)

// IsTableNotExist is the classifier for MySQL/Dolt "table doesn't exist"
// (error 1146). It is load-bearing for optional-table fallback paths, so every
// recognized shape — the typed driver error, the "error 1146" substring, and
// the quoted/unquoted message regexes — must match, while adjacent errors
// (missing column, generic failures) must NOT be misclassified as table
// absence. (beads-rv1)
func TestIsTableNotExist_TypedDriverError(t *testing.T) {
	t.Parallel()

	// The canonical signal: the driver surfaces a typed *mysql.MySQLError.
	if !IsTableNotExist(&mysql.MySQLError{Number: 1146, Message: "Table 'beads.issues' doesn't exist"}) {
		t.Error("typed MySQLError #1146 should classify as table-not-exist")
	}
	// A different MySQL error number must not be classified as table absence,
	// even though it is a typed driver error.
	if IsTableNotExist(&mysql.MySQLError{Number: 1054, Message: "Unknown column 'x'"}) {
		t.Error("typed MySQLError #1054 (unknown column) must NOT be table-not-exist")
	}
	// errors.As must find the typed error even when wrapped.
	wrapped := fmt.Errorf("query failed: %w", &mysql.MySQLError{Number: 1146, Message: "Table 'x' doesn't exist"})
	if !IsTableNotExist(wrapped) {
		t.Error("wrapped typed MySQLError #1146 should be found via errors.As")
	}
}

func TestIsTableNotExist_StringForms(t *testing.T) {
	t.Parallel()

	matches := []string{
		// "error 1146" substring form (some layers stringify the driver error).
		"Error 1146: Table 'beads.wisp_events' doesn't exist",
		"error 1146 (42S02): table missing",
		// Quoted-table regex, both "doesn't exist" and "does not exist".
		"Table 'beads.events' doesn't exist",
		"table 'beads.events' does not exist",
		// Unquoted-table regex (backtick-optional), anchored at start.
		"table events doesn't exist",
		"Table `events` does not exist",
	}
	for _, msg := range matches {
		if !IsTableNotExist(errors.New(msg)) {
			t.Errorf("IsTableNotExist(%q) = false, want true", msg)
		}
	}
}

func TestIsTableNotExist_NonMatches(t *testing.T) {
	t.Parallel()

	nonMatches := []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"generic", errors.New("connection refused")},
		// Missing column is a different object — must not be classified as a
		// missing table (the doc comment calls this out explicitly).
		{"unknown column", errors.New("Error 1054: Unknown column 'foo' in 'field list'")},
		// A different error number that happens to contain "1146" as a
		// substring of a larger token must not false-positive.
		{"number-substring", errors.New("error 11460: unrelated failure")},
		// "does not exist" about something other than a table (e.g. a
		// database/schema) must not match the table-specific patterns.
		{"database missing", errors.New("database 'beads' does not exist")},
		// A mid-sentence "table X doesn't exist" is only matched by the
		// UNQUOTED pattern when anchored at the start; embedded unquoted forms
		// should not match (guards the `^` anchor intent).
		{"embedded unquoted", errors.New("operation aborted: some table events doesn't exist somewhere")},
	}
	for _, tc := range nonMatches {
		if IsTableNotExist(tc.err) {
			t.Errorf("IsTableNotExist(%s) = true, want false", tc.name)
		}
	}
}
