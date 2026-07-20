package dolt

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/storage/versioncontrolops"
	"github.com/steveyegge/beads/internal/types"
)

// doltTransaction implements storage.Transaction for Dolt
type doltTransaction struct {
	regularTx *sql.Tx
	ignoredTx *sql.Tx
	store     *DoltStore
	dirty     versioncontrolops.DirtyTableTracker
}

func (t *doltTransaction) txFor(table string) *sql.Tx {
	if table == "wisps" || strings.HasPrefix(table, "wisp_") ||
		table == "local_metadata" || table == "repo_mtimes" {
		return t.ignoredTx
	}
	return t.regularTx
}

// isActiveWisp checks if an ID exists in the wisps table within the transaction.
// Unlike the store-level isActiveWisp, this queries within the transaction so it
// sees uncommitted wisps. Handles both -wisp- pattern and explicit-ID ephemerals (GH#2053).
func (t *doltTransaction) isActiveWisp(ctx context.Context, id string) bool {
	var exists int
	err := t.ignoredTx.QueryRowContext(ctx, "SELECT 1 FROM wisps WHERE id = ? LIMIT 1", id).Scan(&exists)
	return err == nil
}

// CreateIssueImport is the import-friendly issue creation hook.
// Dolt does not enforce prefix validation at the storage layer, so this delegates to CreateIssue.
func (t *doltTransaction) CreateIssueImport(ctx context.Context, issue *types.Issue, actor string, skipPrefixValidation bool) error {
	return t.CreateIssue(ctx, issue, actor)
}

// RunInTransaction executes a function within a database transaction.
// The commitMsg is used for the DOLT_COMMIT that occurs inside the transaction,
// making the write atomically visible in Dolt's version history.
// Wisp routing is handled within individual transaction methods based on ID/Ephemeral flag.
func (s *DoltStore) RunInTransaction(ctx context.Context, commitMsg string, fn func(tx storage.Transaction) error) error {
	return s.withRetry(ctx, func() error {
		return s.runDoltTransaction(ctx, commitMsg, fn)
	})
}

func (s *DoltStore) runDoltTransaction(ctx context.Context, commitMsg string, fn func(tx storage.Transaction) error) error {
	// Pin a single connection for the entire operation: SQL transaction,
	// config protection, and DOLT_COMMIT must all run on the same Dolt
	// session. Each pool connection has an independent working set in Dolt
	// SQL server mode, so mixing connections causes DOLT_COMMIT to see
	// stale or unrelated changes. (GH#2455)

	// Snapshot pool stats before acquisition to detect pool-wait events (GH#3140).
	statsBefore := s.db.Stats()
	acquireStart := time.Now()

	conn, err := s.db.Conn(ctx)
	acquireMs := float64(time.Since(acquireStart).Microseconds()) / 1000.0
	doltMetrics.connAcquireMs.Record(ctx, acquireMs)

	// Detect pool-wait: if WaitCount increased, the pool was exhausted and
	// this caller had to wait for a connection to become available.
	if err == nil {
		statsAfter := s.db.Stats()
		if statsAfter.WaitCount > statsBefore.WaitCount {
			doltMetrics.poolWaitCount.Add(ctx, statsAfter.WaitCount-statsBefore.WaitCount)
			waitMs := float64(statsAfter.WaitDuration-statsBefore.WaitDuration) / float64(time.Millisecond)
			doltMetrics.poolWaitMs.Record(ctx, waitMs)
		}
	}

	if err != nil {
		return fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Close()

	var currentBranch string
	if err := conn.QueryRowContext(ctx, "SELECT active_branch()").Scan(&currentBranch); err != nil {
		return fmt.Errorf("failed to read active branch: %w", err)
	}

	regularTx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin regular tx: %w", err)
	}

	ignoredDB, ignoredConn, ignoredTx, err := s.beginIgnoredTxOnBranch(ctx, currentBranch)
	if err != nil {
		_ = regularTx.Rollback()
		return err
	}
	defer ignoredDB.Close()
	defer ignoredConn.Close()

	tx := &doltTransaction{regularTx: regularTx, ignoredTx: ignoredTx, store: s}

	defer func() {
		if r := recover(); r != nil {
			_ = regularTx.Rollback()
			_ = ignoredTx.Rollback()
			panic(r)
		}
	}()

	if err := fn(tx); err != nil {
		_ = regularTx.Rollback()
		_ = ignoredTx.Rollback()
		return err
	}

	if err := regularTx.Commit(); err != nil {
		_ = ignoredTx.Rollback()
		return fmt.Errorf("sql commit (regular): %w", err)
	}

	if err := versioncontrolops.StageAndCommit(ctx, conn, tx.dirty.DirtyTables(), commitMsg, s.commitAuthorString()); err != nil {
		_ = ignoredTx.Rollback()
		return err
	}

	if err := ignoredTx.Commit(); err != nil {
		return fmt.Errorf("sql commit (ignored, regular already committed): %w", err)
	}
	return nil
}

