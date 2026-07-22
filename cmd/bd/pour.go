package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/formula"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/utils"
)

// pourCmd is a top-level command for instantiating protos as persistent mols.
//
// In the molecular chemistry metaphor:
//   - Proto (solid) -> pour -> Mol (liquid)
//   - Pour creates persistent, auditable work in .beads/
var pourCmd = &cobra.Command{
	Use:   "pour <proto-id>",
	Short: "Instantiate a proto as a persistent mol (solid -> liquid)",
	Long: `Pour a proto into a persistent mol - like pouring molten metal into a mold.

This is the chemistry-inspired command for creating PERSISTENT work from templates.
The resulting mol lives in .beads/ (permanent storage) and is synced with git.

Phase transition: Proto (solid) -> pour -> Mol (liquid)

WHEN TO USE POUR vs WISP:
  pour (liquid): Persistent work that needs audit trail
    - Feature implementations spanning multiple sessions
    - Work you may need to reference later
    - Anything worth preserving in git history

  wisp (vapor): Ephemeral work that auto-cleans up
    - Release workflows (one-time execution)
    - Operational loops and recurring cycles
    - Health checks and diagnostics
    - Any operational workflow without audit value

TIP: Formulas can specify phase:"vapor" to recommend wisp usage.
     If you pour a vapor-phase formula, you'll get a warning.

Examples:
  bd mol pour mol-feature --var name=auth    # Persistent feature work
  bd mol pour mol-review --var pr=123        # Persistent code review`,
	Args:          cobra.ExactArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runPour,
}

// attachmentInfo holds a resolved attached proto and its loaded subgraph.
type attachmentInfo struct {
	id       string
	issue    *types.Issue
	subgraph *TemplateSubgraph
}

// printPourDryRun renders the `bd pour --dry-run` preview. Titles are routed
// through displayTitle (ui.SanitizeForTerminal) because a proto/attachment
// title can originate from an untrusted import (JSONL/markdown/SCM) carrying
// OSC/CSI terminal-control escapes (OSC 0 window-title / OSC 52 clipboard);
// printing them raw would inject control sequences onto the preview lines.
// 7n9y sink-class slice (beads-knab).
func printPourDryRun(subgraph *TemplateSubgraph, attachments []attachmentInfo, vars map[string]string, assignee, attachType, protoID string) {
	fmt.Printf("\nDry run: would pour %d issues from proto %s\n\n", len(subgraph.Issues), protoID)
	fmt.Printf("Storage: permanent (.beads/)\n\n")
	for _, issue := range subgraph.Issues {
		newTitle := substituteVariables(issue.Title, vars)
		suffix := ""
		if issue.ID == subgraph.Root.ID && assignee != "" {
			suffix = fmt.Sprintf(" (assignee: %s)", assignee)
		}
		fmt.Printf("  - %s (from %s)%s\n", displayTitle(newTitle), issue.ID, suffix)
	}
	if len(attachments) > 0 {
		fmt.Printf("\nAttachments (%s bonding):\n", attachType)
		for _, attach := range attachments {
			fmt.Printf("  + %s (%d issues)\n", displayTitle(attach.issue.Title), len(attach.subgraph.Issues))
		}
	}
}

