package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/beads"
	"github.com/steveyegge/beads/internal/compact"
	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// compactDisplayTitle sanitizes an issue title for the compact candidate tables
// then truncates it to the 40-col TITLE column (beads-1y75t, 7n9y sink class).
// A title can originate from an untrusted import (JSONL/markdown/SCM), so it is
// sanitized BEFORE truncation — both to strip OSC/CSI terminal-control escapes
// and so the length check operates on the visible text (truncation can never
// split a control sequence). Display-only: the stored title is untouched.
func compactDisplayTitle(title string) string {
	title = displayTitle(title)
	if len(title) > 40 {
		title = title[:37] + "..."
	}
	return title
}

var (
	compactDryRun  bool
	compactTier    int
	compactAll     bool
	compactID      string
	compactForce   bool
	compactBatch   int
	compactWorkers int
	compactStats   bool
	compactAnalyze bool
	compactApply   bool
	compactAuto    bool
	compactSummary string
	compactActor   string
	compactLimit   int
	compactDolt    bool
)

// compactNoArgs rejects positional arguments for `bd compact`. compact is
// flag-driven (its Run func reads no args[]) and destructive ("permanent
// graceful decay - original content is discarded"), and it targets a single
// issue via --id. Historically a stray positional was silently discarded, so
// `bd compact bd-42 --force` (natural muscle memory) compacted the WHOLE
// database instead of the intended issue with rc=0 (beads-jg5e). Reject it
// loudly and point at --id.
func compactNoArgs(_ *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	return fmt.Errorf("bd compact does not accept positional arguments; to compact a single issue use --id %q (see bd compact --help). Got unexpected argument %q", args[0], args[0])
}

// validateCompactMode enforces that exactly one compaction mode is selected and
// that the tier is implemented. It is pure (no I/O, no os.Exit) so callers can
// route the error through HandleErrorRespectJSON/FatalErrorRespectJSON — under
// --json that yields a structured error on stdout rather than the historical
// empty-stdout + os.Exit(1) (beads-9fww). Kept as a standalone func so the
// validation is unit-testable without invoking the destructive command body.
func validateCompactMode(analyze, apply, auto bool, tier int) error {
	activeModes := 0
	if analyze {
		activeModes++
	}
	if apply {
		activeModes++
	}
	if auto {
		activeModes++
	}
	if activeModes == 0 {
		return fmt.Errorf("must specify one mode: --analyze, --apply, or --auto")
	}
	if activeModes > 1 {
		return fmt.Errorf("cannot use multiple modes together (--analyze, --apply, --auto are mutually exclusive)")
	}
	// Only Tier 1 compaction is implemented. Reject other tiers up front with a
	// clear message rather than failing deep inside a mode.
	if tier != 1 {
		return fmt.Errorf("Tier %d compaction is not yet implemented; only --tier 1 is available", tier)
	}
	return nil
}

// validateCompactLimit rejects a negative --limit. The candidate truncation
// only fires when limit > 0, so a negative value silently compacts the FULL
// candidate set with rc=0 — the misleading false-green of the eqi4/r9hj/4djp
// negative-limit class (compact was missed by that sweep). --limit 0 is the
// documented "no limit" sentinel and positives are unchanged; only a negative
// is invalid. Kept standalone so it is unit-testable without the command body
// (mirrors validateCompactMode, beads-y55w).
func validateCompactLimit(limit int) error {
	if limit < 0 {
		return fmt.Errorf("--limit must be >= 0")
	}
	return nil
}

