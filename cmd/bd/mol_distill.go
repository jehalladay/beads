package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/formula"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/utils"
)

// distillPourDefaultPriority mirrors the priority that pour assigns to a step
// with no explicit priority (cook.go: `priority := 2`). Distill omits a step's
// priority only when it equals this default, so the round-trip is lossless for
// every other priority — including P0/critical (priority 0), which the earlier
// `> 0` guard wrongly dropped (beads-110o9).
const distillPourDefaultPriority = 2

var molDistillCmd = &cobra.Command{
	Use:   "distill <epic-id> [formula-name]",
	Short: "Extract a formula from an existing epic",
	Long: `Distill a molecule by extracting a reusable formula from an existing epic.

This is the reverse of pour: instead of formula → molecule, it's molecule → formula.

The distill command:
  1. Loads the existing epic and all its children
  2. Converts the structure to a .formula.json file
  3. Replaces concrete values with {{variable}} placeholders (via --var flags)

Use cases:
  - Team develops good workflow organically, wants to reuse it
  - Capture tribal knowledge as executable templates
  - Create starting point for similar future work

Variable syntax (both work - we detect which side is the concrete value):
  --var branch=feature-auth    Spawn-style: variable=value (recommended)
  --var feature-auth=branch    Substitution-style: value=variable

Output locations (first writable wins):
  1. <resolved-beads-dir>/formulas/ (project-level, default)
  2. <checkout-root>/.beads/formulas/ (repo-local formulas)
  3. ~/.beads/formulas/     (user-level, if project not writable)

Examples:
  bd mol distill bd-o5xe my-workflow
  bd mol distill bd-abc release-workflow --var feature_name=auth-refactor`,
	Args:          cobra.RangeArgs(1, 2),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runMolDistill,
}

// DistillResult holds the result of a distill operation
type DistillResult struct {
	FormulaName string   `json:"formula_name"`
	FormulaPath string   `json:"formula_path"`
	Steps       int      `json:"steps"`     // number of steps in formula
	Variables   []string `json:"variables"` // variables introduced
}

// collectSubgraphText gathers all searchable text from a molecule subgraph
func collectSubgraphText(subgraph *MoleculeSubgraph) string {
	var parts []string
	for _, issue := range subgraph.Issues {
		parts = append(parts, issue.Title)
		parts = append(parts, issue.Description)
		parts = append(parts, issue.Design)
		parts = append(parts, issue.AcceptanceCriteria)
		parts = append(parts, issue.Notes)
	}
	return strings.Join(parts, " ")
}

// parseDistillVar parses a --var flag with smart detection of syntax.
// Accepts both spawn-style (variable=value) and substitution-style (value=variable).
// Returns (findText, varName, error).
func parseDistillVar(varFlag, searchableText string) (string, string, error) {
	parts := strings.SplitN(varFlag, "=", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid format '%s', expected 'variable=value' or 'value=variable'", varFlag)
	}

	left, right := parts[0], parts[1]
	leftFound := strings.Contains(searchableText, left)
	rightFound := strings.Contains(searchableText, right)

	switch {
	case rightFound && !leftFound:
		// spawn-style: --var branch=feature-auth
		// left is variable name, right is the value to find
		return right, left, nil
	case leftFound && !rightFound:
		// substitution-style: --var feature-auth=branch
		// left is value to find, right is variable name
		return left, right, nil
	case leftFound && rightFound:
		// Both found - prefer spawn-style (more natural guess)
		// Agent likely typed: --var varname=concrete_value
		return right, left, nil
	default:
		return "", "", fmt.Errorf("neither '%s' nor '%s' found in epic text", left, right)
	}
}