func (s *DoltStore) beginIgnoredTxOnBranch(ctx context.Context, branch string) (*sql.DB, *sql.Conn, *sql.Tx, error) {
	// Use an independent single-connection pool for ignored tables. Reusing the
	// main pool can deadlock when MaxOpenConns=1, and each Dolt SQL session has
	// its own active branch. This intentionally pays one extra connection setup
	// for mixed regular/ignored writes so the ignored transaction can be checked
	// out to the regular transaction's branch before writes.
	db, err := sql.Open("mysql", s.connStr)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to open ignored tx connection: %w", err)
	}
	db.SetMaxOpenConns(1)

	conn, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, nil, nil, fmt.Errorf("failed to acquire ignored tx connection: %w", err)
	}

	if _, err := conn.ExecContext(ctx, "CALL DOLT_CHECKOUT(?)", branch); err != nil {
		_ = conn.Close()
		_ = db.Close()
		return nil, nil, nil, fmt.Errorf("failed to checkout ignored tx branch %s: %w", branch, err)
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		_ = conn.Close()
		_ = db.Close()
		return nil, nil, nil, fmt.Errorf("failed to begin ignored tx: %w", err)
	}

	return db, conn, tx, nil
}

// isDoltNothingToCommit returns true if the error indicates there were no
// staged changes for Dolt to commit — a benign condition.
func isDoltNothingToCommit(err error) bool {
	return issueops.IsNothingToCommitError(err)
}

// CreateIssue creates an issue within the transaction.
// Routes ephemeral issues to the wisps table.
func (t *doltTransaction) CreateIssue(ctx context.Context, issue *types.Issue, actor string) error {
	if issue == nil {
		return fmt.Errorf("issue must not be nil")
	}

	if issueops.IsWisp(issue) {
		bc, err := issueops.NewBatchContext(ctx, t.ignoredTx, storage.BatchCreateOptions{SkipPrefixValidation: true})
		if err != nil {
			return err
		}
		_, err = issueops.CreateIssueInTxWithResult(ctx, t.ignoredTx, bc, issue, actor)
		return err
	}

	bc, err := issueops.NewBatchContext(ctx, t.regularTx, storage.BatchCreateOptions{SkipPrefixValidation: true})
	if err != nil {
		return err
	}
	result, err := issueops.CreateIssueInTxWithResult(ctx, t.regularTx, bc, issue, actor)
	if err != nil {
		return err
	}
	for table := range issueops.CreateIssueDirtyTables(ctx, issue, result) {
		t.dirty.MarkDirty(table)
	}
	return nil
}

