package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/dberrors"
	"github.com/steveyegge/beads/internal/storage/depid"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/storage/sqlbuild"
	"github.com/steveyegge/beads/internal/types"
)

func NewDependencySQLRepository(runner Runner) domain.DependencySQLRepository {
	return &dependencySQLRepositoryImpl{runner: runner}
}

type dependencySQLRepositoryImpl struct {
	runner Runner
}

var _ domain.DependencySQLRepository = (*dependencySQLRepositoryImpl)(nil)

const depTargetExpr = sqlbuild.DepTargetExpr

const depSelectColumns = "issue_id, " + depTargetExpr + " AS depends_on_id, type, created_at, created_by, metadata, thread_id"

func pickDepTable(useWisps bool) string {
	if useWisps {
		return "wisp_dependencies"
	}
	return "dependencies"
}

func (r *dependencySQLRepositoryImpl) pickDepTargetColumn(ctx context.Context, dependsOnID string) (string, error) {
	if strings.HasPrefix(dependsOnID, "external:") {
		return "depends_on_external", nil
	}
	var probe int
	err := r.runner.QueryRowContext(ctx, "SELECT 1 FROM wisps WHERE id = ? LIMIT 1", dependsOnID).Scan(&probe)
	switch {
	case err == nil:
		return "depends_on_wisp_id", nil
	case errors.Is(err, sql.ErrNoRows):
		return "depends_on_issue_id", nil
	case dberrors.IsTableNotExist(err):
		return "depends_on_issue_id", nil
	default:
		return "", fmt.Errorf("classify dep target %s: %w", dependsOnID, err)
	}
}

// validateDepGraphInvariants enforces the source/target existence and
// cross-type blocking (GH#1495) invariants that the direct path
// (issueops.AddDependencyInTx) applies, so proxied-server dep-add cannot land
// an edge the direct/embedded path would reject (beads-kzmq). External and
// wisp targets follow the same handling as the direct path: existence is
// checked for issue-typed endpoints; external refs are skipped.
func (r *dependencySQLRepositoryImpl) validateDepGraphInvariants(ctx context.Context, dep *types.Dependency, opts domain.DepInsertOpts) error {
	// The issue/wisp entity table is "wisps" for wisp sources, "issues" otherwise.
	sourceEntity := "issues"
	if opts.UseWispsTable {
		sourceEntity = "wisps"
	}

	var sourceType string
	//nolint:gosec // G201: sourceEntity is one of two hardcoded constants
	err := r.runner.QueryRowContext(ctx,
		fmt.Sprintf("SELECT issue_type FROM %s WHERE id = ?", sourceEntity),
		dep.IssueID,
	).Scan(&sourceType)
	switch {
	case err == nil:
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("db: DependencySQLRepository.Insert: issue %s not found", dep.IssueID)
	default:
		return fmt.Errorf("db: DependencySQLRepository.Insert: check source existence: %w", err)
	}

	// Target existence + type: external refs have no local row (skip, like the
	// direct path). Otherwise the target lives in issues or wisps.
	var targetType string
	if !strings.HasPrefix(dep.DependsOnID, "external:") {
		targetEntity := "issues"
		var probe int
		wispErr := r.runner.QueryRowContext(ctx, "SELECT 1 FROM wisps WHERE id = ? LIMIT 1", dep.DependsOnID).Scan(&probe)
		switch {
		case wispErr == nil:
			targetEntity = "wisps"
		case errors.Is(wispErr, sql.ErrNoRows), dberrors.IsTableNotExist(wispErr):
			targetEntity = "issues"
		default:
			return fmt.Errorf("db: DependencySQLRepository.Insert: classify target %s: %w", dep.DependsOnID, wispErr)
		}
		//nolint:gosec // G201: targetEntity is one of two hardcoded constants
		tErr := r.runner.QueryRowContext(ctx,
			fmt.Sprintf("SELECT issue_type FROM %s WHERE id = ?", targetEntity),
			dep.DependsOnID,
		).Scan(&targetType)
		switch {
		case tErr == nil:
		case errors.Is(tErr, sql.ErrNoRows):
			return fmt.Errorf("db: DependencySQLRepository.Insert: issue %s not found", dep.DependsOnID)
		default:
			return fmt.Errorf("db: DependencySQLRepository.Insert: check target existence: %w", tErr)
		}
	}

	// Cross-type blocking (GH#1495): a blocks edge with exactly one epic
	// endpoint is rejected — tasks block tasks, epics block epics.
	if dep.Type == types.DepBlocks && targetType != "" {
		sourceIsEpic := sourceType == string(types.TypeEpic)
		targetIsEpic := targetType == string(types.TypeEpic)
		if sourceIsEpic != targetIsEpic {
			if sourceIsEpic {
				return fmt.Errorf("db: DependencySQLRepository.Insert: epics can only block other epics, not tasks")
			}
			return fmt.Errorf("db: DependencySQLRepository.Insert: tasks can only block other tasks, not epics")
		}
	}
	return nil
}