func runMolDistill(cmd *cobra.Command, args []string) error {
	evt := metrics.NewCommandEvent("mol-distill")
	defer func() {
		if c := metrics.Global(); c != nil {
			c.CloseEventAndAdd(evt)
		}
	}()

	ctx := rootCtx

	// beads-ztu1e (ojyjj/mgjco/aocj fail-loud class, read-side): proxied-server
	// mode leaves the global store nil (main.go PersistentPreRunE returns before
	// newDoltStore), so the bare store==nil check misdiagnoses it as a local
	// "no database connection". mol distill scaffolds files + needs direct reads
	// through loadTemplateSubgraph (no proxied loader path), so fail loud with an
	// accurate message BEFORE the nil check, mirroring mol burn (ojyjj).
	if usesProxiedServer() {
		return HandleErrorRespectJSON("mol distill is not supported in proxied-server mode (connect directly with an embedded/dolt store)")
	}
	if store == nil {
		return HandleErrorRespectJSON("no database connection")
	}

	varFlags, _ := cmd.Flags().GetStringArray("var")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	outputDir, _ := cmd.Flags().GetString("output")

	epicID, err := utils.ResolvePartialID(ctx, store, args[0])
	if err != nil {
		return HandleErrorRespectJSON("'%s' not found", args[0])
	}

	subgraph, err := loadTemplateSubgraph(ctx, store, epicID)
	if err != nil {
		return HandleErrorRespectJSON("loading epic: %v", err)
	}

	formulaName := ""
	if len(args) > 1 {
		formulaName = args[1]
	} else {
		formulaName = sanitizeFormulaName(subgraph.Root.Title)
	}

	replacements := make(map[string]string)
	if len(varFlags) > 0 {
		searchableText := collectSubgraphText(subgraph)
		for _, v := range varFlags {
			findText, varName, err := parseDistillVar(v, searchableText)
			if err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
			replacements[findText] = varName
		}
	}

	f := subgraphToFormula(subgraph, formulaName, replacements)

	// beads-8tw1a: distill silently drops dependency edges that cross the epic
	// boundary (targets outside this epic's children). The drop is intended (a
	// formula must be self-contained) but invisible — step count is unchanged
	// and no depends_on is emitted — so the author never learns the poured
	// molecule will lose a blocker the source epic had. Warn (to stderr, so
	// --json stdout stays clean), mirroring the orphan-var warning class.
	if drops := externalDepDrops(subgraph); len(drops) > 0 {
		warnDroppedExternalDeps(cmd.ErrOrStderr(), drops)
	}

	outputPath := ""
	if outputDir != "" {
		outputPath = filepath.Join(outputDir, formulaName+formula.FormulaExt)
	} else {
		outputPath = findWritableFormulaDir(formulaName)
		if outputPath == "" {
			hint := "Try creating one of the formula search paths"
			if searchPaths := getFormulaSearchPaths(); len(searchPaths) > 0 {
				hint = fmt.Sprintf("Try: mkdir -p %s", searchPaths[0])
			}
			return HandleErrorWithHint("no writable formula directory found", hint)
		}
	}

	if dryRun {
		// beads-51w8c (8lqh --json-contract family): under --json emit a parseable
		// preview envelope instead of the plaintext dry-run block, so a scripted
		// `bd mol distill --dry-run --json | jq` parses.
		if jsonOutput {
			vars := make(map[string]string, len(replacements))
			for value, varName := range replacements {
				vars[varName] = value
			}
			return outputJSON(map[string]interface{}{
				"dry_run":      true,
				"epic_id":      epicID,
				"formula_name": formulaName,
				"output_path":  outputPath,
				"steps":        countSteps(f.Steps),
				"variables":    vars,
			})
		}
		fmt.Printf("\nDry run: would distill %d steps from %s into formula\n\n", countSteps(f.Steps), epicID)
		fmt.Printf("Formula: %s\n", formulaName)
		fmt.Printf("Output: %s\n", outputPath)
		if len(replacements) > 0 {
			fmt.Printf("\nVariables:\n")
			for value, varName := range replacements {
				fmt.Printf("  %s: \"%s\" → {{%s}}\n", varName, value, varName)
			}
		}
		fmt.Printf("\nStructure:\n")
		printFormulaStepsTree(f.Steps, "")
		return nil
	}

	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return HandleErrorRespectJSON("creating directory %s: %v", dir, err)
	}

	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return HandleErrorRespectJSON("encoding formula: %v", err)
	}

	// #nosec G306 -- Formula files are not sensitive
	if err := os.WriteFile(outputPath, data, 0644); err != nil {
		return HandleErrorRespectJSON("writing formula: %v", err)
	}

	result := &DistillResult{
		FormulaName: formulaName,
		FormulaPath: outputPath,
		Steps:       countSteps(f.Steps),
		// beads-m503p: report the vars actually DECLARED in the formula (post
		// orphan-pruning), not the raw --var replacements — otherwise --json
		// would advertise a var subgraphToFormula just dropped for having no
		// emitted placeholder. formulaVarNames keeps the 036h non-nil-slice
		// contract (variables:[] not null when empty).
		Variables: formulaVarNames(f.Vars),
	}

	if jsonOutput {
		return outputJSON(result)
	}

	fmt.Printf("%s Distilled formula: %d steps\n", ui.RenderPass("✓"), result.Steps)
	fmt.Printf("  Formula: %s\n", result.FormulaName)
	fmt.Printf("  Path: %s\n", result.FormulaPath)
	if len(result.Variables) > 0 {
		fmt.Printf("  Variables: %s\n", strings.Join(result.Variables, ", "))
	}
	fmt.Printf("\nTo instantiate:\n")
	fmt.Printf("  bd mol pour %s", result.FormulaName)
	for _, v := range result.Variables {
		fmt.Printf(" --var %s=<value>", v)
	}
	fmt.Println()
	return nil
}

