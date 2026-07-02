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
	"database/sql/driver"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	mysql "github.com/go-sql-driver/mysql"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/steveyegge/beads/internal/storage/doltutil"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/storage/versioncontrolops"
)

// BackupAdd registers a Dolt backup destination.
func (s *DoltStore) BackupAdd(ctx context.Context, name, url string) error {
	return versioncontrolops.BackupAdd(ctx, s.db, name, url)
}

// BackupSync pushes the database to the named backup destination.
func (s *DoltStore) BackupSync(ctx context.Context, name string) error {
	return versioncontrolops.BackupSync(ctx, s.db, name)
}

// BackupRemove removes a configured Dolt backup destination.
func (s *DoltStore) BackupRemove(ctx context.Context, name string) error {
	return versioncontrolops.BackupRemove(ctx, s.db, name)
}

// BackupDatabase registers dir as a file:// Dolt backup remote and syncs
// the full database to it, preserving complete commit history.
func (s *DoltStore) BackupDatabase(ctx context.Context, dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("backup destination does not exist: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("backup destination is not a directory: %s", dir)
	}

	backupURL, err := versioncontrolops.DirToFileURL(dir)
	if err != nil {
		return err
	}
	backupName := "backup_export"

	// Register as a backup remote (idempotent — remove first if exists).
	_ = versioncontrolops.BackupRemove(ctx, s.db, backupName)
	if err := versioncontrolops.BackupAdd(ctx, s.db, backupName, backupURL); err != nil {
		// Another backup (e.g. "default" registered by `bd backup init`) may
		// already point to this URL. In that case, sync using the existing
		// remote name rather than failing.
		if conflict := versioncontrolops.ExtractAddressConflictName(err); conflict != "" {
			if syncErr := versioncontrolops.BackupSync(ctx, s.db, conflict); syncErr != nil {
				return fmt.Errorf("sync to backup: %w", syncErr)
			}
			return nil
		}
		return fmt.Errorf("register backup remote: %w", err)
	}
	if err := versioncontrolops.BackupSync(ctx, s.db, backupName); err != nil {
		return fmt.Errorf("sync to backup: %w", err)
	}
	return nil
}

// RestoreDatabase restores the database from a Dolt backup at dir.
// When force is true, an existing database is overwritten.
func (s *DoltStore) RestoreDatabase(ctx context.Context, dir string, force bool) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("backup source does not exist: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("backup source is not a directory: %s", dir)
	}

	backupURL, err := versioncontrolops.DirToFileURL(dir)
	if err != nil {
		return err
	}
	return versioncontrolops.BackupRestore(ctx, s.db, backupURL, s.database, force)
}

// hasMatchingCLIRemote reports whether the local CLI directory contains the
// same remote URL that SQL reports. CLI push/pull/fetch run from CLIDir, so
// SQL visibility alone is not enough to route safely.
func (s *DoltStore) hasMatchingCLIRemote(remote, expectedURL string) bool {
	if expectedURL == "" {
		return false
	}
	cliDir := s.CLIDir()
	if cliDir == "" {
		return false
	}
	if !s.hasCLIDatabase() {
		return false
	}
	return doltutil.RemoteURLsMatch(doltutil.FindCLIRemote(cliDir, remote), expectedURL)
}

// hasCLIDatabase reports whether CLIDir points at an initialized Dolt database.
// SQL-capable routes use this as a CLI availability check and fall back to SQL
// when an external-server client has only a placeholder local directory.
func (s *DoltStore) hasCLIDatabase() bool {
	cliDir := s.CLIDir()
	if cliDir == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(cliDir, ".dolt"))
	return err == nil && info.IsDir()
}

// ensureMatchingCLIRemote materializes the local CLI remote needed before
// subprocess push/pull/fetch routing. SQL remains the source of truth; the CLI
// remote is only the local transport surface that dolt subprocesses read.
func (s *DoltStore) ensureMatchingCLIRemote(remote, expectedURL string) error {
	if s.hasMatchingCLIRemote(remote, expectedURL) {
		return nil
	}
	cliDir := s.CLIDir()
	if expectedURL == "" {
		return fmt.Errorf("remote %q has an empty SQL URL", remote)
	}
	if cliDir == "" {
		return fmt.Errorf("remote %q (%s) requires CLI routing but no CLI directory is configured", remote, expectedURL)
	}
	if err := doltutil.EnsureCLIRemote(cliDir, remote, expectedURL); err != nil {
		return fmt.Errorf("materialize CLI remote %q (%s) in %s: %w", remote, expectedURL, cliDir, err)
	}
	if !s.hasMatchingCLIRemote(remote, expectedURL) {
		return fmt.Errorf("materialized CLI remote %q in %s, but its URL does not match SQL URL %q", remote, cliDir, expectedURL)
	}
	return nil
}

