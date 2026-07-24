package issueops

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/depid"
	"github.com/steveyegge/beads/internal/types"
)

// BatchContext holds per-batch state read once and reused for every issue.
type BatchContext struct {
	CustomStatuses  []string
	CustomTypes     []string
	ConfigPrefix    string
	AllowedPrefixes string
	Opts            storage.BatchCreateOptions
}

// NewBatchContext reads config from the database and returns a BatchContext.
func NewBatchContext(ctx context.Context, tx DBTX, opts storage.BatchCreateOptions) (*BatchContext, error) {
	customStatuses, err := GetCustomStatusesTx(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("failed to get custom statuses: %w", err)
	}
	customTypes, err := ResolveCustomTypesInTx(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("failed to get custom types: %w", err)
	}
	configPrefix, err := ReadConfigPrefix(ctx, tx)
	if err != nil {
		return nil, err
	}
	var allowedPrefixes string
	_ = tx.QueryRowContext(ctx, "SELECT value FROM config WHERE `key` = ?", "allowed_prefixes").Scan(&allowedPrefixes)

	return &BatchContext{
		CustomStatuses:  customStatuses,
		CustomTypes:     customTypes,
		ConfigPrefix:    configPrefix,
		AllowedPrefixes: allowedPrefixes,
		Opts:            opts,
	}, nil
}

func CreateIssueInTx(ctx context.Context, tx *sql.Tx, bc *BatchContext, issue *types.Issue, actor string) error {
	_, err := CreateIssueInTxWithResult(ctx, tx, bc, issue, actor)
	return err
}

// CreateIssueResult reports the tables actually written by CreateIssueInTx.
type CreateIssueResult struct {
	ChangedTables map[string]bool
	// StaleRejected reports that the RejectStaleUpserts guard kept the stored
	// row: nothing was written, and the issue's aux data must not be
	// persisted by later batch stages either (bd-578h9.8).
	StaleRejected bool
}

func (r *CreateIssueResult) markChanged(table string) {
	if table == "" {
		return
	}
	if r.ChangedTables == nil {
		r.ChangedTables = map[string]bool{}
	}
	r.ChangedTables[table] = true
}

func mergeChangedTables(dst map[string]bool, src map[string]bool) map[string]bool {
	for table := range src {
		if dst == nil {
			dst = map[string]bool{}
		}
		dst[table] = true
	}
	return dst
}

func CreateIssueInTxWithResult(ctx context.Context, tx *sql.Tx, bc *BatchContext, issue *types.Issue, actor string) (CreateIssueResult, error) {
	var result CreateIssueResult
	if err := PrepareIssueForInsert(issue, bc.CustomStatuses, bc.CustomTypes); err != nil {
		return result, err
	}

	// Backfill creation provenance at the shared seam so every create stack
	// (direct/proxied/batch/child-mint) stamps the resolving actor when the
	// caller left CreatedBy empty — the batch leg (cmd/bd/batch.go) builds an
	// issue with no CreatedBy, so it would otherwise land created_by='' while
	// bd create stamps getActorWithGit() (beads-81bfd). Empty-only, mirroring
	// dependencyCreatedBy: an import/restore-supplied CreatedBy is preserved,
	// never overwritten by the importing actor.
	issue.CreatedBy = createdByOrActor(issue, actor)

	issueTable, eventTable := TableRouting(issue)

	if issue.ID == "" {
		prefix := bc.ConfigPrefix
		if issue.PrefixOverride != "" {
			prefix = issue.PrefixOverride
		} else if issue.IDPrefix != "" {
			prefix = bc.ConfigPrefix + "-" + issue.IDPrefix
		} else if IsWisp(issue) {
			prefix = bc.ConfigPrefix + "-wisp"
		}
		var err error
		issue.ID, err = GenerateIssueIDInTable(ctx, tx, issueTable, prefix, issue, actor)
		if err != nil {
			return result, fmt.Errorf("failed to generate issue ID: %w", err)
		}
	} else if !bc.Opts.SkipPrefixValidation {
		if err := ValidateIssueIDPrefix(issue.ID, bc.ConfigPrefix, bc.AllowedPrefixes); err != nil {
			return result, fmt.Errorf("prefix validation failed for %s: %w", issue.ID, err)
		}
	}

	if skip, err := CheckOrphan(ctx, tx, issue, issueTable, bc.Opts.OrphanHandling); err != nil {
		return result, err
	} else if skip {
		return result, nil
	}

	isNew, staleRejected, err := InsertIssueIfNew(ctx, tx, issueTable, issue, bc.Opts)
	if err != nil {
		return result, err
	}
	if staleRejected {
		// The stored row is strictly newer than this snapshot: nothing was
		// written, and the snapshot's labels/comments belong to the older
		// version, so they must not merge in either (bd-578h9.8).
		result.StaleRejected = true
		if bc.Opts.OnStaleRejected != nil {
			bc.Opts.OnStaleRejected(issue.ID)
		}
		return result, nil
	}
	result.markChanged(issueTable)

	if isNew {
		if err := RecordEventInTable(ctx, tx, eventTable, issue.ID, types.EventCreated, actor, ""); err != nil {
			return result, fmt.Errorf("failed to record event for %s: %w", issue.ID, err)
		}
		result.markChanged(eventTable)
	}

	labelResult, err := PersistLabels(ctx, tx, issue, actor, eventTable)
	if err != nil {
		return result, err
	}
	result.ChangedTables = mergeChangedTables(result.ChangedTables, labelResult.ChangedTables)
	commentResult, err := PersistComments(ctx, tx, issue)
	if err != nil {
		return result, err
	}
	result.ChangedTables = mergeChangedTables(result.ChangedTables, commentResult.ChangedTables)
	return result, nil
}