func (r *dependencySQLRepositoryImpl) Insert(ctx context.Context, dep *types.Dependency, actor string, opts domain.DepInsertOpts) error {
	if dep == nil {
		return errors.New("db: DependencySQLRepository.Insert: dep must not be nil")
	}
	if dep.IssueID == "" {
		return errors.New("db: DependencySQLRepository.Insert: IssueID must not be empty")
	}
	if dep.DependsOnID == "" {
		return errors.New("db: DependencySQLRepository.Insert: DependsOnID must not be empty")
	}
	if dep.IssueID == dep.DependsOnID {
		return fmt.Errorf("db: DependencySQLRepository.Insert: %s cannot depend on itself", dep.IssueID)
	}

	metadata := dep.Metadata
	if metadata == "" {
		metadata = "{}"
	}

	table := pickDepTable(opts.UseWispsTable)

	// Resolve the target kind up front so the existence pre-check can query the
	// SPECIFIC typed target column. depid.New keys the edge id on the flattened
	// (issue_id, target-string) with no target-kind marker, so an issue-target
	// and a wisp-target sharing the same id string derive the SAME primary key
	// (beads-xaxe). A COALESCE-flattened pre-check would mistake such a
	// genuinely-distinct cross-kind edge for a duplicate — silently refreshing
	// the wrong row's metadata (same type) or reporting a misleading conflict
	// (different type). Discriminating by targetCol lets a cross-kind edge fall
	// through to the INSERT, where the PK collision is detected and surfaced.
	targetCol, err := r.pickDepTargetColumn(ctx, dep.DependsOnID)
	if err != nil {
		return fmt.Errorf("db: DependencySQLRepository.Insert: %w", err)
	}

	var existingType string
	err = r.runner.QueryRowContext(ctx,
		//nolint:gosec // G201: table and targetCol are hardcoded/enumerated constants
		fmt.Sprintf("SELECT type FROM %s WHERE issue_id = ? AND %s = ?", table, targetCol),
		dep.IssueID, dep.DependsOnID,
	).Scan(&existingType)
	switch {
	case err == nil:
		if existingType == string(dep.Type) {
			//nolint:gosec // G201: table and targetCol are hardcoded/enumerated constants
			if _, err := r.runner.ExecContext(ctx,
				fmt.Sprintf("UPDATE %s SET metadata = ? WHERE issue_id = ? AND %s = ?", table, targetCol),
				metadata, dep.IssueID, dep.DependsOnID,
			); err != nil {
				return fmt.Errorf("db: DependencySQLRepository.Insert: refresh metadata: %w", err)
			}
			return nil
		}
		return fmt.Errorf("db: DependencySQLRepository.Insert: %s -> %s already exists with type %q (requested %q)",
			dep.IssueID, dep.DependsOnID, existingType, dep.Type)
	case errors.Is(err, sql.ErrNoRows):
	default:
		return fmt.Errorf("db: DependencySQLRepository.Insert: check existing: %w", err)
	}

	// Graph-integrity validation shared with the direct path
	// (issueops.AddDependencyInTx): source/target existence + cross-type
	// blocking (GH#1495). Without this, proxied-server dep-add accepted edges
	// the direct/embedded path rejects — a task-blocks-epic edge, or an edge to
	// a non-existent source/target (beads-kzmq).
	if err := r.validateDepGraphInvariants(ctx, dep, opts); err != nil {
		return err
	}

	// Deterministic id keyed on (issue_id, target), the same derivation as the
	// embedded/issueops path, so server-mode (use-case) dependency creation stays
	// merge-safe across clones and works once the DEFAULT (UUID()) is dropped (#4259).
	// ON DUPLICATE KEY UPDATE type=type makes a colliding PK a detectable no-op
	// (rowsAffected==0) rather than a hard duplicate-key error — see the
	// cross-kind collision guard below.
	//nolint:gosec // G201: table is one of two hardcoded constants; targetCol is from pickDepTargetColumn
	sqlResult, err := r.runner.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (id, issue_id, %s, type, created_at, created_by, metadata, thread_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE type = type
	`, table, targetCol),
		depid.New(dep.IssueID, dep.DependsOnID), dep.IssueID, dep.DependsOnID, string(dep.Type),
		time.Now().UTC(), actor, metadata, dep.ThreadID,
	)
	if err != nil {
		return fmt.Errorf("db: DependencySQLRepository.Insert: %w", err)
	}
	rowsAffected, err := sqlResult.RowsAffected()
	if err != nil {
		return fmt.Errorf("db: DependencySQLRepository.Insert: check insert result: %w", err)
	}
	if rowsAffected == 0 {
		// The ON DUPLICATE KEY UPDATE type=type no-op'd: a row already occupies
		// this deterministic PK. The kind-discriminated pre-check above ruled out
		// a same-kind duplicate, so depid.New's flattened derivation collided with
		// an edge whose target lives in a DIFFERENT typed column (beads-xaxe).
		// Surface it rather than silently dropping this edge.
		var existsSameKind int
		//nolint:gosec // G201: table + targetCol are hardcoded/enumerated, no user input.
		probeErr := r.runner.QueryRowContext(ctx, fmt.Sprintf(
			"SELECT 1 FROM %s WHERE id = ? AND %s = ?", table, targetCol),
			depid.New(dep.IssueID, dep.DependsOnID), dep.DependsOnID).Scan(&existsSameKind)
		switch {
		case errors.Is(probeErr, sql.ErrNoRows):
			return fmt.Errorf("db: DependencySQLRepository.Insert: cannot add dependency %s -> %s: its deterministic id collides with an existing edge that has a different target kind (issue vs wisp vs external); the id derivation does not distinguish target kinds (beads-xaxe)",
				dep.IssueID, dep.DependsOnID)
		case probeErr != nil:
			return fmt.Errorf("db: DependencySQLRepository.Insert: probe collision for %s -> %s: %w", dep.IssueID, dep.DependsOnID, probeErr)
		}
		// probeErr==nil: a same-kind row appeared between the pre-check and the
		// INSERT (concurrent add) — a benign idempotent no-op.
		return nil
	}

	// Record an audit event so dependency-graph mutations are visible in
	// `bd history` (beads-1qt9). The proxied domain/db path previously recorded
	// NO dependency event at all, while the direct path (issueops.AddDependencyInTx)
	// does — a direct/proxied audit-parity gap (beads-c5efw). Mirror the direct
	// INSERT exactly (comment column, routed to wisp_events when the source is a
	// wisp) so the two legs produce identical history lines. Recorded only on a
	// real fresh add — the same-type metadata-refresh no-op above returns early
	// without an event, matching the direct path.
	if err := r.recordDepEvent(ctx, opts.UseWispsTable, dep.IssueID, actor, types.EventDependencyAdded,
		fmt.Sprintf("Added dependency: %s -> %s (%s)", dep.IssueID, dep.DependsOnID, dep.Type)); err != nil {
		return fmt.Errorf("db: DependencySQLRepository.Insert: record event: %w", err)
	}

	// is_blocked maintenance mirrors the classic AddDependencyInTx flow
	// (issueops/dependencies.go): the affected set expands the source by its
	// parent-child descendants (plus, for parent-child edges, waiters on the
	// target spawner), then a Mark pass propagates blocked state — or, for
	// parent-child adds (not monotonic: an already-closed child can satisfy an
	// any-children waits-for gate), a full mark/unmark Recompute. Skipping the
	// expansion left descendants stale when a blocking edge landed on their
	// ancestor (bd-6dnrw.44 item 3).
	srcIsWisp := opts.UseWispsTable
	var affectedIssues, affectedWisps []string
	var aerr error
	if srcIsWisp {
		affectedIssues, affectedWisps, aerr = issueops.AffectedByDepChangeForWispInTx(ctx, r.runner, dep.IssueID, dep.DependsOnID, dep.Type)
	} else {
		affectedIssues, affectedWisps, aerr = issueops.AffectedByDepChangeInTx(ctx, r.runner, dep.IssueID, dep.DependsOnID, dep.Type)
	}
	if aerr != nil {
		return fmt.Errorf("db: DependencySQLRepository.Insert: affected set: %w", aerr)
	}
	if dep.Type == types.DepBlocks || dep.Type == types.DepConditionalBlocks {
		if err := r.markDirectBlockedSource(ctx, dep.IssueID, srcIsWisp, dep.DependsOnID, targetCol); err != nil {
			return fmt.Errorf("db: DependencySQLRepository.Insert: mark is_blocked: %w", err)
		}
		affectedIssues, affectedWisps = issueops.RemoveSourceFromAffected(dep.IssueID, srcIsWisp, affectedIssues, affectedWisps)
	}
	if dep.Type == types.DepParentChild {
		if err := issueops.RecomputeIsBlockedInTx(ctx, r.runner, affectedIssues, affectedWisps); err != nil {
			return fmt.Errorf("db: DependencySQLRepository.Insert: recompute is_blocked: %w", err)
		}
		return nil
	}
	if err := issueops.MarkIsBlockedInTx(ctx, r.runner, affectedIssues, affectedWisps); err != nil {
		return fmt.Errorf("db: DependencySQLRepository.Insert: mark is_blocked (affected): %w", err)
	}
	return nil
}

// markDirectBlockedSource mirrors issueops.markDirectBlockingDependencySourceInTx:
// is_blocked is derived state, and ready-work queries filter on it directly
// (is_blocked = 0), so a blocking edge insert must set it on the source row
// while the target is still open. updated_at is pinned because recomputing
// derived state is not an edit.
func (r *dependencySQLRepositoryImpl) markDirectBlockedSource(ctx context.Context, source string, srcIsWisp bool, target, targetCol string) error {
	sourceTable := "issues"
	if srcIsWisp {
		sourceTable = "wisps"
	}
	var targetTable string
	switch targetCol {
	case "depends_on_issue_id":
		targetTable = "issues"
	case "depends_on_wisp_id":
		targetTable = "wisps"
	default:
		// External targets carry no local status to derive from.
		return nil
	}

	//nolint:gosec // G201: sourceTable/targetTable are hardcoded constants
	_, err := r.runner.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s s SET s.is_blocked = 1, s.updated_at = s.updated_at
		WHERE s.id = ?
		  AND s.is_blocked = 0
		  AND s.status <> 'closed' AND s.status <> 'pinned'
		  AND EXISTS (
		    SELECT 1 FROM %s t
		    WHERE t.id = ?
		      AND t.status <> 'closed' AND t.status <> 'pinned'
		  )
	`, sourceTable, targetTable), source, target)
	return err
}