// CreateIssues creates multiple issues within the transaction
func (t *doltTransaction) CreateIssues(ctx context.Context, issues []*types.Issue, actor string) error {
	if len(issues) == 0 {
		return nil
	}

	// This must run before splitting regular issues from wisps: the shared
	// create helper below only sees the regular subset.
	if err := issueops.ValidateCreateIssuesMixedBucketDependencies(issues); err != nil {
		return err
	}

	var regularIssues []*types.Issue
	var wispIssues []*types.Issue
	for _, issue := range issues {
		if issueops.IsWisp(issue) {
			wispIssues = append(wispIssues, issue)
		} else {
			regularIssues = append(regularIssues, issue)
		}
	}

	if len(regularIssues) > 0 {
		result, err := issueops.CreateIssuesInTxWithResult(ctx, t.regularTx, regularIssues, actor, storage.BatchCreateOptions{
			OrphanHandling:       storage.OrphanAllow,
			SkipPrefixValidation: true,
		})
		if err != nil {
			return err
		}
		for table := range issueops.CreateIssuesDirtyTables(ctx, regularIssues, result) {
			t.dirty.MarkDirty(table)
		}
	}

	if len(wispIssues) > 0 {
		if _, err := issueops.CreateIssuesInTxWithResult(ctx, t.ignoredTx, wispIssues, actor, storage.BatchCreateOptions{
			OrphanHandling:       storage.OrphanAllow,
			SkipPrefixValidation: true,
		}); err != nil {
			return err
		}
	}
	return nil
}

// GetIssue retrieves an issue within the transaction.
// Checks wisps table for active wisps (including explicit-ID ephemerals).
// GetIssue reads a single issue by ID within the transaction, hydrating labels
// in the same connection.
//
// beads-898t2: this delegates to issueops.GetIssueInTxSplit so the in-tx read
// shares the exact SQL of DoltStore.GetIssue / embeddeddolt (retiring the
// hand-copy DEFECT-GENERATOR class — kyr9q/5rn1c label+filter drift all lived in
// the private reimplementation this replaces). The split arguments preserve the
// doltTransaction two-connection model: issues on the versioned regularTx, wisps
// on the dolt_ignore'd ignoredTx, so a same-transaction wisp write is still
// read-your-own-writes visible (TestRunInTransactionCreateIssuesMixedWispReadYourWrites)
// and DOLT_COMMIT does not stage the ignored tables. The helper reads issues
// first then falls back to wisps, matching DoltStore.GetIssue's table order.
func (t *doltTransaction) GetIssue(ctx context.Context, id string) (*types.Issue, error) {
	return issueops.GetIssueInTxSplit(ctx, t.regularTx, t.ignoredTx, id)
}

// SearchIssues searches for issues within the transaction.
// Supports the same filter fields as DoltStore.SearchIssues (bd-v6v8).
//
// beads-898t2: delegates to issueops.SearchIssuesInTxSplit so the in-tx search
// runs the identical filter/merge/sort/paginate SQL as DoltStore.SearchIssues
// (retiring the ~450-line hand-copied searchIssueIDsInTx that the u9zr/vq0bu/
// nyhdd per-field patches kept re-fixing). The split arguments keep issues on
// the versioned regularTx and wisps on the dolt_ignore'd ignoredTx so the
// non-ephemeral wisps merge (NoHistory beads, GH#3649/#3659, beads-nyhdd) sees
// same-transaction wisp writes on the connection that made them.
func (t *doltTransaction) SearchIssues(ctx context.Context, query string, filter types.IssueFilter) ([]*types.Issue, error) {
	return issueops.SearchIssuesInTxSplit(ctx, t.regularTx, t.ignoredTx, query, filter)
}

func (t *doltTransaction) UpdateIssue(ctx context.Context, id string, updates map[string]interface{}, actor string) error {
	table := "issues"
	if t.isActiveWisp(ctx, id) {
		table = "wisps"
	}

	if rawMeta, ok := updates["metadata"]; ok {
		metadataStr, err := storage.NormalizeMetadataValue(rawMeta)
		if err != nil {
			return fmt.Errorf("invalid metadata: %w", err)
		}
		if err := validateMetadataIfConfigured(json.RawMessage(metadataStr)); err != nil {
			return err
		}
	}

	if _, err := issueops.UpdateIssueWithoutEventInTx(ctx, t.txFor(table), id, updates, actor); err != nil {
		return wrapExecError("update issue in tx", err)
	}
	t.dirty.MarkDirty(table)
	return nil
}