var compactCmd = &cobra.Command{
	Use:   "compact",
	Args:  compactNoArgs,
	Short: "Compact old closed issues to save space",
	Long: `Compact old closed issues using semantic summarization.

Compaction reduces database size by summarizing closed issues that are no longer
actively referenced. This is permanent graceful decay - original content is discarded.

Modes:
  - Analyze: Export candidates for agent review (no API key needed)
  - Apply: Accept agent-provided summary (no API key needed)
  - Auto: AI-powered compaction (requires ANTHROPIC_API_KEY or ai.api_key, legacy)
  - Dolt: Run Dolt garbage collection (for Dolt-backend repositories)

Tiers:
  - Tier 1: Semantic compression (30 days closed, 70% reduction)
  - Tier 2: Ultra compression (90 days closed) - planned, not yet implemented

Dolt Garbage Collection:
  With auto-commit per mutation, Dolt commit history grows over time. Use
  --dolt to run Dolt garbage collection and reclaim disk space.

  --dolt: Run Dolt GC on .beads/dolt directory to free disk space.
          This removes unreachable commits and compacts storage.

Examples:
  # Dolt garbage collection
  bd admin compact --dolt                  # Run Dolt GC
  bd admin compact --dolt --dry-run        # Preview without running GC

  # Agent-driven workflow (recommended)
  bd admin compact --analyze --json        # Get candidates with full content
  bd admin compact --apply --id bd-42 --summary summary.txt
  bd admin compact --apply --id bd-42 --summary - < summary.txt

  # Legacy AI-powered workflow
  bd admin compact --auto --dry-run        # Preview candidates
  bd admin compact --auto --all            # Compact all eligible issues
  bd admin compact --auto --id bd-42       # Compact specific issue

  # Statistics
  bd admin compact --stats                 # Show statistics
`,
	Run: func(_ *cobra.Command, _ []string) {
		// Block mutating operations in embedded mode; allow --stats, --analyze, --dry-run read-only paths.
		if !compactStats && !compactAnalyze && !compactDryRun {
			if err := requireServerMode("compact"); err != nil {
				// beads-t2ebq: the ONE straggler compact error path still on a
				// bare stderr FatalError — convert to FatalErrorRespectJSON so
				// the requireServerMode/embedded-mode guard emits the {error}
				// object on STDOUT under --json, matching sibling bd admin reset
				// (broz) + the other ~30 compact.go paths already on *RespectJSON.
				// Non-json behavior (plaintext stderr + os.Exit(1)) unchanged.
				FatalErrorRespectJSON("%v", err)
			}
		}
		// Compact modifies data unless --stats or --analyze or --dry-run or --dolt with --dry-run
		if !compactStats && !compactAnalyze && !compactDryRun && !(compactDolt && compactDryRun) {
			CheckReadonly("compact")
		}
		ctx := rootCtx

		// Handle compact stats first
		if compactStats {
			// beads-aocj: on hub-connected crew the global `store` is nil in
			// proxiedServerMode, so runCompactStats(nil) would panic. Route the
			// read-only stats path through the proxied UOW instead.
			if usesProxiedServer() {
				if err := runCompactStatsProxiedServer(ctx); err != nil {
					FatalErrorRespectJSON("%v", err)
				}
				return
			}
			runCompactStats(ctx, store)
			return
		}

		// Handle dolt GC mode
		if compactDolt {
			runCompactDolt()
			return
		}

		// Mode/tier validation (pure, testable — no os.Exit). Under --json this
		// must surface as structured JSON on stdout, never an empty-stdout exit
		// (beads-9fww).
		if err := validateCompactMode(compactAnalyze, compactApply, compactAuto, compactTier); err != nil {
			FatalErrorRespectJSON("%v", err)
		}

		// Reject a negative --limit up front (beads-y55w): the candidate
		// truncation only fires when compactLimit > 0, so a negative value
		// silently compacts the FULL candidate set with rc=0 — the misleading
		// false-green of the eqi4/r9hj/4djp negative-limit class. Routed through
		// FatalErrorRespectJSON so --json surfaces a structured error (beads-9fww).
		if err := validateCompactLimit(compactLimit); err != nil {
			FatalErrorRespectJSON("%v", err)
		}

		// Handle analyze mode (requires direct database access)
		if compactAnalyze {
			if err := ensureDirectMode("compact --analyze requires direct database access"); err != nil {
				FatalErrorWithHintRespectJSON(err.Error(), diagHint())
			}
			runCompactAnalyze(ctx, store)
			return
		}

		// Handle apply mode (requires direct database access)
		if compactApply {
			if err := ensureDirectMode("compact --apply requires direct database access"); err != nil {
				FatalErrorWithHintRespectJSON(err.Error(), diagHint())
			}
			if compactID == "" {
				FatalErrorRespectJSON("--apply requires --id")
			}
			if compactSummary == "" {
				FatalErrorRespectJSON("--apply requires --summary")
			}
			runCompactApply(ctx, store)
			return
		}

		// Handle auto mode (legacy)
		if compactAuto {
			// beads-aocj: --auto builds a compact.Compactor over the direct
			// store (compact.New(store,...)); the global store is nil in
			// proxiedServerMode, so guard like --analyze/--apply and fail with a
			// clean hinted error instead of a nil-store panic in runCompactSingle
			// /runCompactAll.
			if err := ensureDirectMode("compact --auto requires direct database access"); err != nil {
				FatalErrorWithHintRespectJSON(err.Error(), diagHint())
			}
			// Validation checks
			if compactID != "" && compactAll {
				FatalErrorRespectJSON("cannot use --id and --all together")
			}
			if compactForce && compactID == "" {
				FatalErrorRespectJSON("--force requires --id")
			}
			if compactID == "" && !compactAll && !compactDryRun {
				FatalErrorRespectJSON("must specify --all, --id, or --dry-run")
			}

			// Direct mode
			apiKey := os.Getenv("ANTHROPIC_API_KEY")
			if apiKey == "" {
				apiKey = config.GetString("ai.api_key")
			}
			if apiKey == "" && !compactDryRun {
				FatalErrorRespectJSON("--auto mode requires ANTHROPIC_API_KEY environment variable or ai.api_key in config")
			}

			compactCfg := &compact.Config{
				APIKey:      apiKey,
				Concurrency: compactWorkers,
				DryRun:      compactDryRun,
			}

			compactor, err := compact.New(store, apiKey, compactCfg)
			if err != nil {
				FatalErrorRespectJSON("failed to create compactor: %v", err)
			}

			if compactID != "" {
				runCompactSingle(ctx, compactor, store, compactID)
				return
			}

			runCompactAll(ctx, compactor, store)
		}
	},
}