func (r *dependencySQLRepositoryImpl) Delete(ctx context.Context, issueID, dependsOnID, actor string, opts domain.DepInsertOpts) (domain.DepDeleteResult, error) {
	if issueID == "" || dependsOnID == "" {
		return domain.DepDeleteResult{}, errors.New("db: DependencySQLRepository.Delete: issueID and dependsOnID must not be empty")
	}
	table := pickDepTable(opts.UseWispsTable)

	var depType string
	//nolint:gosec // G201: table and depTargetExpr are hardcoded constants
	err := r.runner.QueryRowContext(ctx,
		fmt.Sprintf("SELECT type FROM %s WHERE issue_id = ? AND %s = ?", table, depTargetExpr),
		issueID, dependsOnID,
	).Scan(&depType)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return domain.DepDeleteResult{Found: false}, nil
	case err != nil:
		return domain.DepDeleteResult{}, fmt.Errorf("db: DependencySQLRepository.Delete: lookup type %s -> %s: %w", issueID, dependsOnID, err)
	}

	//nolint:gosec // G201: table and depTargetExpr are hardcoded constants
	if _, err := r.runner.ExecContext(ctx,
		fmt.Sprintf("DELETE FROM %s WHERE issue_id = ? AND %s = ?", table, depTargetExpr),
		issueID, dependsOnID,
	); err != nil {
		return domain.DepDeleteResult{}, fmt.Errorf("db: DependencySQLRepository.Delete: %s -> %s: %w", issueID, dependsOnID, err)
	}

	// Record an audit event so dependency-graph mutations are visible in
	// `bd history` (beads-1qt9), mirroring the direct path (removeDependencyInTx)
	// which the proxied twin previously omitted (beads-c5efw). Only reached after
	// a matched row (the ErrNoRows lookup above returns Found:false early), so a
	// no-op remove records nothing — consistent with the direct path and the
	// 5vpoh no-op-event guard.
	dt := types.DependencyType(depType)
	if err := r.recordDepEvent(ctx, opts.UseWispsTable, issueID, actor, types.EventDependencyRemoved,
		fmt.Sprintf("Removed dependency: %s -> %s (%s)", issueID, dependsOnID, dt)); err != nil {
		return domain.DepDeleteResult{}, fmt.Errorf("db: DependencySQLRepository.Delete: record event: %w", err)
	}

	var affectedIssues, affectedWisps []string
	var aerr error
	if opts.UseWispsTable {
		affectedIssues, affectedWisps, aerr = issueops.AffectedByDepChangeForWispInTx(ctx, r.runner, issueID, dependsOnID, dt)
	} else {
		affectedIssues, affectedWisps, aerr = issueops.AffectedByDepChangeInTx(ctx, r.runner, issueID, dependsOnID, dt)
	}
	if aerr != nil {
		return domain.DepDeleteResult{}, fmt.Errorf("db: DependencySQLRepository.Delete: affected set: %w", aerr)
	}
	if err := issueops.RecomputeIsBlockedInTx(ctx, r.runner, affectedIssues, affectedWisps); err != nil {
		return domain.DepDeleteResult{}, fmt.Errorf("db: DependencySQLRepository.Delete: recompute is_blocked: %w", err)
	}

	return domain.DepDeleteResult{Found: true, Type: dt, DependsOnID: dependsOnID}, nil
}

