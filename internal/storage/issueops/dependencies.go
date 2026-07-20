package issueops

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/storage/depid"
	"github.com/steveyegge/beads/internal/storage/sqlbuild"
	"github.com/steveyegge/beads/internal/types"
)

type DepTargetKind int

const (
	DepTargetIssue DepTargetKind = iota
	DepTargetWisp
	DepTargetExternal
)

func (k DepTargetKind) Column() string {
	switch k {
	case DepTargetWisp:
		return "depends_on_wisp_id"
	case DepTargetExternal:
		return "depends_on_external"
	default:
		return "depends_on_issue_id"
	}
}

// DepTargetExpr is the SQL expression that resolves a dependency row's target
// id from its three typed columns. Use this in SELECT projections (aliased as
// depends_on_id) and in WHERE clauses when the caller doesn't know the target
// kind ahead of time.
const DepTargetExpr = sqlbuild.DepTargetExpr

func depTargetExpr(alias string) string {
	if alias == "" {
		return DepTargetExpr
	}
	return fmt.Sprintf("COALESCE(%s.depends_on_issue_id, %s.depends_on_wisp_id, %s.depends_on_external)", alias, alias, alias)
}

func depTargetEquals(alias string) string {
	return depTargetExpr(alias) + " = ?"
}

func depTargetIn(alias, placeholders string) string {
	return depTargetExpr(alias) + " IN (" + placeholders + ")"
}

func ClassifyDepTarget(ctx context.Context, tx *sql.Tx, dep *types.Dependency, isCrossPrefix bool) DepTargetKind {
	if isCrossPrefix || strings.HasPrefix(dep.DependsOnID, "external:") {
		return DepTargetExternal
	}
	if IsActiveWispInTx(ctx, tx, dep.DependsOnID) {
		return DepTargetWisp
	}
	return DepTargetIssue
}

// AddDependencyOpts configures AddDependencyInTx behavior.
// When fields are left empty, AddDependencyInTx performs wisp routing
// automatically via IsActiveWispInTx. Callers that have already determined
// routing (e.g., DoltStore with its pre-tx wisp cache) can set fields
// explicitly to skip the redundant DB check.
type AddDependencyOpts struct {
	// SourceTable is the table to validate the source issue exists in.
	// Auto-detected via wisp routing if empty.
	SourceTable string
	// TargetTable is the table to validate the target issue exists in.
	// Auto-detected via wisp routing if empty. Ignored when target validation is skipped.
	TargetTable string
	// WriteTable is the dependency table to insert/update/check existing deps in.
	// Auto-detected from source wisp routing if empty.
	WriteTable string
	// DepTables are the tables to scan for cycle detection. Defaults to both
	// dependency tables; edge storage is source-routed, so same-class endpoints
	// can still have mixed-table interior paths.
	DepTables []string
	// IsCrossPrefix is true when source and target have different prefixes,
	// meaning the target lives in another rig's database.
	IsCrossPrefix bool
	// SkipCycleCheck skips the recursive pre-insert cycle check for callers
	// that intentionally trade validation cost for bulk graph wiring speed.
	SkipCycleCheck bool
	TargetKind     *DepTargetKind
}

