package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/validation"
)

func resolveProxiedCustomTypes(dbTypes []string) []string {
	if len(dbTypes) > 0 {
		return dbTypes
	}
	return config.GetCustomTypesFromYAML()
}

func runCreateProxiedServer(cmd *cobra.Command, ctx context.Context, in createInput) {
	if in.repoOverrideSet {
		FatalErrorRespectJSON("--repo is not supported with --proxied-server")
	}
	switch {
	case in.graphFile != "":
		runCreateProxiedGraph(cmd, ctx, in)
	case in.markdownFile != "":
		runCreateProxiedMarkdown(cmd, ctx, in)
	default:
		runCreateProxiedSingle(cmd, ctx, in)
	}
}

func proxiedOpenUOW(ctx context.Context) (uow.UnitOfWork, domain.CreateContext) {
	if uowProvider == nil {
		FatalErrorRespectJSON("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		FatalErrorRespectJSON("open unit of work: %v", err)
	}
	cctx, err := uw.ConfigUseCase().LoadCreateContext(ctx)
	if err != nil {
		uw.Close(ctx)
		FatalErrorRespectJSON("load create context: %v", err)
	}
	return uw, cctx
}

func runCreateProxiedSingle(_ *cobra.Command, ctx context.Context, in createInput) {
	runCreateLintIssue(in)
	if in.explicitID != "" {
		if _, err := validation.ValidateIDFormat(in.explicitID); err != nil {
			FatalErrorRespectJSON("%v", err)
		}
	}
	deps, err := parseDepSpecs(in.deps)
	if err != nil {
		FatalErrorRespectJSON("%v", err)
	}
	waitsFor, err := buildWaitsFor(in.waitsFor, in.waitsForGate)
	if err != nil {
		FatalErrorRespectJSON("%v", err)
	}

	if in.dryRun {
		previewLabels := in.labels
		if in.parentID != "" {
			if uowProvider == nil {
				FatalErrorRespectJSON("proxied-server UOW provider not initialized")
			}
			dryUW, err := uowProvider.NewUOW(ctx)
			if err != nil {
				FatalErrorRespectJSON("open unit of work: %v", err)
			}
			if _, err := dryUW.IssueUseCase().GetIssue(ctx, in.parentID); err != nil {
				dryUW.Close(ctx)
				FatalErrorRespectJSON("parent issue %s not found: %v", in.parentID, err)
			}
			if !in.noInheritLabels {
				inherited, lerr := dryUW.LabelUseCase().GetLabels(ctx, in.parentID)
				if lerr != nil {
					dryUW.Close(ctx)
					FatalErrorRespectJSON("dry-run inherit labels: %v", lerr)
				}
				previewLabels = mergeCreateLabels(in.labels, inherited)
			}
			dryUW.Close(ctx)
		}
		previewIssue := buildCreateIssueFromInput(in)
		if in.jsonOutput {
			if err := outputJSON(previewIssue); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
		} else {
			renderCreateDryRunPreview(previewIssue, previewLabels, in.deps)
		}
		return
	}

	// Load create context (read-only) to validate input before the write tx.
	configUW, cctx := proxiedOpenUOW(ctx)
	configUW.Close(ctx)

	customTypes := resolveProxiedCustomTypes(cctx.CustomTypes)
	if in.issueType != "" {
		it := types.IssueType(in.issueType).Normalize()
		if !it.IsValidWithCustom(customTypes) {
			FatalErrorRespectJSON("invalid type %q (allowed: built-ins plus configured custom types)", in.issueType)
		}
	}
	if in.explicitID != "" {
		// Live DB prefix stays authoritative; a disagreeing config.yaml prefix is
		// folded into the allowed-list so the DB's own auto-gen prefix is never
		// rejected (beads-xevo).
		dbPrefix, allowed := resolvePrefixValidation(cctx.IssuePrefix, cctx.AllowedPrefixes)
		if err := validation.ValidateIDPrefixAllowed(in.explicitID, dbPrefix, allowed, in.force); err != nil {
			FatalErrorRespectJSON("%v", err)
		}

		// An explicit --id that already exists would be silently UPSERTED by the
		// domain create path (insertIssueRow uses INSERT ... ON DUPLICATE KEY
		// UPDATE), overwriting the stored bead while still printing "✓ Created"
		// — silent data-loss on id reuse (beads-k75k). The direct path guards
		// this in create.go; the proxied path (the live path for hub-connected
		// crew) must too, or the guard leaks on whichever path is active — the
		// create.go-vs-proxied dual-parse trap (cf. beads-n5xz, beads-83h3).
		// Scoped to parentID=="" (a user-supplied --id, not a parent-minted
		// child id). Refuse unless --force; use `bd update` to modify.
		if !in.force && in.parentID == "" {
			// beads-65cgh: these existence-check error legs must honor the
			// --json contract (JSON error object on stdout), matching the
			// adjacent 'already exists' leg (FatalErrorWithHintRespectJSON) and
			// the closed-epic leg below (FatalErrorRespectJSON). Plain
			// FatalError writes the JSON error to stderr and leaves stdout empty
			// under --json, breaking a `bd create ... --json` consumer if a UOW
			// open or the existence-check DB lookup fails mid-create (same
			// FatalError->RespectJSON class as beads-v5yu).
			if uowProvider == nil {
				FatalErrorRespectJSON("proxied-server UOW provider not initialized")
			}
			checkUW, err := uowProvider.NewUOW(ctx)
			if err != nil {
				FatalErrorRespectJSON("open unit of work: %v", err)
			}
			_, gerr := checkUW.IssueUseCase().GetIssue(ctx, in.explicitID)
			checkUW.Close(ctx)
			if gerr == nil {
				FatalErrorWithHintRespectJSON(
					fmt.Sprintf("issue %s already exists", in.explicitID),
					"Use 'bd update' to modify it, or pass --force to overwrite.")
			} else if !errors.Is(gerr, sql.ErrNoRows) && !errors.Is(gerr, storage.ErrNotFound) {
				// A genuine "not found" is the happy path (the id is free). The
				// domain use-case surfaces sql.ErrNoRows on miss (via
				// issueRepo.Get); storage.ErrNotFound is checked too for
				// defensiveness against a future store swap. Any OTHER error is
				// a real lookup failure that must not be swallowed into a create.
				FatalErrorRespectJSON("failed to check whether %s already exists: %v", in.explicitID, gerr)
			}
		}
	}

	// beads-a8a1b: refuse to create an OPEN child under a CLOSED epic on the
	// proxied path (mirrors the direct guard in create.go) — the parent-
	// assignment axis of the closed-epic-with-open-child invariant, which was
	// wide open on both create paths. New issues are created open, so any
	// closed-epic parent is a violation. Overridable with --force.
	if !in.force && in.parentID != "" {
		// beads-65cgh: honor the --json contract on these existence-check error
		// legs too (see the parentID=="" block above); the closed-epic leg
		// below already uses FatalErrorRespectJSON.
		if uowProvider == nil {
			FatalErrorRespectJSON("proxied-server UOW provider not initialized")
		}
		checkUW, err := uowProvider.NewUOW(ctx)
		if err != nil {
			FatalErrorRespectJSON("open unit of work: %v", err)
		}
		parent, gerr := checkUW.IssueUseCase().GetIssue(ctx, in.parentID)
		// beads-ei6vq: resolve the done-category set from the SAME UOW before
		// closing it — a parent moved to a custom done-category status is
		// terminal but not literally closed, so the literal `== StatusClosed`
		// test was done-category-blind (mirrors u9lkx/ulsg4 proxied legs).
		done := doneCategoryStatusSetProxied(ctx, checkUW)
		checkUW.Close(ctx)
		// beads-czu1s: widen the proxied create-under-closed-parent guard
		// (beads-65cgh) to the shared isAutoClosingParentType (epic OR molecule
		// OR ephemeral), mirroring the direct path (create.go) — a closed
		// MOLECULE/wisp root was previously creatable-under on the proxied path.
		if gerr == nil && isAutoClosingParentType(parent) && parentStatusIsTerminal(parent.Status, done) {
			FatalErrorRespectJSON("cannot create a child under closed parent %s (its status is closed; reopen the parent first or use --force to override)", in.parentID)
		}
	}

	issue := buildCreateIssueFromInput(in)
	params := domain.CreateIssueParams{
		Issue:                   issue,
		ExplicitID:              in.explicitID,
		ParentID:                in.parentID,
		Labels:                  in.labels,
		InheritLabelsFromParent: !in.noInheritLabels && in.parentID != "",
		Dependencies:            deps,
		WaitsFor:                waitsFor,
		DiscoveredFromParent:    discoveredFromParent(in.deps),
		ForcePrefix:             in.force,
		Force:                   in.force, // beads-p1p9n: honor --force on the parent-child dep-spec guard
	}

	var result domain.CreateIssueResult
	if err := uow.RunInTxMsg(ctx, uowProvider, func(uw uow.UnitOfWork) (string, error) {
		var e error
		if issue.Ephemeral {
			result, e = uw.IssueUseCase().CreateWisp(ctx, params, in.createdBy)
		} else {
			result, e = uw.IssueUseCase().CreateIssue(ctx, params, in.createdBy)
		}
		if e != nil {
			return "", e
		}
		return fmt.Sprintf("bd: create %s", result.Issue.ID), nil
	}); err != nil {
		FatalErrorRespectJSON("%v", err)
	}

	// beads-w1vxy: fire on_create after the commit, matching the direct decorator
	// (HookFiringStore.CreateIssue → createHookEvents). The proxied UOW use-case
	// layer bypasses HookFiringStore, so a hub-connected crew's on_create hook
	// never ran. result.Issue does not carry Labels (they persist via
	// params.Labels + inheritance), so pass the merged explicit+inherited set
	// explicitly, mirroring the direct path's issue.Labels. Best-effort (warns to
	// stderr, does not fail the command).
	fireProxiedCreateHooks(ctx, result.Issue, mergeCreateLabels(in.labels, result.InheritedLabels))

	switch {
	case in.jsonOutput:
		if err := outputJSON(result.Issue); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
	case in.silent:
		fmt.Println(result.Issue.ID)
	default:
		fmt.Printf("%s Created issue: %s\n", ui.RenderPass("✓"), formatFeedbackID(result.Issue.ID, result.Issue.Title))
		fmt.Printf("  Priority: P%d\n", result.Issue.Priority)
		fmt.Printf("  Status: %s\n", result.Issue.Status)
	}

	// Record last-touched so `bd show --current` fallback works after a proxied
	// create, matching the direct create path (create.go) + ready_proxied_server.go
	// (beads-gw7s: proxied create/update/close handlers previously omitted this).
	SetLastTouchedID(result.Issue.ID)
}

func runCreateLintIssue(in createInput) {
	if in.validationMode != "error" && in.validationMode != "warn" {
		return
	}
	lintIssue := &types.Issue{
		IssueType:          types.IssueType(in.issueType).Normalize(),
		Description:        in.description,
		AcceptanceCriteria: in.acceptanceCriteria,
	}
	if err := validation.LintIssue(lintIssue); err != nil {
		if in.validationMode == "error" {
			FatalErrorRespectJSON("%v", err)
		}
		fmt.Fprintf(os.Stderr, "%s %v\n", ui.RenderWarn("⚠"), err)
	}
}

func buildCreateIssueFromInput(in createInput) *types.Issue {
	return buildCreateIssue(createIssueParams{
		ID:                 in.explicitID,
		Title:              in.title,
		Description:        in.description,
		Design:             in.design,
		AcceptanceCriteria: in.acceptanceCriteria,
		Notes:              in.notes,
		SpecID:             in.specID,
		Priority:           in.priority,
		IssueType:          types.IssueType(in.issueType).Normalize(),
		Assignee:           in.assignee,
		ExternalRef:        in.externalRef,
		EstimatedMinutes:   in.estimatedMinutes,
		Ephemeral:          in.ephemeral,
		NoHistory:          in.noHistory,
		CreatedBy:          in.createdBy,
		Owner:              in.owner,
		MolType:            in.molType,
		WispType:           in.wispType,
		EventKind:          in.eventCategory,
		Actor:              in.eventActor,
		Target:             in.eventTarget,
		Payload:            in.eventPayload,
		DueAt:              in.dueAt,
		DeferUntil:         in.deferUntil,
		Metadata:           in.metadata,
	})
}

func runCreateProxiedMarkdown(_ *cobra.Command, ctx context.Context, in createInput) {
	templates, err := parseMarkdownFile(in.markdownFile)
	if err != nil {
		FatalErrorRespectJSON("parsing markdown file: %v", err)
	}
	if len(templates) == 0 {
		FatalErrorRespectJSON("no issues found in markdown file")
	}

	if in.validationMode == "error" || in.validationMode == "warn" {
		for _, t := range templates {
			lintIssue := &types.Issue{
				IssueType:          t.IssueType,
				Description:        t.Description,
				AcceptanceCriteria: t.AcceptanceCriteria,
			}
			if err := validation.LintIssue(lintIssue); err != nil {
				if in.validationMode == "error" {
					FatalErrorRespectJSON("template %q: %v", t.Title, err)
				}
				fmt.Fprintf(os.Stderr, "%s template %q: %v\n", ui.RenderWarn("⚠"), t.Title, err)
			}
		}
	}

	type templateBuild struct {
		template *IssueTemplate
		deps     []domain.DependencySpec
	}

	builds := make([]templateBuild, 0, len(templates))
	for _, t := range templates {
		deps, err := parseMarkdownDepSpecs(t.Dependencies, t.Title)
		if err != nil {
			FatalErrorRespectJSON("%v", err)
		}
		builds = append(builds, templateBuild{template: t, deps: deps})
	}

	configUW, cctx := proxiedOpenUOW(ctx)
	configUW.Close(ctx)

	customTypes := resolveProxiedCustomTypes(cctx.CustomTypes)
	for _, b := range builds {
		if b.template.IssueType == "" {
			continue
		}
		if !b.template.IssueType.IsValidWithCustom(customTypes) {
			FatalErrorRespectJSON("template %q: invalid type %q", b.template.Title, b.template.IssueType)
		}
	}

	paramsList := make([]domain.CreateIssueParams, 0, len(builds))
	for _, b := range builds {
		t := b.template
		paramsList = append(paramsList, domain.CreateIssueParams{
			Issue: &types.Issue{
				Title:              t.Title,
				Description:        t.Description,
				Design:             t.Design,
				AcceptanceCriteria: t.AcceptanceCriteria,
				Status:             types.StatusOpen,
				Priority:           t.Priority,
				IssueType:          t.IssueType,
				Assignee:           t.Assignee,
				Ephemeral:          in.ephemeral,
				NoHistory:          in.noHistory,
				MolType:            in.molType,
				CreatedBy:          in.createdBy,
				Owner:              in.owner,
			},
			Labels:       t.Labels,
			Dependencies: b.deps,
			Force:        in.force, // beads-p1p9n: honor --force on the parent-child dep-spec guard
		})
	}

	var result domain.CreateIssuesResult
	if err := uow.RunInTxMsg(ctx, uowProvider, func(uw uow.UnitOfWork) (string, error) {
		var e error
		if in.ephemeral {
			result, e = uw.IssueUseCase().CreateWisps(ctx, paramsList, in.createdBy)
		} else {
			result, e = uw.IssueUseCase().CreateIssues(ctx, paramsList, in.createdBy)
		}
		if e != nil {
			return "", e
		}
		return fmt.Sprintf("bd: create %d issue(s) from %s", len(result.Issues), in.markdownFile), nil
	}); err != nil {
		FatalErrorRespectJSON("creating issues from markdown: %v", err)
	}

	if in.jsonOutput {
		if err := outputJSON(result.Issues); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		return
	}

	printProxiedMarkdownCreated(result.Issues, in.markdownFile)
}

// printProxiedMarkdownCreated renders the human summary for the proxied
// 'bd create --from-markdown' path. Each created issue's Title is routed
// through displayTitle (ui.SanitizeForTerminal): these titles come straight
// from the imported markdown file (untrusted external source), so an OSC/CSI
// terminal-control escape (OSC 0 window-title / OSC 52 clipboard) in a heading
// would otherwise reach the terminal verbatim. Display-only — stored titles
// and the JSON path (outputJSON above) are unchanged.
func printProxiedMarkdownCreated(issues []*types.Issue, markdownFile string) {
	fmt.Printf("%s Created %d issues from %s:\n", ui.RenderPass("✓"), len(issues), markdownFile)
	for _, issue := range issues {
		fmt.Printf("  %s: %s [P%d, %s]\n", issue.ID, displayTitle(issue.Title), issue.Priority, issue.IssueType)
	}
}

func parseMarkdownDepSpecs(deps []string, templateTitle string) ([]domain.DependencySpec, error) {
	var out []domain.DependencySpec
	for _, raw := range deps {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}

		var depType types.DependencyType
		var target string
		if strings.Contains(raw, ":") {
			parts := strings.SplitN(raw, ":", 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("invalid dependency format %q for issue %q", raw, templateTitle)
			}
			depType = types.DependencyType(strings.TrimSpace(parts[0]))
			target = strings.TrimSpace(parts[1])
		} else {
			depType = types.DepBlocks
			target = raw
		}

		if !depType.IsValid() {
			return nil, fmt.Errorf("invalid dependency type %q for issue %q", depType, templateTitle)
		}
		out = append(out, domain.DependencySpec{
			Type:     depType,
			TargetID: target,
		})
	}
	return out, nil
}