// CreateIssuesResult reports side effects that callers need for selective
// Dolt staging after CreateIssuesInTxWithResult returns.
type CreateIssuesResult struct {
	ChangedTables             map[string]bool
	ChangedChildCounterTables map[string]bool
}

func (r *CreateIssuesResult) markChanged(table string) {
	if table == "" {
		return
	}
	if r.ChangedTables == nil {
		r.ChangedTables = map[string]bool{}
	}
	r.ChangedTables[table] = true
}

func (r *CreateIssuesResult) merge(changed map[string]bool) {
	r.ChangedTables = mergeChangedTables(r.ChangedTables, changed)
}

func CreateIssuesInTx(ctx context.Context, tx *sql.Tx, issues []*types.Issue, actor string, opts storage.BatchCreateOptions) error {
	_, err := CreateIssuesInTxWithResult(ctx, tx, issues, actor, opts)
	return err
}

// CreateIssuesInTxWithResult creates issues and reports tables whose writes are
// only knowable after SQL reconciliation, such as child counter advances.
func CreateIssuesInTxWithResult(ctx context.Context, tx *sql.Tx, issues []*types.Issue, actor string, opts storage.BatchCreateOptions) (CreateIssuesResult, error) {
	filteredIssues, err := filterCreateIssuesMixedBucketDependencies(issues, opts)
	if err != nil {
		return CreateIssuesResult{}, err
	}
	issues = filteredIssues

	bc, err := NewBatchContext(ctx, tx, opts)
	if err != nil {
		return CreateIssuesResult{}, err
	}

	result := CreateIssuesResult{}
	accepted := issues[:0:0]
	for _, issue := range issues {
		issueResult, err := CreateIssueInTxWithResult(ctx, tx, bc, issue, actor)
		if err != nil {
			return CreateIssuesResult{}, err
		}
		result.merge(issueResult.ChangedTables)
		if issueResult.StaleRejected {
			continue // stale snapshot: keep its deps out of the batch too
		}
		accepted = append(accepted, issue)
	}
	issues = accepted

	depResult, err := PersistDependenciesWithOptionsResult(ctx, tx, issues, actor, opts)
	if err != nil {
		return CreateIssuesResult{}, err
	}
	result.merge(depResult.ChangedTables)

	changedCounters, err := ReconcileChildCounters(ctx, tx, issues)
	if err != nil {
		return CreateIssuesResult{}, err
	}
	result.ChangedChildCounterTables = changedCounters
	for table := range changedCounters {
		result.markChanged(table)
	}
	issueIDs, wispIDs := createBlockedRecomputeIDs(issues)
	if err := RecomputeIsBlockedInTx(ctx, tx, issueIDs, wispIDs); err != nil {
		return CreateIssuesResult{}, err
	}
	if len(issueIDs) > 0 {
		result.markChanged("issues")
	}
	if len(wispIDs) > 0 {
		result.markChanged("wisps")
	}
	return result, nil
}

// CreateIssueDirtyTables returns the regular Dolt tables CreateIssueInTx may
// dirty for the given issue. Wisp tables are intentionally omitted because they
// are Dolt-ignored and cannot be staged.
func CreateIssueDirtyTables(ctx context.Context, issue *types.Issue, result CreateIssueResult) map[string]bool {
	dirty := stageableChangedTables(result.ChangedTables)
	if issue == nil {
		return dirty
	}
	if parentID, childNum, ok := ParseHierarchicalID(issue.ID); ok &&
		storage.HasReservedChildCounter(ctx, parentID, childNum) {
		dirty["child_counters"] = true
	}
	return dirty
}

// CreateIssuesDirtyTables returns the regular Dolt tables CreateIssuesInTx may
// dirty, including child counters that reconciliation actually advanced.
func CreateIssuesDirtyTables(ctx context.Context, issues []*types.Issue, result CreateIssuesResult) map[string]bool {
	dirty := stageableChangedTables(result.ChangedTables)
	for _, issue := range issues {
		if issue == nil {
			continue
		}
		if parentID, childNum, ok := ParseHierarchicalID(issue.ID); ok &&
			storage.HasReservedChildCounter(ctx, parentID, childNum) {
			dirty["child_counters"] = true
		}
	}
	return dirty
}

func stageableChangedTables(changed map[string]bool) map[string]bool {
	dirty := map[string]bool{}
	for table := range changed {
		if table == "wisps" || strings.HasPrefix(table, "wisp_") {
			continue
		}
		dirty[table] = true
	}
	return dirty
}

// ValidateCreateIssuesMixedBucketDependencies rejects same-batch dependency
// edges between regular issues and wisps. Dependencies are stored in separate
// backing tables per bucket, so a batch cannot create both ends atomically when
// the edge crosses buckets.
func ValidateCreateIssuesMixedBucketDependencies(issues []*types.Issue) error {
	_, err := filterCreateIssuesMixedBucketDependencies(issues, storage.BatchCreateOptions{})
	return err
}