// outputPourDryRunJSON emits the `bd mol pour --dry-run --json` preview as a
// parseable JSON envelope (beads-51w8c), mirroring the fields of printPourDryRun
// so a scripted caller gets machine output on the safe preview path instead of
// plaintext that breaks `| jq`. Titles are sanitized via displayTitle for the
// same untrusted-import reason as the plaintext path (7n9y sink-class).
func outputPourDryRunJSON(subgraph *TemplateSubgraph, attachments []attachmentInfo, vars map[string]string, assignee, attachType, protoID string) error {
	type dryRunIssue struct {
		Title    string `json:"title"`
		FromID   string `json:"from_id"`
		Assignee string `json:"assignee,omitempty"`
	}
	type dryRunAttachment struct {
		Title  string `json:"title"`
		Issues int    `json:"issues"`
	}
	issues := make([]dryRunIssue, 0, len(subgraph.Issues))
	for _, issue := range subgraph.Issues {
		di := dryRunIssue{
			Title:  displayTitle(substituteVariables(issue.Title, vars)),
			FromID: issue.ID,
		}
		if issue.ID == subgraph.Root.ID && assignee != "" {
			di.Assignee = assignee
		}
		issues = append(issues, di)
	}
	attach := make([]dryRunAttachment, 0, len(attachments))
	for _, a := range attachments {
		attach = append(attach, dryRunAttachment{
			Title:  displayTitle(a.issue.Title),
			Issues: len(a.subgraph.Issues),
		})
	}
	out := map[string]interface{}{
		"dry_run":      true,
		"proto_id":     protoID,
		"storage":      "permanent",
		"would_create": len(subgraph.Issues),
		"issues":       issues,
	}
	if len(attach) > 0 {
		out["attach_type"] = attachType
		out["attachments"] = attach
	}
	return outputJSON(out)
}

