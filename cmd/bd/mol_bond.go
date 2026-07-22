package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/formula"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/utils"
)

var molBondCmd = &cobra.Command{
	Use:     "bond <A> <B>",
	Aliases: []string{"fart"}, // Easter egg: molecules can produce gas
	Short:   "Bond two protos or molecules together",
	Long: `Bond two protos or molecules to create a compound.

The bond command is polymorphic - it handles different operand types:

  formula + formula → cook both, compound proto
  formula + proto   → cook formula, compound proto
  formula + mol     → cook formula, spawn and attach
  proto + proto     → compound proto (reusable template)
  proto + mol       → spawn proto, attach to molecule
  mol + proto       → spawn proto, attach to molecule
  mol + mol         → join into compound molecule

Formula names (e.g., mol-polecat-arm) are cooked inline as ephemeral protos.
This avoids needing pre-cooked proto beads in the database.

Bond types:
  sequential (default) - B runs after A completes
  parallel            - B runs alongside A
  conditional         - B runs only if A fails

Phase control:
  By default, spawned protos follow the target's phase:
  - Attaching to mol (Ephemeral=false) → spawns as persistent (Ephemeral=false)
  - Attaching to ephemeral issue (Ephemeral=true) → spawns as ephemeral (Ephemeral=true)

  Override with:
  --pour  Force spawn as liquid (persistent, Ephemeral=false)
  --ephemeral  Force spawn as vapor (ephemeral, Ephemeral=true, excluded from Dolt sync via dolt_ignore)

Dynamic bonding (Christmas Ornament pattern):
  Use --ref to specify a custom child reference with variable substitution.
  This creates IDs like "parent.child-ref" instead of random hashes.

  Example:
    bd mol bond mol-worker-arm bd-patrol --ref arm-{{worker_name}} --var worker_name=ace
    # Creates: bd-patrol.arm-ace (and children like bd-patrol.arm-ace.capture)

Use cases:
  - Found important bug during patrol? Use --pour to persist it
  - Need ephemeral diagnostic on persistent feature? Use --ephemeral
  - Spawning per-worker arms on a patrol? Use --ref for readable IDs

Examples:
  bd mol bond mol-feature mol-deploy                    # Compound proto
  bd mol bond mol-feature mol-deploy --type parallel    # Run in parallel
  bd mol bond mol-feature bd-abc123                     # Attach proto to molecule
  bd mol bond bd-abc123 bd-def456                       # Join two molecules
  bd mol bond mol-critical-bug wisp-patrol --pour       # Persist found bug
  bd mol bond mol-temp-check bd-feature --ephemeral          # Ephemeral diagnostic
  bd mol bond mol-arm bd-patrol --ref arm-{{name}} --var name=ace  # Dynamic child ID`,
	Args:          cobra.ExactArgs(2),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runMolBond,
}

// BondResult holds the result of a bond operation
type BondResult struct {
	ResultID   string            `json:"result_id"`
	ResultType string            `json:"result_type"` // "compound_proto" or "compound_molecule"
	BondType   string            `json:"bond_type"`
	Spawned    int               `json:"spawned,omitempty"`    // Number of issues spawned (if proto was involved)
	IDMapping  map[string]string `json:"id_mapping,omitempty"` // Old ID -> new ID for spawned issues
}

