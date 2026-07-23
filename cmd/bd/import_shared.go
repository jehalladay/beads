package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/utils"
	"github.com/steveyegge/beads/internal/validation"
)

// ImportOptions configures import behavior.
type ImportOptions struct {
	DryRun                     bool
	SkipUpdate                 bool
	Strict                     bool
	RenameOnImport             bool
	ClearDuplicateExternalRefs bool
	OrphanHandling             string
	DeletionIDs                []string
	SkipPrefixValidation       bool
	ProtectLocalExportIDs      map[string]time.Time
	// ConflictSkip makes the import insert-if-new instead of UPSERT: an
	// issue whose ID already exists is left untouched. Set only by the
	// auto-import upgrade-recovery fallback (GH#3955); explicit `bd import`
	// leaves this false and keeps UPSERT semantics.
	ConflictSkip bool
	// AllowStale imports rows even when their updated_at is older than the
	// local issue's, overwriting newer local state. Required for the
	// restore-an-older-snapshot recovery workflow, which the default stale
	// guard otherwise silently no-ops per row (bd-6dnrw.9). Only settable
	// via explicit `bd import --allow-stale`; auto-import paths never set it.
	AllowStale bool
	// PresentFieldsByID maps an incoming issue ID to the set of JSON field
	// names that were literally present on its source JSONL line (beads-w258p).
	// It is the per-line presence signal the parsed *types.Issue can no longer
	// carry: SetDefaults() has already run, so an absent non-omitempty field
	// (e.g. priority, a plain int) is indistinguishable from an explicit zero
	// in the struct. When this map has an entry for an issue that ALSO exists
	// locally, restoreAbsentFieldsFromLocal carries the local value forward for
	// every rewritten column the line omitted — making scalars/priority match
	// the preserve-on-absent behavior labels already have. A nil map (or a nil
	// per-issue entry) means "presence unknown" → full replace, unchanged
	// legacy behavior for callers that do not track it (bootstrap/init/auto-
	// import all operate on full-fidelity exports where nothing is absent).
	PresentFieldsByID map[string]map[string]bool
}

// ImportResult describes what an import operation did.
type ImportResult struct {
	Created int
	// Processed is the total number of DISTINCT rows the batch landed
	// (genuinely-new + updated + tie-kept), i.e. len(ImportedIDs). It is the
	// "how many rows did this import touch" count that human output and the
	// importFromLocalJSONL return value use; Created is the strict newly-created
	// subset (beads-y2y8). Keep them separate — created ⊆ processed.
	Processed int
	Updated   int
	// Unchanged lists existing local issues whose incoming row was byte-for-byte
	// identical (a no-op re-import): the upsert landed them but nothing changed,
	// so they are excluded from Created to keep the created/updated/tie_kept/
	// skipped partition honest (beads-fkzvk).
	Unchanged           []string
	Skipped             int
	Deleted             int
	Collisions          int
	IDMapping           map[string]string
	CollisionIDs        []string
	PrefixMismatch      bool
	ExpectedPrefix      string
	MismatchPrefixes    map[string]int
	ImportedIDs         []string
	StaleSkippedIDs     []string
	SkippedDependencies []string
	// UpdatedIssues lists existing local issues whose row the import
	// rewrote (incoming strictly newer, content differs), with a
	// field-level summary, so reverts of local state are visible instead
	// of silent (bd-hj85c).
	UpdatedIssues []ImportChange
	// TieKeptLocalIDs lists incoming rows whose updated_at equals the
	// local issue's but whose content differs. The upsert keeps the local
	// row for these (second-granularity timestamp ties, bd-hj85c); their
	// aux data still merges.
	TieKeptLocalIDs []string
	// InvalidMetadataIDs lists incoming rows skipped because their metadata is
	// not a JSON object (array/scalar). The metadata column is object-only by
	// design (JSON DEFAULT (JSON_OBJECT())); a non-object value would import
	// verbatim then permanently edit-lock the bead — every metadata edit path
	// unmarshals into a JSON object and hard-errors otherwise (beads-ef2k). So
	// such rows are skipped-and-reported rather than silently poisoning the
	// bead (beads-od9b), mirroring create/update's metadataIsJSONObject reject.
	InvalidMetadataIDs []string
	// ParentDemoteReverted lists incoming rows whose issue_type would have
	// demoted an auto-closing parent (epic/molecule/wisp) with open children to
	// a non-auto-closing type, bypassing the close-guard family (beads-ts7vq).
	// The type change was reverted to the local value (all other fields still
	// import); the ids are reported so the silent close-guard bypass is visible.
	ParentDemoteReverted []string
	// MetadataKeysDropped lists incoming rows whose metadata object OMITS one or
	// more top-level keys that the local issue currently has, so the verbatim
	// import REPLACE silently drops them (beads-85nml). This is the twin
	// divergence with `bd update --metadata`, which does a shallow top-level
	// MERGE (MergeMetadataWithCAS) so unlisted keys survive — an export -> edit
	// (remove a key) -> import round-trip drops the unlisted keys at RC=0 with a
	// success line. Import is a full-state REPLACE by design (import.go), so the
	// drop is NOT reverted (that would be a design-gated behavior flip); instead
	// the ids are reported so the otherwise-silent key loss is visible, mirroring
	// the InvalidMetadataIDs / SkippedDependencies skip-and-report idiom.
	MetadataKeysDropped []string
	// ParentCloseReverted lists incoming rows whose status would have CLOSED an
	// auto-closing parent (epic/molecule/wisp) that still has open children,
	// bypassing the close-guard family on the STATUS axis (beads-1h993, axis B
	// of ts7vq). The status change was reverted to the local value (all other
	// fields still import); the ids are reported so the silent bypass is
	// visible. This is the import twin of the countEpicOpenChildren guard on
	// `bd close` (close.go) and `bd update --status closed` (update.go, zgku).
	ParentCloseReverted []string
}

// ImportChange describes how an import row modified an existing local issue.
type ImportChange struct {
	ID      string `json:"id"`
	Changes string `json:"changes,omitempty"`
}