// AddDependencyInTx validates and inserts a dependency within an existing
// transaction. It handles:
//   - Wisp routing (auto-detected or caller-provided)
//   - Source/target existence validation
//   - Cross-type blocking validation (GH#1495)
//   - Cycle detection via recursive CTE across both dependency tables
//   - Idempotent same-type updates (metadata only)
//   - Type conflict detection
//
// The caller is responsible for transaction lifecycle, dolt commits, and
// any cache invalidation.
func AddDependencyInTx(ctx context.Context, tx *sql.Tx, dep *types.Dependency, actor string, opts AddDependencyOpts) error {
	// Reject a self-referential edge UNCONDITIONALLY (beads-jg2s), before the
	// SkipCycleCheck gate below — CheckDependencyCycleInTx has the same guard but
	// is skipped under --no-cycle-check, and its family walk skips non-blocking
	// types entirely, so a self-loop (esp. related/waits-for or --no-cycle-check)
	// could be committed. A self-edge is invalid for every type and every path.
	if dep.IssueID == dep.DependsOnID {
		return fmt.Errorf("cannot add self-dependency: %s cannot depend on itself", dep.IssueID)
	}

	// Auto-detect source routing if not provided.
	sourceTable := opts.SourceTable
	writeTable := opts.WriteTable
	if sourceTable == "" || writeTable == "" {
		sourceIsWisp := IsActiveWispInTx(ctx, tx, dep.IssueID)
		st, _, _, dt := WispTableRouting(sourceIsWisp)
		if sourceTable == "" {
			sourceTable = st
		}
		if writeTable == "" {
			writeTable = dt
		}
	}

	// Auto-detect target routing if not provided (skip for external/cross-prefix).
	targetTable := opts.TargetTable
	if targetTable == "" && !strings.HasPrefix(dep.DependsOnID, "external:") && !opts.IsCrossPrefix {
		targetIsWisp := IsActiveWispInTx(ctx, tx, dep.DependsOnID)
		targetTable, _, _, _ = WispTableRouting(targetIsWisp)
	}
	if targetTable == "" {
		targetTable = "issues"
	}

	depTables := opts.DepTables
	if len(depTables) == 0 {
		depTables = cycleDetectionTables()
	}

	metadata := dep.Metadata
	if metadata == "" {
		metadata = "{}"
	}

	// Validate source issue exists and get its type.
	var sourceType string
	//nolint:gosec // G201: sourceTable is from WispTableRouting ("issues" or "wisps")
	if err := tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT issue_type FROM %s WHERE id = ?`, sourceTable), dep.IssueID).Scan(&sourceType); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("issue %s not found", dep.IssueID)
		}
		return fmt.Errorf("failed to check issue existence: %w", err)
	}

	// Validate target issue exists (skip for external and cross-prefix refs).
	var targetType string
	if !strings.HasPrefix(dep.DependsOnID, "external:") && !opts.IsCrossPrefix {
		//nolint:gosec // G201: targetTable is from WispTableRouting ("issues" or "wisps")
		if err := tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT issue_type FROM %s WHERE id = ?`, targetTable), dep.DependsOnID).Scan(&targetType); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("issue %s not found", dep.DependsOnID)
			}
			return fmt.Errorf("failed to check target issue existence: %w", err)
		}
	}

	// Cross-type blocking validation (GH#1495): tasks can only block tasks,
	// epics can only block epics.
	if dep.Type == types.DepBlocks && targetType != "" {
		sourceIsEpic := sourceType == string(types.TypeEpic)
		targetIsEpic := targetType == string(types.TypeEpic)
		if sourceIsEpic != targetIsEpic {
			if sourceIsEpic {
				return fmt.Errorf("epics can only block other epics, not tasks")
			}
			return fmt.Errorf("tasks can only block other tasks, not epics")
		}
	}

	if !opts.SkipCycleCheck {
		if err := CheckDependencyCycleInTx(ctx, tx, dep, depTables); err != nil {
			return err
		}
	}

	var kind DepTargetKind
	if opts.TargetKind != nil {
		kind = *opts.TargetKind
	} else {
		kind = ClassifyDepTarget(ctx, tx, dep, opts.IsCrossPrefix)
	}
	targetCol := kind.Column()

	// Check for existing dependency between the same pair. Use the resolved
	// target expression defensively so stale/reclassified rows in another typed
	// target column cannot bypass the idempotency/conflict check.
	var existingType string
	//nolint:gosec // G201: writeTable from WispTableRouting; depTargetEquals has no user input.
	err := tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT type FROM %s WHERE issue_id = ? AND %s`, writeTable, depTargetEquals("")),
		dep.IssueID, dep.DependsOnID).Scan(&existingType)
	if err == nil {
		if existingType == string(dep.Type) {
			// Same type — idempotent; update metadata.
			//nolint:gosec // G201: writeTable from WispTableRouting; depTargetEquals has no user input.
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET metadata = ? WHERE issue_id = ? AND %s`, writeTable, depTargetEquals("")),
				metadata, dep.IssueID, dep.DependsOnID); err != nil {
				return fmt.Errorf("failed to update dependency metadata: %w", err)
			}
			return nil
		}
		return fmt.Errorf("dependency %s -> %s already exists with type %q (requested %q); remove it first with 'bd dep remove' then re-add",
			dep.IssueID, dep.DependsOnID, existingType, dep.Type)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("failed to check existing dependency: %w", err)
	}

	// id is derived deterministically from the natural edge key (issue_id,
	// target) so the same edge gets the same primary key on every clone and the
	// dependencies table merges cleanly across Dolt clones (#4259). DependsOnID
	// is the resolved target written into targetCol.
	//nolint:gosec // G201: writeTable from WispTableRouting; targetCol from DepTargetKind.Column()
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (id, issue_id, %s, type, created_at, created_by, metadata, thread_id)
		VALUES (?, ?, ?, ?, NOW(), ?, ?, ?)
	`, writeTable, targetCol), depid.New(dep.IssueID, dep.DependsOnID), dep.IssueID, dep.DependsOnID, dep.Type, actor, metadata, dep.ThreadID); err != nil {
		return fmt.Errorf("failed to add dependency: %w", err)
	}

	srcIsWisp := writeTable == "wisp_dependencies"

	// Record an audit event so dependency-graph mutations are visible in
	// `bd history` (beads-1qt9). Mirrors the label add/remove event pattern;
	// routes to wisp_events when the source is a wisp.
	eventTable := "events"
	if srcIsWisp {
		eventTable = "wisp_events"
	}
	depAddComment := fmt.Sprintf("Added dependency: %s -> %s (%s)", dep.IssueID, dep.DependsOnID, dep.Type)
	//nolint:gosec // G201: eventTable is a hardcoded constant ("events" or "wisp_events").
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (id, issue_id, event_type, actor, comment) VALUES (?, ?, ?, ?, ?)`, eventTable),
		NewEventID(), dep.IssueID, types.EventDependencyAdded, actor, depAddComment); err != nil {
		return fmt.Errorf("add dependency: record event: %w", err)
	}

	var affectedIssues, affectedWisps []string
	var aerr error
	if srcIsWisp {
		affectedIssues, affectedWisps, aerr = AffectedByDepChangeForWispInTx(ctx, tx, dep.IssueID, dep.DependsOnID, dep.Type)
	} else {
		affectedIssues, affectedWisps, aerr = AffectedByDepChangeInTx(ctx, tx, dep.IssueID, dep.DependsOnID, dep.Type)
	}
	if aerr != nil {
		return fmt.Errorf("affected by add dependency %s -> %s: %w", dep.IssueID, dep.DependsOnID, aerr)
	}
	if dep.Type == types.DepBlocks || dep.Type == types.DepConditionalBlocks {
		if err := markDirectBlockingDependencySourceInTx(ctx, tx, dep.IssueID, srcIsWisp, dep.DependsOnID, kind); err != nil {
			return fmt.Errorf("mark direct is_blocked after add dependency %s -> %s: %w", dep.IssueID, dep.DependsOnID, err)
		}
		affectedIssues, affectedWisps = RemoveSourceFromAffected(dep.IssueID, srcIsWisp, affectedIssues, affectedWisps)
	}
	if dep.Type == types.DepParentChild {
		// Parent-child adds are not monotonic: adding an already-closed child can
		// satisfy an any-children waits-for gate and unblock the waiter.
		if err := RecomputeIsBlockedInTx(ctx, tx, affectedIssues, affectedWisps); err != nil {
			return fmt.Errorf("recompute is_blocked after add dependency %s -> %s: %w", dep.IssueID, dep.DependsOnID, err)
		}
		return nil
	}
	if err := MarkIsBlockedInTx(ctx, tx, affectedIssues, affectedWisps); err != nil {
		return fmt.Errorf("mark is_blocked after add dependency %s -> %s: %w", dep.IssueID, dep.DependsOnID, err)
	}
	return nil
}

// LinkAndCloseInTx adds a dependency edge AND closes dep.IssueID in ONE
// transaction, so the two can never split into an inconsistent state. Before
// this, bd duplicate / bd supersede ran AddDependency and UpdateIssue(closed)
// as two SEPARATE committed transactions: a failure between them left the
// duplicate/supersede edge added while the issue stayed OPEN — a
// recoverable-but-inconsistent split state (the issue reads as linked yet
// remains active in ready/list, and a re-run re-adds the edge). Same class as
// the compaction overwrite+mark split fixed by CompactOverwriteInTx (beads-pj38).
//
// A failure in either step rolls the whole transaction back, so the edge is
// only durable when the close also succeeds (beads-njnw).
func LinkAndCloseInTx(ctx context.Context, tx *sql.Tx, dep *types.Dependency, actor string, opts AddDependencyOpts) error {
	if err := AddDependencyInTx(ctx, tx, dep, actor, opts); err != nil {
		return err
	}
	updates := map[string]interface{}{"status": string(types.StatusClosed)}
	if _, err := UpdateIssueInTx(ctx, tx, dep.IssueID, updates, actor); err != nil {
		return fmt.Errorf("close after link %s -> %s: %w", dep.IssueID, dep.DependsOnID, err)
	}
	return nil
}

// RemoveSourceFromAffected drops the dep source from the affected-ID sets
// after a direct is_blocked mark, so the follow-up Mark/Recompute pass does
// not redo it. Shared with the domain/db dependency repository.
func RemoveSourceFromAffected(source string, srcIsWisp bool, issueIDs, wispIDs []string) ([]string, []string) {
	if srcIsWisp {
		return issueIDs, removeID(wispIDs, source)
	}
	return removeID(issueIDs, source), wispIDs
}