func runMolBond(cmd *cobra.Command, args []string) error {
	CheckReadonly("mol bond")

	evt := metrics.NewCommandEvent("mol-bond")
	defer func() {
		if c := metrics.Global(); c != nil {
			c.CloseEventAndAdd(evt)
		}
	}()

	ctx := rootCtx

	// beads-ojyjj (aocj fail-loud class): in proxied-server mode main.go's
	// PersistentPreRunE returns before newDoltStore (main.go:1147-1155) leaving
	// the global store nil, so mol bond's resolve + bondProtoMol transact would
	// nil-panic — and the bare store==nil check misdiagnoses the proxied config
	// as a local "no database connection". Bond writes via a raw
	// storage.Transaction the proxied UOW does not yield, so fail loud with an
	// accurate message (mirrors mgjco/merge-slot). Guard BEFORE the nil check.
	if usesProxiedServer() {
		return HandleErrorRespectJSON("mol bond is not supported in proxied-server mode (connect directly with an embedded/dolt store)")
	}
	if err := ensureStoreActive(); err != nil {
		return HandleErrorWithHintRespectJSON(err.Error(), diagHint())
	}

	bondType, _ := cmd.Flags().GetString("type")
	customTitle, _ := cmd.Flags().GetString("as")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	varFlags, _ := cmd.Flags().GetStringArray("var")
	ephemeral, _ := cmd.Flags().GetBool("ephemeral")
	pour, _ := cmd.Flags().GetBool("pour")
	childRef, _ := cmd.Flags().GetString("ref")

	if ephemeral && pour {
		return HandleErrorRespectJSON("cannot use both --ephemeral and --pour")
	}

	if bondType != types.BondTypeSequential && bondType != types.BondTypeParallel && bondType != types.BondTypeConditional {
		return HandleErrorRespectJSON("invalid bond type '%s', must be: sequential, parallel, or conditional", bondType)
	}

	vars := make(map[string]string)
	for _, v := range varFlags {
		parts := strings.SplitN(v, "=", 2)
		if len(parts) != 2 {
			return HandleErrorRespectJSON("invalid variable format '%s', expected 'key=value'", v)
		}
		vars[parts[0]] = parts[1]
	}

	if dryRun {
		issueA, formulaA, err := resolveOrDescribe(ctx, store, args[0])
		if err != nil {
			return HandleErrorRespectJSON("%v", err)
		}
		issueB, formulaB, err := resolveOrDescribe(ctx, store, args[1])
		if err != nil {
			return HandleErrorRespectJSON("%v", err)
		}

		printMolBondDryRun(issueA, issueB, formulaA, formulaB, args[0], args[1], bondType, customTitle, childRef, vars, ephemeral, pour)
		return nil
	}

	subgraphA, cookedA, err := resolveOrCookToSubgraph(ctx, store, args[0], vars)
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}
	subgraphB, cookedB, err := resolveOrCookToSubgraph(ctx, store, args[1], vars)
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}

	// No cleanup needed - in-memory subgraphs don't pollute the DB
	issueA := subgraphA.Root
	issueB := subgraphB.Root
	idA := issueA.ID
	idB := issueB.ID

	// Determine operand types. Use the SAME proto predicate as the help text,
	// the --dry-run preview, and isProto() — the template LABEL (isProto) OR a
	// formula-cooked operand (cookedX) — NOT the is_template COLUMN. The
	// is_template column is written only by formula-cooked protos
	// (cook.go/molecules.go), NOT by the documented `bd create --label template`,
	// so keying dispatch off issueX.IsTemplate misrouted every canonically
	// label-defined proto to the bondMolMol default: proto+proto silently
	// produced a compound_molecule (mutating operand A) instead of a compound
	// proto, and proto+mol / mol+proto never spawned the proto — while the
	// dry-run preview promised the correct proto result. beads-v8ck8.
	aIsProto := isProto(issueA) || cookedA
	bIsProto := isProto(issueB) || cookedB

	// Dispatch based on operand types
	// All operations use the main store; wisp flag determines ephemeral vs persistent
	var result *BondResult
	switch {
	case aIsProto && bIsProto:
		// Compound protos are templates - always persistent.
		// beads-dvkc5: pass the subgraphs + cooked flags so a formula-COOKED
		// operand (an in-memory subgraph never written to the DB) is materialized
		// into the DB before the compound is FK-linked to its root. A DB-resident
		// proto operand (cookedX=false) is linked as before.
		result, err = bondProtoProto(ctx, store, subgraphA, subgraphB, cookedA, cookedB, bondType, customTitle, actor)
	case aIsProto && !bIsProto:
		// Pass subgraph directly if cooked from formula
		if cookedA {
			result, err = bondProtoMolWithSubgraph(ctx, store, subgraphA, issueA, issueB, bondType, vars, childRef, actor, ephemeral, pour)
		} else {
			result, err = bondProtoMol(ctx, store, issueA, issueB, bondType, vars, childRef, actor, ephemeral, pour)
		}
	case !aIsProto && bIsProto:
		// Pass subgraph directly if cooked from formula
		if cookedB {
			result, err = bondProtoMolWithSubgraph(ctx, store, subgraphB, issueB, issueA, bondType, vars, childRef, actor, ephemeral, pour)
		} else {
			result, err = bondMolProto(ctx, store, issueA, issueB, bondType, vars, childRef, actor, ephemeral, pour)
		}
	default:
		result, err = bondMolMol(ctx, store, issueA, issueB, bondType, actor)
	}

	if err != nil {
		return HandleErrorRespectJSON("bonding: %v", err)
	}

	if jsonOutput {
		return outputJSON(result)
	}

	fmt.Printf("%s Bonded: %s + %s\n", ui.RenderPass("✓"), idA, idB)
	fmt.Printf("  Result: %s (%s)\n", result.ResultID, result.ResultType)
	if result.Spawned > 0 {
		fmt.Printf("  Spawned: %d issues\n", result.Spawned)
	}
	if ephemeral {
		fmt.Printf("  Phase: vapor (ephemeral, Ephemeral=true)\n")
	} else if pour {
		fmt.Printf("  Phase: liquid (persistent, Ephemeral=false)\n")
	}
	return nil
}