func runCreateProxiedGraph(_ *cobra.Command, ctx context.Context, in createInput) {
	data, err := os.ReadFile(in.graphFile) // #nosec G304 -- user-provided path is intentional
	if err != nil {
		FatalErrorRespectJSON("reading graph plan: %v", err)
	}
	if unknown := detectUnknownGraphFields(data); len(unknown) > 0 {
		warnUnknownGraphFields(os.Stderr, unknown)
	}

	var plan GraphApplyPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		FatalErrorRespectJSON("parsing graph plan: %v", err)
	}

	if in.dryRun {
		if uowProvider == nil {
			FatalErrorRespectJSON("proxied-server UOW provider not initialized")
		}
		dryUW, err := uowProvider.NewUOW(ctx)
		if err != nil {
			FatalErrorRespectJSON("open unit of work: %v", err)
		}
		cctx, err := dryUW.ConfigUseCase().LoadCreateContext(ctx)
		dryUW.Close(ctx)
		if err != nil {
			FatalErrorRespectJSON("load create context: %v", err)
		}
		if err := validateGraphApplyPlan(&plan, resolveProxiedCustomTypes(cctx.CustomTypes)); err != nil {
			FatalErrorRespectJSON("invalid graph plan: %v", err)
		}
		if err := emitGraphApplyDryRun(&plan); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		return
	}

	uw, cctx := proxiedOpenUOW(ctx)
	defer uw.Close(ctx)

	if err := validateGraphApplyPlan(&plan, resolveProxiedCustomTypes(cctx.CustomTypes)); err != nil {
		FatalErrorRespectJSON("invalid graph plan: %v", err)
	}

	domainPlan := buildDomainGraphPlan(plan, in)

	var result domain.GraphApplyResult
	if in.ephemeral {
		result, err = uw.IssueUseCase().ApplyWispGraph(ctx, domainPlan, in.createdBy)
	} else {
		result, err = uw.IssueUseCase().ApplyIssueGraph(ctx, domainPlan, in.createdBy)
	}
	if err != nil {
		FatalErrorRespectJSON("graph create: %v", err)
	}

	commitMsg := plan.CommitMessage
	if commitMsg == "" {
		commitMsg = fmt.Sprintf("bd: graph-apply %d nodes", len(plan.Nodes))
	}

	// beads-pma90: capture each created node's snapshot (with labels + deps)
	// BEFORE Commit so the create hooks can fire after — the direct graph path
	// (graph_apply.go executeGraphApply → tx.CreateIssues + tx.AddDependency)
	// fires per-node on_create (+ synthetic per-label on_update) and per-edge
	// on_update via the HookFiringStore decorator (createHookEvents +
	// dependencyHookEvents), but the proxied UOW use-case layer (ApplyIssueGraph /
	// ApplyWispGraph) fires nothing. GraphApplyResult carries only the ID map, so
	// re-read each issue in-tx (mirrors beads-29tyj captureProxiedHookSnapshot).
	graphSnapshots := captureProxiedGraphCreateSnapshots(ctx, uw, result.IDs, in.ephemeral)

	if err := uw.Commit(ctx, commitMsg); err != nil && !isDoltNothingToCommit(err) {
		FatalErrorRespectJSON("commit: %v", err)
	}

	// beads-pma90: fire the create hooks after the commit (parity with the direct
	// decorator's fire-after-commit contract).
	fireProxiedGraphCreateSnapshots(ctx, graphSnapshots)

	if in.jsonOutput {
		if err := outputJSON(GraphApplyResult{IDs: result.IDs}); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		return
	}

	fmt.Printf("Created %d issues\n", len(result.IDs))
	keys := make([]string, 0, len(result.IDs))
	for k := range result.IDs {
		keys = append(keys, k)
	}

	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("  %s -> %s\n", k, result.IDs[k])
	}
}