func removeID(ids []string, remove string) []string {
	if len(ids) == 0 {
		return ids
	}
	out := ids[:0]
	for _, id := range ids {
		if id != remove {
			out = append(out, id)
		}
	}
	return out
}

//nolint:gosec // G201: table names are selected from fixed issue/wisp tables.
func markDirectBlockingDependencySourceInTx(ctx context.Context, tx *sql.Tx, source string, srcIsWisp bool, target string, targetKind DepTargetKind) error {
	sourceTable := "issues"
	if srcIsWisp {
		sourceTable = "wisps"
	}
	targetTable := ""
	switch targetKind {
	case DepTargetIssue:
		targetTable = "issues"
	case DepTargetWisp:
		targetTable = "wisps"
	default:
		return nil
	}

	// MySQL 8.0+ rejects UPDATE <T> ... WHERE EXISTS (SELECT FROM <T> ...)
	// even with distinct aliases (Error 1093: can't specify target table
	// for update in FROM clause), which fires here whenever sourceTable ==
	// targetTable. Wrapping the inner SELECT in a derived table makes MySQL
	// treat it as an independent rowset, satisfying the restriction. Dolt
	// accepts both forms, so this is a no-op there.
	_, err := tx.ExecContext(ctx, fmt.Sprintf(`
		UPDATE %s s SET s.is_blocked = 1, s.updated_at = s.updated_at
		WHERE s.id = ?
		  AND s.is_blocked = 0
		  AND s.status <> 'closed' AND s.status <> 'pinned'
		  AND EXISTS (
		    SELECT 1 FROM (
		      SELECT id, status FROM %s WHERE id = ?
		    ) AS t
		    WHERE t.status <> 'closed' AND t.status <> 'pinned'
		  )
	`, sourceTable, targetTable), source, target)
	return err
}

// CheckDependencyCycleInTx rejects self-dependencies and dependency cycles
// before an insert. The caller may pass a restricted depTables list for a known
// storage bucket; nil uses all dependency tables.
//
// Cycle detection is per edge-type FAMILY: a blocks/conditional-blocks edge is
// checked for reachability over blocking edges only; a parent-child edge is
// checked over parent-child edges only. Mixing families would wrongly reject a
// valid edge (e.g. a reparent that closes no parent cycle but shares nodes with
// a blocks chain). Parent-child cycles are exactly as corrupting as blocks
// cycles — they break tree/epic-rollup/descendant traversal — so parent-child
// is no longer exempt (beads-8qij), nor are supersedes (beads-8ix02) or
// waits-for (beads-vdqra). Types outside a checked family (e.g. related /
// relates-to loose knowledge edges, which legitimately allow cycles) still skip
// the graph walk.
//
// tx is a DBTX (only QueryRowContext is used), so both a *sql.Tx and a plain
// db.Runner satisfy it — the proxied/domain dependency use-case reuses this
// same family-aware check via the repo runner (beads-7a6n), instead of the
// blocking-only HasCycle probe that let a parent-child cycle through.
func CheckDependencyCycleInTx(ctx context.Context, tx DBTX, dep *types.Dependency, depTables []string) error {
	if dep.IssueID == dep.DependsOnID {
		return fmt.Errorf("cannot add self-dependency: %s cannot depend on itself", dep.IssueID)
	}
	if len(depTables) == 0 {
		depTables = cycleDetectionTables()
	}
	// beads-dfzre: `duplicates` edges are outside the reachability families
	// below (cycleCheckTypesFor returns nil for them), so a duplicate whose
	// canonical is ITSELF already a duplicate — a dup-of-a-dup chain, or the
	// closing edge of a mutual A<->B cycle — was accepted, corrupting the
	// canonical graph (a tracer follows the edge into a closed duplicate, or
	// loops forever). beads-wqrfi guarded this at the CMD layer only
	// (runDuplicate + runLinkAndCloseProxied), which left `bd dep add --type
	// duplicates` as a bypass. Reject it here, at the shared check both the
	// direct (AddDependencyInTx) and proxied (CheckCycleForType) paths call, so
	// ALL entry points — `bd duplicate`, its proxied twin, AND
	// `bd dep add --type duplicates` — are covered at once (seam-level > the
	// cmd-level wqrfi guard precisely because it also catches dep-add).
	if err := checkDuplicateChainInTx(ctx, tx, dep, depTables); err != nil {
		return err
	}
	depTypes := cycleCheckTypesFor(dep.Type)
	if len(depTypes) == 0 {
		return nil
	}
	var reachable int
	query := cycleReachabilityQuery(depTables, depTypes)
	if err := tx.QueryRowContext(ctx, query, dep.DependsOnID, dep.IssueID).Scan(&reachable); err != nil {
		return fmt.Errorf("failed to check for dependency cycle: %w", err)
	}
	if reachable > 0 {
		return fmt.Errorf("adding dependency would create a cycle")
	}
	return nil
}

// checkDuplicateChainInTx rejects a `duplicates` edge whose canonical target is
// itself already the source of a `duplicates` edge (beads-dfzre; beads-wqrfi
// fixed the same class at the cmd layer but left `bd dep add --type duplicates`
// unguarded). Marking A a duplicate of a canonical that is ITSELF a duplicate is
// never valid:
//   - dup-of-a-dup CHAIN: `bd duplicate LEAF --of MID` where MID was already
//     closed via `bd duplicate MID --of ROOT` leaves LEAF pointing at a closed
//     duplicate instead of the live ROOT — no collapse, no redirect.
//   - mutual CYCLE: `bd duplicate A --of B` then `bd duplicate B --of A` — on
//     the second edge, canonical A is already a duplicate-source (A->B), so
//     both issues end up closed each naming the other; a tracer loops forever
//     and `bd duplicates` surfaces nothing.
//
// The reachability walk cannot catch the acyclic chain case (no cycle closes),
// so this is a distinct guard. It fires only for `duplicates` edges — a
// `supersedes` version chain (old -> newer -> newest) is legitimate and stays
// allowed — and only when the canonical has an OUTGOING duplicates edge, so
// marking an issue a duplicate of a perfectly ordinary closed-as-fixed issue is
// unaffected. The error names the real root so the caller can re-point.
func checkDuplicateChainInTx(ctx context.Context, tx DBTX, dep *types.Dependency, depTables []string) error {
	if dep.Type != types.DepDuplicates {
		return nil
	}
	// Does the canonical (DependsOnID) itself point at something via a
	// `duplicates` edge? If so it is already a duplicate — not a live canonical.
	var unions []string
	for _, t := range depTables {
		unions = append(unions, fmt.Sprintf("SELECT %s AS target FROM %s WHERE issue_id = ? AND type = '%s'",
			DepTargetExpr, t, string(types.DepDuplicates)))
	}
	//nolint:gosec // G201: depTables are fixed constants (cycleDetectionTables); DepTargetExpr is a constant.
	query := fmt.Sprintf("SELECT target FROM (%s) d WHERE target IS NOT NULL LIMIT 1", strings.Join(unions, " UNION "))
	args := make([]any, len(depTables))
	for i := range depTables {
		args[i] = dep.DependsOnID
	}
	var root string
	err := tx.QueryRowContext(ctx, query, args...).Scan(&root)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to check duplicate chain: %w", err)
	}
	return fmt.Errorf("canonical %s is itself a duplicate of %s; mark %s a duplicate of %s instead",
		dep.DependsOnID, root, dep.IssueID, root)
}