// recordDepEvent writes a dependency_added / dependency_removed audit event so
// dependency-graph mutations show up in `bd history` (beads-1qt9). It mirrors
// the direct path (issueops.AddDependencyInTx / removeDependencyInTx) exactly:
// the human-readable line lives in the `comment` column (not old_value/new_value,
// which is why the generic events repo helper is not used here), and the event
// routes to wisp_events when the source is a wisp. Keeping this a single helper
// stops the added/removed legs from drifting apart (the hand-mirror hazard the
// cjvxq/5rn1c notes call out). beads-c5efw.
func (r *dependencySQLRepositoryImpl) recordDepEvent(ctx context.Context, srcIsWisp bool, issueID, actor string, eventType types.EventType, comment string) error {
	eventTable := "events"
	if srcIsWisp {
		eventTable = "wisp_events"
	}
	//nolint:gosec // G201: eventTable is one of two hardcoded constants
	if _, err := r.runner.ExecContext(ctx,
		fmt.Sprintf(`INSERT INTO %s (id, issue_id, event_type, actor, comment) VALUES (?, ?, ?, ?, ?)`, eventTable),
		issueops.NewEventID(), issueID, string(eventType), actor, comment,
	); err != nil {
		return err
	}
	return nil
}

func (r *dependencySQLRepositoryImpl) HasCycle(ctx context.Context, issueID, dependsOnID string) (bool, error) {
	if issueID == "" || dependsOnID == "" {
		return false, errors.New("db: DependencySQLRepository.HasCycle: issueID and dependsOnID must not be empty")
	}

	var one int
	err := r.runner.QueryRowContext(ctx, `
		SELECT 1 FROM dependencies
		WHERE issue_id = ? AND depends_on_issue_id = ?
		  AND type IN ('blocks', 'conditional-blocks')
		LIMIT 1
	`, dependsOnID, issueID).Scan(&one)
	switch {
	case err == nil:
		return true, nil
	case !errors.Is(err, sql.ErrNoRows):
		return false, fmt.Errorf("db: DependencySQLRepository.HasCycle: direct probe (dependencies): %w", err)
	}
	err = r.runner.QueryRowContext(ctx, `
		SELECT 1 FROM wisp_dependencies
		WHERE issue_id = ? AND depends_on_issue_id = ?
		  AND type IN ('blocks', 'conditional-blocks')
		LIMIT 1
	`, dependsOnID, issueID).Scan(&one)
	switch {
	case err == nil:
		return true, nil
	case !errors.Is(err, sql.ErrNoRows):
		return false, fmt.Errorf("db: DependencySQLRepository.HasCycle: direct probe (wisp_dependencies): %w", err)
	}

	var count int
	err = r.runner.QueryRowContext(ctx, `
		WITH RECURSIVE reachable(node) AS (
			SELECT ?
			UNION
			SELECT d.depends_on_issue_id FROM (
				SELECT issue_id, depends_on_issue_id, type FROM dependencies
				UNION ALL
				SELECT issue_id, depends_on_issue_id, type FROM wisp_dependencies
			) d
			JOIN reachable r ON d.issue_id = r.node
			WHERE d.type IN ('blocks', 'conditional-blocks')
			  AND d.depends_on_issue_id IS NOT NULL
		)
		SELECT COUNT(*) FROM reachable WHERE node = ?
	`, dependsOnID, issueID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("db: DependencySQLRepository.HasCycle: %w", err)
	}
	return count > 0, nil
}

// CheckCycleForType rejects a new edge whose insertion would close a cycle
// within the edge's own type FAMILY, matching the direct/embedded path
// (issueops.CheckDependencyCycleInTx + cycleCheckTypesFor, beads-8qij). Unlike
// HasCycle — which only ever walks the blocking family — this walks the
// parent-child graph for a parent-child edge, so the proxied dep use-case no
// longer accepts a parent-child cycle the direct path rejects (beads-7a6n).
// Types outside a checked family (waits-for/related) return nil (no walk),
// preserving each caller's existing accept-what-you-added behavior. The check
// runs over the current graph, so for reparent the caller must remove the old
// parent edge(s) first so the walk sees the post-delete graph.
func (r *dependencySQLRepositoryImpl) CheckCycleForType(ctx context.Context, dep *types.Dependency) error {
	return issueops.CheckDependencyCycleInTx(ctx, r.runner, dep, nil)
}