// sanitizeFormulaName converts a title to a valid formula name
func sanitizeFormulaName(title string) string {
	// Convert to lowercase and replace spaces/special chars with hyphens
	re := regexp.MustCompile(`[^a-zA-Z0-9-]+`)
	name := re.ReplaceAllString(strings.ToLower(title), "-")
	// Remove leading/trailing hyphens and collapse multiple hyphens
	name = regexp.MustCompile(`-+`).ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if name == "" {
		name = "untitled"
	}
	return name
}

// findWritableFormulaDir finds the first writable formula directory
func findWritableFormulaDir(formulaName string) string {
	searchPaths := getFormulaSearchPaths()
	for _, dir := range searchPaths {
		// Try to create the directory if it doesn't exist
		if err := os.MkdirAll(dir, 0755); err == nil {
			// Check if we can write to it
			testPath := filepath.Join(dir, ".write-test")
			if f, err := os.Create(testPath); err == nil { //nolint:gosec // testPath is constructed from known search paths
				_ = f.Close()           // Best effort cleanup
				_ = os.Remove(testPath) // Best effort cleanup of temp file
				return filepath.Join(dir, formulaName+formula.FormulaExt)
			}
		}
	}
	return ""
}

// getVarNames extracts variable names from replacements map
func getVarNames(replacements map[string]string) []string {
	// beads-036h: non-nil empty slice so `bd mol distill --json` on a formula
	// with no replacements emits "variables":[] not null (json-ARRAY nil-slice
	// class, same as ExtractVariables). DistillResult.Variables has no omitempty
	// and is emitted via outputJSON; downstream only ranges/lens it.
	names := []string{}
	for _, varName := range replacements {
		names = append(names, varName)
	}
	return names
}

// formulaVarNames extracts the variable names actually DECLARED in a formula's
// Vars (beads-m503p) — the post-orphan-pruning set, so `bd mol distill --json`
// reports the vars the emitted formula truly carries, not the raw --var inputs.
// Returns a non-nil empty slice (036h contract: variables:[] not null).
func formulaVarNames(vars map[string]*formula.VarDef) []string {
	names := []string{}
	for varName := range vars {
		names = append(names, varName)
	}
	return names
}