func (s *DoltStore) prepareDoltCLITransfer(ctx context.Context, remote string, creds *remoteCredentials, args ...string) (*exec.Cmd, context.CancelFunc) {
	return prepareDoltCLITransferCommand(ctx, s.CLIDir(), creds, s.isS3Remote(ctx, remote), args...)
}

func prepareDoltCLITransferCommand(ctx context.Context, cliDir string, creds *remoteCredentials, s3Remote bool, args ...string) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := withCLIExecTimeout(ctx)
	cmd := exec.CommandContext(ctx, "dolt", args...) // #nosec G204 -- fixed command with validated remote/ref args
	cmd.Dir = cliDir
	creds.applyToCmd(cmd)
	if s3Remote {
		applyS3ChecksumEnvToCmd(cmd)
	}
	return cmd, cancel
}

// prepareCLIRouteForGitProtocol reports whether the SQL-visible remote uses
// git wire protocol and prepares the matching local CLI remote before routing.
func (s *DoltStore) prepareCLIRouteForGitProtocol(ctx context.Context, remote string) (bool, error) {
	if s.CLIDir() == "" {
		return false, nil
	}
	if !s.hasCLIDatabase() {
		return false, nil
	}
	remotes, err := s.ListRemotes(ctx)
	if err != nil {
		return false, fmt.Errorf("list Dolt remotes before git-protocol routing: %w", err)
	}
	for _, r := range remotes {
		if r.Name == remote {
			if !doltutil.IsGitProtocolURL(r.URL) {
				return false, nil
			}
			if err := s.ensureMatchingCLIRemote(remote, r.URL); err != nil {
				return false, fmt.Errorf("remote %q uses git protocol and requires CLI routing: %w", remote, err)
			}
			return true, nil
		}
	}
	return false, nil
}

// shouldUseCLIForGitProtocol is a compatibility wrapper for tests and older
// call sites. Prefer prepareCLIRouteForGitProtocol so mutation is explicit.
func (s *DoltStore) shouldUseCLIForGitProtocol(ctx context.Context, remote string) (bool, error) {
	return s.prepareCLIRouteForGitProtocol(ctx, remote)
}

// isGitProtocolRemote reports whether the SQL-visible remote uses git wire
// protocol and the same remote is available in the local CLI directory.
func (s *DoltStore) isGitProtocolRemote(ctx context.Context, remote string) bool {
	ok, err := s.prepareCLIRouteForGitProtocol(ctx, remote)
	if err != nil {
		log.Printf("warning: %v", err)
		return false
	}
	return ok
}

// mainRemoteCredentials returns credentials for the main remote, or nil if none.
func (s *DoltStore) mainRemoteCredentials() *remoteCredentials {
	if s.remoteUser == "" && s.remotePassword == "" {
		return nil
	}
	return &remoteCredentials{username: s.remoteUser, password: s.remotePassword}
}

// credentialsForRemote returns credentials only when the target remote is the
// default remote (s.remote). Non-default remotes get nil creds to avoid sending
// the wrong credentials to the wrong host.
func (s *DoltStore) credentialsForRemote(remote string) *remoteCredentials {
	if remote == s.remote {
		return s.mainRemoteCredentials()
	}
	return nil
}

// prePushFSCK runs dolt fsck --quiet to verify local chunk integrity before
// pushing. This prevents propagating Dolt remote corruption (dangling blob
// references) that arise when concurrent pushes race on the remote manifest.
//
// When multiple agents push simultaneously, one push's manifest update can
// land before another's chunks finish uploading, leaving a manifest that
// references chunks that were never stored. Any agent that then fetches and
// re-pushes that remote faithfully propagates the dangling reference.
//
// If CLIDir is empty or .dolt/noms does not exist, the check is skipped.
// Any fsck failure returns ErrDanglingReference — the push is NOT attempted.
func (s *DoltStore) prePushFSCK(ctx context.Context) error {
	dir := s.CLIDir()
	if dir == "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, ".dolt", "noms")); os.IsNotExist(err) {
		return nil
	}
	fsckCtx, cancel := context.WithTimeout(ctx, fsckTimeout)
	defer cancel()
	cmd := exec.CommandContext(fsckCtx, "dolt", "fsck", "--quiet") // #nosec G204 -- fixed command
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		output := strings.TrimSpace(string(out))
		// Distinguish "fsck couldn't run the integrity check" (environmental /
		// tooling issue) from "fsck ran and found integrity problems" (the actual
		// concern of PR #3447). Wrapping an open-failure as ErrDanglingReference
		// misleads users into thinking their db is corrupt.
		//
		// Concrete example: dolthub/dolt#10915 (Windows url.Parse bug, pre-v1.86.4)
		// caused fsck to construct a malformed file path and fail to open; users
		// running `bd dolt push` saw "dangling chunk reference" errors on perfectly
		// healthy databases.
		//
		// The two known "couldn't open" signatures from dolt are covered below.
		// Any other fsck failure still aborts the push so real dangling references
		// continue to block propagation.
		if fsckCouldNotOpen(output) {
			log.Printf("pre-push fsck could not run, skipping integrity check: %s", output)
			return nil
		}
		return fmt.Errorf("%w: aborting push to prevent propagating corrupt chunks: %s",
			ErrDanglingReference, output)
	}
	return nil
}