// importIssuesCore imports issues into the Dolt store.
// This is a bridge function that delegates to the Dolt store's batch creation.
func importIssuesCore(ctx context.Context, _ string, store storage.DoltStorage, issues []*types.Issue, opts ImportOptions) (*ImportResult, error) {
	if len(issues) == 0 {
		return &ImportResult{Skipped: len(issues)}, nil
	}
	// beads-x7946: DryRun must NOT bail before classification — the old early
	// return here reported a useless all-Skipped result, and the `bd import
	// --dry-run` CLI branch never even set opts.DryRun (it counted every row as
	// a creation). We now run every read-only step (ID/metadata validation, the
	// stale filter / allow-stale plan, absent-field restore) exactly as a real
	// import, and skip ONLY the batch write below (guarded by !opts.DryRun), so
	// the preview returns the true created/updated/tie_kept/unchanged/skipped
	// partition instead of over-reporting created.

	// Reject malformed explicit IDs before the batch write (beads-a2jv).
	// `bd import` accepts arbitrary/hand-edited JSONL, and the storage write
	// path does no ID-format validation — the batch's only ID check is a
	// prefix HasPrefix test that lets trailing/internal whitespace through
	// ("bd-x1 " still "starts with" the prefix). Without this guard a
	// whitespace-corrupted ID lands in storage and then fails to round-trip
	// on lookup by its clean ID. ValidateIDFormat is the same guard `bd
	// create` applies (cmd/bd/create.go), so validating here brings import to
	// create-parity. Empty IDs are left alone — they are legitimately
	// generated downstream. Reject the whole import (naming offenders) rather
	// than silently writing: a malformed ID in an import file is an input
	// error the caller must fix, and `bd export` never emits one.
	var badIDs []string
	for _, issue := range issues {
		if issue == nil || issue.ID == "" {
			continue
		}
		if _, err := validation.ValidateIDFormat(issue.ID); err != nil {
			badIDs = append(badIDs, fmt.Sprintf("%q (%v)", issue.ID, err))
		}
	}
	if len(badIDs) > 0 {
		return nil, fmt.Errorf("import rejected: %d issue ID(s) have an invalid format: %s",
			len(badIDs), strings.Join(badIDs, "; "))
	}
	// Skip-and-report rows whose metadata is not a JSON object (beads-od9b).
	// The metadata column is object-only by design; a non-object value imports
	// verbatim then permanently edit-locks the bead (beads-ef2k). Filter them
	// out before the batch (matching bd import's per-row skip model) rather than
	// silently poisoning the bead or aborting the whole import.
	var invalidMetadataIDs []string
	if len(issues) > 0 {
		kept := issues[:0:0]
		for _, iss := range issues {
			if iss != nil && len(iss.Metadata) > 0 && !metadataIsJSONObject(string(iss.Metadata)) {
				invalidMetadataIDs = append(invalidMetadataIDs, iss.ID)
				continue
			}
			kept = append(kept, iss)
		}
		issues = kept
		if len(issues) == 0 {
			return &ImportResult{
				Skipped:            len(invalidMetadataIDs),
				InvalidMetadataIDs: invalidMetadataIDs,
			}, nil
		}
	}

	// Presence-aware upsert (beads-w258p): before anything reads the incoming
	// rows (change summary, stale compare, batch write), restore every
	// rewritten column the incoming JSONL line OMITTED from the local value,
	// so an update-over-existing carries forward absent fields instead of
	// clearing scalars or resetting priority to 0/P0. This makes scalars +
	// priority match labels, which already preserve-on-absent on the same
	// upsert. It runs BEFORE filterStaleImportIssues so the change summary
	// does not report a spurious "description cleared" for a field the import
	// is actually preserving. Genuinely-new issues (no local row) are
	// untouched. Callers that do not supply PresentFieldsByID keep full-replace
	// semantics — full-fidelity export lines have no absent fields, so this is
	// a no-op for bootstrap/init/auto-import.
	if len(opts.PresentFieldsByID) > 0 {
		if err := restoreAbsentFieldsFromLocal(ctx, store, issues, opts.PresentFieldsByID); err != nil {
			return nil, err
		}
	}

	// Close-guard bypass via import (beads-ts7vq): the epic/molecule-demote
	// guard (wouldRemainAutoClosingParent, cmd/bd/close.go — beads-2hkd/l7l3j)
	// is enforced only on the `bd update --type`/close command path. The import
	// upsert applies the incoming issue_type field-wise with NO demote check, so
	// an import row that flips an auto-closing parent (epic/molecule/wisp) with
	// open children to a non-auto-closing type (e.g. task) silently recreates the
	// forbidden closed-parent-with-open-child state on the next parent close —
	// even with --allow-stale, which requires no --force. This is the IMPORT
	// sibling of the 2hkd/aw9x8/b0tw family (a guard the direct-single command
	// enforces leaking on the bulk/import path). Mirror the demote invariant on
	// the import type-change: rather than aborting the whole import (import's
	// model is skip-and-report / preserve-on-absent, not all-or-nothing), leave
	// the type UNCHANGED for the offending rows and report them — matching
	// restoreAbsentFieldsFromLocal's revert-to-local posture. Runs before the
	// stale filter and the batch write so it covers both the guarded and
	// --allow-stale paths.
	demoteReverted, err := guardImportParentDemote(ctx, store, issues)
	if err != nil {
		return nil, err
	}

	// Close-guard bypass via import — STATUS axis (beads-1h993, axis B of
	// beads-ts7vq): the epic/molecule/wisp close guard (countEpicOpenChildren,
	// cmd/bd/close.go) refuses closing an auto-closing parent with open children
	// on BOTH `bd close` and `bd update --status closed` (update.go zgku). The
	// import upsert applies the incoming status field-wise with NO such check,
	// so an import row that flips an auto-closing parent (with open children)
	// to CLOSED silently plants the forbidden closed-parent-with-open-child
	// state — and unlike the type-demote axis this needs NO flag at all
	// (closed-status is the ordinary payload of any export/import round-trip).
	// Mirror the demote guard: rather than aborting, leave the status UNCHANGED
	// for the offending rows and report them, matching import's skip-and-report
	// / preserve-on-absent model. Runs before the stale filter and batch write
	// so it covers both the guarded and --allow-stale paths.
	closeReverted, err := guardImportParentClose(ctx, store, issues)
	if err != nil {
		return nil, err
	}

	// The stale guard has two halves (bd-pkim8). This pre-filter reports the
	// rows that are already known stale (StaleSkippedIDs) and keeps their
	// labels/comments/dependencies out of the batch entirely. It is a separate
	// read, though, so a local update that commits between it and the batch
	// write would slip through — RejectStaleUpserts below closes that race by
	// re-checking updated_at inside the upsert itself.
	var staleSkippedIDs []string
	var changePlan importChangePlan
	if !opts.AllowStale {
		filtered, skipped, plan, err := filterStaleImportIssues(ctx, store, issues)
		if err != nil {
			return nil, err
		}
		issues = filtered
		staleSkippedIDs = skipped
		changePlan = plan
		if len(issues) == 0 {
			// No rows land here (every row was stale-skipped), so there is no
			// metadata key-drop to report — MetadataKeysDropped stays nil.
			return &ImportResult{
				Skipped:              len(staleSkippedIDs) + len(invalidMetadataIDs),
				StaleSkippedIDs:      staleSkippedIDs,
				InvalidMetadataIDs:   invalidMetadataIDs,
				ParentDemoteReverted: demoteReverted,
				ParentCloseReverted:  closeReverted,
			}, nil
		}
	} else {
		// beads-06x87: --allow-stale skips the stale FILTER (older rows are
		// allowed to overwrite), but it must NOT skip the change-PLAN. Without
		// this, an --allow-stale overwrite of an existing issue is miscounted as
		// a creation and the documented updated_issues summary is never emitted
		// — a snapshot-restore reports zero visibility into which local rows it
		// clobbered. Classify existing-issue overwrites as Updates here (no
		// older-skip), so Created stays a true partition and updated_issues is
		// populated exactly as on the guarded path.
		plan, err := planAllowStaleChanges(ctx, store, issues)
		if err != nil {
			return nil, err
		}
		changePlan = plan
	}

	// Metadata round-trip key-loss (beads-85nml): `bd update --metadata` does a
	// shallow top-level MERGE (unlisted keys survive) but import applies the
	// incoming metadata object VERBATIM (REPLACE), so an incoming line that
	// carries a metadata object with FEWER top-level keys than the local issue
	// silently drops the omitted keys. Detect over the post-stale-filter issue
	// set (the rows that will actually land) so stale-skipped rows do not
	// false-positive, and only when the caller tracks field presence (the CLI
	// import path) so a full-replace/bootstrap caller is unaffected. Report the
	// ids — LOUD-warn, not revert: import is a full-state REPLACE by design.
	// Detection is read-only, so it runs identically on the real and dry-run
	// (preview) paths — a preview surfaces the same drop warning the real import
	// would emit.
	var metadataKeysDropped []string
	if len(opts.PresentFieldsByID) > 0 {
		metadataKeysDropped, err = detectImportMetadataKeyDrops(ctx, store, issues, opts.PresentFieldsByID)
		if err != nil {
			return nil, err
		}
	}

	var skippedDependencies []string
	skippedDependencySet := make(map[string]struct{})
	// In-txn half of the stale guard: rows the conditional upsert rejected
	// (local update committed between the pre-filter read and the batch
	// write). The transaction may retry, so dedup by ID.
	staleRejectedSet := make(map[string]struct{})
	// beads-x7946: skip ONLY the write on a dry run. Everything above (the
	// stale filter / allow-stale plan, absent-field restore, validation) is
	// read-only and already ran, so the partition below is computed from the
	// same classified changePlan a real import would apply. staleRejectedSet
	// stays empty on a preview — the in-txn stale reject is a live-write race
	// artifact the guarded path handles at write time, not something a preview
	// can (or should) predict; the pre-filter already accounts for known-stale
	// rows in staleSkippedIDs.
	if !opts.DryRun {
		err := store.CreateIssuesWithFullOptions(ctx, issues, getActorWithGit(), storage.BatchCreateOptions{
			OrphanHandling:                 storage.OrphanAllow,
			SkipPrefixValidation:           opts.SkipPrefixValidation,
			ConflictSkip:                   opts.ConflictSkip,
			RejectStaleUpserts:             !opts.AllowStale,
			SkipDependencyValidationErrors: true,
			OnSkippedDependency: func(issueID, dependsOnID, reason string) {
				skipped := fmt.Sprintf("%s -> %s: %s", issueID, dependsOnID, reason)
				if _, ok := skippedDependencySet[skipped]; ok {
					return
				}
				skippedDependencySet[skipped] = struct{}{}
				skippedDependencies = append(skippedDependencies, skipped)
			},
			OnStaleRejected: func(issueID string) {
				staleRejectedSet[issueID] = struct{}{}
			},
		})
		if err != nil {
			return nil, err
		}
	}

	// Count DISTINCT ids: the batch upsert collapses intra-batch duplicate ids
	// to a single row (first-wins), so appending once per input occurrence
	// would over-report Created (e.g. two {id:dz-1} records => "Imported 2"
	// when one row landed). Dedup by id, preserving first-seen order (beads-4sxm).
	importedIDs := make([]string, 0, len(issues))
	seenImported := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		if _, rejected := staleRejectedSet[issue.ID]; rejected {
			staleSkippedIDs = append(staleSkippedIDs, issue.ID)
			continue
		}
		if _, dup := seenImported[issue.ID]; dup {
			continue
		}
		seenImported[issue.ID] = struct{}{}
		importedIDs = append(importedIDs, issue.ID)
	}
	// Drop planned updates the in-txn guard rejected (a local update raced
	// in between the pre-filter read and the batch write).
	updatedIssues := make([]ImportChange, 0, len(changePlan.Updates))
	updatedCount := 0
	nonCreated := make(map[string]struct{}, len(changePlan.Updates)+len(changePlan.TieKeptLocal))
	for _, change := range changePlan.Updates {
		if _, rejected := staleRejectedSet[change.ID]; rejected {
			continue
		}
		updatedIssues = append(updatedIssues, change)
		updatedCount++
		nonCreated[change.ID] = struct{}{}
	}
	// Created must be a TRUE partition alongside Updated / TieKept / Skipped:
	// importedIDs is every distinct landed row (updated + tie-kept + unchanged
	// + genuinely new), so counting len(importedIDs) as Created double-counts
	// the updated, tie-kept, and unchanged ids (which also appear in Updated /
	// TieKeptLocalIDs / Unchanged). Exclude them so created + updated + tie_kept
	// + skipped partitions the batch without overlap (beads-y2y8; the unchanged
	// no-op re-import bucket is beads-fkzvk). Set-exclusion (not arithmetic
	// subtraction) keeps this correct if an id is somehow in more than one list.
	for _, id := range changePlan.TieKeptLocal {
		nonCreated[id] = struct{}{}
	}
	// Unchanged existing rows (identical no-op re-imports) also land but are
	// not creations — exclude them from Created so the partition stays honest
	// (beads-fkzvk).
	for _, id := range changePlan.Unchanged {
		nonCreated[id] = struct{}{}
	}
	createdCount := 0
	for _, id := range importedIDs {
		if _, isNonCreated := nonCreated[id]; !isNonCreated {
			createdCount++
		}
	}
	return &ImportResult{
		Created:              createdCount,
		Processed:            len(importedIDs),
		Updated:              updatedCount,
		Unchanged:            changePlan.Unchanged,
		Skipped:              len(staleSkippedIDs) + len(invalidMetadataIDs),
		ImportedIDs:          importedIDs,
		StaleSkippedIDs:      staleSkippedIDs,
		InvalidMetadataIDs:   invalidMetadataIDs,
		SkippedDependencies:  skippedDependencies,
		UpdatedIssues:        updatedIssues,
		TieKeptLocalIDs:      changePlan.TieKeptLocal,
		ParentDemoteReverted: demoteReverted,
		MetadataKeysDropped:  metadataKeysDropped,
		ParentCloseReverted:  closeReverted,
	}, nil
}