// printMolBondDryRun renders the `bd mol bond --dry-run` preview. Operand
// titles are routed through displayTitle (ui.SanitizeForTerminal) because a
// proto/mol title can originate from an untrusted import (JSONL/markdown/SCM)
// carrying OSC/CSI terminal-control escapes (OSC 0 window-title / OSC 52
// clipboard); printing them raw would inject control sequences onto the
// preview lines. 7n9y sink-class slice (beads-hckxx).
func printMolBondDryRun(issueA, issueB *types.Issue, formulaA, formulaB, argA, argB, bondType, customTitle, childRef string, vars map[string]string, ephemeral, pour bool) {
	idA := argA
	idB := argB
	aIsProto := false
	bIsProto := false

	if issueA != nil {
		idA = issueA.ID
		aIsProto = isProto(issueA)
	}
	if issueB != nil {
		idB = issueB.ID
		bIsProto = isProto(issueB)
	}

	// Formulas are treated as protos for dry-run display
	if formulaA != "" {
		aIsProto = true
	}
	if formulaB != "" {
		bIsProto = true
	}

	fmt.Printf("\nDry run: bond %s + %s\n", idA, idB)
	if formulaA != "" {
		fmt.Printf("  A: %s (formula → will cook as proto)\n", formulaA)
	} else if issueA != nil {
		fmt.Printf("  A: %s (%s)\n", displayTitle(issueA.Title), operandType(aIsProto))
	}
	if formulaB != "" {
		fmt.Printf("  B: %s (formula → will cook as proto)\n", formulaB)
	} else if issueB != nil {
		fmt.Printf("  B: %s (%s)\n", displayTitle(issueB.Title), operandType(bIsProto))
	}
	fmt.Printf("  Bond type: %s\n", bondType)
	if ephemeral {
		fmt.Printf("  Phase override: vapor (--ephemeral)\n")
	} else if pour {
		fmt.Printf("  Phase override: liquid (--pour)\n")
	}
	if childRef != "" {
		resolvedRef := substituteVariables(childRef, vars)
		fmt.Printf("  Child ref: %s (resolved: %s)\n", childRef, resolvedRef)
	}
	if aIsProto && bIsProto {
		fmt.Printf("  Result: compound proto\n")
		if customTitleProvided(customTitle) {
			fmt.Printf("  Custom title: %s\n", displayTitle(customTitle))
		}
	} else if aIsProto || bIsProto {
		fmt.Printf("  Result: spawn proto, attach to molecule\n")
	} else {
		fmt.Printf("  Result: compound molecule\n")
	}
	if formulaA != "" || formulaB != "" {
		fmt.Printf("\n  Note: Cooked formulas are ephemeral and deleted after bonding.\n")
	}
}