// fsckCouldNotOpen reports whether dolt fsck output indicates the check
// could not run at all (as opposed to finding integrity problems). Matches
// the known error phrasings dolt emits before any integrity work begins.
func fsckCouldNotOpen(output string) bool {
	switch {
	case strings.Contains(output, "Could not open dolt database"):
		return true
	case strings.Contains(output, "repository state is invalid"):
		return true
	default:
		return false
	}
}

// doltCLIPush shells out to `dolt push` from the database directory.
// Used for git-protocol remotes where CALL DOLT_PUSH times out through the SQL connection.
// If creds is non-nil, credentials are set on the subprocess environment only,
// avoiding process-wide env var races with concurrent goroutines.
func (s *DoltStore) doltCLIPush(ctx context.Context, remote string, force bool, creds *remoteCredentials) error {
	if err := s.prePushFSCK(ctx); err != nil {
		return err
	}
	args := []string{"push"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, remote, s.branch)
	cmd, cancel := s.prepareDoltCLITransfer(ctx, remote, creds, args...)
	defer cancel()
	applyNoGitHooksToCmd(cmd) // GH#3724
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("dolt push failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// doltCLIPull shells out to `dolt pull` from the database directory.
// Used for git-protocol remotes where CALL DOLT_PULL times out through the SQL connection.
// If creds is non-nil, credentials are set on the subprocess environment only.
func (s *DoltStore) doltCLIPull(ctx context.Context, remote string, creds *remoteCredentials) error {
	cmd, cancel := s.prepareDoltCLITransfer(ctx, remote, creds, "pull", remote, s.branch)
	defer cancel()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("dolt pull failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// Push pushes commits to the remote.
// For git-protocol remotes (SSH, git+https://, git://), uses CLI `dolt push` to avoid MySQL connection timeouts.
// For non-SSH Hosted Dolt (remoteUser set), uses CALL DOLT_PUSH with --user authentication.
// For other remotes (DoltHub, S3, GCS, file), uses CALL DOLT_PUSH via SQL.
func (s *DoltStore) Push(ctx context.Context) (retErr error) {
	return s.pushToRemote(ctx, s.remote, false)
}

// ForcePush force-pushes commits to the remote, overwriting remote changes.
// Use when the remote has uncommitted changes in its working set.
// For git-protocol remotes (SSH, git+https://, git://), uses CLI `dolt push --force` to avoid MySQL connection timeouts.
func (s *DoltStore) ForcePush(ctx context.Context) (retErr error) {
	return s.pushToRemote(ctx, s.remote, true)
}

// PushRemote pushes commits to a named remote. Unlike Push(), which always
// uses the configured default remote (s.remote), PushRemote targets an
// explicit remote name. Credentials are only applied when the target remote
// matches the default remote; otherwise nil creds are used.
func (s *DoltStore) PushRemote(ctx context.Context, remote string, force bool) error {
	return s.pushToRemote(ctx, remote, force)
}

// pushToRemote is the internal implementation for all push operations.
// It routes through CLI or SQL based on the remote's protocol and credentials.
func (s *DoltStore) pushToRemote(ctx context.Context, remote string, force bool) (retErr error) {
	spanName := "dolt.push"
	if force {
		spanName = "dolt.force_push"
	}
	ctx, span := doltTracer.Start(ctx, spanName,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(append(s.doltSpanAttrs(),
			attribute.String("dolt.remote", remote),
			attribute.String("dolt.branch", s.branch),
		)...),
	)
	defer func() { endSpan(span, retErr) }()
	creds := s.credentialsForRemote(remote)
	// Git-protocol remotes: use CLI to avoid MySQL connection timeout during transfer.
	// Must check before remoteUser — Hosted Dolt SSH remotes have remoteUser set
	// but still need CLI to avoid SQL connection timeout.
	// Credentials are passed directly to the subprocess via cmd.Env, avoiding
	// process-wide env var races with concurrent goroutines.
	if useCLI, err := s.prepareCLIRouteForGitProtocol(ctx, remote); err != nil {
		return err
	} else if useCLI {
		return s.doltCLIPush(ctx, remote, force, creds)
	}
	// Credential CLI routing: when credentials are set and server is external,
	// route through CLI subprocess so credentials reach the dolt process via
	// cmd.Env (applyToCmd). The SQL path's withEnvCredentials sets process-wide
	// env vars that an external server cannot see.
	if useCLI, err := s.prepareCLIRouteForCredentials(ctx, remote, creds); err != nil {
		return err
	} else if useCLI {
		return s.doltCLIPush(ctx, remote, force, creds)
	}
	// Cloud auth CLI routing: when cloud storage env vars (AZURE_*, AWS_*,
	// etc.) are set and we're in server mode, route through CLI so the dolt
	// subprocess inherits the current env. The SQL server may not have these
	// vars if it was started in a different context (GH#6).
	if useCLI, err := s.prepareCLIRouteForCloudAuth(ctx, remote); err != nil {
		return err
	} else if useCLI {
		return s.doltCLIPush(ctx, remote, force, creds)
	}
	if useCLI, err := s.shouldUseCLIForLocalRemoteWithError(ctx, remote); err != nil {
		return err
	} else if useCLI {
		return s.doltCLIPush(ctx, remote, force, creds)
	}
	if s.remoteUser != "" && remote == s.remote {
		return withRemoteOperationEnv(creds, s.isS3Remote(ctx, remote), func() error {
			if force {
				if err := s.execWithLongTimeoutNoTx(ctx, "CALL DOLT_PUSH('--force', '--user', ?, ?, ?)", s.remoteUser, remote, s.branch); err != nil {
					return fmt.Errorf("failed to force push to %s/%s: %w", remote, s.branch, err)
				}
			} else {
				if err := s.execWithLongTimeoutNoTx(ctx, "CALL DOLT_PUSH('--user', ?, ?, ?)", s.remoteUser, remote, s.branch); err != nil {
					return fmt.Errorf("failed to push to %s/%s: %w", remote, s.branch, err)
				}
			}
			return nil
		})
	}
	return withRemoteOperationEnv(nil, s.isS3Remote(ctx, remote), func() error {
		if force {
			if err := s.execWithLongTimeoutNoTx(ctx, "CALL DOLT_PUSH('--force', ?, ?)", remote, s.branch); err != nil {
				return fmt.Errorf("failed to force push to %s/%s: %w", remote, s.branch, err)
			}
		} else {
			if err := s.execWithLongTimeoutNoTx(ctx, "CALL DOLT_PUSH(?, ?)", remote, s.branch); err != nil {
				return fmt.Errorf("failed to push to %s/%s: %w", remote, s.branch, err)
			}
		}
		return nil
	})
}

// Pull pulls changes from the remote.
// Passes branch explicitly to avoid "did not specify a branch" errors.
// For git-protocol remotes (SSH, git+https://, git://), uses CLI `dolt pull` to avoid MySQL connection timeouts.
// For non-SSH Hosted Dolt (remoteUser set), uses CALL DOLT_PULL with --user authentication.
//
// If the pull results in merge conflicts on the metadata table only (e.g., from
// stale dolt_auto_push_* rows on multi-machine setups), the conflicts are
// automatically resolved using "theirs" strategy (GH#2466).
func (s *DoltStore) Pull(ctx context.Context) (retErr error) {
	return s.pullFromRemote(ctx, s.remote)
}

// PullRemote pulls changes from a named remote. Unlike Pull(), which always
// uses the configured default remote (s.remote), PullRemote targets an
// explicit remote name. Credentials are only applied when the target remote
// matches the default remote; otherwise nil creds are used.
func (s *DoltStore) PullRemote(ctx context.Context, remote string) error {
	return s.pullFromRemote(ctx, remote)
}

// pullFromRemote is the internal implementation for all pull operations.
// It routes through CLI or SQL based on the remote's protocol and credentials.
func (s *DoltStore) pullFromRemote(ctx context.Context, remote string) (retErr error) {
	ctx, span := doltTracer.Start(ctx, "dolt.pull",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(append(s.doltSpanAttrs(),
			attribute.String("dolt.remote", remote),
			attribute.String("dolt.branch", s.branch),
		)...),
	)
	defer func() { endSpan(span, retErr) }()

	// GH#2474: Auto-commit pending changes before pull to prevent
	// "cannot merge with uncommitted changes" errors. Store initialization
	// (schema init, molecule loading, metadata writes) can dirty the working
	// set before the user's pull command runs.
	if !s.readOnly {
		if err := s.commitBeforePull(ctx, "auto-commit before pull"); err != nil {
			// "nothing to commit" is fine — working set is already clean
			if !isDoltNothingToCommit(err) {
				return fmt.Errorf("failed to commit pending changes before pull: %w", err)
			}
		}
	}

	// bd-6dnrw.3: capture the pre-pull HEAD so a successful merge can recompute
	// the denormalized is_blocked column for the rows it changed. Read before
	// the transport; an unreadable HEAD degrades to a full recompute.
	preHead := ""
	if !s.readOnly {
		if h, err := s.GetCurrentCommit(ctx); err == nil {
			preHead = h
		}
	}

	if err := s.pullTransport(ctx, remote); err != nil {
		return err
	}

	if !s.readOnly {
		if err := s.recomputeBlockedAfterPull(ctx, preHead); err != nil {
			return fmt.Errorf("pull succeeded but is_blocked recompute failed: %w", err)
		}
	}
	return nil
}

// pullTransport routes one pull through CLI or SQL based on the remote's
// protocol and credentials, including the post-pull conflict auto-resolution
// each route carries. Split from pullFromRemote so every successful route
// funnels back through the is_blocked recompute.
func (s *DoltStore) pullTransport(ctx context.Context, remote string) error {
	creds := s.credentialsForRemote(remote)
	// Git-protocol remotes: use CLI to avoid MySQL connection timeout during transfer.
	// Must check before remoteUser — Hosted Dolt SSH remotes have remoteUser set
	// but still need CLI to avoid SQL connection timeout.
	// Credentials are passed directly to the subprocess via cmd.Env.
	if useCLI, err := s.prepareCLIRouteForGitProtocol(ctx, remote); err != nil {
		return err
	} else if useCLI {
		// CLI pull leaves any conflicts in the working set; run the auto-resolver so
		// git-protocol remotes get the same audit-only dependency / metadata repair
		// as the SQL DOLT_PULL path (#4259).
		return s.finishCLIPull(ctx, s.doltCLIPull(ctx, remote, creds))
	}
	// Credential CLI routing: mirrors git-protocol path, including post-pull
	// auto-resolution.
	if useCLI, err := s.prepareCLIRouteForCredentials(ctx, remote, creds); err != nil {
		return err
	} else if useCLI {
		return s.finishCLIPull(ctx, s.doltCLIPull(ctx, remote, creds))
	}
	// Cloud auth CLI routing (GH#6), including post-pull auto-resolution.
	if useCLI, err := s.prepareCLIRouteForCloudAuth(ctx, remote); err != nil {
		return err
	} else if useCLI {
		return s.finishCLIPull(ctx, s.doltCLIPull(ctx, remote, creds))
	}
	// Local file:// pulls intentionally stay on the SQL path. The matching CLI
	// guard is a push-only optimization; SQL pull keeps pullWithAutoResolve in
	// charge of metadata-only conflict repair.
	if s.remoteUser != "" && remote == s.remote {
		return withRemoteOperationEnv(creds, s.isS3Remote(ctx, remote), func() error {
			if err := s.pullWithAutoResolve(ctx, remote, "CALL DOLT_PULL('--user', ?, ?, ?)", s.remoteUser, remote, s.branch); err != nil {
				return fmt.Errorf("failed to pull from %s/%s: %w", remote, s.branch, err)
			}
			return nil
		})
	}
	return withRemoteOperationEnv(nil, s.isS3Remote(ctx, remote), func() error {
		if err := s.pullWithAutoResolve(ctx, remote, "CALL DOLT_PULL(?, ?)", remote, s.branch); err != nil {
			return fmt.Errorf("failed to pull from %s/%s: %w", remote, s.branch, err)
		}
		return nil
	})
}

// pullWithAutoResolve executes a DOLT_PULL query with long timeout and auto-resolves
// metadata-only merge conflicts using "theirs" strategy. This handles the common case
// where machine-local metadata rows (e.g., dolt_auto_push_*) diverge across clones
// and cause recurring merge conflicts on pull (GH#2466).
//
// Dolt may report merge conflicts in two ways:
//  1. DOLT_PULL itself returns an error (under autocommit)
//  2. DOLT_PULL succeeds but tx.Commit() fails (conflicts in working set)
//
// This method handles both by checking for conflicts after the pull call
// (whether it errored or not) and auto-resolving metadata-only conflicts.
// openLongTimeoutConn opens a dedicated single-connection *sql.DB to this store's
// database with a long read timeout, for merge/pull/conflict operations that can run
// longer than the default connection timeout. The caller must Close the returned DB.
func (s *DoltStore) openLongTimeoutConn() (*sql.DB, error) {
	cfg, err := mysql.ParseDSN(s.connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse DSN for long-timeout connection: %w", err)
	}
	cfg.ReadTimeout = 5 * time.Minute
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("failed to open long-timeout connection: %w", err)
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// remote names the remote the query pulls from; the GH#3144 fetch+merge
// fallback targets it directly, so pulls from non-default remotes (PullRemote,
// federation peers) no longer fall back to s.remote.
func (s *DoltStore) pullWithAutoResolve(ctx context.Context, remote string, query string, args ...any) error {
	db, err := s.openLongTimeoutConn()
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	// Allow commits with conflicts so we can inspect and resolve them.
	if _, err := tx.ExecContext(ctx, "SET @@dolt_allow_commit_conflicts = 1"); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed to set dolt_allow_commit_conflicts: %w", err)
	}
	// bd-6dnrw.4: a merge that violates a foreign key (e.g. one clone deleted
	// an issue while another inserted a child row referencing it) rolls the
	// whole transaction back before it can be inspected. Let it land in the
	// working set instead so tryRepairFKCascadeViolations can apply the
	// cascade semantics; the violation check before tx.Commit() below refuses
	// to commit anything the repair did not fully clear.
	if _, err := tx.ExecContext(ctx, "SET @@dolt_force_transaction_commit = 1"); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed to set dolt_force_transaction_commit: %w", err)
	}

	_, pullErr := tx.ExecContext(ctx, query, args...)

	// GH#3144: When DOLT_PULL fails because upstream branch tracking is not
	// configured in repo_state.json (common when remote was added via
	// bd dolt remote add rather than bd bootstrap/dolt clone), fall back to
	// DOLT_FETCH + DOLT_MERGE which does not require tracking config.
	if pullErr != nil && isBranchTrackingError(pullErr) {
		if _, err := tx.ExecContext(ctx, "CALL DOLT_FETCH(?, ?)", remote, s.branch); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("fetch from %s/%s: %w", remote, s.branch, err)
		}
		trackingRef := remote + "/" + s.branch
		_, mergeErr := tx.ExecContext(ctx, "CALL DOLT_MERGE(?)", trackingRef)
		if mergeErr != nil && strings.Contains(mergeErr.Error(), "up to date") {
			mergeErr = nil
		}
		pullErr = mergeErr
	}

	return s.settleMergeInTx(ctx, tx, pullErr)
}

// settleMergeInTx finishes a pull/merge that ran in tx: it auto-resolves the
// safe conflict classes, repairs FK cascade violations (bd-6dnrw.4), and
// commits — or rolls back when anything needs the operator. pullErr is the
// pull/merge statement's own error; it is surfaced whenever nothing was
// resolved or repaired. The tx must have been opened with
// dolt_allow_commit_conflicts and dolt_force_transaction_commit set, which is
// why the violation gate here is mandatory: with the force flag on, committing
// without it would persist a violated working set.
func (s *DoltStore) settleMergeInTx(ctx context.Context, tx *sql.Tx, pullErr error) error {
	// Check for merge conflicts regardless of whether DOLT_PULL errored.
	// Some Dolt versions error on conflicts, others leave them in the working set.
	resolved, resolveErr := s.tryAutoResolveMergeConflicts(ctx, tx)
	if resolveErr != nil {
		_ = tx.Rollback()
		if pullErr != nil {
			return pullErr
		}
		return resolveErr
	}

	// bd-578h9.15: conflicts the resolver declined are the operator's. Capture
	// them BEFORE the rollback wipes merge state — a post-rollback GetConflicts
	// on a fresh transaction sees an empty set, which made PullFrom's
	// conflict-reporting contract dead code on the SQL route. The resolver
	// pre-screens every table before resolving any, so a declined resolve
	// leaves dolt_conflicts fully intact here.
	if !resolved {
		if conflicts, cErr := versioncontrolops.GetConflicts(ctx, tx); cErr == nil && len(conflicts) > 0 {
			_ = tx.Rollback()
			return &versioncontrolops.MergeConflictsError{Conflicts: conflicts, MergeErr: pullErr}
		}
	}

	// bd-6dnrw.4: repair FK cascade violations the merge produced (child rows
	// whose parent issue was deleted on the other clone). Unrepaired
	// violations MUST NOT be committed.
	repairedViol, hadViol, violErr := s.tryRepairFKCascadeViolations(ctx, tx)
	if violErr != nil {
		_ = tx.Rollback()
		if pullErr != nil {
			return pullErr
		}
		return violErr
	}
	if hadViol && !repairedViol {
		_ = tx.Rollback()
		if pullErr != nil {
			return pullErr
		}
		return fmt.Errorf("pull merge left constraint violations bd cannot auto-repair; inspect dolt_constraint_violations and resolve before retrying")
	}

	if pullErr != nil && !resolved && !repairedViol {
		// Pull failed for a non-conflict reason, or conflicts include non-metadata tables.
		_ = tx.Rollback()
		return pullErr
	}

	// Conclude the merge for resolved conflicts only now, after the FK repair:
	// DOLT_COMMIT refuses a violated working set, so a merge carrying both
	// classes could never settle when the resolver committed first (bd-578h9.14).
	if resolved {
		if err := versioncontrolops.CommitResolvedConflicts(ctx, tx); err != nil {
			_ = tx.Rollback()
			if pullErr != nil {
				return pullErr
			}
			return err
		}
	}

	return tx.Commit()
}

// recomputeBlockedAfterPull recomputes the denormalized is_blocked column for
// the rows a pull's merge changed (bd-6dnrw.3) and commits the result.
// is_blocked is otherwise maintained only by local write paths, so a merge
// that brings in another clone's status or dependency changes leaves it stale
// and `bd ready` trusts it. fromCommit is the pre-pull HEAD; empty means it
// could not be read, which degrades to a full recompute. A pull that merged
// nothing (HEAD unchanged) is a no-op.
func (s *DoltStore) recomputeBlockedAfterPull(ctx context.Context, fromCommit string) error {
	if err := s.recomputeBlockedTx(ctx, fromCommit); err != nil {
		// The merge this recompute covers is already committed, so a plain
		// retry on the next pull would skip as "nothing merged" — leave a
		// marker so it widens its window instead (bd-578h9.11). Best-effort:
		// the recompute error is what matters.
		s.markBlockedRecomputePending(ctx, fromCommit)
		return err
	}
	// Derived state converges: every clone computes the same values from the
	// same merged graph, so committing is merge-safe. Commit no-ops when the
	// recompute changed nothing.
	if err := s.Commit(ctx, "bd: recompute is_blocked after pull"); err != nil && !isDoltNothingToCommit(err) {
		return fmt.Errorf("commit is_blocked recompute: %w", err)
	}
	return nil
}

// RecomputeAllBlocked recomputes is_blocked for every issue and wisp in one full
// pass and returns the number of rows it corrected. It is the mode-independent
// repair behind 'bd recompute-blocked' and 'bd doctor --fix' (bd-6dnrw.37): the
// scoped post-pull recompute is skipped when a re-pull merges nothing, so a
// recompute that failed after its merge committed — or a conflicted pull the
// operator resolved by hand — leaves is_blocked stale until this full pass runs.
// Idempotent: a consistent database corrects nothing.
func (s *DoltStore) RecomputeAllBlocked(ctx context.Context) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin is_blocked recompute: %w", err)
	}
	// Refuse to derive and commit is_blocked from a dirty graph: the recompute
	// reads the current working set and stages only `issues`, so pre-existing
	// dirty issue/dependency edits would otherwise be swept into — or silently
	// inform — the repair commit (bd-6dnrw.37). Checked inside this tx so it
	// sees the same working set the recompute will read.
	if err := issueops.GuardBlockedRecomputeWorkingSet(ctx, tx); err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	changed, err := issueops.RecomputeAllIsBlockedInTx(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit is_blocked recompute: %w", err)
	}
	if changed > 0 {
		// Stage only issues — the synced table is_blocked lives on (wisps are
		// dolt_ignore'd) — so an unrelated dirty working set is not swept in.
		if err := s.doltAddAndCommit(ctx, []string{"issues"}, "bd: recompute is_blocked (full)"); err != nil {
			return int(changed), err
		}
	}
	return int(changed), nil
}