// restoredUpsertColumns and notRestoredUpsertColumns together classify EVERY
// column in issueops.IssueUpsertColumns() (the ON DUPLICATE KEY UPDATE write
// set), so a drift guard (TestRestoreAbsentColumnsCoverIssueUpsertSet_djgv8)
// fails the build when a new upsert column is added but not consciously
// handled here. This is the durable fix for the beads-djgv8 drift class: the
// restore list is a manual mirror of the upsert set, and w258p's copy fell 21
// columns behind, silently wiping them on a partial newer-ts import.
//
// These are DB COLUMN names (as in issueUpsertColumns). The restore body below
// keys off JSON tag names (the peek presence keys), which differ for exactly
// one column: timeout_ns (column) ↔ "timeout" (JSON tag).
var restoredUpsertColumns = map[string]struct{}{
	"title": {}, "description": {}, "design": {}, "acceptance_criteria": {},
	"notes": {}, "spec_id": {}, "status": {}, "priority": {}, "issue_type": {},
	"assignee": {}, "owner": {}, "estimated_minutes": {}, "due_at": {},
	"defer_until": {}, "started_at": {}, "closed_at": {}, "close_reason": {},
	"closed_by_session": {}, "external_ref": {}, "source_system": {},
	"pinned": {}, "sender": {}, "wisp_type": {}, "mol_type": {}, "work_type": {},
	"await_type": {}, "await_id": {}, "timeout_ns": {}, "waiters": {},
	"bonded_from": {},
	"event_kind":  {}, "actor": {}, "target": {}, "payload": {}, "metadata": {},
}