func (t *doltTransaction) CloseIssue(ctx context.Context, id string, reason string, actor string, session string) error {
	table := "issues"
	if t.isActiveWisp(ctx, id) {
		table = "wisps"
	}

	if _, err := issueops.CloseIssueWithoutEventInTx(ctx, t.txFor(table), id, reason, actor, session); err != nil {
		return wrapExecError("close issue in tx", err)
	}
	t.dirty.MarkDirty(table)
	return nil
}

func (t *doltTransaction) DeleteIssue(ctx context.Context, id string) error {
	table := "issues"
	if t.isActiveWisp(ctx, id) {
		table = "wisps"
	}
	if err := issueops.DeleteIssueInTx(ctx, t.txFor(table), id); err != nil {
		return wrapExecError("delete issue in tx", err)
	}
	t.dirty.MarkDirty(table)
	return nil
}

// AddDependency adds a dependency within the transaction.
// Checks for existing pairs to prevent silent type overwrites.
func (t *doltTransaction) AddDependency(ctx context.Context, dep *types.Dependency, actor string) error {
	return t.AddDependencyWithOptions(ctx, dep, actor, storage.DependencyAddOptions{})
}

func (t *doltTransaction) AddDependencyWithOptions(ctx context.Context, dep *types.Dependency, actor string, addOpts storage.DependencyAddOptions) error {
	table := "dependencies"
	sourceTable := "issues"
	if t.isActiveWisp(ctx, dep.IssueID) {
		table = "wisp_dependencies"
		sourceTable = "wisps"
	}

	isCrossPrefix := isCrossPrefixDep(dep.IssueID, dep.DependsOnID)
	targetTable := "issues"
	kind := issueops.DepTargetIssue
	switch {
	case isCrossPrefix, strings.HasPrefix(dep.DependsOnID, "external:"):
		kind = issueops.DepTargetExternal
	default:
		if t.isActiveWisp(ctx, dep.DependsOnID) {
			targetTable = "wisps"
			kind = issueops.DepTargetWisp
		}
	}

	opts := issueops.AddDependencyOpts{
		SourceTable:    sourceTable,
		TargetTable:    targetTable,
		WriteTable:     table,
		IsCrossPrefix:  isCrossPrefix,
		SkipCycleCheck: addOpts.SkipCycleCheck,
		TargetKind:     &kind,
	}
	if err := issueops.AddDependencyInTx(ctx, t.txFor(table), dep, actor, opts); err != nil {
		return err
	}
	t.dirty.MarkDirty(table)
	return nil
}

// CycleThroughEdges reports a blocking cycle through one of the new edges.
// The graph merges the regular tx's dependencies with the ignored tx's
// wisp_dependencies, so uncommitted writes on both sides are gated — the
// previous DetectCycles ran only on the regular tx and let bulk wisp edges
// commit blocking cycles (bd-578h9.9).
func (t *doltTransaction) CycleThroughEdges(ctx context.Context, edges [][2]string) (string, error) {
	graph := make(map[string][]string)
	if err := issueops.AppendBlockingGraphInTx(ctx, t.txFor("dependencies"), []string{"dependencies"}, graph); err != nil {
		return "", err
	}
	if err := issueops.AppendBlockingGraphInTx(ctx, t.txFor("wisp_dependencies"), []string{"wisp_dependencies"}, graph); err != nil {
		return "", err
	}
	return issueops.CycleThroughEdgesInGraph(graph, edges), nil
}

