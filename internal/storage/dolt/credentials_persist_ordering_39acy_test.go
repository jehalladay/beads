package dolt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// Regression suite for beads-39acy: DoltStore.initCredentialKey must persist the
// new random key to disk BEFORE committing any ciphertext re-encrypted under it,
// and migrateCredentialKeys must re-encrypt every peer in ONE transaction so a
// mid-loop failure can't split rows across two keys. Both holes silently and
// permanently lose federation credentials on an ill-timed crash/error.
//
// These use sqlmock so the exact Begin/Exec/Commit/Rollback sequence and the
// key-write-vs-DB-commit ordering are asserted deterministically, without a live
// server (the family's faultinject_test.go pattern).

// legacyRow returns a federation_peers row whose password is encrypted under the
// store's derivable legacy key, i.e. a row migrateCredentialKeys will re-encrypt.
func legacyRow(t *testing.T, s *DoltStore, name, plaintext string) (string, []byte) {
	t.Helper()
	enc, err := encryptWithKey(plaintext, s.legacyEncryptionKey())
	if err != nil {
		t.Fatalf("seed legacy ciphertext: %v", err)
	}
	return name, enc
}

// TestInitCredentialKey_PersistsKeyBeforeReencrypt_39acy proves hole (A): the key
// file is written to disk BEFORE migrateCredentialKeys commits the re-encrypted
// rows. We enforce the ordering by failing the UPDATE inside migrate and then
// asserting the key file is nonetheless present on disk — under the old order
// (migrate first, write key last) an early migrate failure returned before the
// key was ever written, so a key-covering-committed-ciphertext invariant was
// impossible. Here migrate is wrapped in a tx that rolls back on the failure, so
// nothing is committed AND the key is durable — the crash-safe state.
func TestInitCredentialKey_PersistsKeyBeforeReencrypt_39acy(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	tmp := t.TempDir()
	store := &DoltStore{db: db, dbPath: tmp, beadsDir: tmp}

	name, enc := legacyRow(t, store, "peerone", "s3cret")
	// migrateCredentialKeys first SELECTs the peers, then opens ONE tx and issues
	// the UPDATE. Fail the UPDATE so the whole re-encrypt rolls back.
	mock.ExpectQuery("SELECT name, password_encrypted FROM federation_peers").
		WillReturnRows(sqlmock.NewRows([]string{"name", "password_encrypted"}).AddRow(name, enc))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE federation_peers SET password_encrypted").
		WillReturnError(errors.New("Duplicate entry")) // non-retryable → fails fast, rolls back
	mock.ExpectRollback()

	err = store.initCredentialKey(context.Background())
	if err == nil {
		t.Fatal("expected initCredentialKey to surface the migrate failure")
	}

	// The key MUST already be on disk despite the migrate failure — the ordering
	// guarantee. (credentialKey stays nil so a later ensureCredentialKey re-enters
	// via the load path and retries the idempotent migration.)
	keyPath := filepath.Join(tmp, credentialKeyFile)
	info, statErr := os.Stat(keyPath)
	if statErr != nil {
		t.Fatalf("key file must be persisted before re-encrypt commits (hole A); stat: %v", statErr)
	}
	if info.Size() != 32 {
		t.Fatalf("key file size = %d, want 32", info.Size())
	}
	if store.credentialKey != nil {
		t.Error("credentialKey should stay nil after a failed migration so the next init retries")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unexpected DB op sequence: %v", err)
	}
}

// TestMigrateCredentialKeys_SingleTransaction_39acy proves hole (B): a multi-row
// re-encrypt runs inside exactly ONE transaction (one Begin, one Commit), not a
// per-row auto-commit. sqlmock's ordered expectations flag a second Begin.
func TestMigrateCredentialKeys_SingleTransaction_39acy(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	store := &DoltStore{db: db}
	n1, e1 := legacyRow(t, store, "peerone", "pw-one")
	n2, e2 := legacyRow(t, store, "peertwo", "pw-two")

	newKey := make([]byte, 32)
	for i := range newKey {
		newKey[i] = byte(i + 7)
	}

	mock.ExpectQuery("SELECT name, password_encrypted FROM federation_peers").
		WillReturnRows(sqlmock.NewRows([]string{"name", "password_encrypted"}).
			AddRow(n1, e1).AddRow(n2, e2))
	mock.ExpectBegin() // exactly one tx for BOTH updates
	mock.ExpectExec("UPDATE federation_peers SET password_encrypted").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE federation_peers SET password_encrypted").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := store.migrateCredentialKeys(context.Background(), newKey); err != nil {
		t.Fatalf("migrateCredentialKeys: %v", err)
	}
	// ExpectationsWereMet fails if the code opened a second Begin (per-row commit).
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("re-encrypt must be a single transaction (hole B): %v", err)
	}
}

// TestMigrateCredentialKeys_MidLoopFailureRollsBackAll_39acy proves the atomicity
// payoff of hole (B): a failure on the SECOND row's UPDATE rolls back the FIRST
// row's already-executed UPDATE too, so no peer is left committed under the new
// key while others remain under the old key.
func TestMigrateCredentialKeys_MidLoopFailureRollsBackAll_39acy(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	store := &DoltStore{db: db}
	n1, e1 := legacyRow(t, store, "peerone", "pw-one")
	n2, e2 := legacyRow(t, store, "peertwo", "pw-two")

	newKey := make([]byte, 32)
	for i := range newKey {
		newKey[i] = byte(i + 11)
	}

	mock.ExpectQuery("SELECT name, password_encrypted FROM federation_peers").
		WillReturnRows(sqlmock.NewRows([]string{"name", "password_encrypted"}).
			AddRow(n1, e1).AddRow(n2, e2))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE federation_peers SET password_encrypted").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE federation_peers SET password_encrypted").
		WillReturnError(errors.New("Duplicate entry")) // non-retryable
	mock.ExpectRollback() // the FIRST update must be rolled back with the second

	if err := store.migrateCredentialKeys(context.Background(), newKey); err == nil {
		t.Fatal("expected mid-loop failure to surface")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mid-loop failure must roll back the whole batch (hole B), got: %v", err)
	}
}

// TestInitCredentialKey_LoadPathReMigrates_39acy proves the self-heal: when the
// key already exists on disk (a prior run persisted it), initCredentialKey still
// runs the idempotent migration so a crash-interrupted rotation completes. A row
// still under the legacy key is picked up and re-encrypted under the loaded key.
func TestInitCredentialKey_LoadPathReMigrates_39acy(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	tmp := t.TempDir()
	// Pre-persist a 32-byte key on disk (the "prior run wrote the key" state).
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 3)
	}
	if err := os.WriteFile(filepath.Join(tmp, credentialKeyFile), key, 0600); err != nil {
		t.Fatalf("seed key file: %v", err)
	}

	store := &DoltStore{db: db, dbPath: tmp, beadsDir: tmp}
	name, enc := legacyRow(t, store, "peerone", "stranded-pw")

	// Load path must re-run migrate: SELECT then a single-tx UPDATE of the row
	// that is still under the legacy key.
	mock.ExpectQuery("SELECT name, password_encrypted FROM federation_peers").
		WillReturnRows(sqlmock.NewRows([]string{"name", "password_encrypted"}).AddRow(name, enc))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE federation_peers SET password_encrypted").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := store.initCredentialKey(context.Background()); err != nil {
		t.Fatalf("initCredentialKey (load path) should self-heal, got: %v", err)
	}
	if string(store.credentialKey) != string(key) {
		t.Error("load path must keep the on-disk key")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("load path must re-run the idempotent migration (self-heal): %v", err)
	}
}
