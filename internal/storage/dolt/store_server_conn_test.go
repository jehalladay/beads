package dolt

import (
	"context"
	"fmt"
	"testing"
)

// serverConnectionsTotal reads the Dolt server's monotonic "Connections" status
// counter — the total number of connection attempts since the server started.
// Comparing it before and after an operation tells us how many new server-side
// connections that operation opened.
func serverConnectionsTotal(t *testing.T, port int) int64 {
	t.Helper()
	db := rawTestConn(t, port)
	defer db.Close()

	var name string
	var value int64
	if err := db.QueryRow("SHOW GLOBAL STATUS LIKE 'Connections'").Scan(&name, &value); err != nil {
		t.Fatalf("failed to read Connections status: %v", err)
	}
	return value
}

// storeOpenServerConns measures how many new server-side connections a single
// store open costs, using the server's monotonic global Connections counter and
// subtracting the two connections the measurement itself opens (one status read
// before, one after). The bare TCP fail-fast dial the store does before the
// MySQL handshake also lands on this counter, so the result includes it.
func storeOpenServerConns(t *testing.T, dbName string, createIfMissing bool) int64 {
	t.Helper()
	before := serverConnectionsTotal(t, testServerPort)

	cfg := &Config{
		Path:            t.TempDir(),
		ServerHost:      "127.0.0.1",
		ServerPort:      testServerPort,
		Database:        dbName,
		MaxOpenConns:    1,
		CreateIfMissing: createIfMissing,
	}
	store, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("store open failed (createIfMissing=%v, db=%q): %v", createIfMissing, dbName, err)
	}
	t.Cleanup(func() { _ = store.Close() })

	after := serverConnectionsTotal(t, testServerPort)
	return after - before - 2
}

// TestOpenServerConnection_ReadPathSkipsInitConnection proves the beads-kpm
// hot-loop optimization: on the read path (CreateIfMissing=false, every normal
// bd invocation), opening a store against an existing database must NOT open the
// extra no-database "init" connection that used to run SHOW DATABASES. The read
// path must therefore cost strictly fewer server connections than the create
// path, which still opens the init connection.
func TestOpenServerConnection_ReadPathSkipsInitConnection(t *testing.T) {
	if testServerPort == 0 {
		t.Skip("no test Dolt server running")
	}
	// The server's global Connections counter is shared, so hold ALL test slots
	// to run exclusively — otherwise a concurrent test opening connections
	// between our before/after reads would pollute the measured delta.
	acquireAllTestSlots()
	t.Cleanup(releaseAllTestSlots)

	dbName := fmt.Sprintf("test_conn_readpath_%d", testServerPort)

	// Create the database up front (via the create path) so the read-path open
	// under test succeeds and we isolate exactly its connection cost.
	createTestDatabase(t, testServerPort, dbName)
	t.Cleanup(func() { dropTestDatabase(t, testServerPort, dbName) })

	readConns := storeOpenServerConns(t, dbName, false)
	// Sanity floor: the main pool always opens at least one MySQL connection.
	if readConns < 1 {
		t.Fatalf("read-path store open used %d server connections; expected at least the main pool", readConns)
	}

	createConns := storeOpenServerConns(t, dbName, true)

	// The only difference between the two paths on an existing database is the
	// init connection the create path opens for its SHOW DATABASES existence
	// probe. Eliminating it on the read path is the whole optimization.
	if readConns >= createConns {
		t.Errorf("read path used %d server connections, create path used %d; "+
			"read path must open fewer (the init connection must be skipped)", readConns, createConns)
	}
}

// TestOpenServerConnection_ReadPathMissingDBFailsFast proves that eliminating
// the SHOW DATABASES probe did not weaken the shadow-database guard: on the
// read path a genuinely-missing database still fails, with the friendly
// not-found guidance, and does NOT create the database as a side effect.
func TestOpenServerConnection_ReadPathMissingDBFailsFast(t *testing.T) {
	skipIfNoServer(t)

	ctx := context.Background()
	dbName := fmt.Sprintf("test_conn_missing_%d", testServerPort)

	assertDatabaseNotExists(t, testServerPort, dbName)

	cfg := &Config{
		Path:         t.TempDir(),
		ServerHost:   "127.0.0.1",
		ServerPort:   testServerPort,
		Database:     dbName,
		MaxOpenConns: 1,
		// CreateIfMissing false — missing DB must error, not create.
	}
	_, err := New(ctx, cfg)
	if err == nil {
		t.Fatal("expected error opening a missing database on the read path, got nil")
	}
	if !containsAny(err.Error(), "not found", "does not exist", "unknown database") {
		t.Errorf("error should indicate database not found, got: %v", err)
	}
	// The failing open must not have created the database.
	assertDatabaseNotExists(t, testServerPort, dbName)
}