func runCompactSingle(ctx context.Context, compactor *compact.Compactor, store storage.DoltStorage, issueID string) {
	start := time.Now()

	if !compactForce {
		eligible, reason, err := store.CheckEligibility(ctx, issueID, compactTier)
		if err != nil {
			FatalErrorRespectJSON("failed to check eligibility: %v", err)
		}
		if !eligible {
			FatalErrorRespectJSON("%s is not eligible for Tier %d compaction: %s", issueID, compactTier, reason)
		}
	}

	issue, err := store.GetIssue(ctx, issueID)
	if err != nil {
		FatalErrorRespectJSON("failed to get issue: %v", err)
	}

	originalSize := len(issue.Description) + len(issue.Design) + len(issue.Notes) + len(issue.AcceptanceCriteria)

	if compactDryRun {
		ageDays := 0
		var closedAtStr string
		if issue.ClosedAt != nil {
			ageDays = int(time.Since(*issue.ClosedAt).Hours() / 24)
			closedAtStr = issue.ClosedAt.Format(time.RFC3339)
		}

		candidate := map[string]interface{}{
			"id":           issueID,
			"title":        issue.Title,
			"closed_at":    closedAtStr,
			"age_days":     ageDays,
			"content_size": originalSize,
		}

		if jsonOutput {
			output := map[string]interface{}{
				"dry_run":    true,
				"tier":       compactTier,
				"candidates": []interface{}{candidate},
				"summary": map[string]interface{}{
					"total_candidates":    1,
					"total_content_bytes": originalSize,
				},
			}
			if err := outputJSON(output); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
			return
		}

		fmt.Printf("DRY RUN - Tier %d compaction\n\n", compactTier)
		fmt.Printf("  %-12s %-40s %8s %10s\n", "ID", "TITLE", "AGE", "SIZE")
		title := compactDisplayTitle(issue.Title)
		fmt.Printf("  %-12s %-40s %5dd %10d B\n", issueID, title, ageDays, originalSize)
		fmt.Printf("\nSummary: 1 candidate, %d bytes total content\n", originalSize)
		return
	}

	var compactErr error
	if compactTier == 1 {
		compactErr = compactor.CompactTier1(ctx, issueID)
	} else {
		FatalErrorRespectJSON("Tier 2 compaction not yet implemented")
	}

	if compactErr != nil {
		FatalErrorRespectJSON("%v", compactErr)
	}

	issue, err = store.GetIssue(ctx, issueID)
	if err != nil {
		FatalErrorRespectJSON("failed to get updated issue: %v", err)
	}

	compactedSize := len(issue.Description)
	savingBytes := originalSize - compactedSize
	elapsed := time.Since(start)

	if jsonOutput {
		output := map[string]interface{}{
			"success":        true,
			"tier":           compactTier,
			"issue_id":       issueID,
			"original_size":  originalSize,
			"compacted_size": compactedSize,
			"saved_bytes":    savingBytes,
			"reduction_pct":  float64(savingBytes) / float64(originalSize) * 100,
			"elapsed_ms":     elapsed.Milliseconds(),
		}
		if err := outputJSON(output); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		return
	}

	fmt.Printf("✓ Compacted %s (Tier %d)\n", issueID, compactTier)
	fmt.Printf("  %d → %d bytes (saved %d, %.1f%%)\n",
		originalSize, compactedSize, savingBytes,
		float64(savingBytes)/float64(originalSize)*100)
	fmt.Printf("  Time: %v\n", elapsed)
}