// recomputeBlockedTx runs the post-merge is_blocked recompute in its own
// transaction.
func (s *DoltStore) recomputeBlockedTx(ctx context.Context, fromCommit string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin is_blocked recompute: %w", err)
	}
	if err := issueops.RecomputeIsBlockedAfterMergeInTx(ctx, tx, fromCommit); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit is_blocked recompute: %w", err)
	}
	return nil
}

// markBlockedRecomputePending best-effort records a failed post-merge
// is_blocked recompute (bd-578h9.11); see
// issueops.MarkIsBlockedRecomputePendingInTx.
func (s *DoltStore) markBlockedRecomputePending(ctx context.Context, fromCommit string) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	if err := issueops.MarkIsBlockedRecomputePendingInTx(ctx, tx, fromCommit); err != nil {
		_ = tx.Rollback()
		return
	}
	_ = tx.Commit()
}

// finishCLIPull runs the merge-conflict auto-resolver after a CLI-based pull
// (git-protocol, credentialed, or cloud-auth remotes). CLI `dolt pull` writes any
// merge conflicts into the shared working set but, unlike the SQL DOLT_PULL path,
// returns without a transaction we can inspect — so these remotes historically
// skipped the resolver entirely. With deterministic dependency ids (#4259) a
// same-edge conflict that differs only in audit columns is safe to auto-resolve, and
// the git remote topology in #4259 is exactly this CLI path; route it through the
// same resolver as the SQL path. pullErr is what doltCLIPull returned: a pull that
// fails *because* of conflicts is recoverable once they resolve, so we inspect the
// working set regardless and only surface pullErr when nothing was resolved.
func (s *DoltStore) finishCLIPull(ctx context.Context, pullErr error) error {
	if s.readOnly {
		// A read-only store cannot resolve or commit; surface the pull result as-is.
		return pullErr
	}
	resolved, resolveErr := s.autoResolveConflictsAfterCLIPull(ctx)
	if resolveErr != nil {
		if pullErr != nil {
			return pullErr
		}
		return resolveErr
	}
	if pullErr != nil && !resolved {
		// Pull failed for a non-conflict reason, or conflicts are not auto-resolvable;
		// leave them in the working set for the operator.
		return pullErr
	}
	return nil
}

