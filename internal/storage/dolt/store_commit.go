// Package dolt implements the storage interface using Dolt (versioned MySQL-compatible database).
//
// Dolt provides native version control for SQL data with cell-level merge, history queries,
// and federation via Dolt remotes. The database itself is version-controlled.
//
// Dolt capabilities:
//   - Native version control (commit, push, pull, branch, merge)
//   - Time-travel queries via AS OF and dolt_history_* tables
//   - Cell-level merge for conflict resolution
//   - Multi-writer via dolt sql-server (federation, pure Go)
//
// All operations require a running dolt sql-server. Connect via MySQL protocol (pure Go).
package dolt

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/trace"

	"github.com/steveyegge/beads/internal/storage/kvkeys"
)

func (s *DoltStore) commitAuthorString() string {
	return fmt.Sprintf("%s <%s>", s.committerName, s.committerEmail)
}

// Commit creates a Dolt commit with the given message.
//
// GH#2455: Stages all dirty tables EXCEPT config, then commits with '-m'.
// The old '-Am' approach staged ALL dirty tables including config, which
// swept up stale issue_prefix changes from concurrent operations. By
// excluding config from automatic staging, we prevent the corruption.
//
// Callers that intentionally modify config (e.g., CommitPending after
// 'bd config set') must call CommitWithConfig instead.
func (s *DoltStore) Commit(ctx context.Context, message string) error {
	return s.commitWorkingSet(ctx, message, configExclude)
}

// commitBeforePull commits the working set ahead of a pull's merge, INCLUDING
// config. The pre-pull auto-commit (GH#2474) must include config because user
// KV data lives there as kv.* rows (persistent memories are the kv.memory.*
// subset) and Commit() deliberately skips config (GH#2455): without this those
// rows sit permanently uncommitted, so the "clean the working set before
// merging" step leaves config dirty and DOLT_MERGE refuses to start ("cannot
// merge with uncommitted changes").
//
// It includes ONLY this clone's own user kv.* rows: if any other config key is
// dirty (an internal key such as issue_prefix above all) it refuses rather than
// auto-committing it, so the stale-config corruption GH#2455 guards against is
// never re-opened by a pull. Auto-*resolution* of a config conflict stays
// narrower still — only convergent kv.memory.* keys (see
// configConflictsAreMemoryConvergent) — so widening the commit screen to the
// whole kv. namespace cannot auto-resolve a genuine kv.* conflict; it only stops
// generic `bd kv set` writes from wedging the pull. Config is staged explicitly
// (via DOLT_ADD in commitWorkingSet) rather than through CommitWithConfig's
// DOLT_COMMIT('-Am'), which was observed not to stage config reliably under the
// server-mode stored-procedure path. Committing this clone's own kv.* rows as the
// merge basis is the same explicit, user-initiated action CommitPending ('bd dolt
// commit') already performs, so it does not widen the concurrent-writer race
// GH#2455 guards against.
func (s *DoltStore) commitBeforePull(ctx context.Context, message string) error {
	return s.commitWorkingSet(ctx, message, configIncludeUserKVOnly)
}

// CommitMergeResolution concludes a merge whose conflicts were resolved by an
// explicit operator strategy (bd federation sync --strategy / bd vc merge
// --strategy ours|theirs), committing the resolved working set INCLUDING config.
// Plain Commit excludes config (GH#2455), so a config-only resolution — exactly
// the case this change makes routine by syncing kv.* through config — would be
// silently dropped, leaving the merge unconcluded and re-wedging the next
// pull/sync. Unlike commitBeforePull it does not screen config keys: the operator
// chose this resolution, so whichever config rows it touched (issue_prefix
// included) are committed as-is. It satisfies storage.VersionControl so cmd/bd
// concludes bd vc merge --strategy through the same config-inclusive commit
// instead of the config-excluding Commit that would drop the resolution.
func (s *DoltStore) CommitMergeResolution(ctx context.Context, message string) error {
	return s.commitWorkingSet(ctx, message, configIncludeAll)
}