// cycleCheckTypesFor returns the edge-type family whose graph must stay acyclic
// for the given new-edge type, or nil if the type is not cycle-checked. A
// blocks/conditional-blocks edge walks the blocking graph; a parent-child edge
// walks the parent-child graph (beads-8qij); a supersedes edge walks the
// supersedes graph (beads-8ix02); a waits-for edge walks the waits-for graph
// (beads-vdqra). Each blocking-family relationship is checked over its OWN
// family only — mixing families would wrongly reject a valid edge that merely
// shares nodes with another family's chain.
func cycleCheckTypesFor(t types.DependencyType) []string {
	switch t {
	case types.DepBlocks, types.DepConditionalBlocks:
		return []string{string(types.DepBlocks), string(types.DepConditionalBlocks)}
	case types.DepParentChild:
		return []string{string(types.DepParentChild)}
	case types.DepSupersedes:
		// beads-8ix02: a `supersedes` edge (old→new) must stay acyclic. A
		// forward version chain v1→v2→v3 is legitimate and MUST stay allowed;
		// the reachability walk permits it (v1 cannot reach v3's new edge back,
		// reachable=0) while rejecting a genuine cycle — `bd supersede A --with
		// B` then `B --with A`, or the longer A→B→C→A. Earlier this family was
		// deliberately excluded here (returned nil) to protect version chains,
		// but reachability already protects chains, so the exclusion was
		// over-broad and left `bd dep add --type supersedes` able to close a
		// supersede cycle (the supersede-seam reciprocal guard in
		// cmd/bd/duplicate.go, beads-02v2k, does not cover dep-add). Walking the
		// supersedes graph here at the shared seam covers ALL entry points —
		// `bd supersede`, its proxied twin, AND `bd dep add --type supersedes` —
		// and catches longer cycles the narrow reciprocal guard misses, exactly
		// as the beads-dfzre duplicates guard does. Distinct from duplicates:
		// duplicates ALSO needs an acyclic-CHAIN reject (checkDuplicateChainInTx),
		// because a dup-of-a-dup is invalid; a supersede chain is valid, so
		// supersedes needs only this cycle check.
		return []string{string(types.DepSupersedes)}
	case types.DepWaitsFor:
		// beads-vdqra: a `waits-for` edge is a HARD BLOCKING edge everywhere else
		// (IsBlockingEdge and AffectsReadyWork both include it, types.go:974/981;
		// AddDependencyInTx already guards a waits-for SELF-loop unconditionally,
		// beads-jg2s) — but it was the lone blocking-family member still falling
		// through to `default: return nil` here, so `bd dep add A B --type
		// waits-for` then `B A --type waits-for` accepted a mutual cycle (blocks
		// and conditional-blocks reject the identical sequence). A waits-for is a
		// fanout GATE (wait for a target's dynamic children); a mutual A⇄B — or a
		// longer A→B→C→A — is two gates each gating on the other = a latent
		// deadlock that corrupts fanout-gate traversal. Walk the waits-for graph
		// as its OWN family (NOT unioned with the blocks family): a waits-for is a
		// distinct relationship from blocks/conditional-blocks, and mixing
		// families would wrongly reject a valid edge that merely shares nodes with
		// a blocking chain (the family-isolation rule at CheckDependencyCycleInTx
		// above). Reachability permits a legal acyclic fanout-gate DAG while
		// rejecting a cycle — exactly the beads-8ix02 supersede reasoning. The
		// shared seam's single caller CheckDependencyCycleInTx covers ALL entry
		// points (`bd dep add --type waits-for` and any proxied path) at once.
		return []string{string(types.DepWaitsFor)}
	default:
		return nil
	}
}

// sqlTypeInList renders a quoted, comma-separated SQL IN list for the given
// dependency-type strings. The values come from cycleCheckTypesFor (fixed
// constants), never user input.
func sqlTypeInList(depTypes []string) string {
	quoted := make([]string, len(depTypes))
	for i, t := range depTypes {
		quoted[i] = "'" + t + "'"
	}
	return strings.Join(quoted, ", ")
}

// cycleReachabilityQuery uses UNION distinct recursion so cyclic and diamond
// graphs terminate by unique reachable node instead of enumerating paths. The
// recursion traverses only edges in depTypes so each edge-type family is
// checked against its own graph.
func cycleReachabilityQuery(depTables []string, depTypes []string) string {
	typeIn := sqlTypeInList(depTypes)
	if len(depTables) == 1 {
		return fmt.Sprintf(`
			WITH RECURSIVE reachable(node) AS (
				SELECT ?
				UNION
				SELECT %s
				FROM reachable r
				JOIN %s d ON d.issue_id = r.node AND d.type IN (%s)
			)
			SELECT COUNT(*) FROM reachable WHERE node = ?
		`, DepTargetExpr, depTables[0], typeIn)
	}

	var unions []string
	for _, t := range depTables {
		unions = append(unions, fmt.Sprintf("SELECT issue_id, %s AS depends_on_id FROM %s WHERE type IN (%s)", DepTargetExpr, t, typeIn))
	}
	unionQuery := strings.Join(unions, " UNION ")
	return fmt.Sprintf(`
		WITH RECURSIVE reachable(node) AS (
			SELECT ?
			UNION
			SELECT d.depends_on_id
			FROM reachable r
			JOIN (%s) d ON d.issue_id = r.node
		)
		SELECT COUNT(*) FROM reachable WHERE node = ?
	`, unionQuery)
}

func cycleDetectionTables() []string {
	return []string{"dependencies", "wisp_dependencies"}
}

func DeleteWispFromDependenciesInTx(ctx context.Context, tx *sql.Tx, wispID string) error {
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM dependencies WHERE depends_on_wisp_id = ?", wispID); err != nil {
		return fmt.Errorf("delete wisp %s from dependencies: %w", wispID, err)
	}
	return nil
}

