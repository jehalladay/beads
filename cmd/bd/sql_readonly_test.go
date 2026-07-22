package main

import "testing"

// TestSqlStatementIsReadOnlySafe guards the bd sql --readonly bypass fix
// (beads-5afw): the classifier must treat WITH-prefixed CTE writes and
// SELECT/…DOLT_*(…) mutations as NOT read-only-safe, so the up-front
// CheckReadonly guard rejects them in a worker sandbox.
func TestSqlStatementIsReadOnlySafe(t *testing.T) {
	tests := []struct {
		name  string
		query string
		safe  bool
	}{
		// Provably read-only — allowed under --readonly.
		{"plain select", "SELECT id FROM issues", true},
		{"select lowercase", "select count(*) from issues", true},
		{"leading whitespace", "   SELECT 1", true},
		{"explain", "EXPLAIN SELECT * FROM issues", true},
		{"show", "SHOW TABLES", true},
		{"describe", "DESCRIBE issues", true},
		{"pragma", "PRAGMA table_info(issues)", true},

		// The beads-5afw bypass vectors — must be UNSAFE.
		{"CTE-wrapped delete", "WITH cte AS (SELECT id FROM issues) DELETE FROM issues WHERE id IN (SELECT id FROM cte)", false},
		{"CTE-wrapped read still unsafe (WITH can wrap DML)", "WITH cte AS (SELECT id FROM issues) SELECT * FROM cte", false},
		{"select dolt_reset", `SELECT DOLT_RESET("--hard", "HEAD")`, false},
		{"select dolt_commit", "SELECT DOLT_COMMIT('-Am', 'x')", false},
		{"select dolt_ lowercase", "select dolt_merge('main')", false},

		// Plainly writes — unsafe.
		{"delete", "DELETE FROM issues WHERE id = 'x'", false},
		{"update", "UPDATE issues SET title = 'y'", false},
		{"insert", "INSERT INTO issues (id) VALUES ('z')", false},
		{"drop", "DROP TABLE issues", false},
		{"empty", "", false},

		// The beads-wsvio bypass vector — a stacked <read>; <write> multi-statement
		// has a read prefix but a write tail, and the DSN's MultiStatements:true
		// runs the tail too. Must be UNSAFE.
		{"stacked select then delete", "SELECT 1; DELETE FROM issues", false},
		{"stacked select then update", "SELECT id FROM issues; UPDATE issues SET title = 'x'", false},
		{"stacked select then drop", "SELECT 1; DROP TABLE issues", false},
		{"stacked select then dolt_commit", "SELECT 1; SELECT DOLT_COMMIT('-Am','x')", false},
		{"three stacked statements", "SELECT 1; SELECT 2; DELETE FROM issues", false},
		{"stacked with leading whitespace on tail", "SELECT 1;   DELETE FROM issues", false},

		// A lone trailing ';' on a single statement stays safe (only one non-empty stmt).
		{"single select trailing semicolon", "SELECT id FROM issues;", true},
		{"single select trailing semicolon + whitespace", "SELECT 1;   ", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sqlStatementIsReadOnlySafe(tt.query); got != tt.safe {
				t.Errorf("sqlStatementIsReadOnlySafe(%q) = %v, want %v", tt.query, got, tt.safe)
			}
		})
	}
}