func (r *dependencySQLRepositoryImpl) ListByIssueIDs(ctx context.Context, issueIDs []string, opts domain.DepListOpts) (domain.DepBulkResult, error) {
	result := domain.DepBulkResult{
		Outgoing: make(map[string][]*types.Dependency),
		Incoming: make(map[string][]*types.Dependency),
	}
	if len(issueIDs) == 0 {
		return result, nil
	}

	idPlaceholders, idArgs := buildInPlaceholders(issueIDs)
	typeWhere, typeArgs := buildTypeFilter(opts.Types)
	table := pickDepTable(opts.UseWispsTable)

	if opts.Direction == domain.DepDirectionBoth || opts.Direction == domain.DepDirectionOut {
		//nolint:gosec // G201: table and depSelectColumns are hardcoded
		q := fmt.Sprintf(
			`SELECT %s FROM %s WHERE issue_id IN (%s)%s ORDER BY issue_id`,
			depSelectColumns, table, idPlaceholders, typeWhere,
		)
		args := combineArgs(idArgs, typeArgs)
		if err := r.queryDeps(ctx, q, args, result.Outgoing, true); err != nil {
			return domain.DepBulkResult{}, fmt.Errorf("db: DependencySQLRepository.ListByIssueIDs (out): %w", err)
		}
	}

	if opts.Direction == domain.DepDirectionBoth || opts.Direction == domain.DepDirectionIn {
		//nolint:gosec // G201: table, depSelectColumns, depTargetExpr are hardcoded
		q := fmt.Sprintf(
			`SELECT %s FROM %s WHERE %s IN (%s)%s ORDER BY issue_id`,
			depSelectColumns, table, depTargetExpr, idPlaceholders, typeWhere,
		)
		args := combineArgs(idArgs, typeArgs)
		if err := r.queryDeps(ctx, q, args, result.Incoming, false); err != nil {
			return domain.DepBulkResult{}, fmt.Errorf("db: DependencySQLRepository.ListByIssueIDs (in): %w", err)
		}
	}

	return result, nil
}

func (r *dependencySQLRepositoryImpl) CountsByIssueIDs(ctx context.Context, issueIDs []string, opts domain.DepCountsOpts) (map[string]*types.DependencyCounts, error) {
	result := make(map[string]*types.DependencyCounts)
	if len(issueIDs) == 0 {
		return result, nil
	}
	for _, id := range issueIDs {
		result[id] = &types.DependencyCounts{}
	}

	idPlaceholders, idArgs := buildInPlaceholders(issueIDs)
	table := pickDepTable(opts.UseWispsTable)

	//nolint:gosec // G201: table is one of two hardcoded constants
	outQ := fmt.Sprintf(
		`SELECT issue_id, COUNT(*) FROM %s WHERE issue_id IN (%s) AND type = 'blocks' GROUP BY issue_id`,
		table, idPlaceholders,
	)
	if err := scanCounts(ctx, r.runner, outQ, idArgs, result, func(c *types.DependencyCounts, n int) { c.DependencyCount = n }); err != nil {
		return nil, fmt.Errorf("db: DependencySQLRepository.CountsByIssueIDs (out): %w", err)
	}

	//nolint:gosec // G201: table and depTargetExpr are hardcoded
	inQ := fmt.Sprintf(
		`SELECT %s AS depends_on_id, COUNT(*) FROM %s WHERE %s IN (%s) AND type = 'blocks' GROUP BY %s`,
		depTargetExpr, table, depTargetExpr, idPlaceholders, depTargetExpr,
	)
	if err := scanCounts(ctx, r.runner, inQ, idArgs, result, func(c *types.DependencyCounts, n int) { c.DependentCount = n }); err != nil {
		return nil, fmt.Errorf("db: DependencySQLRepository.CountsByIssueIDs (in): %w", err)
	}

	return result, nil
}