//nolint:gosec // G201: inClause contains only ? placeholders
func DeleteWispsFromDependenciesInTx(ctx context.Context, tx *sql.Tx, wispIDs []string) error {
	if len(wispIDs) == 0 {
		return nil
	}
	inClause, args := buildSQLInClause(wispIDs)
	if _, err := tx.ExecContext(ctx,
		fmt.Sprintf("DELETE FROM dependencies WHERE depends_on_wisp_id IN (%s)", inClause),
		args...); err != nil {
		return fmt.Errorf("delete wisps from dependencies: %w", err)
	}
	return nil
}

// Dependency target rewrites reinsert matching rows because Dolt can leave the
// stored generated depends_on_id column stale after a split target column is
// updated by FK cascade.
func UpdateWispIDInDependenciesInTx(ctx context.Context, tx DBTX, oldID, newID string) error {
	for _, table := range []string{"dependencies", "wisp_dependencies"} {
		if err := replaceDependencyTargetInTx(ctx, tx, table, "depends_on_wisp_id", oldID, newID); err != nil {
			return fmt.Errorf("update wisp %s -> %s in %s: %w", oldID, newID, table, err)
		}
	}
	return nil
}

func UpdateIssueIDInDependenciesInTx(ctx context.Context, tx DBTX, oldID, newID string) error {
	for _, table := range []string{"dependencies", "wisp_dependencies"} {
		if err := replaceDependencyTargetInTx(ctx, tx, table, "depends_on_issue_id", oldID, newID); err != nil {
			return fmt.Errorf("update issue target %s -> %s in %s: %w", oldID, newID, table, err)
		}
	}
	// Re-derive the deterministic primary key for rows whose SOURCE issue was
	// renamed. dependencies.issue_id carries fk_dep_issue ... ON UPDATE CASCADE, so
	// renaming the issues row (updateIssueIDInTx updates issues.id first) cascades
	// issue_id from oldID to newID before we get here — but the cascade leaves the
	// surrogate id at depid.New(oldID, target). A stale id re-forks the primary key
	// across clones (#4259) and breaks the same-PK => same-edge invariant the pull
	// conflict resolver relies on, so recompute it from the post-rename (newID, target).
	if err := rekeyDependencySourceInTx(ctx, tx, oldID, newID); err != nil {
		return fmt.Errorf("rekey dependency sources %s -> %s: %w", oldID, newID, err)
	}
	return nil
}

// rekeyDependencySourceInTx rewrites dependencies.id for every edge whose source
// issue was renamed to newID so the stored id equals depid.New(newID, target). It
// matches rows by both newID (the normal post-FK-cascade state) and oldID (defensive,
// in case a caller reaches here before the cascade) and re-asserts issue_id = newID
// so the row converges either way. Only rows whose id is actually stale are touched.
func rekeyDependencySourceInTx(ctx context.Context, tx DBTX, oldID, newID string) error {
	queryRows, err := tx.QueryContext(ctx, `
		SELECT id, depends_on_issue_id, depends_on_wisp_id, depends_on_external
		FROM dependencies
		WHERE issue_id = ? OR issue_id = ?
	`, newID, oldID)
	if err != nil {
		return fmt.Errorf("query dependency sources: %w", err)
	}
	type rekey struct{ oldRowID, newRowID string }
	var rekeys []rekey
	for queryRows.Next() {
		var id string
		var issueTarget, wispTarget, external sql.NullString
		if err := queryRows.Scan(&id, &issueTarget, &wispTarget, &external); err != nil {
			_ = queryRows.Close()
			return fmt.Errorf("scan dependency source: %w", err)
		}
		target, ok := resolveDependencyTarget(issueTarget, wispTarget, external)
		if !ok {
			continue // ck_dep_one_target guarantees one target; skip defensively
		}
		if want := depid.New(newID, target); want != id {
			rekeys = append(rekeys, rekey{oldRowID: id, newRowID: want})
		}
	}
	_ = queryRows.Close()
	if err := queryRows.Err(); err != nil {
		return fmt.Errorf("iterate dependency sources: %w", err)
	}
	for _, rk := range rekeys {
		if _, err := tx.ExecContext(ctx,
			"UPDATE dependencies SET id = ?, issue_id = ? WHERE id = ?",
			rk.newRowID, newID, rk.oldRowID); err != nil {
			return fmt.Errorf("rekey dependency source id %s -> %s: %w", rk.oldRowID, rk.newRowID, err)
		}
	}
	return nil
}

// resolveDependencyTarget returns the single non-null dependency target — the value
// depid.New and the uk_dep_* unique keys treat as the edge's target — following the
// same precedence as DepTargetExpr (issue, then wisp, then external).
func resolveDependencyTarget(issueTarget, wispTarget, external sql.NullString) (string, bool) {
	switch {
	case issueTarget.Valid:
		return issueTarget.String, true
	case wispTarget.Valid:
		return wispTarget.String, true
	case external.Valid:
		return external.String, true
	default:
		return "", false
	}
}

func replaceDependencyTargetInTx(ctx context.Context, tx DBTX, table, column, oldID, newID string) error {
	// Dolt does not reliably recompute the stored generated depends_on_id when
	// only the split target column changes. Reinsert rows so the generated key
	// is calculated from the new target value.
	if err := checkRenameTargetCollision(ctx, tx, table, column, newID); err != nil {
		return err
	}

	type depRow struct {
		issueID     string
		issueTarget sql.NullString
		wispTarget  sql.NullString
		external    sql.NullString
		depType     string
		createdAt   sql.NullTime
		createdBy   sql.NullString
		metadata    sql.NullString
		threadID    sql.NullString
	}

	rows := make([]depRow, 0)
	//nolint:gosec // table and column are hardcoded by callers.
	queryRows, err := tx.QueryContext(ctx, fmt.Sprintf(`
		SELECT issue_id, depends_on_issue_id, depends_on_wisp_id, depends_on_external, type, created_at, created_by, metadata, thread_id
		FROM %s
		WHERE %s = ? OR (%s = ? AND depends_on_external IS NULL)
	`, table, column, DepTargetExpr), oldID, oldID)
	if err != nil {
		return fmt.Errorf("query dependency targets: %w", err)
	}
	for queryRows.Next() {
		var row depRow
		if err := queryRows.Scan(&row.issueID, &row.issueTarget, &row.wispTarget, &row.external, &row.depType, &row.createdAt, &row.createdBy, &row.metadata, &row.threadID); err != nil {
			_ = queryRows.Close()
			return fmt.Errorf("scan dependency target: %w", err)
		}
		switch column {
		case "depends_on_issue_id":
			row.issueTarget = sql.NullString{String: newID, Valid: true}
			row.wispTarget = sql.NullString{}
			row.external = sql.NullString{}
		case "depends_on_wisp_id":
			row.issueTarget = sql.NullString{}
			row.wispTarget = sql.NullString{String: newID, Valid: true}
			row.external = sql.NullString{}
		default:
			_ = queryRows.Close()
			return fmt.Errorf("replace dependency target: unsupported typed column %q", column)
		}
		rows = append(rows, row)
	}
	_ = queryRows.Close()
	if err := queryRows.Err(); err != nil {
		return fmt.Errorf("iterate dependency targets: %w", err)
	}

	//nolint:gosec // table and column are hardcoded by callers.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DELETE FROM %s WHERE %s = ? OR (%s = ? AND depends_on_external IS NULL)`, table, column, DepTargetExpr), oldID, oldID); err != nil {
		return fmt.Errorf("delete old dependency target: %w", err)
	}
	for _, row := range rows {
		// The retargeted edge's natural key is (issue_id, newID): the switch above
		// set exactly one typed target column to newID. Re-derive id from it so the
		// rewritten row stays merge-safe and keeps a clone-stable primary key (#4259).
		//nolint:gosec // table is hardcoded by callers.
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
			INSERT INTO %s (id, issue_id, depends_on_issue_id, depends_on_wisp_id, depends_on_external, type, created_at, created_by, metadata, thread_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, table), depid.New(row.issueID, newID), row.issueID, nullStringValue(row.issueTarget), nullStringValue(row.wispTarget), nullStringValue(row.external), row.depType, nullTimeValue(row.createdAt), nullStringValue(row.createdBy), nullStringValue(row.metadata), nullStringValue(row.threadID)); err != nil {
			return fmt.Errorf("insert replacement dependency target: %w", err)
		}
	}
	return nil
}