func (t *doltTransaction) GetDependencyRecords(ctx context.Context, issueID string) ([]*types.Dependency, error) {
	table := "dependencies"
	if t.isActiveWisp(ctx, issueID) {
		table = "wisp_dependencies"
	}

	//nolint:gosec // G201: table is hardcoded
	rows, err := t.txFor(table).QueryContext(ctx, fmt.Sprintf(`
		SELECT issue_id, %s AS depends_on_id, type, created_at, created_by, metadata, thread_id
		FROM %s
		WHERE issue_id = ?
	`, issueops.DepTargetExpr, table), issueID)
	if err != nil {
		return nil, wrapQueryError("get dependency records in tx", err)
	}
	defer rows.Close()

	var deps []*types.Dependency
	for rows.Next() {
		var d types.Dependency
		var metadata sql.NullString
		var threadID sql.NullString
		if err := rows.Scan(&d.IssueID, &d.DependsOnID, &d.Type, &d.CreatedAt, &d.CreatedBy, &metadata, &threadID); err != nil {
			return nil, wrapScanError("get dependency records in tx", err)
		}
		if metadata.Valid {
			d.Metadata = metadata.String
		}
		if threadID.Valid {
			d.ThreadID = threadID.String
		}
		deps = append(deps, &d)
	}
	return deps, rows.Err()
}

func (t *doltTransaction) RemoveDependency(ctx context.Context, issueID, dependsOnID string, actor string) error {
	table := "dependencies"
	if t.isActiveWisp(ctx, issueID) {
		table = "wisp_dependencies"
	}
	if err := issueops.RemoveDependencyInTx(ctx, t.txFor(table), issueID, dependsOnID, actor); err != nil {
		return wrapExecError("remove dependency in tx", err)
	}
	t.dirty.MarkDirty(table)
	return nil
}

// AddLabel adds a label within the transaction
func (t *doltTransaction) AddLabel(ctx context.Context, issueID, label, actor string) error {
	table := "labels"
	if t.isActiveWisp(ctx, issueID) {
		table = "wisp_labels"
	}

	//nolint:gosec // G201: table is hardcoded
	_, err := t.txFor(table).ExecContext(ctx, fmt.Sprintf(`
		INSERT IGNORE INTO %s (issue_id, label) VALUES (?, ?)
	`, table), issueID, label)
	if err == nil {
		t.dirty.MarkDirty(table)
	}
	return wrapExecError("add label in tx", err)
}

func (t *doltTransaction) GetLabels(ctx context.Context, issueID string) ([]string, error) {
	table := "labels"
	if t.isActiveWisp(ctx, issueID) {
		table = "wisp_labels"
	}

	//nolint:gosec // G201: table is hardcoded
	rows, err := t.txFor(table).QueryContext(ctx, fmt.Sprintf(`SELECT label FROM %s WHERE issue_id = ? ORDER BY label`, table), issueID)
	if err != nil {
		return nil, wrapQueryError("get labels in tx", err)
	}
	defer rows.Close()
	var labels []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, wrapScanError("get labels in tx", err)
		}
		labels = append(labels, l)
	}
	return labels, rows.Err()
}

// RemoveLabel removes a label within the transaction
func (t *doltTransaction) RemoveLabel(ctx context.Context, issueID, label, actor string) error {
	table := "labels"
	if t.isActiveWisp(ctx, issueID) {
		table = "wisp_labels"
	}

	//nolint:gosec // G201: table is hardcoded
	_, err := t.txFor(table).ExecContext(ctx, fmt.Sprintf(`
		DELETE FROM %s WHERE issue_id = ? AND label = ?
	`, table), issueID, label)
	if err == nil {
		t.dirty.MarkDirty(table)
	}
	return wrapExecError("remove label in tx", err)
}

// SetConfig sets a config value within the transaction
func (t *doltTransaction) SetConfig(ctx context.Context, key, value string) error {
	_, err := t.regularTx.ExecContext(ctx, `
		INSERT INTO config (`+"`key`"+`, value) VALUES (?, ?)
		ON DUPLICATE KEY UPDATE value = VALUES(value)
	`, key, value)
	if err == nil {
		t.dirty.MarkDirty("config")
	}
	return wrapExecError("set config in tx", err)
}

