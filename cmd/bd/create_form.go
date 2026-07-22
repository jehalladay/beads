package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

// createFormRawInput holds the raw string values from the form UI.
// This struct encapsulates all form fields before parsing/conversion.
type createFormRawInput struct {
	Title       string
	Description string
	IssueType   string
	Priority    string // String from select, e.g., "0", "1", "2"
	Assignee    string
	Labels      string // Comma-separated
	Design      string
	Acceptance  string
	ExternalRef string
	Deps        string // Comma-separated, format: "type:id" or "id"
}

// createFormValues holds the parsed values from the create-form input.
// This struct is used to pass form data to the issue creation logic,
// allowing the creation logic to be tested independently of the form UI.
type createFormValues struct {
	Title              string
	Description        string
	IssueType          string
	Priority           int
	Assignee           string
	Labels             []string
	Design             string
	AcceptanceCriteria string
	ExternalRef        string
	Dependencies       []string
	ParentID           string // Parent issue ID for hierarchical child creation
	Force              bool   // beads-3jdex: override the closed-parent close-guard, matching `bd create --force`
}

// parseCreateFormInput parses raw form input into a createFormValues struct.
// It handles comma-separated labels and dependencies, and converts priority strings.
func parseCreateFormInput(raw *createFormRawInput) *createFormValues {
	// Parse priority
	priority, err := strconv.Atoi(raw.Priority)
	if err != nil {
		priority = 2 // Default to medium if parsing fails
	}

	// Parse labels
	var labels []string
	if raw.Labels != "" {
		for _, l := range strings.Split(raw.Labels, ",") {
			l = strings.TrimSpace(l)
			if l != "" {
				labels = append(labels, l)
			}
		}
	}

	// Parse dependencies
	var deps []string
	if raw.Deps != "" {
		for _, d := range strings.Split(raw.Deps, ",") {
			d = strings.TrimSpace(d)
			if d != "" {
				deps = append(deps, d)
			}
		}
	}

	return &createFormValues{
		// beads-1077e: trim the title and normalize the assignee here, matching
		// single `bd create` (create.go: strings.TrimSpace(title) +
		// normalizeAssignee). Without this a padded form title was stored
		// unsearchable and a padded/"none" assignee orphaned the work from
		// `bd ready --assignee`. The empty-after-trim + reserved-label checks
		// run in validateCreateFormValues (they need to return an error).
		Title:              strings.TrimSpace(raw.Title),
		Description:        raw.Description,
		IssueType:          raw.IssueType,
		Priority:           priority,
		Assignee:           normalizeAssignee(raw.Assignee),
		Labels:             labels,
		Design:             raw.Design,
		AcceptanceCriteria: raw.Acceptance,
		ExternalRef:        raw.ExternalRef,
		Dependencies:       deps,
	}
}

// validateCreateFormValues enforces the input guards single `bd create`
// applies that a parsed form value can violate: a non-empty (after-trim) title
// and no reserved gt identity label on a non-gt-internal write (beads-1077e).
// parseCreateFormInput already trims the title and normalizes the assignee, so
// this validates the results and returns an error the RunE surfaces via
// HandleErrorRespectJSON — matching single create rejecting an empty title
// ("title cannot be empty") and a reserved identity label (create.go:200,
// reservedIdentityLabelError, the beads-3c4g spoof-vector reservation shared
// with label add / graph create f8fvh / markdown --file kvq0v).
func validateCreateFormValues(fv *createFormValues) error {
	if strings.TrimSpace(fv.Title) == "" {
		return fmt.Errorf("title cannot be empty")
	}
	for _, label := range fv.Labels {
		if msg := reservedIdentityLabelError(label); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		// beads-4sfae: reserve the 'provides:' capability family here too, at
		// parity with single create (create.go) / graph (graph_apply.go) —
		// beads-o70m1 added providesLabelError to those two seams but not this
		// create-form authoring seam, so a hand-set provides:<cap> still minted an
		// OPEN bead carrying the reserved single-provider label outside `bd ship`.
		if msg := providesLabelError(label); msg != "" {
			return fmt.Errorf("%s", msg)
		}
	}
	return nil
}