// notRestoredUpsertColumns are upsert columns deliberately NOT preserved on
// absence, mirroring issueUpsertColumns' own documented exclusions. content_hash
// is derived from the other fields; updated_at is the stale-guard comparison
// column (restoring it would defeat the "strictly newer wins" gate). source_repo
// is multi-repo ownership (json:"-", never on an export line, so its presence is
// meaningless). All are safe to let the incoming value stand.
var notRestoredUpsertColumns = map[string]struct{}{
	"content_hash": {}, "updated_at": {}, "source_repo": {},
}

// restoreAbsentFieldsFromLocal implements preserve-on-absent for the import
// upsert (beads-w258p). For each incoming issue that already exists locally,
// any rewritten column whose JSON field name was NOT present on the source
// JSONL line is reset to the local value, so an update carries the stored
// field forward instead of clearing it. This closes two defects: (1) an
// absent `priority` (a non-omitempty int) decoded to 0 and silently escalated
// P2/P3 → P0-critical; (2) absent scalar columns (description/notes/design/
// assignee/…) were cleared while labels were preserved — an inconsistent
// absent-field policy in the SAME upsert. presentByID carries the per-line
// presence the parsed struct can no longer express (SetDefaults has run).
//
// An explicit "priority":0 on the line still sets P0 — presence, not value,
// gates the restore, so the documented export|import round-trip (every field
// present) is byte-identical. Genuinely-new issues (no local row) are left
// exactly as parsed.
func restoreAbsentFieldsFromLocal(ctx context.Context, store storage.DoltStorage, issues []*types.Issue, presentByID map[string]map[string]bool) error {
	ids := make([]string, 0, len(issues))
	seen := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		if issue == nil || issue.ID == "" {
			continue
		}
		if _, tracked := presentByID[issue.ID]; !tracked {
			continue
		}
		if _, ok := seen[issue.ID]; ok {
			continue
		}
		seen[issue.ID] = struct{}{}
		ids = append(ids, issue.ID)
	}
	if len(ids) == 0 {
		return nil
	}

	localIssues, err := store.GetIssuesByIDs(ctx, ids)
	if err != nil {
		return fmt.Errorf("load local issues for preserve-on-absent import: %w", err)
	}
	localByID := make(map[string]*types.Issue, len(localIssues))
	for _, local := range localIssues {
		if local != nil && local.ID != "" {
			localByID[local.ID] = local
		}
	}
	if len(localByID) == 0 {
		return nil
	}

	for _, issue := range issues {
		if issue == nil || issue.ID == "" {
			continue
		}
		local, ok := localByID[issue.ID]
		if !ok {
			continue // genuinely new — nothing to preserve
		}
		present := presentByID[issue.ID]
		absent := func(field string) bool { return !present[field] }

		// beads-djgv8: the restore set must mirror issueops.issueUpsertColumns
		// (the ON DUPLICATE KEY UPDATE write set) EXACTLY — every user-mutable
		// column the upsert rewrites needs a preserve-on-absent branch, or a
		// partial newer-ts line silently wipes the ones left out. The original
		// w258p list covered only ~14 of them, so pinned/due_at/defer_until/
		// closed_at/started_at/closed_by_session/spec_id/sender/wisp_type/
		// mol_type/work_type/waiters/timeout/await_*/event_kind/actor/target/
		// payload/title were still cleared on absence. Fields are keyed by their
		// JSON tag (the peek map keys are the literal JSON field names).
		//
		// Deliberately NOT restored (mirrors issueUpsertColumns' exclusions,
		// verified not assumed): id/created_at/created_by (identity/immutable);
		// content_hash/updated_at (derived / the stale-guard comparison column);
		// ephemeral/no_history/is_template (table-routing, not in-place updated);
		// compaction_level/compacted_at/compacted_at_commit/original_size
		// (compaction-manager-owned, not user round-trip data); source_repo
		// (multi-repo ownership, not JSON-exported — json:"-").

		// Content
		if absent("title") {
			issue.Title = local.Title
		}
		if absent("description") {
			issue.Description = local.Description
		}
		if absent("design") {
			issue.Design = local.Design
		}
		if absent("acceptance_criteria") {
			issue.AcceptanceCriteria = local.AcceptanceCriteria
		}
		if absent("notes") {
			issue.Notes = local.Notes
		}
		if absent("spec_id") {
			issue.SpecID = local.SpecID
		}
		// Status & workflow. priority is the sharp edge: non-omitempty int,
		// so absent == explicit 0 in the struct — presence is the only signal.
		if absent("priority") {
			issue.Priority = local.Priority
		}
		if absent("status") {
			issue.Status = local.Status
		}
		if absent("issue_type") {
			issue.IssueType = local.IssueType
		}
		// Assignment
		if absent("assignee") {
			issue.Assignee = local.Assignee
		}
		if absent("owner") {
			issue.Owner = local.Owner
		}
		if absent("estimated_minutes") {
			issue.EstimatedMinutes = local.EstimatedMinutes
		}
		// Scheduling
		if absent("due_at") {
			issue.DueAt = local.DueAt
		}
		if absent("defer_until") {
			issue.DeferUntil = local.DeferUntil
		}
		// Lifecycle timestamps & close bookkeeping
		if absent("started_at") {
			issue.StartedAt = local.StartedAt
		}
		if absent("closed_at") {
			issue.ClosedAt = local.ClosedAt
		}
		if absent("close_reason") {
			issue.CloseReason = local.CloseReason
		}
		if absent("closed_by_session") {
			issue.ClosedBySession = local.ClosedBySession
		}
		// External integration
		if absent("external_ref") {
			issue.ExternalRef = local.ExternalRef
		}
		if absent("source_system") {
			issue.SourceSystem = local.SourceSystem
		}
		// Context / persistence markers
		if absent("pinned") {
			issue.Pinned = local.Pinned
		}
		if absent("sender") {
			issue.Sender = local.Sender
		}
		if absent("wisp_type") {
			issue.WispType = local.WispType
		}
		if absent("mol_type") {
			issue.MolType = local.MolType
		}
		if absent("work_type") {
			issue.WorkType = local.WorkType
		}
		// Gate / await condition
		if absent("await_type") {
			issue.AwaitType = local.AwaitType
		}
		if absent("await_id") {
			issue.AwaitID = local.AwaitID
		}
		if absent("timeout") {
			issue.Timeout = local.Timeout
		}
		if absent("waiters") {
			issue.Waiters = local.Waiters
		}
		// Compound lineage (beads-ijzkb): bonded_from is user-mutable (mol bond)
		// and JSON-exported, so preserve the stored lineage when the incoming
		// line omits it, same as waiters.
		if absent("bonded_from") {
			issue.BondedFrom = local.BondedFrom
		}
		// Event fields
		if absent("event_kind") {
			issue.EventKind = local.EventKind
		}
		if absent("actor") {
			issue.Actor = local.Actor
		}
		if absent("target") {
			issue.Target = local.Target
		}
		if absent("payload") {
			issue.Payload = local.Payload
		}
		// Custom metadata
		if absent("metadata") {
			issue.Metadata = local.Metadata
		}
	}
	return nil
}