// subgraphToFormula converts a molecule subgraph to a formula
func subgraphToFormula(subgraph *TemplateSubgraph, name string, replacements map[string]string) *formula.Formula {
	// Helper to apply replacements. Uses word-boundary regex to avoid
	// substring corruption (e.g., "4" matching inside "404").
	applyReplacements := func(text string) string {
		result := text
		for value, varName := range replacements {
			pattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(value) + `\b`)
			result = pattern.ReplaceAllString(result, "{{"+varName+"}}")
		}
		return result
	}

	// Build ID mapping for step references
	idToStepID := make(map[string]string)
	for _, issue := range subgraph.Issues {
		// Create a sanitized step ID from the issue ID
		stepID := sanitizeFormulaName(issue.Title)
		if stepID == "" {
			stepID = issue.ID
		}
		idToStepID[issue.ID] = stepID
	}

	// Build dependency map (issue ID -> list of depends-on IDs)
	depsByIssue := make(map[string][]string)
	for _, dep := range subgraph.Dependencies {
		depsByIssue[dep.IssueID] = append(depsByIssue[dep.IssueID], dep.DependsOnID)
	}

	// Convert issues to steps
	var steps []*formula.Step
	for _, issue := range subgraph.Issues {
		if issue.ID == subgraph.Root.ID {
			continue // Root becomes the formula itself
		}

		step := &formula.Step{
			ID:          idToStepID[issue.ID],
			Title:       applyReplacements(issue.Title),
			Description: applyReplacements(issue.Description),
			Type:        string(issue.IssueType),
		}

		// Copy priority unless it equals the pour default (beads-110o9).
		// Priority 0 is a VALID priority (P0/critical), NOT "unset" — the old
		// `> 0` guard treated P0 as unset, leaving step.Priority nil, so pour
		// (cook.go: `priority := 2; if step.Priority != nil ...`) silently
		// downgraded a P0 step to P2. Omitting only the pour default keeps
		// distilled formulas clean while round-tripping P0/P1/P3/P4 losslessly.
		if issue.Priority != distillPourDefaultPriority {
			p := issue.Priority
			step.Priority = &p
		}

		// Copy labels (excluding internal ones)
		for _, label := range issue.Labels {
			if label != MoleculeLabel && !strings.HasPrefix(label, "mol:") {
				step.Labels = append(step.Labels, label)
			}
		}

		// Convert dependencies to depends_on (skip root)
		if deps, ok := depsByIssue[issue.ID]; ok {
			for _, depID := range deps {
				if depID == subgraph.Root.ID {
					continue // Skip dependency on root (becomes formula itself)
				}
				if stepID, ok := idToStepID[depID]; ok {
					step.DependsOn = append(step.DependsOn, stepID)
				}
			}
		}

		steps = append(steps, step)
	}

	formulaDescription := applyReplacements(subgraph.Root.Description)

	// Build variable definitions — declare a var ONLY if its {{placeholder}}
	// actually landed in an emitted field (a step Title/Description or the
	// formula Description). beads-m503p: the validation scope
	// (collectSubgraphText, which INCLUDES the root epic Title) diverges from
	// the emit scope (the root becomes the formula itself, so its Title is
	// carried into NO emitted field). A --var value living only in the root
	// title matched at validation but its substitution landed nowhere, yielding
	// a declared-required var referenced by zero placeholders — orphaned and
	// unusable (pour keys required vars off {{...}} occurrences in the emitted
	// text, not the VarDefs flag, so it silently ignored the orphan too). Only
	// emitting vars whose placeholder is actually present keeps the declared
	// vars and the pour-side {{}} scan consistent and drops the orphan.
	var emitted strings.Builder
	for _, step := range steps {
		emitted.WriteString(step.Title)
		emitted.WriteByte(' ')
		emitted.WriteString(step.Description)
		emitted.WriteByte(' ')
	}
	emitted.WriteString(formulaDescription)
	emittedText := emitted.String()

	vars := make(map[string]*formula.VarDef)
	for _, varName := range replacements {
		if !strings.Contains(emittedText, "{{"+varName+"}}") {
			// Orphaned: value matched only in a non-emitted field (e.g. the
			// root Title). Don't declare a required var nothing references.
			continue
		}
		vars[varName] = &formula.VarDef{
			Description: fmt.Sprintf("Value for %s", varName),
			Required:    true,
		}
	}

	return &formula.Formula{
		Formula:     name,
		Description: formulaDescription,
		Version:     1,
		Type:        formula.TypeWorkflow,
		Vars:        vars,
		Steps:       steps,
	}
}