// isProto checks if an issue is a proto (has the template label)
func isProto(issue *types.Issue) bool {
	for _, label := range issue.Labels {
		if label == MoleculeLabel {
			return true
		}
	}
	return false
}

// operandType returns a human-readable type string
func operandType(isProtoIssue bool) string {
	if isProtoIssue {
		return "proto"
	}
	return "molecule"
}

// customTitleProvided reports whether a `bd mol bond --as` value should override
// the computed "Compound: A + B" title. --as is an OPTIONAL free-text override
// with no default, so a whitespace-only value (beads-2itry, the in93a
// whitespace-override class: dolt commit -m by9ph, mol squash --summary au0rt,
// todo done --reason 07sko) must be treated as NOT provided and fall through to
// the computed title rather than clobbering it with blank whitespace. A genuine
// title is accepted as-provided and used VERBATIM. Both consumers — the dry-run
// display (printMolBondDryRun) and the store leg (bondProtoProto) — gate on this
// so they agree by construction.
func customTitleProvided(customTitle string) bool {
	return strings.TrimSpace(customTitle) != ""
}

// materializeCookedProtoTx persists a formula-COOKED in-memory subgraph as a
// DB-resident proto within an existing transaction, so a compound can be
// FK-linked to its root. beads-dvkc5: a formula operand is cooked to an
// in-memory *TemplateSubgraph (gt-4v1eo: "no DB storage") whose root was never
// CreateIssue'd, so bondProtoProto's AddDependency(compound → root.ID) used to
// FK-fail ("issue mol-deploy not found"). This mirrors cook's persist path
// (cookPlanTx): create the issues, stamp the root's template label, recreate
// deps — with the SAME reserved-label guard (beads-1zq73/o70m1) since a
// formula's step labels flow verbatim from author-controlled TOML and storage
// AddLabel carries no guard.
func materializeCookedProtoTx(ctx context.Context, tx storage.Transaction, subgraph *TemplateSubgraph, actorName string) error {
	for _, issue := range subgraph.Issues {
		for _, label := range issue.Labels {
			if msg := reservedIdentityLabelError(label); msg != "" {
				return fmt.Errorf("%s", msg)
			}
			if msg := providesLabelError(label); msg != "" {
				return fmt.Errorf("%s", msg)
			}
		}
	}
	// Create all issues; inline step labels persist via PersistLabels.
	if err := tx.CreateIssues(ctx, subgraph.Issues, actorName); err != nil {
		return fmt.Errorf("materializing proto %s: %w", subgraph.Root.ID, err)
	}
	// The cooked root carries IsTemplate=true but NOT the molecule LABEL inline
	// (cook adds it separately in collectCookPlan) — add it so the materialized
	// proto matches a cook-persisted proto and isProto() recognizes it.
	if err := tx.AddLabel(ctx, subgraph.Root.ID, MoleculeLabel, actorName); err != nil {
		return fmt.Errorf("adding template label to %s: %w", subgraph.Root.ID, err)
	}
	// Recreate dependencies with the (verbatim) cooked IDs.
	for _, dep := range subgraph.Dependencies {
		if err := tx.AddDependency(ctx, dep, actorName); err != nil {
			return fmt.Errorf("linking proto %s deps: %w", subgraph.Root.ID, err)
		}
	}
	return nil
}