// guardImportParentDemote enforces the close-guard family's demote invariant on
// the import type-change path (beads-ts7vq). For each incoming row whose
// issue_type would demote an existing auto-closing parent (epic/molecule/wisp)
// with open children to a non-auto-closing type — the same transition
// wouldRemainAutoClosingParent refuses on `bd update --type` (beads-2hkd/l7l3j)
// — it REVERTS the incoming issue_type to the local value in place so the batch
// upsert cannot recreate the forbidden closed-parent-with-open-child state, and
// returns the ids it reverted so the import can report the (otherwise silent)
// bypass. Every other field on those rows still imports; only the type demote is
// suppressed. This mirrors restoreAbsentFieldsFromLocal's revert-to-local model
// rather than aborting the whole import (import is skip-and-report, not
// all-or-nothing). Best-effort like countEpicOpenChildren: a local-lookup error
// is fatal (the import can't safely proceed without knowing local types), but a
// per-issue dependents error yields zero open children (fail-open to the normal
// import), matching the guard's error posture on the direct path.
func guardImportParentDemote(ctx context.Context, store storage.DoltStorage, issues []*types.Issue) ([]string, error) {
	// Only rows that carry an explicit issue_type could demote. Collect their
	// ids for a single local fetch.
	ids := make([]string, 0, len(issues))
	seen := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		if issue == nil || issue.ID == "" || issue.IssueType == "" {
			continue
		}
		if _, ok := seen[issue.ID]; ok {
			continue
		}
		seen[issue.ID] = struct{}{}
		ids = append(ids, issue.ID)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	localIssues, err := store.GetIssuesByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("load local issues for import demote-guard: %w", err)
	}
	localByID := make(map[string]*types.Issue, len(localIssues))
	for _, local := range localIssues {
		if local != nil && local.ID != "" {
			localByID[local.ID] = local
		}
	}
	if len(localByID) == 0 {
		return nil, nil // every row genuinely new — nothing to demote
	}

	var reverted []string
	revertedSet := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		if issue == nil || issue.ID == "" || issue.IssueType == "" {
			continue
		}
		local, ok := localByID[issue.ID]
		if !ok {
			continue // genuinely new — no existing parent to demote
		}
		// Is this a demote of an auto-closing parent to a non-auto-closing
		// type? (The transition test, not a bare type check — molecule->epic
		// stays auto-closing and is NOT a demote.) Uses the LOCAL type as the
		// source so a same-type re-import is a no-op.
		if !isAutoClosingParentType(local) || wouldRemainAutoClosingParent(local, issue.IssueType) {
			continue
		}
		if issue.IssueType == local.IssueType {
			continue // no actual change
		}
		if countEpicOpenChildren(ctx, store, local.ID) == 0 {
			continue // no open children — demote is safe (matches direct guard)
		}
		// Suppress the demote: revert the incoming type to local. All other
		// fields on this row still import.
		issue.IssueType = local.IssueType
		if _, dup := revertedSet[issue.ID]; !dup {
			revertedSet[issue.ID] = struct{}{}
			reverted = append(reverted, issue.ID)
		}
	}
	return reverted, nil
}

// detectImportMetadataKeyDrops reports incoming rows whose metadata object
// OMITS one or more top-level keys the local issue currently has (beads-85nml).
// Import applies the incoming metadata VERBATIM (a full-state REPLACE), unlike
// `bd update --metadata`, which shallow-MERGEs top-level keys — so an export ->
// edit (drop a key) -> import round-trip silently drops the omitted keys. This
// is read-only: it does NOT mutate the incoming metadata (the REPLACE is
// intentional per import.go; reverting would be a design-gated behavior flip).
// It returns the ids so the import can LOUD-warn, mirroring the
// InvalidMetadataIDs / SkippedDependencies skip-and-report idiom.
//
// A drop is reported only when ALL of:
//   - the incoming JSON line explicitly carried the "metadata" field (present),
//     so preserve-on-absent rows (handled by restoreAbsentFieldsFromLocal) and
//     bootstrap/full-replace callers that don't track presence never trip it;
//   - both the local and incoming metadata are JSON objects (non-object rows
//     are handled by the InvalidMetadataIDs skip);
//   - the local object has at least one top-level key absent from the incoming
//     object.
func detectImportMetadataKeyDrops(ctx context.Context, store storage.DoltStorage, issues []*types.Issue, presentByID map[string]map[string]bool) ([]string, error) {
	// Collect ids of rows that explicitly carry a metadata field and are not
	// genuinely new (a local lookup is only worthwhile for those).
	ids := make([]string, 0, len(issues))
	seen := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		if issue == nil || issue.ID == "" {
			continue
		}
		if present := presentByID[issue.ID]; present == nil || !present["metadata"] {
			continue // metadata absent on the line — preserve-on-absent already ran
		}
		if _, ok := seen[issue.ID]; ok {
			continue
		}
		seen[issue.ID] = struct{}{}
		ids = append(ids, issue.ID)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	localIssues, err := store.GetIssuesByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("load local issues for import metadata-drop check: %w", err)
	}
	localByID := make(map[string]*types.Issue, len(localIssues))
	for _, local := range localIssues {
		if local != nil && local.ID != "" {
			localByID[local.ID] = local
		}
	}
	if len(localByID) == 0 {
		return nil, nil // every row genuinely new — nothing to drop
	}

	var dropped []string
	droppedSet := make(map[string]struct{}, len(ids))
	for _, issue := range issues {
		if issue == nil || issue.ID == "" {
			continue
		}
		if present := presentByID[issue.ID]; present == nil || !present["metadata"] {
			continue
		}
		local, ok := localByID[issue.ID]
		if !ok {
			continue // genuinely new
		}
		localKeys := topLevelMetadataKeys(local.Metadata)
		if len(localKeys) == 0 {
			continue // local has no metadata keys to drop
		}
		incomingKeys := topLevelMetadataKeys(issue.Metadata)
		// A drop is any local key not present in the incoming object.
		droppedAny := false
		for k := range localKeys {
			if _, kept := incomingKeys[k]; !kept {
				droppedAny = true
				break
			}
		}
		if !droppedAny {
			continue
		}
		if _, dup := droppedSet[issue.ID]; !dup {
			droppedSet[issue.ID] = struct{}{}
			dropped = append(dropped, issue.ID)
		}
	}
	return dropped, nil
}