// captureProxiedGraphCreateSnapshots re-reads each created graph node (with
// labels + dependency records) in-tx before Commit, in deterministic node-ID
// order, so beads-pma90 can fire the create hooks after the commit. Mirrors the
// direct graph path, where HookFiringStore.hookTrackingTransaction accumulates
// createHookEvents (per node) + dependencyHookEvents (per persisted edge).
func captureProxiedGraphCreateSnapshots(ctx context.Context, uw uow.UnitOfWork, ids map[string]string, ephemeral bool) []*types.Issue {
	if uw == nil || len(ids) == 0 {
		return nil
	}
	// Fire in a stable order (by resolved issue ID) so hook side effects are
	// deterministic across runs.
	resolved := make([]string, 0, len(ids))
	for _, id := range ids {
		resolved = append(resolved, id)
	}
	sort.Strings(resolved)

	snapshots := make([]*types.Issue, 0, len(resolved))
	for _, id := range resolved {
		// beads-pma90: an ephemeral (wisp) graph writes to the wisps table, and
		// captureProxiedHookSnapshot re-reads via IssueUseCase.GetIssue (which
		// reads the ISSUES table only) — so a wisp node would capture nil and
		// never fire its on_create hook. Read wisps from the wisps table so the
		// ephemeral graph branch fires on_create at parity with the direct path
		// (createHookEvents does not skip Ephemeral).
		snap := captureProxiedGraphNodeSnapshot(ctx, uw, id, ephemeral)
		if snap == nil {
			continue
		}
		// captureProxiedHookSnapshot (via IssueUseCase.GetIssue) does not hydrate
		// labels, but the synthetic per-label on_update stream in
		// fireProxiedCreateHooks needs them — read them in-tx explicitly so a
		// labeled graph node fires its on_update stream (createHookEvents parity).
		if len(snap.Labels) == 0 {
			if labels, lerr := uw.LabelUseCase().GetLabels(ctx, id); lerr == nil {
				snap.Labels = labels
			}
		}
		snapshots = append(snapshots, snap)
	}
	return snapshots
}