func filterCreateIssuesMixedBucketDependencies(issues []*types.Issue, opts storage.BatchCreateOptions) ([]*types.Issue, error) {
	batchWispByID := make(map[string]bool, len(issues))
	hasRegular := false
	hasWisp := false
	for _, issue := range issues {
		if issue == nil {
			continue
		}
		isWisp := IsWisp(issue)
		if isWisp {
			hasWisp = true
		} else {
			hasRegular = true
		}
		if issue.ID != "" {
			batchWispByID[issue.ID] = isWisp
		}
	}
	if !hasRegular || !hasWisp {
		return issues, nil
	}

	var filteredIssues []*types.Issue
	for issueIndex, issue := range issues {
		if issue == nil {
			continue
		}
		var keptDeps []*types.Dependency
		filteredDeps := false
		for depIndex, dep := range issue.Dependencies {
			if dep == nil {
				if filteredDeps {
					keptDeps = append(keptDeps, dep)
				}
				continue
			}
			sourceID := issue.ID
			sourceIsWisp := IsWisp(issue)
			if dep.IssueID != "" {
				sourceID = dep.IssueID
				if isWisp, ok := batchWispByID[sourceID]; ok {
					sourceIsWisp = isWisp
				}
			}
			targetIsWisp, targetInBatch := batchWispByID[dep.DependsOnID]
			if targetInBatch && sourceIsWisp != targetIsWisp {
				if !opts.SkipDependencyValidationErrors {
					return nil, fmt.Errorf("mixed regular/wisp CreateIssues batch cannot include cross-bucket dependency %s -> %s; create the issues first, then add the in-batch dependency after both issues exist", sourceID, dep.DependsOnID)
				}
				if !filteredDeps {
					keptDeps = append([]*types.Dependency(nil), issue.Dependencies[:depIndex]...)
					filteredDeps = true
				}
				recordSkippedDependencyEdge(opts, sourceID, dep.DependsOnID, "cross-bucket dependency between regular issue and wisp in the same batch")
				continue
			}
			if filteredDeps {
				keptDeps = append(keptDeps, dep)
			}
		}
		if filteredDeps {
			if filteredIssues == nil {
				filteredIssues = append([]*types.Issue(nil), issues...)
			}
			issueCopy := *issue
			issueCopy.Dependencies = keptDeps
			filteredIssues[issueIndex] = &issueCopy
		}
	}
	if filteredIssues != nil {
		return filteredIssues, nil
	}
	return issues, nil
}

func createBlockedRecomputeIDs(issues []*types.Issue) ([]string, []string) {
	issueSeen := make(map[string]bool, len(issues))
	wispSeen := make(map[string]bool, len(issues))
	issueIDs := make([]string, 0, len(issues))
	wispIDs := make([]string, 0, len(issues))
	add := func(id string, isWisp bool) {
		if id == "" {
			return
		}
		if isWisp {
			if !wispSeen[id] {
				wispSeen[id] = true
				wispIDs = append(wispIDs, id)
			}
			return
		}
		if !issueSeen[id] {
			issueSeen[id] = true
			issueIDs = append(issueIDs, id)
		}
	}
	for _, issue := range issues {
		if issue == nil {
			continue
		}
		isWisp := IsWisp(issue)
		add(issue.ID, isWisp)
		for _, dep := range issue.Dependencies {
			if dep == nil {
				continue
			}
			src := dep.IssueID
			if src == "" {
				src = issue.ID
			}
			add(src, isWisp)
		}
	}
	return issueIDs, wispIDs
}

// issueLabel returns a human-readable identifier for an issue in error
// messages: its ID when assigned, otherwise its quoted title, so a validation
// failure before ID assignment doesn't produce a bare "issue :" fragment.
func issueLabel(issue *types.Issue) string {
	if issue.ID != "" {
		return issue.ID
	}
	if issue.Title != "" {
		return fmt.Sprintf("%q", issue.Title)
	}
	return "(unnamed)"
}

// PrepareIssueForInsert normalizes timestamps, validates, and computes the content hash.
func PrepareIssueForInsert(issue *types.Issue, customStatuses, customTypes []string) error {
	// Trim the title at the shared seam so every create stack matches
	// `bd create`. TrimSpace is applied only at the cmd RunE
	// (cmd/bd/create.go) and the batch legs (beads-fo5l1); the seam itself
	// never trimmed, so create paths that bypass the cmd layer — import
	// (importIssuesCore → CreateIssuesInTxWithResult) and the domain/proxied
	// create — stored a padded title verbatim, which is unsearchable by exact
	// match and breaks the markdown "### Title" round-trip (beads-cm94s, the
	// shared-write-path normalizer-parity class: dc0rt label / u4rks metadata /
	// 82pv3 timestamps / 81bfd created_by all landed at this same seam). This
	// runs before mint in the caller (CreateIssueInTxWithResult), so a padded
	// title hashes to the same ID as its trimmed form — matching single-create.
	// Empty-after-trim still trips the len==0 guard in ValidateWithCustom below,
	// exactly as create.go trims then guards empty.
	issue.Title = strings.TrimSpace(issue.Title)

	// Reject control characters in metadata before storing — a raw control byte
	// in the Dolt JSON column re-emits unreadable JSON on readback and bricks
	// every subsequent list/show/export repo-wide (beads-nc639). Unconditional,
	// independent of the schema-mode gate below.
	if err := storage.ValidateMetadataReadable(issue.Metadata); err != nil {
		return fmt.Errorf("metadata validation failed for issue %s: %w", issueLabel(issue), err)
	}
	if err := ValidateMetadataIfConfigured(issue.Metadata); err != nil {
		return fmt.Errorf("metadata validation failed for issue %s: %w", issueLabel(issue), err)
	}

	// Normalize timestamps to UTC, defaulting to now.
	//
	// beads-8ukct: the created_at/updated_at columns are DATETIME (second
	// precision), so the stored value is second-truncated. Truncate the
	// in-memory value to match, because cmd/bd/create.go emits this same
	// struct verbatim under --json without re-reading — without the truncate
	// the create-emit carries nanosecond time.Now() that no later read (show/
	// list, all second-precision) can reproduce (read-after-write mismatch).
	now := time.Now().UTC().Truncate(time.Second)
	if issue.CreatedAt.IsZero() {
		issue.CreatedAt = now
	} else {
		issue.CreatedAt = issue.CreatedAt.UTC().Truncate(time.Second)
	}
	if issue.UpdatedAt.IsZero() {
		issue.UpdatedAt = now
	} else {
		issue.UpdatedAt = issue.UpdatedAt.UTC().Truncate(time.Second)
	}
	// beads-17n4h: due_at/defer_until are DATETIME (second precision) too, but
	// PrepareIssueForInsert did not truncate them — a relative `--due +6h` /
	// `--defer +3h` parse (ParseRelativeTime -> now.Add(...)) preserves
	// time.Now()'s nanoseconds, and cmd/bd/create.go emits this struct verbatim
	// under --json without re-reading, so the create-emit carried a ns value no
	// later read (show/list, all second-precision) could reproduce (the same
	// read-after-write mismatch 8ukct fixed for created_at/updated_at). Truncate
	// at this shared insert point so the emit matches the persisted column.
	if issue.DueAt != nil {
		truncatedDue := issue.DueAt.UTC().Truncate(time.Second)
		issue.DueAt = &truncatedDue
	}
	if issue.DeferUntil != nil {
		truncatedDefer := issue.DeferUntil.UTC().Truncate(time.Second)
		issue.DeferUntil = &truncatedDefer
	}

	// Ensure closed issues have a closed_at timestamp.
	if issue.Status == types.StatusClosed && issue.ClosedAt == nil {
		maxTime := issue.CreatedAt
		if issue.UpdatedAt.After(maxTime) {
			maxTime = issue.UpdatedAt
		}
		closedAt := maxTime.Add(time.Second)
		issue.ClosedAt = &closedAt
	}

	if err := issue.ValidateWithCustom(customStatuses, customTypes); err != nil {
		return fmt.Errorf("validation failed for issue %s: %w", issueLabel(issue), err)
	}
	if issue.ContentHash == "" {
		issue.ContentHash = issue.ComputeContentHash()
	}
	return nil
}