func runCompactAll(ctx context.Context, compactor *compact.Compactor, store storage.DoltStorage) {
	start := time.Now()

	var candidates []string
	if compactTier == 1 {
		tier1, err := store.GetTier1Candidates(ctx)
		if err != nil {
			FatalErrorRespectJSON("failed to get candidates: %v", err)
		}
		for _, c := range tier1 {
			candidates = append(candidates, c.IssueID)
		}
	} else {
		tier2, err := store.GetTier2Candidates(ctx)
		if err != nil {
			FatalErrorRespectJSON("failed to get candidates: %v", err)
		}
		for _, c := range tier2 {
			candidates = append(candidates, c.IssueID)
		}
	}

	if len(candidates) == 0 {
		if jsonOutput {
			if err := outputJSON(map[string]interface{}{
				"success": true,
				"count":   0,
				"message": "No eligible candidates",
			}); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
			return
		}
		fmt.Println("No eligible candidates for compaction")
		return
	}

	if compactDryRun {
		type dryRunCandidate struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			ClosedAt    string `json:"closed_at"`
			AgeDays     int    `json:"age_days"`
			ContentSize int    `json:"content_size"`
		}

		var dryCandidates []dryRunCandidate
		totalSize := 0
		for _, id := range candidates {
			issue, err := store.GetIssue(ctx, id)
			if err != nil {
				continue
			}
			contentSize := len(issue.Description) + len(issue.Design) + len(issue.Notes) + len(issue.AcceptanceCriteria)
			totalSize += contentSize

			ageDays := 0
			var closedAtStr string
			if issue.ClosedAt != nil {
				ageDays = int(time.Since(*issue.ClosedAt).Hours() / 24)
				closedAtStr = issue.ClosedAt.Format(time.RFC3339)
			}

			dryCandidates = append(dryCandidates, dryRunCandidate{
				ID:          issue.ID,
				Title:       issue.Title,
				ClosedAt:    closedAtStr,
				AgeDays:     ageDays,
				ContentSize: contentSize,
			})
		}

		if jsonOutput {
			output := map[string]interface{}{
				"dry_run":    true,
				"tier":       compactTier,
				"candidates": dryCandidates,
				"summary": map[string]interface{}{
					"total_candidates":    len(dryCandidates),
					"total_content_bytes": totalSize,
				},
			}
			if err := outputJSON(output); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
			return
		}

		fmt.Printf("DRY RUN - Tier %d compaction\n\n", compactTier)
		fmt.Printf("  %-12s %-40s %8s %10s\n", "ID", "TITLE", "AGE", "SIZE")
		for _, c := range dryCandidates {
			title := compactDisplayTitle(c.Title)
			fmt.Printf("  %-12s %-40s %5dd %10d B\n", c.ID, title, c.AgeDays, c.ContentSize)
		}
		fmt.Printf("\nSummary: %d candidates, %d bytes total content\n", len(dryCandidates), totalSize)
		return
	}

	if !jsonOutput {
		fmt.Printf("Compacting %d issues (Tier %d)...\n\n", len(candidates), compactTier)
	}

	results, err := compactor.CompactTier1Batch(ctx, candidates)
	if err != nil {
		FatalErrorRespectJSON("batch compaction failed: %v", err)
	}

	// beads-jxe6a: summarize via a pure helper so the success flag, counts and
	// per-issue failure list are computed in one testable place. Before this
	// fix the batch --json output hardcoded "success": true and returned rc0
	// even when CompactTier1Batch reported non-fatal per-issue failures
	// (BatchResult.Err) — a self-contradicting {success:true, failed:N}
	// envelope automation could not act on.
	summary := summarizeCompactBatch(results)

	if !jsonOutput {
		for i := range results {
			fmt.Printf("[%s] %d/%d\r", progressBar(i+1, len(results)), i+1, len(results))
		}
	}

	elapsed := time.Since(start)

	if jsonOutput {
		output := map[string]interface{}{
			"success":       summary.Success,
			"tier":          compactTier,
			"total":         len(results),
			"succeeded":     summary.Succeeded,
			"failed":        summary.Failed,
			"saved_bytes":   summary.SavedBytes,
			"original_size": summary.OriginalSize,
			"elapsed_ms":    elapsed.Milliseconds(),
		}
		if len(summary.Failures) > 0 {
			output["failures"] = summary.Failures
		}
		if err := outputJSON(output); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		// beads-jxe6a: a partial batch must exit non-zero so automation can
		// detect it — matching the batch partial-failure contract (beads-uctf/
		// z8z9) and this file's own post-output os.Exit(1) precedent (dolt gc).
		// The stdout JSON document is already complete above.
		if !summary.Success {
			os.Exit(1)
		}
		return
	}

	fmt.Printf("\n\nCompleted in %v\n\n", elapsed)
	fmt.Printf("Summary:\n")
	fmt.Printf("  Succeeded: %d\n", summary.Succeeded)
	fmt.Printf("  Failed: %d\n", summary.Failed)
	if summary.OriginalSize > 0 {
		fmt.Printf("  Saved: %d bytes (%.1f%%)\n", summary.SavedBytes, float64(summary.SavedBytes)/float64(summary.OriginalSize)*100)
	}
	// beads-jxe6a: the human path silently returned rc0 on partial failure too;
	// align it with the --json exit code.
	if !summary.Success {
		os.Exit(1)
	}
}