// externalDepDrop records a dependency edge that subgraphToFormula silently
// discards because its target lives outside the distilled epic (beads-8tw1a).
type externalDepDrop struct {
	FromID    string // in-epic issue whose dependency was dropped
	FromTitle string // its title (for a human-readable warning)
	TargetID  string // the external (cross-epic-boundary) dependency target
}

// externalDepDrops enumerates the cross-epic-boundary dependency edges that
// subgraphToFormula drops (beads-8tw1a). subgraphToFormula only carries a
// depends_on when the target is another child of the same epic (idToStepID
// hit at mol_distill.go:375); a dep whose target is NOT among the epic's own
// children (and is not the root, which becomes the formula itself) is dropped.
// Dropping them is INTENDED — a distilled formula must be self-contained — but
// the drop is otherwise invisible (step count unchanged, no depends_on entry),
// which silently strips a blocker the source epic had. Surfacing them lets the
// command warn, mirroring the orphan-var warning class. This mirrors
// subgraphToFormula's own membership test rather than threading a second return
// value through it, so the existing single-return callers/tests are untouched.
func externalDepDrops(subgraph *TemplateSubgraph) []externalDepDrop {
	inEpic := make(map[string]bool, len(subgraph.Issues))
	titleByID := make(map[string]string, len(subgraph.Issues))
	for _, issue := range subgraph.Issues {
		inEpic[issue.ID] = true
		titleByID[issue.ID] = issue.Title
	}

	var drops []externalDepDrop
	for _, dep := range subgraph.Dependencies {
		// A dep on the root is intentionally elided (root becomes the formula
		// itself), and in-epic deps are preserved as step depends_on — neither
		// is a silent cross-boundary loss.
		if dep.DependsOnID == subgraph.Root.ID {
			continue
		}
		if inEpic[dep.DependsOnID] {
			continue
		}
		drops = append(drops, externalDepDrop{
			FromID:    dep.IssueID,
			FromTitle: titleByID[dep.IssueID],
			TargetID:  dep.DependsOnID,
		})
	}
	return drops
}

// warnDroppedExternalDeps prints a warning (beads-8tw1a) naming the external
// dependency targets distill dropped, so the author knows the emitted formula —
// and therefore any poured molecule — no longer carries those blockers. Written
// to w (stderr) to keep --json stdout parseable.
func warnDroppedExternalDeps(w io.Writer, drops []externalDepDrop) {
	fmt.Fprintf(w, "Warning: dropped %d cross-epic dependency %s (formulas are self-contained; the poured molecule will not re-block on %s):\n",
		len(drops), pluralizeWord(len(drops), "edge", "edges"), pluralizeWord(len(drops), "it", "them"))
	for _, d := range drops {
		from := d.FromID
		if d.FromTitle != "" {
			from = fmt.Sprintf("%s (%s)", d.FromID, d.FromTitle)
		}
		fmt.Fprintf(w, "  %s → %s\n", from, d.TargetID)
	}
}

// pluralizeWord picks the singular or plural form based on n.
func pluralizeWord(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}

func init() {
	molDistillCmd.Flags().StringArray("var", []string{}, "Replace value with {{variable}} placeholder (variable=value)")
	molDistillCmd.Flags().Bool("dry-run", false, "Preview what would be created")
	molDistillCmd.Flags().String("output", "", "Output directory for formula file")

	molCmd.AddCommand(molDistillCmd)
}