// ValidateIssueIDPrefix validates that the issue ID matches the configured prefix
// or any of the allowed_prefixes.
func ValidateIssueIDPrefix(id, prefix, allowedPrefixes string) error {
	if strings.HasPrefix(id, prefix+"-") {
		return nil
	}
	if allowedPrefixes != "" {
		for _, allowed := range strings.Split(allowedPrefixes, ",") {
			allowed = strings.TrimSpace(allowed)
			if allowed != "" && strings.HasPrefix(id, allowed+"-") {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: issue ID %s does not match configured prefix %s", storage.ErrPrefixMismatch, id, prefix)
}

// ParseHierarchicalID checks if an ID is hierarchical (e.g., "bd-abc.1")
// and returns the parent ID and child number.
func ParseHierarchicalID(id string) (parentID string, childNum int, ok bool) {
	lastDot := strings.LastIndex(id, ".")
	if lastDot == -1 {
		return "", 0, false
	}
	parentID = id[:lastDot]
	var num int
	if _, err := fmt.Sscanf(id[lastDot+1:], "%d", &num); err != nil {
		return "", 0, false
	}
	return parentID, num, true
}

// AllWisps returns true if every issue in the slice should be routed to the
// wisps table (i.e., is ephemeral or no-history). Used to gate the fast path
// that skips Dolt versioning in batch creates.
func AllWisps(issues []*types.Issue) bool {
	for _, issue := range issues {
		if !issue.Ephemeral && !issue.NoHistory {
			return false
		}
	}
	return true
}

// CheckOrphan handles orphan detection for hierarchical IDs.
// Returns (skip=true, nil) if the issue should be skipped.
//
//nolint:gosec // G201: table is a hardcoded constant
func CheckOrphan(ctx context.Context, tx *sql.Tx, issue *types.Issue, issueTable string, handling storage.OrphanHandling) (skip bool, err error) {
	if issue.ID == "" {
		return false, nil
	}
	parentID, _, ok := ParseHierarchicalID(issue.ID)
	if !ok {
		return false, nil
	}

	var parentCount int
	if err := tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE id = ?`, issueTable), parentID).Scan(&parentCount); err != nil {
		return false, fmt.Errorf("failed to check parent existence: %w", err)
	}
	if parentCount > 0 {
		return false, nil
	}

	switch handling {
	case storage.OrphanStrict:
		return false, fmt.Errorf("parent issue %s does not exist (strict mode)", parentID)
	case storage.OrphanSkip:
		return true, nil
	default: // OrphanAllow, OrphanResurrect
		return false, nil
	}
}

// InsertIssueIfNew inserts the issue and returns whether it was genuinely new,
// and whether the RejectStaleUpserts guard rejected it.
//
// When opts.ConflictSkip is true and an issue with the same ID already exists,
// the row is left untouched (no UPSERT) and isNew is false. This is the
// auto-import upgrade-recovery guarantee (GH#3955): even if the emptiness
// guard in maybeAutoImportJSONL regresses, a stale issues.jsonl can never
// overwrite live rows — worst case is a no-op. Otherwise the INSERT … ON
// DUPLICATE KEY UPDATE runs, so explicit `bd import` keeps UPSERT semantics;
// with opts.RejectStaleUpserts the update half is conditional on the incoming
// row being strictly newer than the stored one (bd-pkim8, bd-hj85c).
// Staleness is decided by an explicit in-transaction read (stored updated_at
// strictly newer ⇒ rejected) so callers can skip aux persistence and count
// the row as skipped instead of created (bd-578h9.8). Equal-timestamp rows
// are deliberately NOT rejected here, even though the ODKU's
// VALUES(updated_at) > updated_at condition keeps every stored column for
// them: updated_at has second granularity, so a tie may be two distinct
// same-second updates — the local row must win the tie (an incoming row with
// an empty notes field must not wipe local notes), but its aux data
// (labels/comments/deps, which never bump updated_at) still merges
// additively (bd-hj85c).
//
//nolint:gosec // G201: table is a hardcoded constant
func InsertIssueIfNew(ctx context.Context, tx DBTX, issueTable string, issue *types.Issue, opts storage.BatchCreateOptions) (isNew bool, staleRejected bool, err error) {
	var existingCount int
	if issue.ID != "" {
		// issues and wisps are separate tables with no cross-table uniqueness,
		// so inserting into one an id that already lives in the other would mint
		// a same-id issue+wisp — the collision the rename (mgsx) and promote
		// (jym1) guards exist to prevent (beads-tnv9, xaxe family). Reject
		// fail-closed here so every create stack (direct, proxied, bulk-import,
		// explicit-id, child-mint) inherits the guard. Promote opts out
		// (SkipCrossTableIDCollisionCheck): it legitimately inserts into `issues`
		// an id whose wisp row is deleted later in the same tx, and carries its
		// own cross-table guard first.
		if !opts.SkipCrossTableIDCollisionCheck {
			otherTable, otherLabel := "wisps", "a wisp"
			if issueTable == "wisps" {
				otherTable, otherLabel = "issues", "an issue"
			}
			exists, existsErr := rowExistsInTable(ctx, tx, otherTable, issue.ID)
			if existsErr != nil {
				return false, false, existsErr
			}
			if exists {
				return false, false, fmt.Errorf("cannot create %s: id already exists as %s", issue.ID, otherLabel)
			}
		}
		if err := tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE id = ?`, issueTable), issue.ID).Scan(&existingCount); err != nil {
			return false, false, fmt.Errorf("failed to check issue existence for %s: %w", issue.ID, err)
		}
	}
	if opts.ConflictSkip && existingCount > 0 {
		return false, false, nil // issue already exists — skip, never overwrite
	}
	if opts.RejectStaleUpserts && existingCount > 0 {
		var storedNewer int
		if err := tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE id = ? AND updated_at > ?`, issueTable), issue.ID, issue.UpdatedAt).Scan(&storedNewer); err != nil {
			return false, false, fmt.Errorf("failed to check issue staleness for %s: %w", issue.ID, err)
		}
		if storedNewer > 0 {
			// The conditional ODKU would keep every stored column anyway;
			// skipping the no-op insert makes the rejection observable.
			return false, true, nil
		}
	}
	if err := insertIssueIntoTable(ctx, tx, issueTable, issue, opts.RejectStaleUpserts); err != nil {
		return false, false, fmt.Errorf("failed to insert issue %s: %w", issue.ID, err)
	}
	return existingCount == 0, false, nil
}

func PersistLabels(ctx context.Context, tx *sql.Tx, issue *types.Issue, actor, eventTable string) (CreateIssueResult, error) {
	var result CreateIssueResult
	if len(issue.Labels) == 0 {
		return result, nil
	}
	labelTable := "labels"
	if IsWisp(issue) {
		labelTable = "wisp_labels"
	}
	seen := make(map[string]struct{}, len(issue.Labels))
	for _, label := range issue.Labels {
		// Trim whitespace and skip empty labels so a create-time label matches
		// `bd label add` (which trims at the CLI) and the query/filter side
		// (utils.NormalizeLabels). Without this, `bd create -l '  x  '` stored
		// the label verbatim and it became permanently unmatchable — no query,
		// `bd label remove`, or filter normalizes to the padded stored value
		// (beads-4g2h). Dedup on the TRIMMED value so '  x  ' and 'x' collapse.
		label = strings.TrimSpace(label)
		if label == "" {
			continue
		}
		// Reject interior delimiter chars (beads-f3y1): the markdown "### Labels"
		// round-trip splits on ',' and newlines (parseLabels), so a create-time
		// label containing ','/'\n'/'\r' re-imports as MULTIPLE labels. This
		// mirrors the AddLabelInTx guard (beads-pqzx) on the create/import path,
		// which persists here via PersistLabels rather than AddLabel. Spaces stay
		// legal (beads-ehw7: parseLabels never splits on spaces).
		if strings.ContainsAny(label, ",\n\r") {
			return result, fmt.Errorf("label %q must not contain a comma or newline (these are reserved as label delimiters)", label)
		}
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		//nolint:gosec // G201: table is determined by ephemeral flag
		sqlResult, err := tx.ExecContext(ctx, fmt.Sprintf(`
			INSERT IGNORE INTO %s (issue_id, label)
			VALUES (?, ?)
		`, labelTable), issue.ID, label)
		if err != nil {
			return result, fmt.Errorf("failed to insert label %q for %s: %w", label, issue.ID, err)
		}
		rowsAffected, err := sqlResult.RowsAffected()
		if err != nil {
			return result, fmt.Errorf("failed to check label insert result for %q on %s: %w", label, issue.ID, err)
		}
		if rowsAffected == 0 {
			continue
		}
		result.markChanged(labelTable)
		comment := "Added label: " + label
		//nolint:gosec // G201: eventTable is determined by ephemeral flag
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
			INSERT INTO %s (id, issue_id, event_type, actor, comment)
			VALUES (?, ?, ?, ?, ?)
		`, eventTable), NewEventID(), issue.ID, types.EventLabelAdded, actor, comment); err != nil {
			return result, fmt.Errorf("failed to record label event %q for %s: %w", label, issue.ID, err)
		}
		result.markChanged(eventTable)
	}
	return result, nil
}