// commitWorkingSet stages the dirty tables reported by dolt_status and commits
// them with '-m'. The config table is staged according to mode: configExclude
// skips it (GH#2455) so a concurrent writer's half-applied issue_prefix change
// is never swept into an unrelated commit; configIncludeUserKVOnly stages it for
// the pre-pull path but refuses when any non-kv. (internal) config key is dirty;
// configIncludeAll stages every dirty config row to conclude an explicit merge
// resolution.
func (s *DoltStore) commitWorkingSet(ctx context.Context, message string, mode configCommitMode) (retErr error) {
	ctx, span := doltTracer.Start(ctx, "dolt.commit",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(s.doltSpanAttrs()...),
	)
	defer func() { endSpan(span, retErr) }()

	// Pin a single connection so all operations run on the same Dolt session.
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Close()

	// GH#2455: stage each dirty table individually, skipping config unless the
	// mode opts it in, to avoid sweeping up stale issue_prefix changes from
	// concurrent operations. Query dolt_status first.
	rows, err := conn.QueryContext(ctx, "SELECT table_name FROM dolt_status")
	if err != nil {
		// If dolt_status fails, fall back to nothing (rare edge case).
		return fmt.Errorf("failed to query dolt_status: %w", err)
	}
	var tables []string
	configDirty := false
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			_ = rows.Close()
			return fmt.Errorf("failed to scan dolt_status: %w", err)
		}
		if table == "config" {
			configDirty = true
			if mode == configExclude {
				continue
			}
		}
		tables = append(tables, table)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to iterate dolt_status: %w", err)
	}

	// GH#2455 + GH#2474: the pre-pull auto-commit includes config so user kv.*
	// writes sync, but it must NOT auto-commit any internal (non-kv.) config key.
	// Refuse before staging anything so the merge is never concluded over an
	// unsafe config row; the operator commits those explicitly.
	if configDirty && mode == configIncludeUserKVOnly {
		if err := s.assertDirtyConfigUserKVOnly(ctx, conn); err != nil {
			return err
		}
	}

	if len(tables) == 0 {
		return nil // Nothing to commit (all changes were config-only or dolt_ignore'd)
	}

	for _, table := range tables {
		if _, err := conn.ExecContext(ctx, "CALL DOLT_ADD(?)", table); err != nil {
			// config when the mode intentionally includes it is the whole reason
			// we stage here: silently skipping a failed DOLT_ADD('config') would
			// leave config dirty and re-wedge the merge, so surface it instead.
			if table == "config" && mode != configExclude {
				return fmt.Errorf("failed to stage config before commit: %w", err)
			}
			// Best effort: some tables may be dolt_ignore'd (e.g., wisps).
			// DOLT_ADD fails for ignored tables; skip silently.
			continue
		}
	}

	// NOTE: In SQL procedure mode, Dolt defaults author to the authenticated SQL user
	// (e.g. root@localhost). Always pass an explicit author for deterministic history.
	if _, err := conn.ExecContext(ctx, "CALL DOLT_COMMIT('-m', ?, '--author', ?)", message, s.commitAuthorString()); err != nil {
		if isDoltNothingToCommit(err) {
			return nil
		}
		return fmt.Errorf("failed to commit: %w", err)
	}

	return nil
}

// assertDirtyConfigUserKVOnly returns an error unless every config row dirty in
// the working set is this clone's own user KV data (the kv.* namespace, which
// includes kv.memory.* memories). The pre-pull auto-commit opts config into the
// staged set so user KV writes sync and stop wedging DOLT_MERGE (GH#2474), but
// auto-committing an unrelated dirty internal config key such as issue_prefix
// would re-open the GH#2455 stale-config corruption — that is the operator's
// explicit `bd dolt commit` to make, not the pull's. Screening on the whole kv.
// namespace (not just kv.memory.*) un-wedges generic `bd kv set` writes too: a
// kv.* row is this clone's own data, exactly as safe to auto-commit as a memory,
// and a genuine kv.* merge conflict is still left for the operator because
// auto-resolution stays kv.memory.*-only (configConflictsAreMemoryConvergent).
// config's primary key is `key`, so dolt_diff exposes to_key/from_key; an add or
// delete leaves one side NULL, so COALESCE picks whichever key the change carries.
func (s *DoltStore) assertDirtyConfigUserKVOnly(ctx context.Context, conn *sql.Conn) error {
	rows, err := conn.QueryContext(ctx,
		"SELECT COALESCE(to_key, from_key) FROM dolt_diff('HEAD', 'WORKING', 'config')")
	if err != nil {
		return fmt.Errorf("inspect dirty config before pull: %w", err)
	}
	defer rows.Close()

	var unsafe []string
	for rows.Next() {
		var key sql.NullString
		if err := rows.Scan(&key); err != nil {
			return fmt.Errorf("scan dirty config key: %w", err)
		}
		if key.Valid && !strings.HasPrefix(key.String, kvkeys.Prefix) {
			unsafe = append(unsafe, key.String)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate dirty config diff: %w", err)
	}
	if len(unsafe) > 0 {
		return fmt.Errorf("refusing to auto-commit %d dirty internal config key(s) before pull: %s; "+
			"only user %s* keys auto-commit before a pull (GH#2455) — commit or revert "+
			"these explicitly with `bd dolt commit` first", len(unsafe), strings.Join(unsafe, ", "), kvkeys.Prefix)
	}
	return nil
}

// CommitWithConfig creates a Dolt commit that includes the config table.
// Use this instead of Commit when the caller intentionally modified config
// (e.g., CommitPending after 'bd config set', 'bd init', or 'bd rename-prefix').
// GH#2455: Commit() excludes config to prevent sweeping up stale changes.
func (s *DoltStore) CommitWithConfig(ctx context.Context, message string) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "CALL DOLT_COMMIT('-Am', ?, '--author', ?)", message, s.commitAuthorString()); err != nil {
		if isDoltNothingToCommit(err) {
			return nil
		}
		return fmt.Errorf("failed to commit: %w", err)
	}
	return nil
}