// compactBatchFailure names an issue whose batch compaction failed, with its
// error string (beads-jxe6a). It is exported into the --json output under
// "failures" so a consumer can act on WHICH issues failed, not just a count.
type compactBatchFailure struct {
	IssueID string `json:"issue_id"`
	Error   string `json:"error"`
}

// compactBatchSummary is the outcome of a `bd compact --all` batch.
type compactBatchSummary struct {
	Success      bool
	Succeeded    int
	Failed       int
	SavedBytes   int
	OriginalSize int
	Failures     []compactBatchFailure
}

// summarizeCompactBatch reduces the per-issue BatchResults into the batch
// outcome (beads-jxe6a). Success is TRUE only when no issue failed — the batch
// --json path previously hardcoded success:true regardless of failCount, so a
// partial batch reported {success:true, failed:N} at rc0 and a structured
// consumer could not detect the failed compactions. Pure (no I/O) so the teeth
// can mutation-verify the success/exit mapping without a live store.
func summarizeCompactBatch(results []compact.BatchResult) compactBatchSummary {
	var s compactBatchSummary
	for _, result := range results {
		if result.Err != nil {
			s.Failed++
			s.Failures = append(s.Failures, compactBatchFailure{IssueID: result.IssueID, Error: result.Err.Error()})
		} else {
			s.Succeeded++
			s.OriginalSize += result.OriginalSize
			s.SavedBytes += result.OriginalSize - result.CompactedSize
		}
	}
	s.Success = s.Failed == 0
	return s
}

func runCompactStats(ctx context.Context, store storage.DoltStorage) {
	tier1, err := store.GetTier1Candidates(ctx)
	if err != nil {
		FatalErrorRespectJSON("failed to get Tier 1 candidates: %v", err)
	}

	tier2, err := store.GetTier2Candidates(ctx)
	if err != nil {
		FatalErrorRespectJSON("failed to get Tier 2 candidates: %v", err)
	}

	// Shared with the proxied path (runCompactStatsProxiedServer) so both render
	// identically (beads-aocj).
	renderCompactStats(tier1, tier2)
}