func (r *dependencySQLRepositoryImpl) GetBlockingInfo(ctx context.Context, issueIDs []string, opts domain.DepListOpts) (domain.BlockingInfo, error) {
	info := domain.BlockingInfo{
		BlockedBy: make(map[string][]string),
		Blocks:    make(map[string][]string),
		Parent:    make(map[string]string),
	}
	if len(issueIDs) == 0 {
		return info, nil
	}

	table := pickDepTable(opts.UseWispsTable)
	idPlaceholders, idArgs := buildInPlaceholders(issueIDs)

	// beads-h7u56/dqje3: the proxied display blocked-indicator must count the
	// SAME blocking-edge families the authority (is_blocked / bd ready / bd
	// blocked) counts, or bd list under-signals a genuinely-blocked issue as
	// ○ open on the hub/proxied path (the recurring proxied-twin gap — the
	// direct fix lives in issueops.queryBlockedByInfo). 'blocks'/'parent-child'
	// keep any-close-unblocks semantics; 'conditional-blocks' is resolved
	// reason-aware below (a success-closed target still blocks — beads-a3hm);
	// the waits-for GATE is handled by a separate query gated on
	// issueops.WaitsForGateBlockedSQL (activeBlockerSQL returns false for
	// waits-for, so a status check alone cannot resolve it).
	//nolint:gosec // G201: table and depTargetExpr are hardcoded constants
	outQ := fmt.Sprintf(
		"SELECT issue_id, %s AS depends_on_id, type FROM %s WHERE issue_id IN (%s) AND type IN ('blocks', 'parent-child', 'conditional-blocks')",
		depTargetExpr, table, idPlaceholders,
	)
	outRows, err := r.scanBlockingRows(ctx, outQ, idArgs)
	if err != nil {
		return domain.BlockingInfo{}, fmt.Errorf("db: DependencySQLRepository.GetBlockingInfo: outbound: %w", err)
	}

	//nolint:gosec // G201: table and depTargetExpr are hardcoded constants
	inQ := fmt.Sprintf(
		"SELECT issue_id, %s AS depends_on_id, type FROM %s WHERE %s IN (%s) AND type = 'blocks'",
		depTargetExpr, table, depTargetExpr, idPlaceholders,
	)
	inRows, err := r.scanBlockingRows(ctx, inQ, idArgs)
	if err != nil {
		return domain.BlockingInfo{}, fmt.Errorf("db: DependencySQLRepository.GetBlockingInfo: inbound: %w", err)
	}

	statusIDs := make(map[string]struct{})
	for _, row := range outRows {
		statusIDs[row.dependsOnID] = struct{}{}
	}
	for _, row := range inRows {
		statusIDs[row.dependsOnID] = struct{}{}
	}
	// Load status + close_reason (not status alone) so conditional-blocks
	// activeness is resolved reason-aware, matching the authority (beads-a3hm).
	stateByID, err := r.loadStateByID(ctx, statusIDs)
	if err != nil {
		return domain.BlockingInfo{}, fmt.Errorf("db: DependencySQLRepository.GetBlockingInfo: status lookup: %w", err)
	}

	for _, row := range outRows {
		depType := types.DependencyType(row.depType)
		if depType == types.DepConditionalBlocks {
			// Reason-aware: keep only while still an active blocker (open OR
			// success-closed target), matching bd ready / activeBlockerSQL.
			st := stateByID[row.dependsOnID]
			if !issueops.IsActiveBlockerByState(depType, st.status, st.closeReason) {
				continue
			}
		} else if stateByID[row.dependsOnID].status == types.StatusClosed {
			// 'blocks'/'parent-child': any close unblocks (unchanged).
			continue
		}
		if row.depType == "parent-child" {
			info.Parent[row.issueID] = row.dependsOnID
		} else {
			info.BlockedBy[row.issueID] = append(info.BlockedBy[row.issueID], row.dependsOnID)
		}
	}
	for _, row := range inRows {
		if stateByID[row.dependsOnID].status == types.StatusClosed {
			continue
		}
		info.Blocks[row.dependsOnID] = append(info.Blocks[row.dependsOnID], row.issueID)
	}

	// beads-dqje3: waits-for fanout-gate edges. The gate (open parent-child
	// children of the spawner, honoring the all-children/any-children gate
	// metadata) is exactly issueops.WaitsForGateBlockedSQL — the predicate the
	// is_blocked recompute uses — so reusing it makes the proxied display map
	// agree with is_blocked / bd blocked / bd ready by construction. Named
	// blocker = the spawner (the waits-for depends_on). The predicate requires
	// the dependency-row alias `d`.
	//nolint:gosec // G201: table/depTargetExpr are hardcoded constants; predicate is a package const.
	wfQ := fmt.Sprintf(
		"SELECT d.issue_id, %s AS depends_on_id FROM %s d WHERE d.issue_id IN (%s) AND d.type = 'waits-for' AND (%s)",
		depTargetExpr, table, idPlaceholders, issueops.WaitsForGateBlockedSQL(),
	)
	wfRows, err := r.runner.QueryContext(ctx, wfQ, idArgs...)
	if err != nil {
		return domain.BlockingInfo{}, fmt.Errorf("db: DependencySQLRepository.GetBlockingInfo: waits-for: %w", err)
	}
	defer wfRows.Close()
	for wfRows.Next() {
		var issueID, spawnerID string
		if scanErr := wfRows.Scan(&issueID, &spawnerID); scanErr != nil {
			return domain.BlockingInfo{}, fmt.Errorf("db: DependencySQLRepository.GetBlockingInfo: scan waits-for: %w", scanErr)
		}
		info.BlockedBy[issueID] = append(info.BlockedBy[issueID], spawnerID)
	}
	if err := wfRows.Err(); err != nil {
		return domain.BlockingInfo{}, fmt.Errorf("db: DependencySQLRepository.GetBlockingInfo: waits-for rows: %w", err)
	}

	return info, nil
}

func (r *dependencySQLRepositoryImpl) GetBlockingInfoAcrossIssuesAndWisps(ctx context.Context, issueIDs []string) (domain.BlockingInfo, error) {
	perm, err := r.GetBlockingInfo(ctx, issueIDs, domain.DepListOpts{UseWispsTable: false})
	if err != nil {
		return domain.BlockingInfo{}, err
	}
	wisp, err := r.GetBlockingInfo(ctx, issueIDs, domain.DepListOpts{UseWispsTable: true})
	if err != nil {
		if !dberrors.IsTableNotExist(err) {
			return domain.BlockingInfo{}, err
		}
		wisp = domain.BlockingInfo{
			BlockedBy: map[string][]string{},
			Blocks:    map[string][]string{},
			Parent:    map[string]string{},
		}
	}
	for k, v := range wisp.BlockedBy {
		perm.BlockedBy[k] = append(perm.BlockedBy[k], v...)
	}
	for k, v := range wisp.Blocks {
		perm.Blocks[k] = append(perm.Blocks[k], v...)
	}
	for k, v := range wisp.Parent {
		if _, ok := perm.Parent[k]; !ok {
			perm.Parent[k] = v
		}
	}
	return perm, nil
}

type blockingRow struct {
	issueID, dependsOnID, depType string
}