// CreateIssueFromFormValues creates an issue from the given form values.
// It returns the created issue and any error that occurred.
// This function handles parent-child relationships, labels, dependencies,
// and source_repo inheritance.
func CreateIssueFromFormValues(ctx context.Context, s storage.DoltStorage, fv *createFormValues, actor string) (*types.Issue, error) {
	// If parent is specified, validate it exists and generate child ID
	var explicitID string
	var inheritedLabels []string
	if fv.ParentID != "" {
		parentIssue, err := s.GetIssue(ctx, fv.ParentID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return nil, fmt.Errorf("parent issue %s not found", fv.ParentID)
			}
			return nil, fmt.Errorf("failed to check parent issue: %w", err)
		}
		// beads-3jdex: the create-FORM axis of the close-guard family. The single
		// `bd create --parent` path refuses creating a child under a closed
		// auto-closing parent (create.go:486, a8a1b/czu1s) — that reaches the
		// forbidden "closed parent with an open child" invariant the family
		// (zgku/b0tw/aw9x8) enforces. This shared exported entry drove by a real
		// form submission only checked the parent EXISTS, not its status. New
		// issues are created open, so any closed auto-closing parent (epic OR
		// molecule OR wisp, per the aw9x8 shared isAutoClosingParentType) is a
		// violation. Overridable with --force, matching `bd create --force`.
		if !fv.Force && parentIssue != nil &&
			isAutoClosingParentType(parentIssue) && parentIssue.Status == types.StatusClosed {
			return nil, fmt.Errorf("cannot create a child under closed parent %s (its status is closed; reopen the parent first or use --force to override)", fv.ParentID)
		}
		childID, err := s.GetNextChildID(ctx, fv.ParentID)
		if err != nil {
			return nil, fmt.Errorf("failed to generate child ID: %w", err)
		}
		explicitID = childID
		ctx = storage.WithReservedChildCounter(ctx, fv.ParentID, childID)

		// Inherit parent labels (GH#2100), matching bd create --parent behavior
		inheritedLabels, _ = s.GetLabels(ctx, fv.ParentID)
	}

	var externalRefPtr *string
	if fv.ExternalRef != "" {
		externalRefPtr = &fv.ExternalRef
	}

	labels := mergeCreateLabels(fv.Labels, inheritedLabels)

	issue := &types.Issue{
		Title:              fv.Title,
		Description:        fv.Description,
		Design:             fv.Design,
		AcceptanceCriteria: fv.AcceptanceCriteria,
		Status:             types.StatusOpen,
		Priority:           fv.Priority,
		IssueType:          types.IssueType(fv.IssueType).Normalize(),
		Assignee:           fv.Assignee,
		ExternalRef:        externalRefPtr,
		CreatedBy:          getActorWithGit(), // GH#748: track who created the issue
		Labels:             labels,
	}

	if explicitID != "" {
		issue.ID = explicitID
	}

	// Check if any dependencies are discovered-from type
	// If so, inherit source_repo from the parent issue
	var discoveredFromParentID string
	for _, depSpec := range fv.Dependencies {
		depSpec = strings.TrimSpace(depSpec)
		if depSpec == "" {
			continue
		}

		if strings.Contains(depSpec, ":") {
			parts := strings.SplitN(depSpec, ":", 2)
			if len(parts) == 2 {
				depType := types.DependencyType(strings.TrimSpace(parts[0]))
				dependsOnID := strings.TrimSpace(parts[1])

				if depType == types.DepDiscoveredFrom && dependsOnID != "" {
					discoveredFromParentID = dependsOnID
					break
				}
			}
		}
	}

	// If we found a discovered-from dependency, inherit source_repo from parent
	if discoveredFromParentID != "" {
		parentIssue, err := s.GetIssue(ctx, discoveredFromParentID)
		if err == nil && parentIssue != nil && parentIssue.SourceRepo != "" {
			issue.SourceRepo = parentIssue.SourceRepo
		}
	}

	// beads-14m3s: the create-FORM sibling of the a8d14 create atomicity fix.
	// Previously CreateIssue self-committed the issue and then each parent/deps
	// edge was best-effort (Warning + continue), so a create whose issue
	// succeeded but whose edge write failed left a durable issue MISSING its
	// declared edges — while the atomic proxied create twin buffers the same
	// writes on one UOW. Parse+validate ALL edges up-front (no writes — an
	// invalid dep-type / format is still surfaced before anything is created),
	// then wrap CreateIssue + every AddDependency in ONE transaction so an edge
	// failure rolls the issue back too. Mirrors create.go's transactHonoring-
	// AutoCommit wrap exactly (same commit semantics the old shouldCommitCreate-
	// PostWrites gate produced: version commit in server + embedded-autocommit-on,
	// SQL-only otherwise).
	//
	// beads-1gvh4: for an AUTO-GENERATED id (a form create with --deps but no
	// --parent), issue.ID is EMPTY here — it is only minted inside tx.CreateIssue.
	// So capture only the edge target+type up-front and build the actual
	// types.Dependency (which needs issue.ID) INSIDE the closure, after the id is
	// minted; capturing issue.ID now would store an edge with an empty endpoint.
	type pendingEdge struct {
		dependsOnID string
		depType     types.DependencyType
	}
	var pendingEdges []pendingEdge

	// If parent was specified, add parent-child dependency (GH#1983). (With a
	// parent, issue.ID is already the reserved child id, but keep the same
	// build-inside-tx shape for all edges.)
	if fv.ParentID != "" {
		pendingEdges = append(pendingEdges, pendingEdge{
			dependsOnID: fv.ParentID,
			depType:     types.DepParentChild,
		})
	}

	// Add dependencies if specified
	for _, depSpec := range fv.Dependencies {
		depSpec = strings.TrimSpace(depSpec)
		if depSpec == "" {
			continue
		}

		var depType types.DependencyType
		var dependsOnID string

		if strings.Contains(depSpec, ":") {
			parts := strings.SplitN(depSpec, ":", 2)
			if len(parts) != 2 {
				fmt.Fprintf(os.Stderr, "Warning: invalid dependency format '%s', expected 'type:id' or 'id'\n", depSpec)
				continue
			}
			depType = types.DependencyType(strings.TrimSpace(parts[0]))
			dependsOnID = strings.TrimSpace(parts[1])
		} else {
			depType = types.DepBlocks
			dependsOnID = depSpec
		}

		if !depType.IsValid() {
			fmt.Fprintf(os.Stderr, "Warning: invalid dependency type '%s' (valid: blocks, related, parent-child, discovered-from)\n", depType)
			continue
		}

		// beads-o8h79: a parent-child edge supplied via the form's Dependencies
		// field (`parent-child:<id>`) reaches the SAME "closed parent with an
		// open child" invariant as the --parent leg, but the 3jdex guard above
		// (L157) only fires for fv.ParentID. Mirror it here so the generic-dep
		// axis of the form can't smuggle an open child under a closed
		// auto-closing parent (epic/molecule/wisp) — the create-FORM straggler
		// of the closed-parent guard family (3jdex form --parent, p1p9n
		// create --deps/markdown, t39ph graph, aw9x8/j8ekq dep-add). Overridable
		// with --force (fv.Force, already threaded by 3jdex). Fail OPEN on a
		// read miss (err==nil gate): this loop does not otherwise validate the
		// dep target's existence, so a lookup error must not newly reject the
		// create — only a successfully-read closed auto-closing parent is barred.
		if !fv.Force && depType == types.DepParentChild {
			if parent, err := s.GetIssue(ctx, dependsOnID); err == nil &&
				isAutoClosingParentType(parent) && parent.Status == types.StatusClosed {
				return nil, fmt.Errorf("cannot create a child under closed parent %s (its status is closed; reopen the parent first or use --force to override)", dependsOnID)
			}
		}

		pendingEdges = append(pendingEdges, pendingEdge{
			dependsOnID: dependsOnID,
			depType:     depType,
		})
	}

	// beads-1gvh4: transactHonoringAutoCommit needs a non-empty commit msg (an
	// empty msg makes StageAndCommit skip the version commit). For an auto-gen id
	// issue.ID is still empty here, so fall back to the title.
	idOrTitle := issue.ID
	if idOrTitle == "" {
		idOrTitle = fmt.Sprintf("%q", issue.Title)
	}
	commitMsg := fmt.Sprintf("bd: create %s", idOrTitle)
	if len(pendingEdges) > 0 {
		commitMsg = fmt.Sprintf("bd: create %s (metadata)", idOrTitle)
	}
	if err := transactHonoringAutoCommit(ctx, s, commitMsg, func(tx storage.Transaction) error {
		if err := tx.CreateIssue(ctx, issue, actor); err != nil {
			return fmt.Errorf("failed to create issue: %w", err)
		}
		// issue.ID is now minted by tx.CreateIssue — build each edge with it.
		for _, e := range pendingEdges {
			dep := &types.Dependency{
				IssueID:     issue.ID,
				DependsOnID: e.dependsOnID,
				Type:        e.depType,
			}
			if err := tx.AddDependency(ctx, dep, actor); err != nil {
				return fmt.Errorf("failed to add dependency %s -> %s: %w", dep.IssueID, dep.DependsOnID, err)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return issue, nil
}

var createFormCmd = &cobra.Command{
	Use:     "create-form",
	GroupID: "issues",
	Short:   "Create a new issue using an interactive form",
	Long: `Create a new issue using an interactive terminal form.

This command provides a user-friendly form interface for creating issues,
with fields for title, description, type, priority, labels, and more.

Use --parent to create a sub-issue under an existing parent issue.
The child will get an auto-generated hierarchical ID (e.g., parent-id.1).

The form uses keyboard navigation:
  - Tab/Shift+Tab: Move between fields
  - Enter: Submit the form (on the last field or submit button)
  - Ctrl+C: Cancel and exit
  - Arrow keys: Navigate within select fields`,
	// beads-s11cc: reject stray positionals with a clean usage error. The form
	// reads all input interactively; RunE ignores args, so without this a stray
	// positional (bd create-form garbage) was silently swallowed with rc=0.
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		CheckReadonly("create-form")

		evt := metrics.NewCommandEvent("create-form")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		return runCreateForm(cmd)
	},
}

func runCreateForm(cmd *cobra.Command) error {
	// beads-xwoug: create-form has no proxied-server (_proxied_server.go)
	// companion — CreateIssueFromFormValues below derefs the global `store`
	// unconditionally (s.CreateIssue / s.GetIssue / s.GetNextChildID). In
	// proxied-server mode PersistentPreRunE returns early (main.go) WITHOUT
	// initializing `store`, so it is a nil interface. Without this guard a
	// proxied (hub-connected) crew fills out the entire interactive form and
	// then SIGSEGVs on the nil deref (the non-TTY path fails earlier in
	// form.Run(), but a real tmux pty reaches the deref). Fail loudly up front
	// with the shared bucket-3 idiom (rename-prefix/kv/mgjco) so the store
	// factory's proxied-config refusal surfaces a clean error, not a panic.
	if store == nil {
		if err := ensureStoreActive(); err != nil {
			return HandleErrorRespectJSON("%v", err)
		}
	}

	parentID, _ := cmd.Flags().GetString("parent")

	// Raw form input - will be populated by the form
	raw := &createFormRawInput{}

	// Issue type options
	typeOptions := []huh.Option[string]{
		huh.NewOption("Task", "task"),
		huh.NewOption("Bug", "bug"),
		huh.NewOption("Feature", "feature"),
		huh.NewOption("Epic", "epic"),
		huh.NewOption("Chore", "chore"),
	}

	// Priority options
	priorityOptions := []huh.Option[string]{
		huh.NewOption("P0 - Critical", "0"),
		huh.NewOption("P1 - High", "1"),
		huh.NewOption("P2 - Medium (default)", "2"),
		huh.NewOption("P3 - Low", "3"),
		huh.NewOption("P4 - Backlog", "4"),
	}

	// Build the form
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Title").
				Description("Brief summary of the issue (required)").
				Placeholder("e.g., Fix authentication bug in login handler").
				Value(&raw.Title).
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return fmt.Errorf("title is required")
					}
					if len(s) > 500 {
						return fmt.Errorf("title must be 500 characters or less")
					}
					return nil
				}),

			huh.NewText().
				Title("Description").
				Description("Detailed context about the issue").
				Placeholder("Explain why this issue exists and what needs to be done...").
				CharLimit(5000).
				Value(&raw.Description),

			huh.NewSelect[string]().
				Title("Type").
				Description("Categorize the kind of work").
				Options(typeOptions...).
				Value(&raw.IssueType),

			huh.NewSelect[string]().
				Title("Priority").
				Description("Set urgency level").
				Options(priorityOptions...).
				Value(&raw.Priority),
		),

		huh.NewGroup(
			huh.NewInput().
				Title("Assignee").
				Description("Who should work on this? (optional)").
				Placeholder("username or email").
				Value(&raw.Assignee),

			huh.NewInput().
				Title("Labels").
				Description("Comma-separated tags (optional)").
				Placeholder("e.g., urgent, backend, needs-review").
				Value(&raw.Labels),

			huh.NewInput().
				Title("External Reference").
				Description("Link to external tracker (optional)").
				Placeholder("e.g., gh-123, jira-ABC-456").
				Value(&raw.ExternalRef),
		),

		huh.NewGroup(
			huh.NewText().
				Title("Design Notes").
				Description("Technical approach or design details (optional)").
				Placeholder("Describe the implementation approach...").
				CharLimit(5000).
				Value(&raw.Design),

			huh.NewText().
				Title("Acceptance Criteria").
				Description("How do we know this is done? (optional)").
				Placeholder("List the criteria for completion...").
				CharLimit(5000).
				Value(&raw.Acceptance),
		),

		huh.NewGroup(
			huh.NewInput().
				Title("Dependencies").
				Description("Format: type:id or just id (optional)").
				Placeholder("e.g., discovered-from:bd-20, blocks:bd-15").
				Value(&raw.Deps),

			huh.NewConfirm().
				Title("Create this issue?").
				Affirmative("Create").
				Negative("Cancel"),
		),
	).WithTheme(huh.ThemeFunc(huh.ThemeDracula))

	err := form.Run()
	if err != nil {
		if err == huh.ErrUserAborted {
			fmt.Fprintln(os.Stderr, "Issue creation canceled.")
			return nil
		}
		// beads-csgk: honor the persistent --json error contract (8lqh /
		// 0wp9 / jial class), matching the sibling `bd create` (create.go
		// routes every error through HandleErrorRespectJSON). A bare
		// HandleError left stdout EMPTY + plain-text on stderr under
		// `bd create-form --json` — e.g. in a non-TTY, form.Run() fails
		// ("could not open TTY") and a --json consumer got unparseable text.
		return HandleErrorRespectJSON("form error: %v", err)
	}

	fv := parseCreateFormInput(raw)
	fv.ParentID = parentID
	fv.Force, _ = cmd.Flags().GetBool("force")

	// beads-1077e: enforce the empty-title + reserved-identity-label guards
	// single `bd create` applies, before minting the issue.
	if err := validateCreateFormValues(fv); err != nil {
		return HandleErrorRespectJSON("%v", err)
	}

	issue, err := CreateIssueFromFormValues(rootCtx, store, fv, actor)
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}

	if jsonOutput {
		return outputJSON(issue)
	}
	printCreatedIssue(issue)
	return nil
}

func printCreatedIssue(issue *types.Issue) {
	fmt.Printf("\n%s Created issue: %s\n", ui.RenderPass("✓"), formatFeedbackID(issue.ID, issue.Title))
	fmt.Printf("  Type:     %s\n", issue.IssueType)
	fmt.Printf("  Priority: P%d\n", issue.Priority)
	fmt.Printf("  Status:   %s\n", issue.Status)
	if issue.Assignee != "" {
		fmt.Printf("  Assignee: %s\n", ui.SanitizeForTerminal(issue.Assignee))
	}
	if issue.Description != "" {
		desc := issue.Description
		if len(desc) > 100 {
			desc = desc[:97] + "..."
		}
		fmt.Printf("  Description: %s\n", desc)
	}
}

func init() {
	// Note: --json flag is defined as a persistent flag in main.go
	createFormCmd.Flags().String("parent", "", "Parent issue ID for creating a hierarchical child (e.g., 'bd-a3f8e9')")
	createFormCmd.Flags().Bool("force", false, "Override the closed-parent guard (create a child under a closed parent)")
	rootCmd.AddCommand(createFormCmd)
}