// autoResolveConflictsAfterCLIPull inspects the working set and auto-resolves the
// conflict classes that are safe without operator input (#4259 audit-only dependency
// edges, GH#2466 metadata). It runs on a connection from the store pool (s.db) on
// purpose: those connections are on the same branch the CLI `dolt pull` merged into,
// whereas a separately opened connection would default to the base branch and never
// see the conflicts. The pull's
// network transfer already completed in the subprocess, so no long-timeout connection
// is needed for the local resolve. Returns (true, nil) only if all conflicts were
// resolved and committed; (false, nil) when there is nothing to resolve or a conflict
// needs the operator, leaving the working set untouched for manual resolution.
func (s *DoltStore) autoResolveConflictsAfterCLIPull(ctx context.Context) (bool, error) {
	// Pin a single connection: @@dolt_allow_commit_conflicts is session-scoped,
	// and setting it through a pooled transaction leaks it to whichever caller
	// drains that connection next. Reset it before releasing the connection; if
	// the reset cannot run, discard the connection rather than return it dirty.
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to acquire connection: %w", err)
	}
	varSet := false
	defer func() {
		if varSet {
			if _, err := conn.ExecContext(ctx, "SET @@dolt_allow_commit_conflicts = 0"); err != nil {
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			}
		}
		_ = conn.Close()
	}()
	// Allow committing while conflicts exist so we can inspect and resolve them.
	if _, err := conn.ExecContext(ctx, "SET @@dolt_allow_commit_conflicts = 1"); err != nil {
		return false, fmt.Errorf("failed to set dolt_allow_commit_conflicts: %w", err)
	}
	varSet = true
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("failed to begin transaction: %w", err)
	}
	resolved, err := s.tryAutoResolveMergeConflicts(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		return false, err
	}
	// bd-6dnrw.4: a CLI pull can also leave FK cascade violations in the
	// shared working set (child rows whose parent issue was deleted on the
	// other clone). Repair them like the SQL route does; unrepaired
	// violations roll back untouched for the operator.
	repairedViol, hadViol, violErr := s.tryRepairFKCascadeViolations(ctx, tx)
	if violErr != nil {
		_ = tx.Rollback()
		return false, violErr
	}
	if hadViol && !repairedViol {
		_ = tx.Rollback()
		return false, nil
	}
	if !resolved && !repairedViol {
		_ = tx.Rollback()
		return false, nil
	}
	// Conclude the merge for resolved conflicts only now, after the FK repair:
	// DOLT_COMMIT refuses a violated working set, so a merge carrying both
	// classes could never settle when the resolver committed first (bd-578h9.14).
	if resolved {
		if err := versioncontrolops.CommitResolvedConflicts(ctx, tx); err != nil {
			_ = tx.Rollback()
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("failed to commit resolved conflicts: %w", err)
	}
	return true, nil
}