// topLevelMetadataKeys returns the set of top-level keys of a metadata blob,
// or an empty set when the blob is empty or not a JSON object (non-object
// metadata is handled separately by the InvalidMetadataIDs skip).
func topLevelMetadataKeys(raw json.RawMessage) map[string]struct{} {
	keys := make(map[string]struct{})
	if len(raw) == 0 {
		return keys
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return keys // non-object / malformed — not our concern here
	}
	for k := range obj {
		keys[k] = struct{}{}
	}
	return keys
}

// guardImportParentClose enforces the close-guard family's parent-close
// invariant on the import STATUS-change path (beads-1h993, axis B of ts7vq).
// For each incoming row whose status would transition an existing auto-closing
// parent (epic/molecule/wisp) that still has open children from non-closed to
// CLOSED — the same transition countEpicOpenChildren refuses on `bd close`
// (close.go) and `bd update --status closed` (update.go, beads-zgku) — it
// REVERTS the incoming status to the local value in place so the batch upsert
// cannot plant the forbidden closed-parent-with-open-child state, and returns
// the reverted ids so the import can report the (otherwise silent) bypass.
// Every other field on those rows still imports; only the close is suppressed.
// This mirrors guardImportParentDemote / restoreAbsentFieldsFromLocal's
// revert-to-local model rather than aborting the whole import (import is
// skip-and-report, not all-or-nothing).
//
// Scope note: like the direct guard, this keys on the parent's own type +
// its currently-committed open children. It acts only on rows that update an
// EXISTING local auto-closing parent (a genuinely-new closed parent has no
// committed children yet, so there is nothing to leave open); a subsequent
// direct close of that parent once children are added is still guarded by the
// live command path. Best-effort like countEpicOpenChildren: a local-lookup
// error is fatal (can't safely proceed without local status), a per-issue
// dependents error yields zero open children (fail-open to the normal import).
func guardImportParentClose(ctx context.Context, store storage.DoltStorage, issues []*types.Issue) ([]string, error) {
	// Only rows that carry an explicit status could close a parent. Collect
	// their ids for a single local fetch.
	ids := make([]string, 0, len(issues))
	seen := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		if issue == nil || issue.ID == "" || issue.Status == "" {
			continue
		}
		if _, ok := seen[issue.ID]; ok {
			continue
		}
		seen[issue.ID] = struct{}{}
		ids = append(ids, issue.ID)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	localIssues, err := store.GetIssuesByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("load local issues for import parent-close guard: %w", err)
	}
	localByID := make(map[string]*types.Issue, len(localIssues))
	for _, local := range localIssues {
		if local != nil && local.ID != "" {
			localByID[local.ID] = local
		}
	}
	if len(localByID) == 0 {
		return nil, nil // every row genuinely new — no existing parent to close
	}

	var reverted []string
	revertedSet := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		if issue == nil || issue.ID == "" || issue.Status == "" {
			continue
		}
		local, ok := localByID[issue.ID]
		if !ok {
			continue // genuinely new — no existing parent to close
		}
		// Only a real non-closed -> closed transition of an auto-closing parent
		// matters. Uses the LOCAL status as the source (a closed->closed
		// re-import is a no-op) and the LOCAL type (import can't demote-and-close
		// in one row: the demote guard already reverted any type change, and the
		// close invariant keys on being an auto-closing parent).
		if issue.Status != types.StatusClosed || local.Status == types.StatusClosed {
			continue
		}
		if !isAutoClosingParentType(local) {
			continue
		}
		if countEpicOpenChildren(ctx, store, local.ID) == 0 {
			continue // no open children — close is safe (matches direct guard)
		}
		// Suppress the close: revert the incoming status to local. All other
		// fields on this row still import.
		issue.Status = local.Status
		if _, dup := revertedSet[issue.ID]; !dup {
			revertedSet[issue.ID] = struct{}{}
			reverted = append(reverted, issue.ID)
		}
	}
	return reverted, nil
}

// importChangePlan reports how the import batch relates to existing local
// issues, so the import can surface what it changed instead of doing it
// silently (bd-hj85c).
type importChangePlan struct {
	// Updates lists existing issues the batch will rewrite: incoming row
	// strictly newer and row content differs.
	Updates []ImportChange
	// TieKeptLocal lists incoming rows with the same updated_at as the
	// local issue but different row content. The stale-guarded upsert keeps
	// every stored column for these (second-granularity timestamp tie),
	// while their aux data still merges.
	TieKeptLocal []string
	// Unchanged lists incoming rows that already exist locally with IDENTICAL
	// row content (empty change summary): a no-op re-import. The idempotent
	// upsert still lands them, but they are neither creations nor updates, so
	// they must be excluded from Created to keep the created/updated/tie_kept/
	// skipped partition honest (beads-fkzvk). Without this, a round-trip
	// export→import of unchanged data reports "created N" while ground truth is
	// unchanged.
	Unchanged []string
}

