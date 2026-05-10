package migrations

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/steveyegge/beads/internal/storage/doltutil"
	"github.com/steveyegge/beads/internal/storage/schema"
	"github.com/steveyegge/beads/internal/testutil"
)

// openTestDoltBranch returns a *sql.DB connected to an isolated branch on the
// shared test database. The branch inherits the base issues table from main.
// Each test gets COW isolation — schema/data changes are invisible to other tests.
func openTestDoltBranch(t *testing.T) *sql.DB {
	t.Helper()

	testutil.RequireDoltBinary(t)
	if testServerPort == 0 {
		t.Skip("test Dolt server not running, skipping migration test")
	}
	t.Parallel()

	dsn := doltutil.ServerDSN{Host: "127.0.0.1", Port: testServerPort, User: "root", Database: testSharedDB, Timeout: 10 * time.Second}.String()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("failed to open connection: %v", err)
	}
	db.SetMaxOpenConns(1) // Required for session-level DOLT_CHECKOUT

	// Create an isolated branch for this test
	_, branchCleanup := testutil.StartTestBranch(t, db, testSharedDB)

	t.Cleanup(func() {
		branchCleanup()
		db.Close()
	})

	return db
}

func TestColumnExists(t *testing.T) {
	db := openTestDoltBranch(t)

	exists, err := columnExists(db, "issues", "id")
	if err != nil {
		t.Fatalf("failed to check column: %v", err)
	}
	if !exists {
		t.Fatal("id column should exist")
	}

	exists, err = columnExists(db, "issues", "nonexistent")
	if err != nil {
		t.Fatalf("failed to check column: %v", err)
	}
	if exists {
		t.Fatal("nonexistent column should not exist")
	}
}

func TestTableExists(t *testing.T) {
	db := openTestDoltBranch(t)

	exists, err := TableExists(db, "issues")
	if err != nil {
		t.Fatalf("failed to check table: %v", err)
	}
	if !exists {
		t.Fatal("issues table should exist")
	}

	exists, err = TableExists(db, "nonexistent")
	if err != nil {
		t.Fatalf("failed to check table: %v", err)
	}
	if exists {
		t.Fatal("nonexistent table should not exist")
	}
}

func TestColumnExistsNoTable(t *testing.T) {
	db := openTestDoltBranch(t)

	// columnExists on a nonexistent table should return (false, nil),
	// not propagate the Error 1146 from SHOW COLUMNS.
	exists, err := columnExists(db, "nonexistent_table", "id")
	if err != nil {
		t.Fatalf("columnExists on nonexistent table should not error, got: %v", err)
	}
	if exists {
		t.Fatal("columnExists on nonexistent table should return false")
	}
}

func TestColumnExistsWithPhantom(t *testing.T) {
	db := openTestDoltBranch(t)

	// Create a phantom-like database entry (simulates naming convention phantom).
	// This is a server-level operation; cleaned up after the test.
	//nolint:gosec // G202: test-only database name, not user input
	_, err := db.Exec("CREATE DATABASE IF NOT EXISTS beads_phantom_mig")
	if err != nil {
		t.Fatalf("failed to create phantom database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec("DROP DATABASE IF EXISTS beads_phantom_mig")
	})

	// Positive: still finds columns in primary database
	exists, err := columnExists(db, "issues", "id")
	if err != nil {
		t.Fatalf("columnExists failed with phantom present: %v", err)
	}
	if !exists {
		t.Fatal("should find 'id' column even with phantom database present")
	}

	// Positive: still finds tables
	exists, err = TableExists(db, "issues")
	if err != nil {
		t.Fatalf("tableExists failed with phantom present: %v", err)
	}
	if !exists {
		t.Fatal("should find 'issues' table even with phantom database present")
	}

	// Negative: missing column still returns false
	exists, err = columnExists(db, "issues", "nonexistent")
	if err != nil {
		t.Fatalf("should not error for missing column: %v", err)
	}
	if exists {
		t.Fatal("should return false for nonexistent column")
	}

	// Negative: missing table still returns (false, nil)
	exists, err = TableExists(db, "nonexistent_table")
	if err != nil {
		t.Fatalf("should not error for missing table: %v", err)
	}
	if exists {
		t.Fatal("should return false for nonexistent table")
	}

	// Negative: nonexistent table + column returns (false, nil)
	exists, err = columnExists(db, "nonexistent_table", "id")
	if err != nil {
		t.Fatalf("should not error with phantom database present: %v", err)
	}
	if exists {
		t.Fatal("should return false for column in nonexistent table")
	}
}