// tryAutoResolveMergeConflicts auto-resolves merge conflicts that are safe to
// resolve without operator input (GH#2466 metadata, #4259 audit-only
// dependency edges, bd-6dnrw.29 schema_migrations vintage rows, GH#2474
// convergent kv.memory.* config rows), returning
// (true, nil) only if ALL conflicts were resolved. The implementation is
// shared with the embedded pull path (bd-6dnrw.40); see
// versioncontrolops.TryAutoResolveMergeConflicts for the full contract.
func (s *DoltStore) tryAutoResolveMergeConflicts(ctx context.Context, tx *sql.Tx) (bool, error) {
	return versioncontrolops.TryAutoResolveMergeConflicts(ctx, tx)
}

// tryRepairFKCascadeViolations repairs the post-merge foreign-key constraint
// violations produced by the delete-vs-insert cascade hazard (bd-6dnrw.4).
// The caller's transaction must run with @@dolt_force_transaction_commit=1
// for the merge to survive long enough to be repaired, and must NOT commit
// when (repaired=false, had=true) — unrepaired violations are the operator's.
// The implementation is shared with the embedded pull path (bd-6dnrw.40); see
// versioncontrolops.TryRepairFKCascadeViolations for the full contract.
func (s *DoltStore) tryRepairFKCascadeViolations(ctx context.Context, tx *sql.Tx) (repaired, had bool, err error) {
	return versioncontrolops.TryRepairFKCascadeViolations(ctx, tx)
}