func PersistComments(ctx context.Context, tx *sql.Tx, issue *types.Issue) (CreateIssueResult, error) {
	var result CreateIssueResult
	if len(issue.Comments) == 0 {
		return result, nil
	}
	commentTable := "comments"
	if IsWisp(issue) {
		commentTable = "wisp_comments"
	}
	for _, comment := range issue.Comments {
		createdAt := comment.CreatedAt
		if createdAt.IsZero() {
			createdAt = time.Now().UTC()
		}
		// Check for existing identical comment to prevent duplicates on re-import.
		// The UUID PK means ON DUPLICATE KEY UPDATE would never fire,
		// so we do an explicit existence check instead.
		var exists int
		//nolint:gosec // G201: table is determined by ephemeral flag
		if err := tx.QueryRowContext(ctx, fmt.Sprintf(`
				SELECT COUNT(*) FROM %s
				WHERE issue_id = ? AND author = ? AND created_at = ? AND text = ?
			`, commentTable), issue.ID, comment.Author, createdAt, comment.Text).Scan(&exists); err != nil {
			return result, fmt.Errorf("failed to check comment existence for %s: %w", issue.ID, err)
		}
		if exists > 0 {
			continue
		}
		commentID := comment.ID
		if commentID == "" {
			commentID = uuid.Must(uuid.NewV7()).String()
			comment.ID = commentID
		}
		//nolint:gosec // G201: table is determined by ephemeral flag
		_, err := tx.ExecContext(ctx, fmt.Sprintf(`
			INSERT INTO %s (id, issue_id, author, text, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, commentTable), commentID, issue.ID, comment.Author, comment.Text, createdAt)
		if err != nil {
			return result, fmt.Errorf("failed to insert comment for %s: %w", issue.ID, err)
		}
		result.markChanged(commentTable)
	}
	return result, nil
}

func PersistDependencies(ctx context.Context, tx *sql.Tx, issues []*types.Issue, actor string) error {
	_, err := PersistDependenciesWithResult(ctx, tx, issues, actor)
	return err
}

func PersistDependenciesWithResult(ctx context.Context, tx *sql.Tx, issues []*types.Issue, actor string) (CreateIssueResult, error) {
	return PersistDependenciesWithOptionsResult(ctx, tx, issues, actor, storage.BatchCreateOptions{})
}

func PersistDependenciesWithOptionsResult(ctx context.Context, tx *sql.Tx, issues []*types.Issue, actor string, opts storage.BatchCreateOptions) (CreateIssueResult, error) {
	var result CreateIssueResult
	for _, issue := range issues {
		if len(issue.Dependencies) == 0 {
			continue
		}
		depTable := "dependencies"
		if IsWisp(issue) {
			depTable = "wisp_dependencies"
		}
		for _, dep := range issue.Dependencies {
			// Default IssueID to the owning issue when not pre-set (e.g.,
			// markdown bulk create where the ID is auto-generated).
			if dep.IssueID == "" {
				dep.IssueID = issue.ID
			}

			// A dependency edge with no target (e.g. hand-authored JSONL that
			// used the top-level "id" field instead of the dependency schema's
			// "depends_on_id") would otherwise hit the target lookup with an
			// empty id and be reported as the misleading "-> : target not
			// found". Name the real cause instead (beads-p96v).
			if dep.DependsOnID == "" {
				if opts.SkipDependencyValidationErrors {
					recordSkippedDependency(opts, dep, "missing 'depends_on_id' in dependency entry")
					continue
				}
				return result, fmt.Errorf("dependency for %s is missing 'depends_on_id'", dep.IssueID)
			}

			// Every interactive dep-creation path (dep add / link / create /
			// bulk dep-file) enforces DependencyType.IsValid() — non-empty and
			// <=32 chars. Import once skipped this, so an empty-type edge could
			// persist; worse, the cycle guard only fires for blocking types, so
			// an empty-type 2-cycle survived import that dep add rejects in
			// every direction (beads-3rk4). Validate here, mirroring the empty
			// depends_on_id skip above, so import stays consistent with the
			// interactive paths.
			if !dep.Type.IsValid() {
				if opts.SkipDependencyValidationErrors {
					recordSkippedDependency(opts, dep, fmt.Sprintf("invalid dependency type %q: must be non-empty and at most 32 characters", dep.Type))
					continue
				}
				return result, fmt.Errorf("dependency %s -> %s has invalid type %q: must be non-empty and at most 32 characters", dep.IssueID, dep.DependsOnID, dep.Type)
			}

			// The edge's metadata is persisted verbatim into a Dolt JSON column
			// (the INSERT below). Unlike issue.Metadata — which every create path
			// validates as well-formed JSON before persisting (storage.Validate
			// MetadataReadable / ValidateMetadataIfConfigured) — dep.Metadata was
			// never checked here, so a malformed-JSON edge (e.g. a truncated
			// `{"gate":"any-children"` from a hand-edited or corrupted JSONL) hit
			// the JSON column and returned Dolt Error 1105 ("Invalid JSON text").
			// That error is NOT a SkipDependencyValidationErrors skip — it aborts
			// the whole ExecContext, so the batch transaction rolls back and ZERO
			// issues import even though only one edge was bad (beads-u47yy). The
			// interactive path never hits this because dep add builds the gate
			// metadata itself; only import/bulk accepts caller-supplied metadata.
			// Validate well-formedness here, mirroring the IsValid + gate-value
			// skips, so one bad edge skips-with-reason instead of failing the run.
			if strings.TrimSpace(dep.Metadata) != "" && !json.Valid([]byte(dep.Metadata)) {
				if opts.SkipDependencyValidationErrors {
					recordSkippedDependency(opts, dep, "invalid dependency metadata: not well-formed JSON")
					continue
				}
				return result, fmt.Errorf("dependency %s -> %s has invalid metadata: not well-formed JSON", dep.IssueID, dep.DependsOnID)
			}

			// A cross-prefix target (source and target have different ID
			// prefixes) lives in another rig's database, so it can't be
			// validated against — or found in — this DB's issues/wisps tables.
			// Every interactive path (dep add / link) derives cross-prefix via
			// ExtractPrefix and treats such a target as external, skipping the
			// local-existence check (dolt/transaction.go, embeddeddolt/*). Import
			// once hardcoded false here, so a cross-prefix edge that dep add
			// accepts and export emits was validated locally, missed, and got
			// silently dropped as "target not found" — a lossy export->import
			// round-trip (beads-77i6). Derive cross-prefix the same way so the
			// edge is preserved.
			isCrossPrefix := types.ExtractPrefix(dep.IssueID) != types.ExtractPrefix(dep.DependsOnID)
			kind := ClassifyDepTarget(ctx, tx, dep, isCrossPrefix)

			if kind != DepTargetExternal {
				lookupTable := "issues"
				if kind == DepTargetWisp {
					lookupTable = "wisps"
				}
				var exists int
				//nolint:gosec // G201: lookupTable is one of two hardcoded constants
				if err := tx.QueryRowContext(ctx,
					fmt.Sprintf("SELECT 1 FROM %s WHERE id = ?", lookupTable),
					dep.DependsOnID).Scan(&exists); err != nil {
					if err == sql.ErrNoRows {
						recordSkippedDependency(opts, dep, "target not found")
						continue
					}
					return result, fmt.Errorf("failed to check dependency target %s for %s: %w", dep.DependsOnID, dep.IssueID, err)
				}
			}

			if err := CheckDependencyCycleInTx(ctx, tx, dep, nil); err != nil {
				if opts.SkipDependencyValidationErrors {
					recordSkippedDependency(opts, dep, err.Error())
					continue
				}
				return result, fmt.Errorf("invalid dependency %s -> %s: %w", dep.IssueID, dep.DependsOnID, err)
			}

			createdAt := dep.CreatedAt
			if createdAt.IsZero() {
				createdAt = time.Now().UTC()
			}
			// Deterministic id from (issue_id, target) keeps bulk-imported edges
			// merge-safe across clones — two clones importing the same JSONL get the
			// same primary key, not two random UUIDs that collide on uk_dep_* (#4259).
			createdBy := dependencyCreatedBy(dep, actor)
			// beads-gnopw: persist the edge's metadata and thread_id, mirroring
			// the interactive AddDependencyInTx path (dependencies.go:226-228).
			// The batch INSERT historically listed neither column, so every
			// import / batch-create silently dropped edge metadata (waits-for
			// fanout-gate config, similarity/approval/attestation payloads) and
			// thread_id — an export->import round-trip lost them, and an
			// any-children waits-for gate re-imported as {} flips to all-children
			// on read (ParseWaitsForGateMetadata, types.go). Default empty
			// metadata to "{}" exactly as the interactive path does
			// (dependencies.go:143-145). The ON DUPLICATE KEY UPDATE stays
			// `type = type`: depid.New keys on the flattened (issue_id, target),
			// so a re-import that no-ops here may be a same-kind idempotent edge
			// OR a cross-kind PK collision (beads-xaxe) — refreshing metadata via
			// VALUES() would clobber the colliding edge's payload and defeat the
			// rowsAffected==0 collision probe below, so import stays additive-only
			// per the merge-safe-clone contract (#4259).
			//
			// beads-8292k (RULED (A) additive-only): import is additive-only BY
			// DESIGN — re-importing an edge that already exists at this PK never
			// refreshes its stored metadata/thread_id (first-write-wins). The new
			// VALUES here are bound but discarded by the `type = type` no-op on PK
			// conflict; the stored payload is authoritative. This deliberately
			// DIVERGES from the two interactive write paths (issueops
			// AddDependencyInTx dependencies.go:206-212 and domain/db
			// DependencySQLRepository.Insert dependency.go:158-168), which blind-
			// refresh metadata on a same-type re-add. Import is idempotent-additive
			// on purpose so a merge-safe clone round-trip (#4259) is deterministic
			// and cannot silently mutate an existing edge, and so the cross-kind PK
			// collision (beads-xaxe) stays detectable via the rowsAffected==0 probe
			// below. Do NOT change this to a VALUES()-based refresh without a
			// same-kind guard + the hj85c newer-wins guard; see beads-8292k for the
			// (B) metadata-refreshing alternative if the merge contract ever
			// requires import to propagate metadata EDITS. Locked by
			// TestPersistDependenciesReimportDoesNotRefreshMetadata.
			metadata := dep.Metadata
			if metadata == "" {
				metadata = "{}"
			}
			//nolint:gosec // G201: depTable is one of two hardcoded constants; target column from DepTargetKind.Column()
			sqlResult, err := tx.ExecContext(ctx, fmt.Sprintf(`
					INSERT INTO %s (id, issue_id, %s, type, created_by, created_at, metadata, thread_id)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?)
					ON DUPLICATE KEY UPDATE type = type
				`, depTable, kind.Column()), depid.New(dep.IssueID, dep.DependsOnID), dep.IssueID, dep.DependsOnID, dep.Type, createdBy, createdAt, metadata, dep.ThreadID)
			if err != nil {
				return result, fmt.Errorf("failed to insert dependency %s -> %s: %w", dep.IssueID, dep.DependsOnID, err)
			}
			rowsAffected, err := sqlResult.RowsAffected()
			if err != nil {
				return result, fmt.Errorf("failed to check dependency insert result for %s -> %s: %w", dep.IssueID, dep.DependsOnID, err)
			}
			if rowsAffected > 0 {
				result.markChanged(depTable)
			} else {
				// rowsAffected==0 means the ON DUPLICATE KEY UPDATE type=type
				// no-op'd: a row already exists at this PK. depid.New keys on the
				// FLATTENED (issue_id, target-string) with no target-kind marker,
				// so an issue-target and a wisp-target that share the same id
				// string derive the SAME PK (beads-xaxe, root of uekw/jym1). If the
				// existing row holds the target in a DIFFERENT typed column than
				// this edge's kind, the two are genuinely distinct edges colliding
				// on one PK — silently keeping only the first would drop this one.
				// Surface it as a skipped dependency instead of a silent collapse.
				//nolint:gosec // G201: depTable + kind.Column() are hardcoded/enumerated, no user input.
				var existsSameKind int
				probeErr := tx.QueryRowContext(ctx, fmt.Sprintf(
					"SELECT 1 FROM %s WHERE id = ? AND %s = ?", depTable, kind.Column()),
					depid.New(dep.IssueID, dep.DependsOnID), dep.DependsOnID).Scan(&existsSameKind)
				switch {
				case probeErr == sql.ErrNoRows:
					// PK exists but not in this kind's column → cross-kind collision.
					recordSkippedDependency(opts, dep,
						"dependency-id collision: an edge with a different target kind (issue vs wisp vs external) already occupies this deterministic id")
				case probeErr != nil:
					return result, fmt.Errorf("failed to probe dependency collision for %s -> %s: %w", dep.IssueID, dep.DependsOnID, probeErr)
				}
				// probeErr==nil: same-kind idempotent re-import (the edge already
				// exists with the same target column) — a legitimate no-op, not a
				// collision; leave it unmarked.
			}
		}
	}
	return result, nil
}

// createdByOrActor returns the author stamped on an issue's created_by column.
// Import/restore paths populate issue.CreatedBy from JSONL; interactive/batch
// creation leaves it empty and falls back to the current actor (beads-81bfd).
// Mirrors dependencyCreatedBy: empty-only, so an import-supplied CreatedBy is
// preserved rather than overwritten by the importing actor.
func createdByOrActor(issue *types.Issue, actor string) string {
	if issue != nil && issue.CreatedBy != "" {
		return issue.CreatedBy
	}
	return actor
}

// dependencyCreatedBy returns the author stamped on a dependency edge.
// Import/restore paths populate dep.CreatedBy from JSONL; interactive
// creation leaves it empty and falls back to the current actor.
func dependencyCreatedBy(dep *types.Dependency, actor string) string {
	if dep != nil && dep.CreatedBy != "" {
		return dep.CreatedBy
	}
	return actor
}

func recordSkippedDependency(opts storage.BatchCreateOptions, dep *types.Dependency, reason string) {
	if dep == nil {
		return
	}
	recordSkippedDependencyEdge(opts, dep.IssueID, dep.DependsOnID, reason)
}

func recordSkippedDependencyEdge(opts storage.BatchCreateOptions, issueID, dependsOnID, reason string) {
	if opts.OnSkippedDependency == nil {
		return
	}
	opts.OnSkippedDependency(issueID, dependsOnID, reason)
}

func ReconcileChildCounters(ctx context.Context, tx *sql.Tx, issues []*types.Issue) (map[string]bool, error) {
	type bucket struct {
		maxChild int
		isWisp   bool
		known    bool
	}
	parents := make(map[string]*bucket)
	var changed map[string]bool

	for _, issue := range issues {
		if issue == nil {
			continue
		}
		if IsWisp(issue) {
			if b, ok := parents[issue.ID]; ok {
				b.isWisp, b.known = true, true
			} else {
				parents[issue.ID] = &bucket{isWisp: true, known: true}
			}
		}
	}

	for _, issue := range issues {
		if issue == nil {
			continue
		}
		parentID, childNum, ok := ParseHierarchicalID(issue.ID)
		if !ok {
			continue
		}
		b, exists := parents[parentID]
		if !exists {
			b = &bucket{}
			parents[parentID] = b
		}
		if childNum > b.maxChild {
			b.maxChild = childNum
		}
	}

	for parentID, b := range parents {
		if b.maxChild == 0 {
			continue
		}
		if !b.known {
			b.isWisp = IsActiveWispInTx(ctx, tx, parentID)
		}
		table := "child_counters"
		if b.isWisp {
			table = "wisp_child_counters"
		}
		var current int
		//nolint:gosec // G201: table is one of two hardcoded constants.
		err := tx.QueryRowContext(ctx, fmt.Sprintf(`
			SELECT last_child FROM %s WHERE parent_id = ?
		`, table), parentID).Scan(&current)
		if err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf("failed to read child counter for %s: %w", parentID, err)
		}
		if err == nil && current >= b.maxChild {
			continue
		}
		//nolint:gosec // G201: table is one of two hardcoded constants.
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
			INSERT INTO %s (parent_id, last_child) VALUES (?, ?)
			ON DUPLICATE KEY UPDATE last_child = GREATEST(last_child, ?)
		`, table), parentID, b.maxChild, b.maxChild); err != nil {
			return nil, fmt.Errorf("failed to reconcile child counter for %s: %w", parentID, err)
		}
		if changed == nil {
			changed = map[string]bool{}
		}
		changed[table] = true
	}
	return changed, nil
}