func nullStringValue(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}

func nullTimeValue(value sql.NullTime) any {
	if !value.Valid {
		return nil
	}
	return value.Time
}

func RetargetInboundDependenciesToWispInTx(ctx context.Context, tx *sql.Tx, id string) error {
	for _, table := range []string{"dependencies", "wisp_dependencies"} {
		if err := checkRetargetTargetCollision(ctx, tx, table, "depends_on_issue_id", "depends_on_wisp_id", id); err != nil {
			return err
		}
		if err := checkRenameTargetCollision(ctx, tx, table, "depends_on_wisp_id", id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
			UPDATE %s
			SET depends_on_wisp_id = ?, depends_on_issue_id = NULL
			WHERE depends_on_issue_id = ?
		`, table), id, id); err != nil {
			return fmt.Errorf("retarget inbound dependencies to wisp in %s for %s: %w", table, id, err)
		}
	}
	return nil
}

func RetargetInboundDependenciesToIssueInTx(ctx context.Context, tx DBTX, id string) error {
	for _, table := range []string{"dependencies", "wisp_dependencies"} {
		if err := checkRetargetTargetCollision(ctx, tx, table, "depends_on_wisp_id", "depends_on_issue_id", id); err != nil {
			return err
		}
		if err := checkRenameTargetCollision(ctx, tx, table, "depends_on_issue_id", id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
			UPDATE %s
			SET depends_on_issue_id = ?, depends_on_wisp_id = NULL
			WHERE depends_on_wisp_id = ?
		`, table), id, id); err != nil {
			return fmt.Errorf("retarget inbound dependencies to issue in %s for %s: %w", table, id, err)
		}
	}
	return nil
}

// UpdateIssueIDInDependencyTargetsInTx is called after the issues PK is updated
// from oldID to newID. FK ON UPDATE CASCADE has already propagated
// depends_on_issue_id from oldID to newID across dependencies and
// wisp_dependencies, so no rewrite is needed.
func UpdateIssueIDInDependencyTargetsInTx(ctx context.Context, tx *sql.Tx, _, newID string) error {
	for _, table := range []string{"dependencies", "wisp_dependencies"} {
		if err := checkRenameTargetCollision(ctx, tx, table, "depends_on_issue_id", newID); err != nil {
			return err
		}
	}
	return nil
}

//nolint:gosec // G201: table and typed columns are hardcoded constants.
func checkRetargetTargetCollision(ctx context.Context, tx DBTX, table, sourceCol, destCol, id string) error {
	var conflictCols []string
	switch destCol {
	case "depends_on_issue_id":
		conflictCols = []string{"depends_on_issue_id", "depends_on_external"}
	case "depends_on_wisp_id":
		conflictCols = []string{"depends_on_wisp_id", "depends_on_external"}
	default:
		return fmt.Errorf("checkRetargetTargetCollision: unsupported destination column %q", destCol)
	}
	if sourceCol != "depends_on_issue_id" && sourceCol != "depends_on_wisp_id" {
		return fmt.Errorf("checkRetargetTargetCollision: unsupported source column %q", sourceCol)
	}

	query := fmt.Sprintf(`
		SELECT 1 FROM %s moving
		JOIN %s existing ON moving.issue_id = existing.issue_id
		WHERE moving.%s = ?
		  AND (existing.%s = ? OR existing.%s = ?)
		LIMIT 1
	`, table, table, sourceCol, conflictCols[0], conflictCols[1])

	var found int
	err := tx.QueryRowContext(ctx, query, id, id, id).Scan(&found)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		if isTableNotExistError(err) {
			return nil
		}
		return fmt.Errorf("check retarget collision in %s: %w", table, err)
	}
	return fmt.Errorf("retarget to %s collides with existing dependency target in %s", id, table)
}

//nolint:gosec // G201: table and typedCol are hardcoded constants.
func checkRenameTargetCollision(ctx context.Context, tx DBTX, table, typedCol, newID string) error {
	var otherCols []string
	switch typedCol {
	case "depends_on_issue_id":
		otherCols = []string{"depends_on_wisp_id", "depends_on_external"}
	case "depends_on_wisp_id":
		otherCols = []string{"depends_on_issue_id", "depends_on_external"}
	default:
		return fmt.Errorf("checkRenameTargetCollision: unsupported typed column %q", typedCol)
	}

	query := fmt.Sprintf(`
		SELECT 1 FROM %s a
		JOIN %s b ON a.issue_id = b.issue_id
		WHERE a.%s = ?
		  AND (b.%s = ? OR b.%s = ?)
		LIMIT 1
	`, table, table, typedCol, otherCols[0], otherCols[1])

	var found int
	err := tx.QueryRowContext(ctx, query, newID, newID, newID).Scan(&found)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		if isTableNotExistError(err) {
			return nil
		}
		return fmt.Errorf("check rename collision in %s: %w", table, err)
	}
	return fmt.Errorf("rename to %s collides with existing dependency target in %s", newID, table)
}