func runPour(cmd *cobra.Command, args []string) error {
	CheckReadonly("pour")

	evt := metrics.NewCommandEvent("pour")
	defer func() {
		if c := metrics.Global(); c != nil {
			c.CloseEventAndAdd(evt)
		}
	}()

	ctx := rootCtx

	// beads-mgjco (aocj fail-loud class): in proxied-server mode main.go's
	// PersistentPreRun returns early (main.go:1155) leaving the global store
	// nil, so pour's store.GetIssue/Pour would nil-panic — and the bare
	// store==nil check misdiagnoses it as a local "no database connection".
	// pour instantiates a persistent molecule via a raw storage.Transaction the
	// proxied UOW does not yield, so fail loud with an accurate message (mirrors
	// merge-slot/restore). Guard BEFORE the nil check.
	if usesProxiedServer() {
		return HandleErrorRespectJSON("pour is not supported in proxied-server mode (connect directly with an embedded/dolt store)")
	}
	if err := ensureStoreActive(); err != nil {
		return HandleErrorWithHintRespectJSON(err.Error(), diagHint())
	}

	dryRun, _ := cmd.Flags().GetBool("dry-run")
	varFlags, _ := cmd.Flags().GetStringArray("var")
	rawAssignee, _ := cmd.Flags().GetString("assignee")
	// Trim + fold "none" so the molecule root's stored assignee matches what
	// `bd ready/list --assignee x` searches for (beads-llzt); a padded value
	// would orphan the poured root from its assignee.
	assignee := normalizeAssignee(rawAssignee)
	attachFlags, _ := cmd.Flags().GetStringSlice("attach")
	attachType, _ := cmd.Flags().GetString("attach-type")

	// Validate --attach-type up front, before any mutation, mirroring
	// `bd mol bond --type` (mol_bond.go runMolBond). Both entry points feed the
	// shared bondProtoMolWithSubgraph switch whose default arm maps ANY unknown
	// value to DepParentChild (parallel) — so without this check a typo'd
	// --attach-type (e.g. "sequentail") is NOT rejected, it silently bonds the
	// attachment as parallel instead of the intended sequential/conditional. pour
	// spawns the molecule root before the attach loop, so a late check would be
	// fail-after-side-effect; the guard belongs here next to the flag read, same
	// as mol bond's up-front reject (beads-u6suf, EARLY-VALIDATION-PARITY class).
	if attachType != types.BondTypeSequential && attachType != types.BondTypeParallel && attachType != types.BondTypeConditional {
		return HandleErrorRespectJSON("invalid attach-type '%s', must be: sequential, parallel, or conditional", attachType)
	}

	// Parse variables
	vars := make(map[string]string)
	for _, v := range varFlags {
		parts := strings.SplitN(v, "=", 2)
		if len(parts) != 2 {
			return HandleErrorRespectJSON("invalid variable format '%s', expected 'key=value'", v)
		}
		vars[parts[0]] = parts[1]
	}

	// Try to load as formula first (ephemeral proto - gt-4v1eo)
	// If that fails, fall back to loading from DB (legacy proto beads)
	var subgraph *TemplateSubgraph
	var protoID string
	isFormula := false

	// Try to cook formula inline (gt-4v1eo: ephemeral protos)
	// This works for any valid formula name, not just "mol-" prefixed ones
	// Pass vars for step condition filtering (bd-7zka.1)
	sg, err := resolveAndCookFormulaWithVars(args[0], nil, vars)
	if err == nil {
		subgraph = sg
		protoID = sg.Root.ID
		isFormula = true

		// Warn if formula recommends vapor phase (bd-mol cleanup).
		// beads-8zed6: this advisory is a human hint (suggest `bd mol wisp`),
		// NOT a machine result. Under --json runPour emits a pourResult success
		// envelope on stdout; an unconditional multi-line plaintext write to
		// stderr here interleaves with that object under `--json 2>&1 | jq`
		// (8lqh Direction-2). Suppress it under --json, exactly like the sibling
		// advisories at mol_burn.go / migrate_issues.go — the JSON envelope
		// already carries the phase.
		if sg.Phase == "vapor" && !jsonOutput {
			fmt.Fprintf(os.Stderr, "%s Formula %q recommends vapor phase (ephemeral)\n", ui.RenderWarn("⚠"), args[0])
			fmt.Fprintf(os.Stderr, "  Consider using: bd mol wisp %s", args[0])
			for _, v := range varFlags {
				fmt.Fprintf(os.Stderr, " --var %s", v)
			}
			fmt.Fprintf(os.Stderr, "\n")
			fmt.Fprintf(os.Stderr, "  Pour creates persistent issues that sync to git.\n")
			fmt.Fprintf(os.Stderr, "  Wisp creates ephemeral issues that auto-cleanup.\n\n")
		}
	}

	if subgraph == nil {
		// Try to load as existing proto bead (legacy path)
		resolvedID, err := utils.ResolvePartialID(ctx, store, args[0])
		if err != nil {
			return HandleErrorRespectJSON("%s not found as formula or proto ID", args[0])
		}
		protoID = resolvedID

		protoIssue, err := store.GetIssue(ctx, protoID)
		if err != nil {
			return HandleErrorRespectJSON("loading proto %s: %v", protoID, err)
		}
		if !isProto(protoIssue) {
			return HandleErrorRespectJSON("%s is not a proto (missing '%s' label)", protoID, MoleculeLabel)
		}

		subgraph, err = loadTemplateSubgraph(ctx, store, protoID)
		if err != nil {
			return HandleErrorRespectJSON("loading proto: %v", err)
		}
	}

	_ = isFormula // For future use (e.g., logging)

	// Resolve and load attached protos
	var attachments []attachmentInfo
	for _, attachArg := range attachFlags {
		attachID, err := utils.ResolvePartialID(ctx, store, attachArg)
		if err != nil {
			return HandleErrorRespectJSON("resolving attachment ID %s: %v", attachArg, err)
		}
		attachIssue, err := store.GetIssue(ctx, attachID)
		if err != nil {
			return HandleErrorRespectJSON("loading attachment %s: %v", attachID, err)
		}
		if !isProto(attachIssue) {
			return HandleErrorRespectJSON("%s is not a proto (missing '%s' label)", attachID, MoleculeLabel)
		}
		attachSubgraph, err := loadTemplateSubgraph(ctx, store, attachID)
		if err != nil {
			return HandleErrorRespectJSON("loading attachment subgraph %s: %v", attachID, err)
		}
		attachments = append(attachments, attachmentInfo{
			id:       attachID,
			issue:    attachIssue,
			subgraph: attachSubgraph,
		})
	}

	// Apply variable defaults from formula
	vars = applyVariableDefaults(vars, subgraph)

	// Check for missing required variables (those without defaults)
	requiredVars := extractRequiredVariables(subgraph)
	for _, attach := range attachments {
		attachVars := extractRequiredVariables(attach.subgraph)
		for _, v := range attachVars {
			found := false
			for _, rv := range requiredVars {
				if rv == v {
					found = true
					break
				}
			}
			if !found {
				requiredVars = append(requiredVars, v)
			}
		}
	}
	var missingVars []string
	for _, v := range requiredVars {
		if _, ok := vars[v]; !ok {
			missingVars = append(missingVars, v)
		}
	}
	if len(missingVars) > 0 {
		return HandleErrorWithHintRespectJSON(
			fmt.Sprintf("missing required variables: %s", strings.Join(missingVars, ", ")),
			fmt.Sprintf("Provide them with: --var %s=<value>", missingVars[0]),
		)
	}

	// beads-8m9o7: enforce enum/pattern var constraints before spawning issues.
	// The cooked proto subgraph carries VarDefs (formula.VarDef); pour previously
	// only checked missing-required vars, so out-of-enum / pattern-violating
	// values were substituted into real, persistent issues. Validate the merged
	// (defaults-applied) value set against the same constraints cook enforces,
	// including attachment protos' var defs. Fail loud (rc=1, JSON-aware).
	if subgraph.VarDefs != nil {
		mergedDefs := make(map[string]formula.VarDef, len(subgraph.VarDefs))
		for name, def := range subgraph.VarDefs {
			mergedDefs[name] = def
		}
		for _, attach := range attachments {
			for name, def := range attach.subgraph.VarDefs {
				if _, exists := mergedDefs[name]; !exists {
					mergedDefs[name] = def
				}
			}
		}
		if err := formula.ValidateVarValues(mergedDefs, vars); err != nil {
			return HandleErrorRespectJSON("%v", err)
		}
	}

	if dryRun {
		// beads-51w8c (8lqh --json-contract family, sibling of the 8zed6 pour
		// vapor-advisory fix): --dry-run is the SAFE preview path scripts/agents
		// use before a real pour, so it is exactly where --json consumers live.
		// printPourDryRun writes plaintext via fmt.Printf, so `--dry-run --json`
		// leaked human text and broke `| jq`. Under --json emit a parseable
		// preview envelope instead.
		if jsonOutput {
			return outputPourDryRunJSON(subgraph, attachments, vars, assignee, attachType, protoID)
		}
		printPourDryRun(subgraph, attachments, vars, assignee, attachType, protoID)
		return nil
	}

	result, err := spawnMolecule(ctx, store, subgraph, vars, assignee, actor, false, types.IDPrefixMol)
	if err != nil {
		return HandleErrorRespectJSON("pouring proto: %v", err)
	}

	// Attach bonded protos
	totalAttached := 0
	if len(attachments) > 0 {
		spawnedMol, err := store.GetIssue(ctx, result.NewEpicID)
		if err != nil {
			return HandleErrorRespectJSON("loading spawned mol: %v", err)
		}

		for _, attach := range attachments {
			bondResult, err := bondProtoMol(ctx, store, attach.issue, spawnedMol, attachType, vars, "", actor, false, true)
			if err != nil {
				return HandleErrorRespectJSON("attaching %s: %v", attach.id, err)
			}
			totalAttached += bondResult.Spawned
		}
	}

	if jsonOutput {
		type pourResult struct {
			*InstantiateResult
			Attached int    `json:"attached"`
			Phase    string `json:"phase"`
		}
		return outputJSON(pourResult{result, totalAttached, "liquid"})
	}

	fmt.Printf("%s Poured mol: created %d issues\n", ui.RenderPass("✓"), result.Created)
	fmt.Printf("  Root issue: %s\n", result.NewEpicID)
	fmt.Printf("  Phase: liquid (persistent in .beads/)\n")
	if totalAttached > 0 {
		fmt.Printf("  Attached: %d issues from %d protos\n", totalAttached, len(attachments))
	}
	return nil
}

func init() {
	// Pour command flags
	pourCmd.Flags().StringArray("var", []string{}, "Variable substitution (key=value)")
	pourCmd.Flags().Bool("dry-run", false, "Preview what would be created")
	pourCmd.Flags().String("assignee", "", "Assign the root issue to this agent/user")
	pourCmd.Flags().StringSlice("attach", []string{}, "Proto to attach after spawning (repeatable)")
	pourCmd.Flags().String("attach-type", types.BondTypeSequential, "Bond type for attachments: sequential, parallel, or conditional")

	molCmd.AddCommand(pourCmd)
}