func (r *dependencySQLRepositoryImpl) scanBlockingRows(ctx context.Context, q string, args []any) ([]blockingRow, error) {
	rows, err := r.runner.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []blockingRow
	for rows.Next() {
		var row blockingRow
		if err := rows.Scan(&row.issueID, &row.dependsOnID, &row.depType); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// blockerState is a target blocker's status + close_reason, the minimum needed
// to resolve conditional-blocks activeness reason-aware (beads-a3hm/h7u56) on
// the proxied path — the twin of issueops.targetState.
type blockerState struct {
	status      types.Status
	closeReason string
}

// loadStateByID is loadStatusByID plus the close_reason column so the proxied
// GetBlockingInfo display path can apply the SAME failure-vs-success close
// semantics as the authoritative is_blocked recompute for conditional-blocks
// edges (beads-h7u56). Iterates issues then wisps; a cross-table dup errors
// (same invariant loadStatusByID enforces).
func (r *dependencySQLRepositoryImpl) loadStateByID(ctx context.Context, idSet map[string]struct{}) (map[string]blockerState, error) {
	stateByID := make(map[string]blockerState, len(idSet))
	if len(idSet) == 0 {
		return stateByID, nil
	}
	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	placeholders, args := buildInPlaceholders(ids)
	sourceByID := make(map[string]string, len(idSet))
	for _, table := range []string{"issues", "wisps"} {
		//nolint:gosec // G201: table is a hardcoded constant
		q := fmt.Sprintf("SELECT id, status, close_reason FROM %s WHERE id IN (%s)", table, placeholders)
		rows, err := r.runner.QueryContext(ctx, q, args...)
		if err != nil {
			if dberrors.IsTableNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("status+reason from %s: %w", table, err)
		}
		func() {
			defer rows.Close()
			for rows.Next() {
				var id string
				var status types.Status
				var closeReason sql.NullString
				if scanErr := rows.Scan(&id, &status, &closeReason); scanErr != nil {
					err = fmt.Errorf("status+reason from %s: scan: %w", table, scanErr)
					return
				}
				if existing, dup := sourceByID[id]; dup {
					err = fmt.Errorf("status id %q exists in both %s and %s", id, existing, table)
					return
				}
				sourceByID[id] = table
				stateByID[id] = blockerState{status: status, closeReason: closeReason.String}
			}
			if rowsErr := rows.Err(); rowsErr != nil && err == nil {
				err = fmt.Errorf("status+reason rows from %s: %w", table, rowsErr)
			}
		}()
		if err != nil {
			return nil, err
		}
	}
	return stateByID, nil
}

func (r *dependencySQLRepositoryImpl) queryDeps(ctx context.Context, q string, args []any, into map[string][]*types.Dependency, keyByIssueID bool) error {
	rows, err := r.runner.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var d types.Dependency
		var typ string
		var createdBy, metadata, threadID sql.NullString
		var createdAt sql.NullTime
		if err := rows.Scan(&d.IssueID, &d.DependsOnID, &typ, &createdAt, &createdBy, &metadata, &threadID); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		d.Type = types.DependencyType(typ)
		if createdAt.Valid {
			d.CreatedAt = createdAt.Time
		}
		if createdBy.Valid {
			d.CreatedBy = createdBy.String
		}
		if metadata.Valid && metadata.String != "" && metadata.String != "{}" {
			d.Metadata = metadata.String
		}
		if threadID.Valid {
			d.ThreadID = threadID.String
		}
		dd := d
		var key string
		if keyByIssueID {
			key = d.IssueID
		} else {
			key = d.DependsOnID
		}
		into[key] = append(into[key], &dd)
	}
	return rows.Err()
}

func scanCounts(ctx context.Context, runner Runner, q string, args []any, into map[string]*types.DependencyCounts, assign func(c *types.DependencyCounts, n int)) error {
	rows, err := runner.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		if c, ok := into[id]; ok {
			assign(c, n)
		}
	}
	return rows.Err()
}

func buildInPlaceholders[T ~string](values []T) (string, []any) {
	ph := make([]string, len(values))
	args := make([]any, len(values))
	for i, v := range values {
		ph[i] = "?"
		args[i] = string(v)
	}
	return strings.Join(ph, ","), args
}

func buildTypeFilter(depTypes []types.DependencyType) (string, []any) {
	if len(depTypes) == 0 {
		return "", nil
	}
	ph := make([]string, len(depTypes))
	args := make([]any, len(depTypes))
	for i, t := range depTypes {
		ph[i] = "?"
		args[i] = string(t)
	}
	return " AND type IN (" + strings.Join(ph, ",") + ")", args
}

func combineArgs(a, b []any) []any {
	out := make([]any, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}

func (r *dependencySQLRepositoryImpl) DeleteAllForIDs(ctx context.Context, ids []string, opts domain.DepInsertOpts) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	table := "dependencies"
	if opts.UseWispsTable {
		table = "wisp_dependencies"
	}
	total := 0
	for start := 0; start < len(ids); start += deleteBatchSize {
		end := start + deleteBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		placeholders := make([]string, len(batch))
		args := make([]any, 0, 2*len(batch))
		for i, id := range batch {
			placeholders[i] = "?"
			args = append(args, id)
		}
		for _, id := range batch {
			args = append(args, id)
		}
		ph := strings.Join(placeholders, ",")
		//nolint:gosec // G201: table is one of two hardcoded constants; ? placeholders only.
		res, err := r.runner.ExecContext(ctx,
			fmt.Sprintf("DELETE FROM %s WHERE issue_id IN (%s) OR %s IN (%s)", table, ph, issueops.DepTargetExpr, ph),
			args...)
		if err != nil {
			if opts.UseWispsTable && dberrors.IsTableNotExist(err) {
				return total, nil
			}
			return total, fmt.Errorf("db: DependencySQLRepository.DeleteAllForIDs from %s: %w", table, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("db: DependencySQLRepository.DeleteAllForIDs rows affected: %w", err)
		}
		total += int(n)
	}
	return total, nil
}

func (r *dependencySQLRepositoryImpl) CountAllForIDs(ctx context.Context, ids []string, opts domain.DepCountsOpts) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	table := "dependencies"
	if opts.UseWispsTable {
		table = "wisp_dependencies"
	}
	total := 0
	for start := 0; start < len(ids); start += deleteBatchSize {
		end := start + deleteBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		placeholders := make([]string, len(batch))
		args := make([]any, 0, 2*len(batch))
		for i, id := range batch {
			placeholders[i] = "?"
			args = append(args, id)
		}
		for _, id := range batch {
			args = append(args, id)
		}
		ph := strings.Join(placeholders, ",")
		var count int
		//nolint:gosec // G201: table is one of two hardcoded constants; ? placeholders only.
		err := r.runner.QueryRowContext(ctx,
			fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE issue_id IN (%s) OR %s IN (%s)", table, ph, issueops.DepTargetExpr, ph),
			args...).Scan(&count)
		if err != nil {
			if opts.UseWispsTable && dberrors.IsTableNotExist(err) {
				return total, nil
			}
			return total, fmt.Errorf("db: DependencySQLRepository.CountAllForIDs from %s: %w", table, err)
		}
		total += count
	}
	return total, nil
}