// GetConfig gets a config value within the transaction
func (t *doltTransaction) GetConfig(ctx context.Context, key string) (string, error) {
	var value string
	err := t.regularTx.QueryRowContext(ctx, "SELECT value FROM config WHERE `key` = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, wrapQueryError("get config in tx", err)
}

// SetMetadata sets a metadata value within the transaction
func (t *doltTransaction) SetMetadata(ctx context.Context, key, value string) error {
	_, err := t.regularTx.ExecContext(ctx, `
		INSERT INTO metadata (`+"`key`"+`, value) VALUES (?, ?)
		ON DUPLICATE KEY UPDATE value = VALUES(value)
	`, key, value)
	if err == nil {
		t.dirty.MarkDirty("metadata")
	}
	return wrapExecError("set metadata in tx", err)
}

// GetMetadata gets a metadata value within the transaction
func (t *doltTransaction) GetMetadata(ctx context.Context, key string) (string, error) {
	var value string
	err := t.regularTx.QueryRowContext(ctx, "SELECT value FROM metadata WHERE `key` = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, wrapQueryError("get metadata in tx", err)
}

// SetLocalMetadata sets a value in the dolt-ignored local_metadata table within the transaction.
func (t *doltTransaction) SetLocalMetadata(ctx context.Context, key, value string) error {
	_, err := t.ignoredTx.ExecContext(ctx, "REPLACE INTO local_metadata (`key`, value) VALUES (?, ?)", key, value)
	return wrapExecError("set local metadata in tx", err)
}

// GetLocalMetadata gets a value from the dolt-ignored local_metadata table within the transaction.
func (t *doltTransaction) GetLocalMetadata(ctx context.Context, key string) (string, error) {
	var value string
	err := t.ignoredTx.QueryRowContext(ctx, "SELECT value FROM local_metadata WHERE `key` = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, wrapQueryError("get local metadata in tx", err)
}

func (t *doltTransaction) ImportIssueComment(ctx context.Context, issueID, author, text string, createdAt time.Time) (*types.Comment, error) {
	_, err := t.GetIssue(ctx, issueID)
	if err != nil {
		return nil, err
	}

	table := "comments"
	if t.isActiveWisp(ctx, issueID) {
		table = "wisp_comments"
	}

	createdAt = createdAt.UTC()
	id := uuid.Must(uuid.NewV7()).String()
	//nolint:gosec // G201: table is hardcoded
	_, err = t.txFor(table).ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (id, issue_id, author, text, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, table), id, issueID, author, text, createdAt)
	if err != nil {
		return nil, fmt.Errorf("failed to add comment: %w", err)
	}
	t.dirty.MarkDirty(table)

	return &types.Comment{ID: id, IssueID: issueID, Author: author, Text: text, CreatedAt: createdAt}, nil
}

func (t *doltTransaction) GetIssueComments(ctx context.Context, issueID string) ([]*types.Comment, error) {
	table := "comments"
	if t.isActiveWisp(ctx, issueID) {
		table = "wisp_comments"
	}

	//nolint:gosec // G201: table is hardcoded
	rows, err := t.txFor(table).QueryContext(ctx, fmt.Sprintf(`
		SELECT id, issue_id, author, text, created_at
		FROM %s
		WHERE issue_id = ?
		ORDER BY created_at ASC, id ASC
	`, table), issueID)
	if err != nil {
		return nil, wrapQueryError("get comments in tx", err)
	}
	defer rows.Close()
	var comments []*types.Comment
	for rows.Next() {
		var c types.Comment
		if err := rows.Scan(&c.ID, &c.IssueID, &c.Author, &c.Text, &c.CreatedAt); err != nil {
			return nil, wrapScanError("get comments in tx", err)
		}
		comments = append(comments, &c)
	}
	return comments, rows.Err()
}

// AddComment adds a comment within the transaction
func (t *doltTransaction) AddComment(ctx context.Context, issueID, actor, comment string) error {
	table := "events"
	if t.isActiveWisp(ctx, issueID) {
		table = "wisp_events"
	}

	//nolint:gosec // G201: table is hardcoded
	_, err := t.txFor(table).ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (id, issue_id, event_type, actor, comment)
		VALUES (?, ?, ?, ?, ?)
	`, table), issueops.NewEventID(), issueID, types.EventCommented, actor, comment)
	if err == nil {
		t.dirty.MarkDirty(table)
	}
	return wrapExecError("add comment in tx", err)
}