// captureProxiedGraphNodeSnapshot re-reads a single created graph node in-tx,
// selecting the wisps table for an ephemeral graph (parity with the direct
// path, which persists wisp nodes to the wisps table). Mirrors
// captureProxiedHookSnapshot for the non-wisp case but is wisp-aware so a
// `bd create --graph --ephemeral` node fires its on_create hook (beads-pma90).
func captureProxiedGraphNodeSnapshot(ctx context.Context, uw uow.UnitOfWork, id string, ephemeral bool) *types.Issue {
	if uw == nil {
		return nil
	}
	if !ephemeral {
		return captureProxiedHookSnapshot(ctx, uw, id, true)
	}
	snapshot, err := uw.IssueUseCase().GetWisp(ctx, id)
	if err != nil || snapshot == nil {
		return nil
	}
	if recs, derr := uw.DependencyUseCase().GetIssueDependencyRecords(ctx, []string{id}); derr == nil {
		snapshot.Dependencies = recs[id]
	}
	return snapshot
}

// fireProxiedGraphCreateSnapshots fires, per created graph node, the on_create
// hook (+ synthetic per-label on_update stream) and then a per-dependency-edge
// on_update, matching the direct decorator's createHookEvents +
// dependencyHookEvents (beads-pma90). Best-effort: hook failures warn to stderr
// and never fail the command (the direct decorator's fire-and-forget contract).
func fireProxiedGraphCreateSnapshots(ctx context.Context, snapshots []*types.Issue) {
	for _, snap := range snapshots {
		if snap == nil {
			continue
		}
		fireProxiedCreateHooks(ctx, snap, snap.Labels)
		// Per-dependency-edge on_update, mirroring dependencyHookEvents: the direct
		// graph path fires one on_update per persisted dependency edge the node
		// carries (hook_decorator.go:335).
		for range snap.Dependencies {
			fireProxiedUpdateSnapshots(ctx, snap)
		}
	}
}