func TestMigrateInfraToWisps_SchemaEvolution(t *testing.T) {
	db := openTestDoltBranch(t)

	// 1. Create older issues table WITH a column that wisps won't have (deleted_at)
	// and WITHOUT a column that wisps will have (metadata).
	// Branch isolation means this DROP/CREATE only affects this test's branch.
	db.Exec("DROP TABLE IF EXISTS issues")
	_, err := db.Exec(`
		CREATE TABLE issues (
			id VARCHAR(255) PRIMARY KEY,
			title VARCHAR(500) NOT NULL,
			issue_type VARCHAR(32) NOT NULL DEFAULT 'task',
			deleted_at DATETIME
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create issues table: %v", err)
	}

	// Insert an infra issue
	_, err = db.Exec("INSERT INTO issues (id, title, issue_type) VALUES ('test-1', 'Agent Wisp', 'agent')")
	if err != nil {
		t.Fatalf("Failed to insert issue: %v", err)
	}

	// 2. Create older dependencies table missing a column (thread_id)
	_, err = db.Exec(`
		CREATE TABLE dependencies (
			issue_id VARCHAR(255) NOT NULL,
			depends_on_id VARCHAR(255) NOT NULL,
			type VARCHAR(32) NOT NULL DEFAULT 'blocks',
			PRIMARY KEY (issue_id, depends_on_id)
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create dependencies table: %v", err)
	}

	// Insert a dependency
	_, err = db.Exec("INSERT INTO dependencies (issue_id, depends_on_id, type) VALUES ('test-1', 'test-2', 'blocks')")
	if err != nil {
		t.Fatalf("Failed to insert dependency: %v", err)
	}

	// 3. Create missing minimal tables for other relations so copyCommonColumns works
	_, err = db.Exec(`CREATE TABLE labels (issue_id VARCHAR(255), label VARCHAR(255))`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE events (id BIGINT AUTO_INCREMENT PRIMARY KEY, issue_id VARCHAR(255), event_type VARCHAR(32))`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE comments (id BIGINT AUTO_INCREMENT PRIMARY KEY, issue_id VARCHAR(255), text TEXT)`)
	if err != nil {
		t.Fatal(err)
	}

	// 4. Create the wisps and wisp auxiliary tables (schema migrations 0020 and 0021).
	if _, err := db.Exec(schema.ReadMigrationSQL(20)); err != nil {
		t.Fatalf("Failed to create wisps table: %v", err)
	}
	if _, err := db.Exec(schema.ReadMigrationSQL(21)); err != nil {
		t.Fatalf("Failed to create wisp auxiliary tables: %v", err)
	}

	// 5. Run migration 007 - it should gracefully map columns instead of crashing
	if err := MigrateInfraToWisps(db); err != nil {
		t.Fatalf("Migration 007 failed: %v", err)
	}

	// 6. Verify row was moved
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM wisps WHERE id = 'test-1'").Scan(&count); err != nil {
		t.Fatalf("Failed to query wisps: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 wisp, got %d", count)
	}

	if err := db.QueryRow("SELECT COUNT(*) FROM issues WHERE id = 'test-1'").Scan(&count); err != nil {
		t.Fatalf("Failed to query issues: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 issues, got %d", count)
	}
}