func runCompactAnalyze(ctx context.Context, store storage.DoltStorage) {
	type Candidate struct {
		ID                 string `json:"id"`
		Title              string `json:"title"`
		Description        string `json:"description"`
		Design             string `json:"design"`
		Notes              string `json:"notes"`
		AcceptanceCriteria string `json:"acceptance_criteria"`
		SizeBytes          int    `json:"size_bytes"`
		AgeDays            int    `json:"age_days"`
		Tier               int    `json:"tier"`
		Compacted          bool   `json:"compacted"`
	}

	// beads-vdaym: non-nil empty slice so the --analyze --json path emits
	// "candidates":[] (not null) on an empty candidate set — twin-invariant
	// member of the tamf/5fv3/nqv0/jbwv nil-slice->[] family. The populated
	// path (line ~319) already emits an array.
	candidates := []Candidate{}

	// Single issue mode
	if compactID != "" {
		issue, err := store.GetIssue(ctx, compactID)
		if err != nil {
			FatalErrorRespectJSON("failed to get issue: %v", err)
		}

		sizeBytes := len(issue.Description) + len(issue.Design) + len(issue.Notes) + len(issue.AcceptanceCriteria)
		ageDays := 0
		if issue.ClosedAt != nil {
			ageDays = int(time.Since(*issue.ClosedAt).Hours() / 24)
		}

		candidates = append(candidates, Candidate{
			ID:                 issue.ID,
			Title:              issue.Title,
			Description:        issue.Description,
			Design:             issue.Design,
			Notes:              issue.Notes,
			AcceptanceCriteria: issue.AcceptanceCriteria,
			SizeBytes:          sizeBytes,
			AgeDays:            ageDays,
			Tier:               compactTier,
			Compacted:          issue.CompactionLevel > 0,
		})
	} else {
		// Get tier candidates
		var tierCandidates []*types.CompactionCandidate
		var err error
		if compactTier == 1 {
			tierCandidates, err = store.GetTier1Candidates(ctx)
		} else {
			tierCandidates, err = store.GetTier2Candidates(ctx)
		}
		if err != nil {
			FatalErrorRespectJSON("failed to get candidates: %v", err)
		}

		// Apply limit if specified
		if compactLimit > 0 && len(tierCandidates) > compactLimit {
			tierCandidates = tierCandidates[:compactLimit]
		}

		// Fetch full details for each candidate
		for _, c := range tierCandidates {
			issue, err := store.GetIssue(ctx, c.IssueID)
			if err != nil {
				continue // Skip issues we can't fetch
			}

			ageDays := int(time.Since(c.ClosedAt).Hours() / 24)

			candidates = append(candidates, Candidate{
				ID:                 issue.ID,
				Title:              issue.Title,
				Description:        issue.Description,
				Design:             issue.Design,
				Notes:              issue.Notes,
				AcceptanceCriteria: issue.AcceptanceCriteria,
				SizeBytes:          c.OriginalSize,
				AgeDays:            ageDays,
				Tier:               compactTier,
				Compacted:          issue.CompactionLevel > 0,
			})
		}
	}

	if jsonOutput {
		totalSize := 0
		for _, c := range candidates {
			totalSize += c.SizeBytes
		}
		output := map[string]interface{}{
			"candidates": candidates,
			"summary": map[string]interface{}{
				"total_candidates":    len(candidates),
				"total_content_bytes": totalSize,
			},
		}
		if err := outputJSON(output); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		return
	}

	// Human-readable output
	fmt.Printf("Compaction Candidates (Tier %d)\n\n", compactTier)
	fmt.Printf("  %-12s %-40s %8s %10s\n", "ID", "TITLE", "AGE", "SIZE")
	totalSize := 0
	for _, c := range candidates {
		compactStatus := ""
		if c.Compacted {
			compactStatus = " *"
		}
		title := compactDisplayTitle(c.Title)
		fmt.Printf("  %-12s %-40s %5dd %10d B%s\n", c.ID, title, c.AgeDays, c.SizeBytes, compactStatus)
		totalSize += c.SizeBytes
	}
	fmt.Printf("\nSummary: %d candidates, %d bytes total content\n", len(candidates), totalSize)
}