// bondProtoProto bonds two protos to create a compound proto.
// A formula-COOKED operand (cookedX=true) is an in-memory subgraph that is
// materialized into the DB inside the bond transaction before FK-linking; a
// DB-resident proto operand (cookedX=false) is linked as-is (beads-dvkc5).
func bondProtoProto(ctx context.Context, s storage.DoltStorage, subgraphA, subgraphB *TemplateSubgraph, cookedA, cookedB bool, bondType, customTitle, actorName string) (*BondResult, error) {
	protoA := subgraphA.Root
	protoB := subgraphB.Root

	// Register any non-built-in issue types (e.g. formula "gate" beads) used by
	// a cooked subgraph BEFORE opening the transaction — SetConfig may commit
	// on its own, matching cloneSubgraph (GH#3213). No-op for DB-resident protos.
	if cookedA {
		if err := ensureSubgraphCustomTypes(ctx, s, subgraphA); err != nil {
			return nil, fmt.Errorf("registering custom types for %s: %w", protoA.ID, err)
		}
	}
	if cookedB {
		if err := ensureSubgraphCustomTypes(ctx, s, subgraphB); err != nil {
			return nil, fmt.Errorf("registering custom types for %s: %w", protoB.ID, err)
		}
	}

	// Create compound proto: a new root that references both protos as children
	// The compound root will be a new issue that ties them together
	compoundTitle := fmt.Sprintf("Compound: %s + %s", protoA.Title, protoB.Title)
	if customTitleProvided(customTitle) {
		compoundTitle = customTitle
	}

	spawned := 0
	var compoundID string
	err := transact(ctx, s, fmt.Sprintf("bd: bond protos %s + %s", protoA.ID, protoB.ID), func(tx storage.Transaction) error {
		// Materialize any formula-cooked operand into the DB first, so the
		// compound's parent-child dependencies below reference real rows.
		if cookedA {
			if err := materializeCookedProtoTx(ctx, tx, subgraphA, actorName); err != nil {
				return err
			}
			spawned += len(subgraphA.Issues)
		}
		if cookedB {
			if err := materializeCookedProtoTx(ctx, tx, subgraphB, actorName); err != nil {
				return err
			}
			spawned += len(subgraphB.Issues)
		}

		// Create compound root issue
		compound := &types.Issue{
			Title:       compoundTitle,
			Description: fmt.Sprintf("Compound proto bonding %s and %s", protoA.ID, protoB.ID),
			Status:      types.StatusOpen,
			Priority:    minPriority(protoA.Priority, protoB.Priority),
			IssueType:   types.TypeEpic,
			BondedFrom: []types.BondRef{
				{SourceID: protoA.ID, BondType: bondType, BondPoint: ""},
				{SourceID: protoB.ID, BondType: bondType, BondPoint: ""},
			},
		}
		if err := tx.CreateIssue(ctx, compound, actorName); err != nil {
			return fmt.Errorf("creating compound: %w", err)
		}
		compoundID = compound.ID

		// Add template label (labels are stored separately, not in issue table)
		if err := tx.AddLabel(ctx, compoundID, MoleculeLabel, actorName); err != nil {
			return fmt.Errorf("adding template label: %w", err)
		}

		// Add parent-child dependencies from compound to both proto roots
		depA := &types.Dependency{
			IssueID:     protoA.ID,
			DependsOnID: compoundID,
			Type:        types.DepParentChild,
		}
		if err := tx.AddDependency(ctx, depA, actorName); err != nil {
			return fmt.Errorf("linking proto A: %w", err)
		}

		depB := &types.Dependency{
			IssueID:     protoB.ID,
			DependsOnID: compoundID,
			Type:        types.DepParentChild,
		}
		if err := tx.AddDependency(ctx, depB, actorName); err != nil {
			return fmt.Errorf("linking proto B: %w", err)
		}

		// For sequential/conditional bonding, add blocking dependency: B blocks on A
		// Sequential: B runs after A completes (any outcome)
		// Conditional: B runs only if A fails
		if bondType == types.BondTypeSequential || bondType == types.BondTypeConditional {
			depType := types.DepBlocks
			if bondType == types.BondTypeConditional {
				depType = types.DepConditionalBlocks
			}
			seqDep := &types.Dependency{
				IssueID:     protoB.ID,
				DependsOnID: protoA.ID,
				Type:        depType,
			}
			if err := tx.AddDependency(ctx, seqDep, actorName); err != nil {
				return fmt.Errorf("adding sequence dep: %w", err)
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return &BondResult{
		ResultID:   compoundID,
		ResultType: "compound_proto",
		BondType:   bondType,
		// Spawned counts issues materialized into the DB from formula-cooked
		// operands (0 for two DB-resident protos, preserving prior behavior).
		Spawned: spawned,
	}, nil
}

// bondProtoMol bonds a proto to an existing molecule by spawning the proto.
// If childRef is provided, generates custom IDs like "parent.childref" (dynamic bonding).
// protoSubgraph can be nil if proto is from DB (will be loaded), or pre-loaded for formulas.
func bondProtoMol(ctx context.Context, s storage.DoltStorage, proto, mol *types.Issue, bondType string, vars map[string]string, childRef string, actorName string, ephemeralFlag, pourFlag bool) (*BondResult, error) {
	return bondProtoMolWithSubgraph(ctx, s, nil, proto, mol, bondType, vars, childRef, actorName, ephemeralFlag, pourFlag)
}

// bondProtoMolWithSubgraph is the internal implementation that accepts a pre-loaded subgraph.
func bondProtoMolWithSubgraph(ctx context.Context, s storage.DoltStorage, protoSubgraph *TemplateSubgraph, proto, mol *types.Issue, bondType string, vars map[string]string, childRef string, actorName string, ephemeralFlag, pourFlag bool) (*BondResult, error) {
	// Use provided subgraph or load from DB
	subgraph := protoSubgraph
	if subgraph == nil {
		var err error
		subgraph, err = loadTemplateSubgraph(ctx, s, proto.ID)
		if err != nil {
			return nil, fmt.Errorf("loading proto: %w", err)
		}
	}

	// Check for missing variables
	requiredVars := extractAllVariables(subgraph)
	var missingVars []string
	for _, v := range requiredVars {
		if _, ok := vars[v]; !ok {
			missingVars = append(missingVars, v)
		}
	}
	if len(missingVars) > 0 {
		return nil, fmt.Errorf("missing required variables: %s (use --var)", strings.Join(missingVars, ", "))
	}

	// Determine ephemeral flag based on explicit flags or target's phase
	// --ephemeral: force ephemeral=true, --pour: force ephemeral=false, neither: follow target
	makeEphemeral := mol.Ephemeral // Default: follow target's phase
	if ephemeralFlag {
		makeEphemeral = true
	} else if pourFlag {
		makeEphemeral = false
	}

	// Determine dependency type for attachment
	// Sequential: use blocks (B runs after A completes)
	// Conditional: use conditional-blocks (B runs only if A fails)
	// Parallel: use parent-child (organizational, no blocking)
	var depType types.DependencyType
	switch bondType {
	case types.BondTypeSequential:
		depType = types.DepBlocks
	case types.BondTypeConditional:
		depType = types.DepConditionalBlocks
	default:
		depType = types.DepParentChild
	}

	// Build CloneOptions for spawning
	// AttachToID ensures spawn + attach happen in a single transaction (bd-wvplu)
	opts := CloneOptions{
		Vars:          vars,
		Actor:         actorName,
		Ephemeral:     makeEphemeral,
		AttachToID:    mol.ID,
		AttachDepType: depType,
	}

	// Dynamic bonding: use custom IDs if childRef is provided
	if childRef != "" {
		opts.ParentID = mol.ID
		opts.ChildRef = childRef
	}

	// Spawn the proto and atomically attach to molecule
	spawnResult, err := spawnMoleculeWithOptions(ctx, s, subgraph, opts)
	if err != nil {
		return nil, fmt.Errorf("spawning and attaching proto: %w", err)
	}

	return &BondResult{
		ResultID:   mol.ID,
		ResultType: "compound_molecule",
		BondType:   bondType,
		Spawned:    spawnResult.Created,
		IDMapping:  spawnResult.IDMapping,
	}, nil
}

// bondMolProto bonds a molecule to a proto (symmetric with bondProtoMol)
func bondMolProto(ctx context.Context, s storage.DoltStorage, mol, proto *types.Issue, bondType string, vars map[string]string, childRef string, actorName string, ephemeralFlag, pourFlag bool) (*BondResult, error) {
	// Same as bondProtoMol but with arguments swapped
	return bondProtoMol(ctx, s, proto, mol, bondType, vars, childRef, actorName, ephemeralFlag, pourFlag)
}

// wouldCreateCycle checks whether adding an edge (newDepID depends on newDependsOnID)
// would create a cycle in the dependency graph. It does a BFS from newDependsOnID
// following "depends on" edges; if newDepID is reachable, a cycle would be formed.
// Returns (hasCycle, cyclePath) where cyclePath shows the chain if found.
func wouldCreateCycle(ctx context.Context, s storage.DoltStorage, newDepID, newDependsOnID string) (bool, []string) {
	visited := map[string]bool{newDependsOnID: true}
	// parent tracks how we reached each node, for path reconstruction.
	parent := map[string]string{newDependsOnID: ""}
	queue := []string{newDependsOnID}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		deps, err := s.GetDependencyRecords(ctx, current)
		if err != nil {
			// If we can't query deps for a node, skip it rather than failing.
			continue
		}
		for _, dep := range deps {
			next := dep.DependsOnID
			if next == newDepID {
				// Found the cycle. Reconstruct the path.
				path := []string{newDepID}
				for node := current; node != ""; node = parent[node] {
					path = append(path, node)
				}
				// Reverse to get forward direction.
				for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
					path[i], path[j] = path[j], path[i]
				}
				// Append newDepID again to show the cycle closing.
				path = append(path, newDepID)
				return true, path
			}
			if !visited[next] {
				visited[next] = true
				parent[next] = current
				queue = append(queue, next)
			}
		}
	}
	return false, nil
}

// bondMolMol bonds two molecules together.
// It checks for transitive cycles in the dependency graph (GH#2719).
func bondMolMol(ctx context.Context, s storage.DoltStorage, molA, molB *types.Issue, bondType, actorName string) (*BondResult, error) {
	// The bond creates: molB depends on molA (IssueID=molB.ID, DependsOnID=molA.ID).
	// A cycle exists if molA already transitively depends on molB, because then
	// adding molB→molA would close the loop: molA→...→molB→molA.
	hasCycle, cyclePath := wouldCreateCycle(ctx, s, molB.ID, molA.ID)
	if hasCycle {
		return nil, fmt.Errorf("cannot bond %s → %s: would create a transitive dependency cycle: %s",
			molA.ID, molB.ID, strings.Join(cyclePath, " → "))
	}

	err := transact(ctx, s, fmt.Sprintf("bd: bond molecules %s + %s", molA.ID, molB.ID), func(tx storage.Transaction) error {
		// Add dependency: B links to A
		// Sequential: use blocks (B runs after A completes)
		// Conditional: use conditional-blocks (B runs only if A fails)
		// Parallel: use parent-child (organizational, no blocking)
		// Note: Schema only allows one dependency per (issue_id, target) pair (target = typed column)
		var depType types.DependencyType
		switch bondType {
		case types.BondTypeSequential:
			depType = types.DepBlocks
		case types.BondTypeConditional:
			depType = types.DepConditionalBlocks
		default:
			depType = types.DepParentChild
		}
		dep := &types.Dependency{
			IssueID:     molB.ID,
			DependsOnID: molA.ID,
			Type:        depType,
		}
		if err := tx.AddDependency(ctx, dep, actorName); err != nil {
			return fmt.Errorf("linking molecules: %w", err)
		}

		// Note: bonded_from field tracking is not yet supported by storage layer.
		// The dependency relationship captures the bonding semantics.
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("linking molecules: %w", err)
	}

	return &BondResult{
		ResultID:   molA.ID,
		ResultType: "compound_molecule",
		BondType:   bondType,
	}, nil
}

// minPriority returns the higher priority (lower number)
func minPriority(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// resolveOrDescribe checks if an operand is an issue or formula without cooking.
// Used for dry-run mode. Returns (issue, formulaName, error).
// If it's an issue, issue is set. If it's a formula, formulaName is set.
func resolveOrDescribe(ctx context.Context, s storage.DoltStorage, operand string) (*types.Issue, string, error) {
	// First, try to resolve as an existing issue
	id, err := utils.ResolvePartialID(ctx, s, operand)
	if err == nil {
		issue, err := s.GetIssue(ctx, id)
		if err == nil {
			return issue, "", nil
		}
	}

	// Not found as an issue — try to load it as a formula UNCONDITIONALLY,
	// matching `bd mol pour`'s resolveAndCookFormulaWithVars (pour.go:~154).
	// beads-dvkc5: the old looksLikeFormulaName pre-gate (mol-/.formula//\ only)
	// rejected plain distilled formula names (e.g. `deploy-proto`) that pour
	// cooks fine — a pour/bond formula-resolution twin-divergence. Let the
	// formula loader be the authority on what is a valid formula name; a name
	// that is neither an issue nor a loadable formula still yields the same
	// not-found error below.
	parser := formula.NewParser()
	f, err := parser.LoadByName(operand)
	if err != nil {
		return nil, "", fmt.Errorf("'%s' not found as issue or formula: %w", operand, err)
	}

	return nil, f.Formula, nil
}

// resolveOrCookToSubgraph tries to resolve an operand as an issue ID or formula.
// If it's an issue, loads the subgraph from DB. If it's a formula, cooks inline to subgraph.
// Returns the subgraph, whether it was cooked from formula, and any error.
//
// The vars parameter is used for step condition filtering (bd-7zka.1).
// This implements gt-4v1eo: formulas are cooked to in-memory subgraphs (no DB storage).
func resolveOrCookToSubgraph(ctx context.Context, s storage.DoltStorage, operand string, vars map[string]string) (*TemplateSubgraph, bool, error) {
	// First, try to resolve as an existing issue
	id, err := utils.ResolvePartialID(ctx, s, operand)
	if err == nil {
		issue, err := s.GetIssue(ctx, id)
		if err == nil {
			// Check if it's a proto (template)
			if isProto(issue) {
				subgraph, err := loadTemplateSubgraph(ctx, s, id)
				if err != nil {
					return nil, false, fmt.Errorf("loading proto subgraph '%s': %w", id, err)
				}
				return subgraph, false, nil
			}
			// It's a molecule, not a proto - wrap it as a single-issue subgraph
			return &TemplateSubgraph{
				Root:     issue,
				Issues:   []*types.Issue{issue},
				IssueMap: map[string]*types.Issue{issue.ID: issue},
			}, false, nil
		}
	}

	// Not found as an issue — cook it as a formula UNCONDITIONALLY, matching
	// `bd mol pour` (pour.go:~154: "This works for any valid formula name, not
	// just 'mol-' prefixed ones"). beads-dvkc5: the old looksLikeFormulaName
	// pre-gate rejected plain distilled formula names that pour cooks fine
	// (pour/bond twin-divergence). resolveAndCookFormulaWithVars is the
	// authority — a name that is neither an issue nor a valid formula returns
	// the same not-found error.
	// Pass vars for step condition filtering (bd-7zka.1)
	subgraph, err := resolveAndCookFormulaWithVars(operand, nil, vars)
	if err != nil {
		return nil, false, fmt.Errorf("'%s' not found as issue or formula: %w", operand, err)
	}

	return subgraph, true, nil
}

func init() {
	molBondCmd.Flags().String("type", types.BondTypeSequential, "Bond type: sequential, parallel, or conditional")
	molBondCmd.Flags().String("as", "", "Custom title for compound proto (proto+proto only)")
	molBondCmd.Flags().Bool("dry-run", false, "Preview what would be created")
	molBondCmd.Flags().StringArray("var", []string{}, "Variable substitution for spawned protos (key=value)")
	molBondCmd.Flags().Bool("ephemeral", false, "Force spawn as vapor (ephemeral, Ephemeral=true)")
	molBondCmd.Flags().Bool("pour", false, "Force spawn as liquid (persistent, Ephemeral=false)")
	molBondCmd.Flags().String("ref", "", "Custom child reference with {{var}} substitution (e.g., arm-{{polecat_name}})")

	molCmd.AddCommand(molBondCmd)
}