// doltAddAndCommit stages the specified tables and commits on a pinned
// connection. This prevents DOLT_COMMIT('-Am') from sweeping up stale
// working set changes from concurrent operations (GH#2455).
func (s *DoltStore) doltAddAndCommit(ctx context.Context, tables []string, commitMsg string) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Close()

	for _, table := range tables {
		if _, err := conn.ExecContext(ctx, "CALL DOLT_ADD(?)", table); err != nil {
			return fmt.Errorf("dolt add %s: %w", table, err)
		}
	}
	if _, err := conn.ExecContext(ctx, "CALL DOLT_COMMIT('-m', ?, '--author', ?)",
		commitMsg, s.commitAuthorString()); err != nil && !isDoltNothingToCommit(err) {
		return fmt.Errorf("dolt commit: %w", err)
	}
	return nil
}

// CommitPending creates a single Dolt commit for all uncommitted changes in the working set.
// Returns (true, nil) if changes were committed, (false, nil) if there was nothing to commit,
// or (false, err) on failure. The commit message summarizes the accumulated changes by
// querying dolt_diff to count issue-level operations.
//
// This is the primary commit mechanism for batch mode, where multiple bd commands
// accumulate changes in the working set before committing at a logical boundary.
func (s *DoltStore) CommitPending(ctx context.Context, actor string) (bool, error) {
	// Check if there are any committable changes (excluding dolt_ignore'd tables
	// like wisp tables, which appear in dolt_status but can't be staged).
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM dolt_status s
		WHERE NOT EXISTS (
			SELECT 1 FROM dolt_ignore di
			WHERE di.ignored = 1
			AND s.table_name LIKE di.pattern
		)`).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check status: %w", err)
	}
	if count == 0 {
		return false, nil // Nothing to commit
	}

	msg := s.buildBatchCommitMessage(ctx, actor)
	// GH#2455: CommitPending is an explicit user action (bd dolt commit) that
	// should include ALL pending changes, including config. Use CommitWithConfig
	// instead of Commit to ensure intentional config changes are committed.
	if err := s.CommitWithConfig(ctx, msg); err != nil {
		// Dolt may report "nothing to commit" even when Status() showed changes
		// (e.g., system tables or schema-only diffs). Treat as no-op.
		errLower := strings.ToLower(err.Error())
		if strings.Contains(errLower, "nothing to commit") || strings.Contains(errLower, "no changes") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// buildBatchCommitMessage generates a descriptive commit message summarizing
// what changed since the last commit by querying dolt_diff against HEAD.
// It reports issue-level create/update/delete counts and lists any other
// tables (labels, comments, events, etc.) that have uncommitted changes.
func (s *DoltStore) buildBatchCommitMessage(ctx context.Context, actor string) string {
	if actor == "" {
		actor = s.committerName
	}

	// Count issue-level changes by diff type
	var added, modified, removed int
	rows, err := s.db.QueryContext(ctx, `
		SELECT diff_type, COUNT(*) as cnt
		FROM dolt_diff('HEAD', 'WORKING', 'issues')
		GROUP BY diff_type
	`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var diffType string
			var count int
			if scanErr := rows.Scan(&diffType, &count); scanErr == nil {
				switch diffType {
				case "added":
					added = count
				case "modified":
					modified = count
				case "removed":
					removed = count
				}
			}
		}
		if rowErr := rows.Err(); rowErr != nil {
			// Best effort — proceed with whatever counts we gathered
			_ = rowErr
		}
	}

	// Check which other tables have uncommitted changes beyond issues.
	// This surfaces label, comment, event, and dependency changes that
	// would otherwise produce a generic fallback message.
	var otherTables []string
	statusRows, statusErr := s.db.QueryContext(ctx, `
		SELECT table_name FROM dolt_status s
		WHERE table_name != 'issues'
		AND NOT EXISTS (
			SELECT 1 FROM dolt_ignore di
			WHERE di.ignored = 1
			AND s.table_name LIKE di.pattern
		)`)
	if statusErr == nil {
		defer statusRows.Close()
		for statusRows.Next() {
			var table string
			if scanErr := statusRows.Scan(&table); scanErr == nil {
				otherTables = append(otherTables, table)
			}
		}
		_ = statusRows.Err() // Best effort
	}

	// Build descriptive message
	var parts []string
	if added > 0 {
		parts = append(parts, fmt.Sprintf("%d created", added))
	}
	if modified > 0 {
		parts = append(parts, fmt.Sprintf("%d updated", modified))
	}
	if removed > 0 {
		parts = append(parts, fmt.Sprintf("%d deleted", removed))
	}

	if len(parts) == 0 && len(otherTables) == 0 {
		return fmt.Sprintf("bd: batch commit by %s", actor)
	}

	msg := fmt.Sprintf("bd: batch commit by %s", actor)
	if len(parts) > 0 {
		msg += " — " + strings.Join(parts, ", ")
	}
	if len(otherTables) > 0 {
		msg += fmt.Sprintf(" (+ %s)", strings.Join(otherTables, ", "))
	}
	return msg
}