func runCompactApply(ctx context.Context, store storage.DoltStorage) {
	start := time.Now()

	// Read summary
	var summaryBytes []byte
	var err error
	if compactSummary == "-" {
		// Read from stdin
		summaryBytes, err = readAllLimited(os.Stdin, "summary")
		if err != nil {
			FatalErrorRespectJSON("failed to read summary from stdin: %v", err)
		}
	} else {
		// #nosec G304 -- summary file path provided explicitly by operator
		summaryBytes, err = os.ReadFile(compactSummary)
		if err != nil {
			FatalErrorRespectJSON("failed to read summary file: %v", err)
		}
	}
	summary := string(summaryBytes)

	// Get issue
	issue, err := store.GetIssue(ctx, compactID)
	if err != nil {
		FatalErrorRespectJSON("failed to get issue: %v", err)
	}

	// Calculate sizes
	originalSize := len(issue.Description) + len(issue.Design) + len(issue.Notes) + len(issue.AcceptanceCriteria)
	compactedSize := len(summary)

	// Check eligibility unless --force
	if !compactForce {
		eligible, reason, err := store.CheckEligibility(ctx, compactID, compactTier)
		if err != nil {
			FatalErrorRespectJSON("failed to check eligibility: %v", err)
		}
		if !eligible {
			FatalErrorWithHintRespectJSON(fmt.Sprintf("%s is not eligible for Tier %d compaction: %s", compactID, compactTier, reason), "use --force to bypass eligibility checks")
		}

		// Enforce size reduction unless --force
		if compactedSize >= originalSize {
			FatalErrorWithHintRespectJSON(fmt.Sprintf("summary (%d bytes) is not shorter than original (%d bytes)", compactedSize, originalSize), "use --force to bypass size validation")
		}
	}

	// Apply compaction
	actor := compactActor
	if actor == "" {
		actor = "agent"
	}

	// Archive the original content BEFORE the destructive overwrite so the
	// compaction is reversible (bd restore --apply reads this snapshot). The
	// --auto path (compact.CompactTier1) already does this; --apply must too, or
	// the advertised undo hard-fails "no archived snapshot" and the original
	// design/notes/acceptance text is lost (beads-zh1r). If archiving fails we
	// abort with the original content intact rather than silently destroy it.
	if err := store.SnapshotIssue(ctx, compactID, compactTier); err != nil {
		FatalErrorRespectJSON("failed to archive pre-compaction snapshot: %v", err)
	}

	updates := map[string]interface{}{
		"description":         summary,
		"design":              "",
		"notes":               "",
		"acceptance_criteria": "",
	}

	// Overwrite + mark compaction ATOMICALLY (one tx, beads-pj38): a failure
	// between the overwrite and the mark used to leave text compacted while
	// compaction_level stayed 0. The snapshot above is the recovery anchor.
	commitHash := compact.GetCurrentCommitHash()
	if err := store.CompactOverwrite(ctx, compactID, updates, compactTier, originalSize, commitHash, actor); err != nil {
		FatalErrorRespectJSON("failed to overwrite+mark compaction: %v", err)
	}

	savingBytes := originalSize - compactedSize
	reductionPct := float64(savingBytes) / float64(originalSize) * 100
	eventData := fmt.Sprintf("Tier %d compaction: %d → %d bytes (saved %d, %.1f%%)", compactTier, originalSize, compactedSize, savingBytes, reductionPct)
	// beads-4yi7 (CLI sibling of beads-ezng): this event comment is a COSMETIC
	// post-log — CompactOverwrite above already committed the overwrite +
	// compaction mark durably (beads-pj38). A FatalErrorRespectJSON here would
	// os.Exit(1) for an issue that WAS successfully compacted (false failure; a
	// retry then hits the eligibility/size skip). Warn and continue to the
	// success output instead — mirroring promote.go's post-commit comment
	// handling and the "durable state committed = success" contract.
	if err := store.AddComment(ctx, compactID, actor, eventData); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %s compacted successfully but recording the compaction event comment failed: %v\n", compactID, err)
	}

	elapsed := time.Since(start)

	if jsonOutput {
		output := map[string]interface{}{
			"success":        true,
			"issue_id":       compactID,
			"tier":           compactTier,
			"original_size":  originalSize,
			"compacted_size": compactedSize,
			"saved_bytes":    savingBytes,
			"reduction_pct":  reductionPct,
			"elapsed_ms":     elapsed.Milliseconds(),
		}
		if err := outputJSON(output); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		return
	}

	fmt.Printf("✓ Compacted %s (Tier %d)\n", compactID, compactTier)
	fmt.Printf("  %d → %d bytes (saved %d, %.1f%%)\n", originalSize, compactedSize, savingBytes, reductionPct)
	fmt.Printf("  Time: %v\n", elapsed)
}

// runCompactDolt runs Dolt garbage collection on the .beads/dolt directory
func runCompactDolt() {
	start := time.Now()

	// Find beads directory
	beadsDir := beads.FindBeadsDir()
	if beadsDir == "" {
		FatalErrorWithHint(activeWorkspaceNotFoundError(), diagHint())
	}

	// Check for dolt directory
	doltPath := filepath.Join(beadsDir, "dolt")
	if _, err := os.Stat(doltPath); os.IsNotExist(err) {
		if compactDryRun {
			if jsonOutput {
				output := map[string]interface{}{
					"dry_run":   true,
					"dolt_path": doltPath,
					"available": false,
				}
				if err := outputJSON(output); err != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				}
				return
			}
			fmt.Printf("DRY RUN - Dolt garbage collection\n\n")
			fmt.Printf("Dolt directory: %s\n", doltPath)
			fmt.Printf("No local Dolt directory found; nothing to collect.\n")
			return
		}
		FatalErrorWithHintRespectJSON(fmt.Sprintf("Dolt directory not found at %s", doltPath), "--dolt flag is only for repositories using the Dolt backend")
	}

	// Get size before GC
	sizeBefore, err := getDirSize(doltPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not calculate directory size: %v\n", err)
		sizeBefore = 0
	}

	if compactDryRun {
		if jsonOutput {
			output := map[string]interface{}{
				"dry_run":      true,
				"dolt_path":    doltPath,
				"size_before":  sizeBefore,
				"size_display": formatBytes(sizeBefore),
			}
			if err := outputJSON(output); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
			return
		}
		fmt.Printf("DRY RUN - Dolt garbage collection\n\n")
		fmt.Printf("Dolt directory: %s\n", doltPath)
		fmt.Printf("Current size: %s\n", formatBytes(sizeBefore))
		fmt.Printf("\nRun without --dry-run to perform garbage collection.\n")
		return
	}

	// Check if dolt command is available
	if _, err := exec.LookPath("dolt"); err != nil {
		FatalErrorWithHintRespectJSON("dolt command not found in PATH", "install Dolt from https://github.com/dolthub/dolt")
	}

	if !jsonOutput {
		fmt.Printf("Running Dolt garbage collection...\n")
	}

	// Run dolt gc
	cmd := exec.Command("dolt", "gc") // #nosec G204 -- fixed command, no user input
	cmd.Dir = doltPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: dolt gc failed: %v\n", err)
		if len(output) > 0 {
			fmt.Fprintf(os.Stderr, "Output: %s\n", string(output))
		}
		os.Exit(1)
	}

	// Get size after GC
	sizeAfter, err := getDirSize(doltPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not calculate directory size after GC: %v\n", err)
		sizeAfter = 0
	}

	elapsed := time.Since(start)
	freed := sizeBefore - sizeAfter
	if freed < 0 {
		freed = 0 // GC may not always reduce size
	}

	if jsonOutput {
		result := map[string]interface{}{
			"success":       true,
			"dolt_path":     doltPath,
			"size_before":   sizeBefore,
			"size_after":    sizeAfter,
			"freed_bytes":   freed,
			"freed_display": formatBytes(freed),
			"elapsed_ms":    elapsed.Milliseconds(),
		}
		if err := outputJSON(result); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		return
	}

	fmt.Printf("✓ Dolt garbage collection complete\n")
	fmt.Printf("  %s → %s (freed %s)\n", formatBytes(sizeBefore), formatBytes(sizeAfter), formatBytes(freed))
	fmt.Printf("  Time: %v\n", elapsed)
}