func buildDomainGraphPlan(plan GraphApplyPlan, in createInput) domain.GraphPlan {
	nodes := make([]domain.GraphNode, 0, len(plan.Nodes))
	for _, n := range plan.Nodes {
		nodes = append(nodes, domain.GraphNode{
			Key:       n.Key,
			Issue:     materializeGraphNodeIssue(n, in),
			ParentKey: n.ParentKey,
			ParentID:  n.ParentID,
			// Trim/fold-"none" the assignee like assign/create/update do
			// (normalizeAssignee), so a graph node's padded "  alice  " isn't
			// stored unmatchable and "none" unassigns (beads-7i4m, llzt graph
			// sibling — this was a 4th create input site the llzt seam missed).
			Assignee:          normalizeAssignee(n.Assignee),
			AssignAfterCreate: n.AssignAfterCreate,
			MetadataRefs:      n.MetadataRefs,
			Labels:            n.Labels,
		})
	}
	edges := make([]domain.GraphEdge, 0, len(plan.Edges))
	for _, e := range plan.Edges {
		edges = append(edges, domain.GraphEdge{
			FromKey: e.FromKey,
			FromID:  e.FromID,
			ToKey:   e.ToKey,
			ToID:    e.ToID,
			Type:    graphApplyDependencyType(e.Type),
		})
	}
	return domain.GraphPlan{Nodes: nodes, Edges: edges, NoInheritLabels: in.noInheritLabels, Force: in.force}
}

func materializeGraphNodeIssue(n GraphApplyNode, in createInput) *types.Issue {
	// Normalize aliases/case to canonical, matching the direct graph-apply path
	// and bd create -t (beads-h3k5). Empty stays empty -> defaults to task.
	issueType := types.IssueType(n.Type).Normalize()
	if issueType == "" {
		issueType = types.TypeTask
	}
	priority := 2
	if n.Priority != nil {
		priority = *n.Priority
	}
	var metadataJSON json.RawMessage
	if len(n.Metadata) > 0 {
		raw, err := json.Marshal(n.Metadata)
		if err != nil {
			FatalErrorRespectJSON("node %q: marshaling metadata: %v", n.Key, err)
		}
		metadataJSON = raw
	}
	return &types.Issue{
		Title:       n.Title,
		Description: n.Description,
		IssueType:   issueType,
		Status:      types.StatusOpen,
		Priority:    priority,
		Labels:      n.Labels,
		Metadata:    metadataJSON,
		Ephemeral:   in.ephemeral,
		NoHistory:   in.noHistory,
		CreatedBy:   in.createdBy,
		Owner:       in.owner,
	}
}