// RemoveDependencyInTx removes a dependency between two issues within an
// existing transaction. Automatically routes to wisp_dependencies if the
// source issue is an active wisp. The audit event is credited to actor,
// mirroring AddDependencyInTx (beads-gmiu — previously hardcoded "system",
// so `bd history` mis-attributed every dependency removal).
//
//nolint:gosec // G201: depTable from WispTableRouting (hardcoded constants)
func RemoveDependencyInTx(ctx context.Context, tx *sql.Tx, issueID, dependsOnID, actor string) error {
	isWisp := IsActiveWispInTx(ctx, tx, issueID)
	_, _, _, depTable := WispTableRouting(isWisp)

	// Capture the row's type before deleting so we can dispatch the right
	// affected-set helper. If no row matches, treat as a no-op.
	var depType string
	row := tx.QueryRowContext(ctx, fmt.Sprintf(
		`SELECT type FROM %s WHERE issue_id = ? AND %s = ?`, depTable, DepTargetExpr),
		issueID, dependsOnID)
	if err := row.Scan(&depType); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("lookup dependency type for %s -> %s: %w", issueID, dependsOnID, err)
	}

	if _, err := tx.ExecContext(ctx, fmt.Sprintf(
		`DELETE FROM %s WHERE issue_id = ? AND %s = ?`, depTable, DepTargetExpr),
		issueID, dependsOnID); err != nil {
		return fmt.Errorf("remove dependency: %w", err)
	}

	// Record an audit event so dependency-graph mutations are visible in
	// `bd history` (beads-1qt9). Routes to wisp_events when the source is a wisp.
	removeEventTable := "events"
	if isWisp {
		removeEventTable = "wisp_events"
	}
	depRemoveComment := fmt.Sprintf("Removed dependency: %s -> %s (%s)", issueID, dependsOnID, depType)
	//nolint:gosec // G201: removeEventTable is a hardcoded constant ("events" or "wisp_events").
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (id, issue_id, event_type, actor, comment) VALUES (?, ?, ?, ?, ?)`, removeEventTable),
		NewEventID(), issueID, types.EventDependencyRemoved, actor, depRemoveComment); err != nil {
		return fmt.Errorf("remove dependency: record event: %w", err)
	}

	var affectedIssues, affectedWisps []string
	var aerr error
	if isWisp {
		affectedIssues, affectedWisps, aerr = AffectedByDepChangeForWispInTx(ctx, tx, issueID, dependsOnID, types.DependencyType(depType))
	} else {
		affectedIssues, affectedWisps, aerr = AffectedByDepChangeInTx(ctx, tx, issueID, dependsOnID, types.DependencyType(depType))
	}
	if aerr != nil {
		return fmt.Errorf("affected by remove dependency %s -> %s: %w", issueID, dependsOnID, aerr)
	}
	if err := RecomputeIsBlockedInTx(ctx, tx, affectedIssues, affectedWisps); err != nil {
		return fmt.Errorf("recompute is_blocked after remove dependency %s -> %s: %w", issueID, dependsOnID, err)
	}
	return nil
}

// GetIssuesByIDsInTx retrieves multiple issues by ID within an existing
// transaction, including labels. Automatically routes each ID to the correct
// table (issues/wisps). Uses batched IN clauses.
//
// wispSet is an optional pre-built set of active wisp IDs scoped to
// cover ids (see WispIDSetInTx). Pass nil to have the helper build
// a scoped set internally; callers hydrating multiple batches inside
// one tx can build the set once over the union of their IDs and
// reuse it across calls.
//
//nolint:gosec // G201: table names come from WispTableRouting (hardcoded constants)
func GetIssuesByIDsInTx(ctx context.Context, tx DBTX, ids []string, wispSet map[string]struct{}) ([]*types.Issue, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	if wispSet == nil {
		var err error
		wispSet, err = WispIDSetInTx(ctx, tx, ids)
		if err != nil {
			return nil, fmt.Errorf("get issues by IDs: build wisp set: %w", err)
		}
	}

	// Partition IDs by wisp status.
	wispIDs, permIDs := partitionByWispSet(ids, wispSet)

	var allIssues []*types.Issue
	for _, pair := range []struct {
		table    string
		labelTbl string
		ids      []string
	}{
		{"issues", "labels", permIDs},
		{"wisps", "wisp_labels", wispIDs},
	} {
		if len(pair.ids) == 0 {
			continue
		}
		for start := 0; start < len(pair.ids); start += queryBatchSize {
			end := start + queryBatchSize
			if end > len(pair.ids) {
				end = len(pair.ids)
			}
			batch := pair.ids[start:end]

			placeholders := make([]string, len(batch))
			args := make([]any, len(batch))
			for i, id := range batch {
				placeholders[i] = "?"
				args[i] = id
			}
			inClause := strings.Join(placeholders, ",")

			rows, err := tx.QueryContext(ctx, fmt.Sprintf(
				`SELECT %s FROM %s WHERE id IN (%s)`,
				IssueSelectColumns, pair.table, inClause), args...)
			if err != nil {
				return nil, fmt.Errorf("get issues by IDs from %s: %w", pair.table, err)
			}
			issueMap := make(map[string]*types.Issue)
			for rows.Next() {
				issue, scanErr := ScanIssueFrom(rows)
				if scanErr != nil {
					_ = rows.Close()
					return nil, fmt.Errorf("get issues by IDs: scan: %w", scanErr)
				}
				allIssues = append(allIssues, issue)
				issueMap[issue.ID] = issue
			}
			_ = rows.Close()
			if err := rows.Err(); err != nil {
				return nil, fmt.Errorf("get issues by IDs: rows: %w", err)
			}

			// Hydrate labels.
			if len(issueMap) > 0 {
				labelRows, err := tx.QueryContext(ctx, fmt.Sprintf(
					`SELECT issue_id, label FROM %s WHERE issue_id IN (%s) ORDER BY issue_id, label`,
					pair.labelTbl, inClause), args...)
				if err != nil {
					return nil, fmt.Errorf("get issues by IDs: labels from %s: %w", pair.labelTbl, err)
				}
				for labelRows.Next() {
					var issueID, label string
					if scanErr := labelRows.Scan(&issueID, &label); scanErr != nil {
						_ = labelRows.Close()
						return nil, fmt.Errorf("get issues by IDs: scan label: %w", scanErr)
					}
					if issue, ok := issueMap[issueID]; ok {
						issue.Labels = append(issue.Labels, label)
					}
				}
				_ = labelRows.Close()
				if err := labelRows.Err(); err != nil {
					return nil, fmt.Errorf("get issues by IDs: label rows: %w", err)
				}
			}
		}
	}

	return allIssues, nil
}

// GetDependenciesWithMetadataInTx returns issues that the given issueID depends on,
// along with the dependency type. Works within an existing transaction.
// Queries both dependency tables to handle cross-table dependencies.
//
//nolint:gosec // G201: table names come from hardcoded constants
func GetDependenciesWithMetadataInTx(ctx context.Context, tx DBTX, issueID string) ([]*types.IssueWithDependencyMetadata, error) {
	type depMeta struct {
		depID, depType string
	}

	// Query both dependency tables to find all dependencies.
	var deps []depMeta
	for _, depTable := range []string{"dependencies", "wisp_dependencies"} {
		rows, err := tx.QueryContext(ctx, fmt.Sprintf(
			`SELECT %s AS depends_on_id, type FROM %s WHERE issue_id = ?`, DepTargetExpr, depTable), issueID)
		if err != nil {
			return nil, fmt.Errorf("get dependencies from %s: %w", depTable, err)
		}
		for rows.Next() {
			var d depMeta
			if scanErr := rows.Scan(&d.depID, &d.depType); scanErr != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("get dependencies: scan: %w", scanErr)
			}
			deps = append(deps, d)
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("get dependencies: rows from %s: %w", depTable, err)
		}
	}

	if len(deps) == 0 {
		return nil, nil
	}

	// Fetch all dependency target issues.
	ids := make([]string, len(deps))
	for i, d := range deps {
		ids[i] = d.depID
	}
	issues, err := GetIssuesByIDsInTx(ctx, tx, ids, nil)
	if err != nil {
		return nil, fmt.Errorf("get dependencies: fetch issues: %w", err)
	}
	issueMap := make(map[string]*types.Issue, len(issues))
	for _, iss := range issues {
		issueMap[iss.ID] = iss
	}

	var results []*types.IssueWithDependencyMetadata
	for _, d := range deps {
		issue, ok := issueMap[d.depID]
		if !ok {
			// beads-n49j: the target is not a local issue (cross-prefix / external /
			// not-yet-synced). Such edges are intentionally allowed to skip existence
			// validation on add and are persisted + round-trip through export, so they
			// must remain VISIBLE here rather than being silently dropped. Surface a
			// minimal placeholder carrying the target ID and dependency type.
			results = append(results, &types.IssueWithDependencyMetadata{
				Issue:          unresolvedDepTargetIssue(d.depID),
				DependencyType: types.DependencyType(d.depType),
			})
			continue
		}
		results = append(results, &types.IssueWithDependencyMetadata{
			Issue:          *issue,
			DependencyType: types.DependencyType(d.depType),
		})
	}
	return results, nil
}

// unresolvedDepTargetIssue builds a minimal placeholder Issue for a dependency
// whose target is not a local issue (cross-prefix, external, or not-yet-synced).
// It carries only the ID so the edge stays visible in dependency listings; the
// title marks it unresolved so callers/users can tell it is not a local row.
func unresolvedDepTargetIssue(id string) types.Issue {
	return types.Issue{
		ID:     id,
		Title:  "(unresolved: external or cross-prefix)",
		Status: types.StatusOpen,
	}
}

// GetDependentsWithMetadataInTx returns issues that depend on the given issueID
// along with the dependency type. Works within an existing transaction.
//
//nolint:gosec // G201: table names come from WispTableRouting (hardcoded constants)
func GetDependentsWithMetadataInTx(ctx context.Context, tx DBTX, issueID string) ([]*types.IssueWithDependencyMetadata, error) {
	type depMeta struct {
		depID, depType string
	}

	// Query both dependency tables to find all dependents.
	var deps []depMeta
	for _, depTable := range []string{"dependencies", "wisp_dependencies"} {
		rows, err := tx.QueryContext(ctx, fmt.Sprintf(
			`SELECT issue_id, type FROM %s WHERE %s = ?`, depTable, DepTargetExpr), issueID)
		if err != nil {
			return nil, fmt.Errorf("get dependents from %s: %w", depTable, err)
		}
		for rows.Next() {
			var d depMeta
			if scanErr := rows.Scan(&d.depID, &d.depType); scanErr != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("get dependents: scan: %w", scanErr)
			}
			deps = append(deps, d)
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("get dependents: rows from %s: %w", depTable, err)
		}
	}

	if len(deps) == 0 {
		return nil, nil
	}

	// Fetch all dependent issues.
	ids := make([]string, len(deps))
	for i, d := range deps {
		ids[i] = d.depID
	}
	issues, err := GetIssuesByIDsInTx(ctx, tx, ids, nil)
	if err != nil {
		return nil, fmt.Errorf("get dependents: fetch issues: %w", err)
	}
	issueMap := make(map[string]*types.Issue, len(issues))
	for _, iss := range issues {
		issueMap[iss.ID] = iss
	}

	var results []*types.IssueWithDependencyMetadata
	for _, d := range deps {
		issue, ok := issueMap[d.depID]
		if !ok {
			continue
		}
		results = append(results, &types.IssueWithDependencyMetadata{
			Issue:          *issue,
			DependencyType: types.DependencyType(d.depType),
		})
	}
	return results, nil
}

// GetDependenciesInTx returns issues that the given issueID depends on.
// Queries both dependencies and wisp_dependencies tables.
//
//nolint:gosec // G201: table names come from hardcoded constants
func GetDependenciesInTx(ctx context.Context, tx *sql.Tx, issueID string) ([]*types.Issue, error) {
	var ids []string
	for _, depTable := range []string{"dependencies", "wisp_dependencies"} {
		rows, err := tx.QueryContext(ctx, fmt.Sprintf(
			`SELECT %s AS depends_on_id FROM %s WHERE issue_id = ?`, DepTargetExpr, depTable), issueID)
		if err != nil {
			return nil, fmt.Errorf("get dependencies from %s: %w", depTable, err)
		}
		for rows.Next() {
			var id string
			if scanErr := rows.Scan(&id); scanErr != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("get dependencies: scan: %w", scanErr)
			}
			ids = append(ids, id)
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("get dependencies: rows from %s: %w", depTable, err)
		}
	}

	if len(ids) == 0 {
		return nil, nil
	}

	return GetIssuesByIDsInTx(ctx, tx, ids, nil)
}

// GetDependentsInTx returns issues that depend on the given issueID.
// Queries both dependencies and wisp_dependencies tables.
//
//nolint:gosec // G201: table names come from hardcoded constants
func GetDependentsInTx(ctx context.Context, tx *sql.Tx, issueID string) ([]*types.Issue, error) {
	var ids []string
	for _, depTable := range []string{"dependencies", "wisp_dependencies"} {
		rows, err := tx.QueryContext(ctx, fmt.Sprintf(
			`SELECT issue_id FROM %s WHERE %s = ?`, depTable, DepTargetExpr), issueID)
		if err != nil {
			return nil, fmt.Errorf("get dependents from %s: %w", depTable, err)
		}
		for rows.Next() {
			var id string
			if scanErr := rows.Scan(&id); scanErr != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("get dependents: scan: %w", scanErr)
			}
			ids = append(ids, id)
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("get dependents: rows from %s: %w", depTable, err)
		}
	}

	if len(ids) == 0 {
		return nil, nil
	}

	return GetIssuesByIDsInTx(ctx, tx, ids, nil)
}