// getDirSize calculates the total size of a directory recursively
func getDirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}

// formatBytes formats a byte count as a human-readable string
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// progressBar renders a text-based progress bar.
func progressBar(current, total int) string {
	const width = 40
	if total == 0 {
		return "[" + string(make([]byte, width)) + "]"
	}
	filled := (current * width) / total
	bar := ""
	for i := 0; i < width; i++ {
		if i < filled {
			bar += "█"
		} else {
			bar += " "
		}
	}
	return "[" + bar + "]"
}

func init() {
	compactCmd.Flags().BoolVar(&compactDryRun, "dry-run", false, "Preview without compacting")
	compactCmd.Flags().IntVar(&compactTier, "tier", 1, "Compaction tier (only tier 1 is implemented)")
	compactCmd.Flags().BoolVar(&compactAll, "all", false, "Process all candidates")
	compactCmd.Flags().StringVar(&compactID, "id", "", "Compact specific issue")
	compactCmd.Flags().BoolVar(&compactForce, "force", false, "Force compact (bypass checks, requires --id)")
	compactCmd.Flags().IntVar(&compactBatch, "batch-size", 10, "Issues per batch")
	compactCmd.Flags().IntVar(&compactWorkers, "workers", 5, "Parallel workers")
	compactCmd.Flags().BoolVar(&compactStats, "stats", false, "Show compaction statistics")
	// NOTE: do NOT register a local --json flag. The root command already
	// provides a persistent --json, and PersistentPreRun resolves jsonOutput
	// from it. A command-local --json binding to the same global shadows the
	// persistent flag: cobra sets jsonOutput from the local flag, but
	// PersistentPreRun then sees root.PersistentFlags().Changed("json")==false
	// and clobbers jsonOutput back to the config default (false), so a local
	// --json is silently non-functional and the structured-stdout error paths
	// below never fire under --json (beads-9fww / beads-lv51). Inherit the
	// honored persistent flag instead.

	// New mode flags
	compactCmd.Flags().BoolVar(&compactAnalyze, "analyze", false, "Analyze mode: export candidates for agent review")
	compactCmd.Flags().BoolVar(&compactApply, "apply", false, "Apply mode: accept agent-provided summary")
	compactCmd.Flags().BoolVar(&compactAuto, "auto", false, "Auto mode: AI-powered compaction (legacy)")
	compactCmd.Flags().StringVar(&compactSummary, "summary", "", "Path to summary file (use '-' for stdin)")
	compactCmd.Flags().StringVar(&compactActor, "actor", "agent", "Actor name for audit trail")
	compactCmd.Flags().IntVar(&compactLimit, "limit", 0, "Limit number of candidates (0 = no limit)")
	compactCmd.Flags().BoolVar(&compactDolt, "dolt", false, "Dolt mode: run Dolt garbage collection on .beads/dolt")

	// Note: compactCmd is added to adminCmd in admin.go
}