// planAllowStaleChanges classifies which incoming rows overwrite an existing
// local issue when --allow-stale is set (beads-06x87). It mirrors the change
// bookkeeping in filterStaleImportIssues but WITHOUT the stale-skip: under
// --allow-stale every row lands (even an older one overwriting newer local
// state), so an existing-issue overwrite whose content differs is an Update
// regardless of updated_at direction. This keeps Created a true partition and
// populates the documented updated_issues summary on the restore path. TieKept
// is not applicable here — --allow-stale never keeps local state on a tie.
func planAllowStaleChanges(ctx context.Context, store storage.DoltStorage, issues []*types.Issue) (importChangePlan, error) {
	var plan importChangePlan
	ids := make([]string, 0, len(issues))
	seen := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		if issue == nil || issue.ID == "" {
			continue
		}
		if _, ok := seen[issue.ID]; ok {
			continue
		}
		seen[issue.ID] = struct{}{}
		ids = append(ids, issue.ID)
	}
	if len(ids) == 0 {
		return plan, nil
	}

	localIssues, err := store.GetIssuesByIDs(ctx, ids)
	if err != nil {
		return plan, fmt.Errorf("check existing issues before import: %w", err)
	}
	localByID := make(map[string]*types.Issue, len(localIssues))
	for _, issue := range localIssues {
		if issue != nil && issue.ID != "" && !issue.UpdatedAt.IsZero() {
			localByID[issue.ID] = issue
		}
	}
	if len(localByID) == 0 {
		return plan, nil
	}

	// One entry per distinct id (the batch upsert collapses duplicates), so the
	// Updates list never double-counts an id repeated in the input.
	planned := make(map[string]struct{}, len(ids))
	for _, issue := range issues {
		if issue == nil || issue.ID == "" {
			continue
		}
		if _, done := planned[issue.ID]; done {
			continue
		}
		local, ok := localByID[issue.ID]
		if !ok {
			continue
		}
		if summary := importRowChangeSummary(local, issue); summary != "" {
			plan.Updates = append(plan.Updates, ImportChange{ID: issue.ID, Changes: summary})
		} else {
			// A local row exists and its content is IDENTICAL to the incoming
			// one: the --allow-stale upsert lands but is a no-op, neither a
			// creation nor an overwrite. Record it in Unchanged so it stays out
			// of Created (beads-grmih) — mirroring the guarded path's else leg
			// (beads-fkzvk). Without this an idempotent --allow-stale restore of
			// an unchanged snapshot (the common backup-restore case) reports the
			// unchanged row as created, breaking the created/updated/unchanged
			// partition beads-06x87 upholds on this path.
			plan.Unchanged = append(plan.Unchanged, issue.ID)
		}
		planned[issue.ID] = struct{}{}
	}
	return plan, nil
}

func filterStaleImportIssues(ctx context.Context, store storage.DoltStorage, issues []*types.Issue) ([]*types.Issue, []string, importChangePlan, error) {
	var plan importChangePlan
	ids := make([]string, 0, len(issues))
	seen := make(map[string]struct{}, len(issues))
	for _, issue := range issues {
		if issue == nil || issue.ID == "" {
			continue
		}
		if _, ok := seen[issue.ID]; ok {
			continue
		}
		seen[issue.ID] = struct{}{}
		ids = append(ids, issue.ID)
	}
	if len(ids) == 0 {
		return issues, nil, plan, nil
	}

	localIssues, err := store.GetIssuesByIDs(ctx, ids)
	if err != nil {
		return nil, nil, plan, fmt.Errorf("check existing issues before import: %w", err)
	}
	localByID := make(map[string]*types.Issue, len(localIssues))
	for _, issue := range localIssues {
		if issue != nil && issue.ID != "" && !issue.UpdatedAt.IsZero() {
			localByID[issue.ID] = issue
		}
	}
	if len(localByID) == 0 {
		return issues, nil, plan, nil
	}

	filtered := make([]*types.Issue, 0, len(issues))
	skippedIDs := make([]string, 0)
	for _, issue := range issues {
		if issue == nil || issue.ID == "" || issue.UpdatedAt.IsZero() {
			filtered = append(filtered, issue)
			continue
		}
		local, ok := localByID[issue.ID]
		if !ok {
			filtered = append(filtered, issue)
			continue
		}
		// Compare at second granularity: updated_at is DATETIME(0) in the
		// store, so a sub-second component on the JSONL side must not turn
		// a tie into a spurious "newer" classification.
		incomingAt := issue.UpdatedAt.UTC().Truncate(time.Second)
		localAt := local.UpdatedAt.UTC().Truncate(time.Second)
		if incomingAt.Before(localAt) {
			skippedIDs = append(skippedIDs, issue.ID)
			continue
		}
		if summary := importRowChangeSummary(local, issue); summary != "" {
			if incomingAt.Equal(localAt) {
				plan.TieKeptLocal = append(plan.TieKeptLocal, issue.ID)
			} else {
				plan.Updates = append(plan.Updates, ImportChange{ID: issue.ID, Changes: summary})
			}
		} else {
			// A local row exists and its content is IDENTICAL to the incoming
			// one: the upsert is a no-op. It is neither a creation nor an
			// update/tie-conflict, so record it explicitly (beads-fkzvk). This
			// keeps it out of Created — without it an idempotent re-import (the
			// common backup-restore / configured import.path sync case) reports
			// the unchanged row as created, breaking the y2y8 partition.
			plan.Unchanged = append(plan.Unchanged, issue.ID)
		}
		filtered = append(filtered, issue)
	}
	return filtered, skippedIDs, plan, nil
}

// importRowChangeSummary summarizes the differences between the local issue
// row and the incoming import row, restricted to the columns the import
// upsert rewrites. Returns "" when none of those fields differ. Status,
// priority, and type transitions show old → new; long-form fields are listed
// by name only.
func importRowChangeSummary(local, incoming *types.Issue) string {
	var parts []string
	if local.Status != incoming.Status {
		parts = append(parts, fmt.Sprintf("status %s → %s", local.Status, incoming.Status))
	}
	if local.Priority != incoming.Priority {
		parts = append(parts, fmt.Sprintf("priority %d → %d", local.Priority, incoming.Priority))
	}
	if local.IssueType != incoming.IssueType {
		parts = append(parts, fmt.Sprintf("type %s → %s", local.IssueType, incoming.IssueType))
	}
	if local.Assignee != incoming.Assignee {
		parts = append(parts, "assignee")
	}
	if local.Title != incoming.Title {
		parts = append(parts, "title")
	}
	if local.Description != incoming.Description {
		parts = append(parts, "description")
	}
	if local.Design != incoming.Design {
		parts = append(parts, "design")
	}
	if local.AcceptanceCriteria != incoming.AcceptanceCriteria {
		parts = append(parts, "acceptance_criteria")
	}
	if local.Notes != incoming.Notes {
		if incoming.Notes == "" {
			parts = append(parts, "notes cleared")
		} else {
			parts = append(parts, "notes")
		}
	}
	if local.CloseReason != incoming.CloseReason {
		parts = append(parts, "close_reason")
	}
	if !stringPtrEqual(local.ExternalRef, incoming.ExternalRef) {
		parts = append(parts, "external_ref")
	}
	if !intPtrEqual(local.EstimatedMinutes, incoming.EstimatedMinutes) {
		parts = append(parts, "estimate")
	}
	if string(local.Metadata) != string(incoming.Metadata) {
		parts = append(parts, "metadata")
	}
	return strings.Join(parts, ", ")
}

func stringPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func intPtrEqual(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// importLocalResult holds counts from a local JSONL import.
type importLocalResult struct {
	Issues   int
	Memories int
}

// memoryRecord represents a memory entry in the JSONL export.
type memoryRecord struct {
	Type  string `json:"_type"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

// importFromLocalJSONL imports issues (and memories) from a local JSONL file on disk
// into the Dolt store. Returns the number of issues imported and any error.
// This is a convenience wrapper around importFromLocalJSONLFull.
func importFromLocalJSONL(ctx context.Context, store storage.DoltStorage, localPath string) (int, error) {
	result, err := importFromLocalJSONLFull(ctx, store, localPath)
	if err != nil {
		return 0, err
	}
	return result.Issues, nil
}

// parseJSONLFile reads a JSONL file and returns parsed issues and config
// entries (memories). Pure function — no store I/O.
func parseJSONLFile(path string) ([]*types.Issue, map[string]string, error) {
	//nolint:gosec // G304: path from user-provided CLI argument
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read JSONL file %s: %w", path, err)
	}

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	// Allow up to 64MB per line for large descriptions
	scanner.Buffer(make([]byte, 0, 1024*1024), 64*1024*1024)
	var issues []*types.Issue
	configEntries := make(map[string]string)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// Peek at the record to check for _type field
		var peek map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &peek); err != nil {
			return nil, nil, fmt.Errorf("failed to parse JSONL line: %w", err)
		}

		// Skip the optional beads-jsonl metadata/header record.
		// Canonical exports produced by the stable-ordering /
		// git-merge convention prepend a schema+provenance line, e.g.
		// {"_schema":"beads-jsonl/1","_dolt_branch":"main",
		// "_dolt_commit":"...","_sort":"stable-v1"}. It carries no
		// _type and no issue fields; without this guard it falls
		// through to the issue path, unmarshals into an empty Issue,
		// and aborts the whole import with "validation failed for
		// issue : title is required". Identified by the _schema
		// sentinel, which real issue/memory records never carry.
		if _, isHeader := peek["_schema"]; isHeader {
			continue
		}

		// Check if this is a memory record
		if rawType, ok := peek["_type"]; ok {
			var typeStr string
			if err := json.Unmarshal(rawType, &typeStr); err == nil && typeStr == "memory" {
				var mem memoryRecord
				if err := json.Unmarshal([]byte(line), &mem); err != nil {
					return nil, nil, fmt.Errorf("failed to parse memory record: %w", err)
				}
				if mem.Key != "" && mem.Value != "" {
					configEntries[kvPrefix+memoryPrefix+mem.Key] = mem.Value
				}
				continue
			}
		}

		// Regular issue record
		var issue types.Issue
		if err := json.Unmarshal([]byte(line), &issue); err != nil {
			return nil, nil, fmt.Errorf("failed to parse issue from JSONL: %w", err)
		}
		// Skip tombstone entries: these are deleted issues exported by older
		// versions (pre-v0.50) with status "tombstone" and deleted_at set.
		// They are not valid for re-import since "tombstone" is not a real status.
		if issue.Status == "tombstone" {
			continue
		}

		// v0.35–v0.37 exported "wisp" (bool), renamed to "ephemeral" in v0.38+.
		// map old field name so the flag is preserved on import.
		if _, hasWisp := peek["wisp"]; hasWisp && !issue.Ephemeral {
			var wisp bool
			if err := json.Unmarshal(peek["wisp"], &wisp); err == nil && wisp {
				issue.Ephemeral = true
			}
		}

		issue.SetDefaults()
		issues = append(issues, &issue)
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("failed to scan JSONL: %w", err)
	}

	return issues, configEntries, nil
}

// importFromLocalJSONLFull imports issues and memories from a local JSONL file
// using UPSERT semantics (an existing issue row is overwritten). Used by the
// explicit recovery paths: `bd bootstrap` and `bd init --from-jsonl`.
func importFromLocalJSONLFull(ctx context.Context, store storage.DoltStorage, localPath string) (*importLocalResult, error) {
	return importFromLocalJSONLWithOpts(ctx, store, localPath, false)
}

// importFromLocalJSONLConflictSkip is the auto-import upgrade-recovery
// fallback (GH#3955; the fallbackImporter seam in auto_import_upgrade.go).
// It is identical to importFromLocalJSONLFull except that an issue whose ID
// already exists is left untouched instead of being overwritten, so a
// regressed emptiness guard can never clobber live rows — worst case is a
// no-op.
func importFromLocalJSONLConflictSkip(ctx context.Context, store storage.DoltStorage, localPath string) (*importLocalResult, error) {
	return importFromLocalJSONLWithOpts(ctx, store, localPath, true)
}

// importFromLocalJSONLWithOpts is the shared implementation. It detects
// memory records (lines with "_type":"memory") and imports them via
// SetConfig, while routing regular issue records through the normal path.
// conflictSkip selects insert-if-new (true) vs UPSERT (false) for issue rows.
func importFromLocalJSONLWithOpts(ctx context.Context, store storage.DoltStorage, localPath string, conflictSkip bool) (*importLocalResult, error) {
	issues, configEntries, err := parseJSONLFile(localPath)
	if err != nil {
		return nil, err
	}

	result := &importLocalResult{}

	// Import memories
	for key, value := range configEntries {
		if err := store.SetConfig(ctx, key, value); err != nil {
			return nil, fmt.Errorf("failed to import config %q: %w", key, err)
		}
		result.Memories++
	}

	// Import issues
	if len(issues) > 0 {
		// Auto-detect prefix from first issue if not already configured
		configuredPrefix, err := store.GetConfig(ctx, "issue_prefix")
		if err == nil && strings.TrimSpace(configuredPrefix) == "" {
			firstPrefix := utils.ExtractIssuePrefix(issues[0].ID)
			if firstPrefix != "" {
				if err := store.SetConfig(ctx, "issue_prefix", firstPrefix); err != nil {
					return nil, fmt.Errorf("failed to set issue_prefix from imported issues: %w", err)
				}
			}
		}

		opts := ImportOptions{
			SkipPrefixValidation: true,
			ConflictSkip:         conflictSkip,
		}
		importResult, err := importIssuesCore(ctx, "", store, issues, opts)
		if err != nil {
			return nil, err
		}
		result.Issues = importResult.Processed
	}

	return result, nil
}