func (r *dependencySQLRepositoryImpl) ListWithIssueMetadata(ctx context.Context, sourceID string, opts domain.DepListOpts) ([]*types.IssueWithDependencyMetadata, error) {
	var out []*types.IssueWithDependencyMetadata
	if opts.Direction == domain.DepDirectionOut || opts.Direction == domain.DepDirectionBoth {
		deps, err := issueops.GetDependenciesWithMetadataInTx(ctx, r.runner, sourceID)
		if err != nil {
			return nil, err
		}
		out = append(out, filterDepsByType(deps, opts.Types)...)
	}
	if opts.Direction == domain.DepDirectionIn || opts.Direction == domain.DepDirectionBoth {
		deps, err := issueops.GetDependentsWithMetadataInTx(ctx, r.runner, sourceID)
		if err != nil {
			return nil, err
		}
		out = append(out, filterDepsByType(deps, opts.Types)...)
	}
	return out, nil
}

func (r *dependencySQLRepositoryImpl) IterWithIssueMetadata(ctx context.Context, sourceID string, opts domain.DepListOpts) (storage.Iter[types.IssueWithDependencyMetadata], error) {
	items, err := r.ListWithIssueMetadata(ctx, sourceID, opts)
	if err != nil {
		return nil, err
	}
	return storage.NewSliceIter(items), nil
}

func (r *dependencySQLRepositoryImpl) CountByID(ctx context.Context, sourceID string, opts domain.DepListOpts) (int64, error) {
	return issueops.CountDependencyEdgesInTx(ctx, r.runner, sourceID, opts.Direction, opts.Types)
}

func filterDepsByType(deps []*types.IssueWithDependencyMetadata, filter []types.DependencyType) []*types.IssueWithDependencyMetadata {
	if len(filter) == 0 {
		return deps
	}
	allowed := make(map[types.DependencyType]struct{}, len(filter))
	for _, t := range filter {
		allowed[t] = struct{}{}
	}
	out := make([]*types.IssueWithDependencyMetadata, 0, len(deps))
	for _, d := range deps {
		if _, ok := allowed[d.DependencyType]; ok {
			out = append(out, d)
		}
	}
	return out
}

func (r *dependencySQLRepositoryImpl) IsBlocked(ctx context.Context, issueID string, opts domain.DepListOpts) (bool, []string, error) {
	blocked, blockers, err := issueops.IsBlockedInTx(ctx, r.runner, issueID)
	if err != nil {
		return false, nil, fmt.Errorf("db: DependencySQLRepository.IsBlocked %s: %w", issueID, err)
	}
	return blocked, blockers, nil
}

func (r *dependencySQLRepositoryImpl) DetectCycles(ctx context.Context) ([][]*types.Issue, error) {
	out, err := issueops.DetectCyclesInTx(ctx, r.runner)
	if err != nil {
		return nil, fmt.Errorf("db: DependencySQLRepository.DetectCycles: %w", err)
	}
	return out, nil
}

func (r *dependencySQLRepositoryImpl) GetTree(ctx context.Context, rootID string, opts domain.DepTreeOpts) ([]*types.TreeNode, error) {
	if rootID == "" {
		return nil, errors.New("db: DependencySQLRepository.GetTree: rootID must not be empty")
	}
	if opts.Direction == domain.DepDirectionBoth {
		return nil, errors.New("db: DependencySQLRepository.GetTree: DepDirectionBoth not supported; callers must invoke once per direction and merge")
	}
	maxDepth := opts.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 50
	}
	reverse := opts.Direction == domain.DepDirectionIn
	out, err := issueops.GetDependencyTreeInTx(ctx, r.runner, rootID, maxDepth, opts.ShowAllPaths, reverse)
	if err != nil {
		return nil, fmt.Errorf("db: DependencySQLRepository.GetTree: %w", err)
	}
	return out, nil
}

func (r *dependencySQLRepositoryImpl) CycleThroughEdges(ctx context.Context, edges [][2]string) (string, error) {
	if len(edges) == 0 {
		return "", nil
	}
	graph := make(map[string][]string)
	if err := issueops.AppendBlockingGraphInTx(ctx, r.runner, []string{"dependencies"}, graph); err != nil {
		return "", fmt.Errorf("db: DependencySQLRepository.CycleThroughEdges: %w", err)
	}
	if err := issueops.AppendBlockingGraphInTx(ctx, r.runner, []string{"wisp_dependencies"}, graph); err != nil && !dberrors.IsTableNotExist(err) {
		return "", fmt.Errorf("db: DependencySQLRepository.CycleThroughEdges (wisps): %w", err)
	}
	return issueops.CycleThroughEdgesInGraph(graph, edges), nil
}

func (r *dependencySQLRepositoryImpl) GetDependencyRecordsForIssues(ctx context.Context, issueIDs []string) (map[string][]*types.Dependency, error) {
	if len(issueIDs) == 0 {
		return map[string][]*types.Dependency{}, nil
	}
	out, err := issueops.GetDependencyRecordsForIssuesInTx(ctx, r.runner, issueIDs)
	if err != nil {
		return nil, fmt.Errorf("db: DependencySQLRepository.GetDependencyRecordsForIssues: %w", err)
	}
	return out, nil
}

func (r *dependencySQLRepositoryImpl) GetWispDependencyRecordsForIDs(ctx context.Context, wispIDs []string) (map[string][]*types.Dependency, error) {
	if len(wispIDs) == 0 {
		return map[string][]*types.Dependency{}, nil
	}
	out, err := issueops.GetDependencyRecordsForIssuesFromTableInTx(ctx, r.runner, "wisp_dependencies", wispIDs)
	if err != nil {
		if dberrors.IsTableNotExist(err) {
			return map[string][]*types.Dependency{}, nil
		}
		return nil, fmt.Errorf("db: DependencySQLRepository.GetWispDependencyRecordsForIDs: %w", err)
	}
	return out, nil
}
