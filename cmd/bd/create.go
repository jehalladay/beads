package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/debug"
	"github.com/steveyegge/beads/internal/metrics"
	"github.com/steveyegge/beads/internal/remotecache"
	"github.com/steveyegge/beads/internal/routing"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/dolt"
	"github.com/steveyegge/beads/internal/timeparsing"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/validation"
)

var createCmd = &cobra.Command{
	Use:     "create [title]",
	GroupID: "issues",
	Aliases: []string{"new"},
	Short:   "Create a new issue (or batch from markdown/graph JSON)",
	// beads-einrb: MaximumNArgs(1), not MinimumNArgs(0). create reads only
	// args[0] as the title (batch --file/--graph reject any positional, and the
	// --title path uses args[0]), so a 2nd+ positional was silently DROPPED
	// (`bd create "a" "b"` created only "a", rc0, no warning). MaxN(1) still
	// allows 0 args (batch + --title-only) and 1 (positional title) but errors
	// on extras like other leaf cmds; under --json the 71br ExecuteC handler
	// json-ifies the resulting arg-count error.
	Args:          cobra.MaximumNArgs(1),
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		CheckReadonly("create")

		evt := metrics.NewCommandEvent("create")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		if usesProxiedServer() {
			in, err := gatherCreateInput(cmd, args)
			if err != nil {
				return err
			}
			runCreateProxiedServer(cmd, rootCtx, in)
			return nil
		}
		file, _ := cmd.Flags().GetString("file")
		graphFile, _ := cmd.Flags().GetString("graph")

		if file != "" {
			if graphFile != "" {
				return HandleErrorRespectJSON("cannot specify both --file and --graph")
			}
			if len(args) > 0 {
				return HandleErrorRespectJSON("cannot specify both title and --file flag")
			}
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			return createIssuesFromMarkdown(cmd, file, dryRun)
		}

		if graphFile != "" {
			if len(args) > 0 {
				return HandleErrorRespectJSON("cannot specify both title and --graph flag")
			}
			graphDryRun, _ := cmd.Flags().GetBool("dry-run")
			wisp, _ := cmd.Flags().GetBool("ephemeral")
			noHistory, _ := cmd.Flags().GetBool("no-history")
			noInheritLabels, _ := cmd.Flags().GetBool("no-inherit-labels")
			graphForce, _ := cmd.Flags().GetBool("force")
			graphOpts := GraphApplyOptions{
				Ephemeral:       wisp,
				NoHistory:       noHistory,
				NoInheritLabels: noInheritLabels,
				Force:           graphForce,
			}
			if err := graphOpts.Validate(); err != nil {
				return HandleErrorRespectJSON("invalid graph options: %v", err)
			}
			return createIssuesFromGraph(graphFile, graphDryRun, graphOpts)
		}

		titleFlag, _ := cmd.Flags().GetString("title")
		var title string

		if len(args) > 0 && titleFlag != "" {
			if args[0] != titleFlag {
				return HandleErrorRespectJSON("cannot specify different titles as both positional argument and --title flag\n  Positional: %q\n  --title:    %q", args[0], titleFlag)
			}
			title = args[0]
		} else if len(args) > 0 {
			if strings.HasPrefix(args[0], "-") {
				return HandleErrorRespectJSON("title %q looks like a flag (starts with '-').\n  Run 'bd create --help' for available options.\n  To use this title anyway, pass it explicitly: bd create --title=%q", args[0], args[0])
			}
			title = args[0]
		} else if titleFlag != "" {
			title = titleFlag
		} else {
			return HandleErrorRespectJSON("title required (or use --file to create from markdown)")
		}

		// Trim leading/trailing whitespace and reject an empty-after-trim title,
		// mirroring the update path (cmd/bd/update.go) and gatherCreateInput's
		// resolveTitle (the proxied path). Without this, a padded title was
		// stored verbatim (unsearchable) and a whitespace-only title was
		// accepted as valid (types.Validate only rejects len==0) — a
		// create/update asymmetry (beads-n5xz, sibling of the label-trim gap
		// beads-4g2h).
		title = strings.TrimSpace(title)
		if title == "" {
			return HandleErrorRespectJSON("title cannot be empty")
		}

		// Get silent flag
		silent, _ := cmd.Flags().GetBool("silent")

		// Warn if creating a test issue in a database with existing issues.
		// A brand-new repo with zero issues is not a "production database" (#2898).
		if isTestIssue(title) && !silent && !debug.IsQuiet() {
			if stats, err := store.GetStatistics(context.Background()); err == nil && stats != nil && stats.TotalIssues >= 5 {
				fmt.Fprintf(os.Stderr, "%s Creating test issue in production database\n", ui.RenderWarn("⚠"))
				fmt.Fprintf(os.Stderr, "  Title: %q appears to be test data\n", title)
				fmt.Fprintf(os.Stderr, "  Recommendation: Use isolated test database with --db\n")
				fmt.Fprintf(os.Stderr, "    bd --db /tmp/test-beads create %q\n", title)
			}
		}

		description, _, err := getDescriptionFlag(cmd)
		if err != nil {
			return err
		}

		skills, _ := cmd.Flags().GetString("skills")
		if skills != "" {
			if description != "" {
				description += "\n\n"
			}
			description += "## Required Skills\n" + skills
		}

		ctxStr, _ := cmd.Flags().GetString("context")
		if ctxStr != "" {
			if description != "" {
				description += "\n\n"
			}
			description += "## Context\n" + ctxStr
		}

		if description == "" && !isTestIssue(title) {
			if config.GetBool("create.require-description") {
				return HandleErrorRespectJSON("description is required (set create.require-description: false in config.yaml to disable)")
			}
		}

		design, _, err := getDesignFlag(cmd)
		if err != nil {
			return err
		}
		acceptance, _ := cmd.Flags().GetString("acceptance")
		notes, _ := cmd.Flags().GetString("notes")
		specID, _ := cmd.Flags().GetString("spec-id")

		priorityStr, _ := cmd.Flags().GetString("priority")
		priority, err := validation.ValidatePriority(priorityStr)
		if err != nil {
			return HandleErrorRespectJSON("%v", err)
		}

		issueType, _ := cmd.Flags().GetString("type")
		// Trim + fold the "none" sentinel through the shared normalizer so the
		// create path stores the canonical form the read/filter side searches
		// for. A padded `-a "  x  "` stored verbatim is permanently unmatchable
		// by `bd list/ready --assignee x` (beads-llzt, assignee sibling of the
		// label-trim class) — silently orphaning the work from its assignee.
		rawAssignee, _ := cmd.Flags().GetString("assignee")
		assignee := normalizeAssignee(rawAssignee)

		labels, _ := cmd.Flags().GetStringSlice("labels")
		labelAlias, _ := cmd.Flags().GetStringSlice("label")
		if len(labelAlias) > 0 {
			labels = append(labels, labelAlias...)
		}
		// Reserve the gt identity family (beads-3c4g): a hand-set gt:agent/role/rig
		// label would mint a bead the ready discriminator silently hides. gt stamps
		// these via its own (GT_INTERNAL) shell-outs, so only non-internal writes
		// are rejected.
		for _, label := range labels {
			if msg := reservedIdentityLabelError(label); msg != "" {
				return HandleErrorRespectJSON("%s", msg)
			}
			// Reserve the 'provides:' capability family (beads-o70m1): a hand-set
			// provides:<cap> at create time would mint an OPEN bead carrying a
			// cross-project capability label, bypassing the closed-requirement and
			// single-provider invariants that `bd ship` enforces. `bd label add`
			// already rejects this (label.go); mirror it here so the create seam
			// can't route around ship.
			if msg := providesLabelError(label); msg != "" {
				return HandleErrorRespectJSON("%s", msg)
			}
		}

		explicitID, _ := cmd.Flags().GetString("id")
		parentID, _ := cmd.Flags().GetString("parent")
		externalRef, _ := cmd.Flags().GetString("external-ref")
		deps, _ := cmd.Flags().GetStringSlice("deps")
		waitsFor, _ := cmd.Flags().GetString("waits-for")
		waitsForGate, _ := cmd.Flags().GetString("waits-for-gate")
		forceCreate, _ := cmd.Flags().GetBool("force")
		repoOverride, _ := cmd.Flags().GetString("repo")
		wisp, _ := cmd.Flags().GetBool("ephemeral")
		noHistory, _ := cmd.Flags().GetBool("no-history")
		if wisp && noHistory {
			return HandleErrorRespectJSON("--ephemeral and --no-history are mutually exclusive")
		}
		molTypeStr, _ := cmd.Flags().GetString("mol-type")
		var molType types.MolType
		if molTypeStr != "" {
			molType = types.MolType(molTypeStr)
			if !molType.IsValid() {
				return HandleErrorRespectJSON("invalid mol-type %q (must be swarm, patrol, or work)", molTypeStr)
			}
		}

		wispTypeStr, _ := cmd.Flags().GetString("wisp-type")
		var wispType types.WispType
		if wispTypeStr != "" {
			wispType = types.WispType(wispTypeStr)
			if !wispType.IsValid() {
				return HandleErrorRespectJSON("invalid wisp-type %q (must be heartbeat, ping, patrol, gc_report, recovery, error, or escalation)", wispTypeStr)
			}
		}

		eventCategory, _ := cmd.Flags().GetString("event-category")
		eventActor, _ := cmd.Flags().GetString("event-actor")
		eventTarget, _ := cmd.Flags().GetString("event-target")
		eventPayload, _ := cmd.Flags().GetString("event-payload")

		if (eventCategory != "" || eventActor != "" || eventTarget != "" || eventPayload != "") && issueType != "event" {
			return HandleErrorRespectJSON("--event-category, --event-actor, --event-target, and --event-payload flags require --type=event")
		}

		var dueAt *time.Time
		dueStr, _ := cmd.Flags().GetString("due")
		if dueStr != "" {
			t, err := timeparsing.ParseRelativeTime(dueStr, time.Now())
			if err != nil {
				return HandleErrorRespectJSON("invalid --due format %q. Examples: +6h, tomorrow, next monday, 2025-01-15", dueStr)
			}
			dueAt = &t
		}

		var deferUntil *time.Time
		deferStr, _ := cmd.Flags().GetString("defer")
		if deferStr != "" {
			t, err := timeparsing.ParseRelativeTime(deferStr, time.Now())
			if err != nil {
				return HandleErrorRespectJSON("invalid --defer format %q. Examples: +1h, tomorrow, next monday, 2025-01-15", deferStr)
			}
			// Warn if defer date is in the past (user probably meant future)
			if t.Before(time.Now()) && !silent && !debug.IsQuiet() {
				fmt.Fprintf(os.Stderr, "%s Defer date %q is in the past. Issue will appear in bd ready immediately.\n",
					ui.RenderWarn("!"), t.Format("2006-01-02 15:04"))
				fmt.Fprintf(os.Stderr, "  Did you mean a future date? Use --defer=+1h or --defer=tomorrow\n")
			}
			deferUntil = &t
		}

		var metadata json.RawMessage
		if cmd.Flags().Changed("metadata") {
			metadataValue, _ := cmd.Flags().GetString("metadata")
			var metadataJSON string
			if strings.HasPrefix(metadataValue, "@") {
				filePath := metadataValue[1:]
				// #nosec G304 -- user explicitly provides file path via @file.json syntax
				data, err := os.ReadFile(filePath)
				if err != nil {
					return HandleErrorRespectJSON("failed to read metadata file %s: %v", filePath, err)
				}
				metadataJSON = string(data)
			} else {
				metadataJSON = metadataValue
			}
			if !json.Valid([]byte(metadataJSON)) {
				return HandleErrorRespectJSON("invalid JSON in --metadata: must be valid JSON")
			}
			// This is the live single-issue create path; gatherCreateInput's
			// gate covers only the batch path (beads-eum2/ef2k).
			if !metadataIsJSONObject(metadataJSON) {
				return HandleErrorRespectJSON(`--metadata must be a JSON object, e.g. {"key":"value"} (arrays and scalars can't be edited by --set-metadata/--unset-metadata)`)
			}
			metadata = json.RawMessage(metadataJSON)
		}

		validateTemplate, _ := cmd.Flags().GetBool("validate")
		validationMode := config.GetString("validation.on-create")
		if validateTemplate || validationMode == "error" || validationMode == "warn" {
			lintIssue := &types.Issue{
				IssueType:          types.IssueType(issueType).Normalize(),
				Description:        description,
				AcceptanceCriteria: acceptance,
			}
			if err := validation.LintIssue(lintIssue); err != nil {
				if validateTemplate || validationMode == "error" {
					return HandleErrorRespectJSON("%v", err)
				}
				fmt.Fprintf(os.Stderr, "%s %v\n", ui.RenderWarn("⚠"), err)
			}
		}

		dryRun, _ := cmd.Flags().GetBool("dry-run")

		var estimatedMinutes *int
		if cmd.Flags().Changed("estimate") {
			est, _ := cmd.Flags().GetInt("estimate")
			if est < 0 {
				return HandleErrorRespectJSON("estimate must be a non-negative number of minutes")
			}
			estimatedMinutes = &est
		}

		// Use global jsonOutput set by PersistentPreRun

		// Determine target repository using routing logic
		repoPath := "." // default to current directory
		if cmd.Flags().Changed("repo") {
			// Explicit --repo flag overrides auto-routing
			repoPath = repoOverride
		} else {
			// Auto-routing based on user role
			userRole, err := routing.DetectUserRole(".")
			if err != nil {
				debug.Logf("Warning: failed to detect user role: %v\n", err)
			}

			// Build routing config with backward compatibility for legacy contributor.* keys.
			// Prefer config.yaml values, but fall back to DB config values set by bd init --contributor.
			routingMode := getRoutingConfigValue(rootCtx, store, "routing.mode")
			contributorRepo := getRoutingConfigValue(rootCtx, store, "routing.contributor")

			// NFR-001: Backward compatibility - fall back to legacy contributor.* keys
			if routingMode == "" {
				if getRoutingConfigValue(rootCtx, store, "contributor.auto_route") == "true" {
					routingMode = "auto"
				}
			}
			if contributorRepo == "" {
				contributorRepo = getRoutingConfigValue(rootCtx, store, "contributor.planning_repo")
			}

			routingConfig := &routing.RoutingConfig{
				Mode:             routingMode,
				DefaultRepo:      getRoutingConfigValue(rootCtx, store, "routing.default"),
				MaintainerRepo:   getRoutingConfigValue(rootCtx, store, "routing.maintainer"),
				ContributorRepo:  contributorRepo,
				ExplicitOverride: repoOverride,
			}

			repoPath = routing.DetermineTargetRepo(routingConfig, userRole, ".")
		}

		renderDryRun := func() error {
			previewIssue := buildCreateIssue(createIssueParams{
				ID:                 explicitID,
				Title:              title,
				Description:        description,
				Design:             design,
				AcceptanceCriteria: acceptance,
				Notes:              notes,
				SpecID:             specID,
				Priority:           priority,
				IssueType:          types.IssueType(issueType).Normalize(),
				Assignee:           assignee,
				ExternalRef:        externalRef,
				EstimatedMinutes:   estimatedMinutes,
				Ephemeral:          wisp,
				NoHistory:          noHistory,
				CreatedBy:          getActorWithGit(),
				Owner:              getOwner(),
				Labels:             labels,
				MolType:            molType,
				WispType:           wispType,
				DueAt:              dueAt,
				DeferUntil:         deferUntil,
				Metadata:           metadata,
				EventKind:          eventCategory,
				Actor:              eventActor,
				Target:             eventTarget,
				Payload:            eventPayload,
			})

			if jsonOutput {
				return outputJSON(previewIssue)
			}
			renderCreateDryRunPreview(previewIssue, labels, deps)
			return nil
		}

		if dryRun && parentID == "" {
			return renderDryRun()
		}

		var targetStore storage.DoltStorage
		var remoteCache *remotecache.Cache
		if !dryRun && repoPath != "." {
			if remotecache.IsRemoteURL(repoPath) {
				var err error
				remoteCache, err = remotecache.DefaultCache()
				if err != nil {
					return HandleErrorRespectJSON("failed to initialize remote cache: %v", err)
				}
				if _, err := remoteCache.Ensure(rootCtx, repoPath); err != nil {
					return HandleErrorRespectJSON("failed to sync remote %s: %v", repoPath, err)
				}
				targetStore, err = remoteCache.OpenStore(rootCtx, repoPath, newDoltStoreFromConfig)
				if err != nil {
					return HandleErrorRespectJSON("failed to open remote store: %v", err)
				}
			} else {
				targetBeadsDir := routing.ExpandPath(repoPath)
				debug.Logf("DEBUG: Routing to target repo: %s\n", targetBeadsDir)

				if err := ensureBeadsDirForPath(rootCtx, targetBeadsDir, store); err != nil {
					return HandleErrorRespectJSON("failed to initialize target repo: %v", err)
				}

				targetBeadsDirPath := filepath.Join(targetBeadsDir, ".beads")
				var err error
				targetStore, err = newDoltStoreFromConfig(rootCtx, targetBeadsDirPath)
				if err != nil {
					return HandleErrorRespectJSON("failed to open target store: %v", err)
				}
			}

			// Close the original store before replacing it (it won't be used anymore)
			// Note: We don't defer-close targetStore here because PersistentPostRun
			// will close whatever store is assigned to the global `store` variable.
			// This fixes the "database is closed" error during auto-flush (GH#routing-close-bug).
			if store != nil {
				_ = store.Close() // Best effort cleanup on error path
			}

			// Replace store for remainder of create operation.
			// Must use setStore to sync cmdCtx.Store — a bare `store = targetStore`
			// leaves cmdCtx.Store pointing at the closed original, which causes
			// "store is closed" in PostRun tip auto-commit (GH#tip-closed-bug).
			setStore(targetStore)
		}

		if explicitID != "" && parentID != "" {
			return HandleErrorRespectJSON("cannot specify both --id and --parent flags")
		}

		parentLookupStore := store
		if dryRun && repoPath != "." {
			var err error
			parentLookupStore, err = openDryRunTargetStore(rootCtx, repoPath)
			if err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
			defer func() { _ = parentLookupStore.Close() }()
		}

		var inheritedLabels []string
		if parentID != "" {
			ctx := rootCtx
			parentIssue, err := parentLookupStore.GetIssue(ctx, parentID)
			if err != nil {
				if errors.Is(err, storage.ErrNotFound) {
					return HandleErrorRespectJSON("parent issue %s not found", parentID)
				}
				return HandleErrorRespectJSON("failed to check parent issue: %v", err)
			}

			// beads-a8a1b: refuse to create an OPEN child under a CLOSED
			// auto-closing parent — that reaches the forbidden "closed parent
			// with an open child" invariant the close-guard family (zgku/b0tw)
			// enforces on the status-transition axes but which was WIDE OPEN on
			// the parent-assignment axis (create only validated the parent
			// EXISTS, not its status). Overridable with --force, matching
			// `bd close --force`. New issues are created open, so any closed
			// auto-closing parent is a violation here.
			// beads-czu1s: use the shared isAutoClosingParentType (epic OR
			// molecule OR ephemeral) — the "scope to epics like the other guards"
			// reasoning is stale now that aw9x8/bigro/eth8/j8ekq widened the
			// reopen/close/dep-add guards, so a closed MOLECULE/wisp root was the
			// family straggler still creatable-under here.
			if !forceCreate && isAutoClosingParentType(parentIssue) && parentIssue.Status == types.StatusClosed {
				return HandleErrorRespectJSON("cannot create a child under closed parent %s (its status is closed; reopen the parent first or use --force to override)", parentID)
			}

			noInheritLabels, _ := cmd.Flags().GetBool("no-inherit-labels")
			if !noInheritLabels {
				inheritedLabels, _ = parentLookupStore.GetLabels(ctx, parentID)
			}
		}

		labels = mergeCreateLabels(labels, inheritedLabels)

		if dryRun {
			return renderDryRun()
		}

		createCtx := rootCtx
		if parentID != "" {
			childID, err := store.GetNextChildID(rootCtx, parentID)
			if err != nil {
				return HandleErrorRespectJSON("%v", err)
			}
			explicitID = childID
			createCtx = storage.WithReservedChildCounter(createCtx, parentID, childID)
		}

		if explicitID != "" {
			_, err := validation.ValidateIDFormat(explicitID)
			if err != nil {
				return HandleErrorRespectJSON("%v", err)
			}

			ctx := createCtx

			// The live DB prefix (issue_counter) is authoritative — it is what
			// every auto-generated id carries — so it is always accepted; a
			// config.yaml issue-prefix that disagrees is folded into the
			// allowed-list rather than shadowing the DB prefix (beads-xevo).
			liveDBPrefix, _ := store.GetConfig(ctx, "issue_prefix")
			allowedFromDB, _ := store.GetConfig(ctx, "allowed_prefixes")
			dbPrefix, allowedPrefixes := resolvePrefixValidation(liveDBPrefix, allowedFromDB)

			if err := validation.ValidateIDPrefixAllowed(explicitID, dbPrefix, allowedPrefixes, forceCreate); err != nil {
				return HandleErrorRespectJSON("%v", err)
			}

			// An explicit --id that already exists would be silently UPSERTED by
			// the create path (INSERT ... ON DUPLICATE KEY UPDATE), overwriting
			// the existing bead while still printing "✓ Created" — silent
			// data-loss on id reuse (beads-k75k). Refuse unless --force is set;
			// use `bd update` to modify an existing issue.
			if !forceCreate {
				if _, err := store.GetIssue(ctx, explicitID); err == nil {
					// beads-rafd: honor the --json error contract — emit the JSON
					// error object on stdout (RespectJSON), not stderr, so a
					// `bd create --json --id <existing>` consumer can parse it.
					return HandleErrorWithHintRespectJSON(
						fmt.Sprintf("issue %s already exists", explicitID),
						"Use 'bd update' to modify it, or pass --force to overwrite.")
				} else if !errors.Is(err, storage.ErrNotFound) {
					return HandleErrorRespectJSON("failed to check whether %s already exists: %v", explicitID, err)
				}
			}
		}

		issue := buildCreateIssue(createIssueParams{
			ID:                 explicitID,
			Title:              title,
			Description:        description,
			Design:             design,
			AcceptanceCriteria: acceptance,
			Notes:              notes,
			SpecID:             specID,
			Priority:           priority,
			IssueType:          types.IssueType(issueType).Normalize(),
			Assignee:           assignee,
			ExternalRef:        externalRef,
			EstimatedMinutes:   estimatedMinutes,
			Ephemeral:          wisp,
			NoHistory:          noHistory,
			CreatedBy:          getActorWithGit(),
			Owner:              getOwner(),
			Labels:             labels,
			MolType:            molType,
			WispType:           wispType,
			EventKind:          eventCategory,
			Actor:              eventActor,
			Target:             eventTarget,
			Payload:            eventPayload,
			DueAt:              dueAt,
			DeferUntil:         deferUntil,
			Metadata:           metadata,
		})

		ctx := createCtx

		// Check if any dependencies are discovered-from type
		// If so, inherit source_repo from the parent issue
		var discoveredFromParentID string
		for _, depSpec := range deps {
			depSpec = strings.TrimSpace(depSpec)
			if depSpec == "" {
				continue
			}

			var depType types.DependencyType
			var dependsOnID string

			if strings.Contains(depSpec, ":") {
				parts := strings.SplitN(depSpec, ":", 2)
				if len(parts) == 2 {
					depType = types.DependencyType(strings.TrimSpace(parts[0]))
					dependsOnID = strings.TrimSpace(parts[1])

					if depType == types.DepDiscoveredFrom && dependsOnID != "" {
						discoveredFromParentID = dependsOnID
						break
					}
				}
			}
		}

		// If we found a discovered-from dependency, inherit source_repo from parent
		if discoveredFromParentID != "" {
			parentIssue, err := store.GetIssue(ctx, discoveredFromParentID)
			if err == nil && parentIssue.SourceRepo != "" {
				issue.SourceRepo = parentIssue.SourceRepo
			}
			// If error getting parent or parent has no source_repo, continue with default
		}

		// Build the full dependency edge set up-front (pure parsing + validation,
		// NO writes) so an invalid --deps type / --waits-for-gate value fails before
		// we create anything, and so the issue + every edge commit atomically in the
		// single transaction below.
		//
		// beads-a8d14: the DIRECT create path previously self-committed the issue via
		// store.CreateIssue, then added each edge best-effort (WarnError + RC=0 on
		// failure). A create whose issue succeeded but whose edge write failed
		// therefore left a durable issue MISSING its parent/dep/waits-for edges at
		// exit 0 — while the atomic PROXIED twin (create_proxied_server.go) buffers
		// the same writes on one UOW and commits once. This wraps CreateIssue + all
		// AddDependency calls in one store.RunInTransaction (via
		// transactHonoringAutoCommit, mirroring the proxied UOW + graph_apply.go /
		// cook.go precedents), so an edge failure rolls the issue back too and the
		// command exits non-zero — restoring parity with the proxied path.
		// beads-1gvh4: parse + validate every requested edge here (pure
		// parsing, NO writes) so a bad --deps type / --waits-for-gate fails before
		// anything is created — but do NOT capture issue.ID yet. For an
		// auto-generated id, issue.ID is EMPTY until tx.CreateIssue mints it inside
		// the transaction below (issueops GenerateIssueIDInTable writes it back onto
		// the struct). Capturing issue.ID into the edge structs HERE bound the
		// EMPTY id for the bare `bd create --deps/--waits-for` path (no --parent/--id),
		// producing a durable edge with an empty endpoint (e.g. blocks:X stored as
		// "X -> ''"). We therefore record only the parsed spec (target/type/swap/
		// metadata) and build the actual types.Dependency INSIDE the closure after
		// the id exists — restoring the a8d14 atomicity while fixing the empty-id
		// regression it introduced.
		type edgeSpec struct {
			target   string
			depType  types.DependencyType
			swap     bool // "blocks:X": store X -> new issue (target becomes IssueID)
			metadata string
		}
		var edgeSpecs []edgeSpec

		// If parent was specified, add parent-child dependency
		if parentID != "" {
			edgeSpecs = append(edgeSpecs, edgeSpec{
				target:  parentID,
				depType: types.DepParentChild,
			})
		}

		// Add dependencies if specified (format: type:id or just id for default "blocks" type)
		for _, depSpec := range deps {
			depSpec = strings.TrimSpace(depSpec)
			if depSpec == "" {
				continue
			}

			var depType types.DependencyType
			var dependsOnID string
			swapDirection := false

			if strings.Contains(depSpec, ":") {
				parts := strings.SplitN(depSpec, ":", 2)
				if len(parts) != 2 {
					WarnError("invalid dependency format '%s', expected 'type:id' or 'id'", depSpec)
					continue
				}
				rawType := types.DependencyType(strings.TrimSpace(parts[0]))
				dependsOnID = strings.TrimSpace(parts[1])

				switch rawType {
				case "depends-on", "blocked-by":
					// Alias: the new issue depends on the target. Store as a blocks edge.
					depType = types.DepBlocks
				case types.DepBlocks:
					// Explicit "blocks:X" means the new issue blocks X, so store X -> new issue.
					depType = types.DepBlocks
					swapDirection = true
				default:
					depType = rawType
				}
			} else {
				depType = types.DepBlocks
				dependsOnID = depSpec
			}

			if !depType.IsValid() {
				return HandleErrorRespectJSON("invalid dependency type %q (must be non-empty, max 32 chars); valid types: %s", depType, createDepsAcceptedTypeList())
			}
			if !depType.IsWellKnown() {
				return HandleErrorRespectJSON("unknown dependency type %q; valid types: %s", depType, createDepsAcceptedTypeList())
			}

			// beads-p1p9n: a parent-child edge supplied via --deps
			// (`--deps parent-child:<id>`) reaches the SAME "closed parent with
			// an open child" invariant as `--parent`, but the guard above at
			// L501 only fires for the --parent flag (gated on parentID). Mirror
			// it here so the generic-dep axis can't smuggle an open child under
			// a closed auto-closing parent (epic/molecule/wisp) — the create
			// straggler of the closed-parent guard family (a8a1b/czu1s --parent,
			// t39ph graph, aw9x8/j8ekq dep-add). Overridable with --force.
			if !forceCreate && depType == types.DepParentChild {
				depParent, err := parentLookupStore.GetIssue(rootCtx, dependsOnID)
				if err != nil {
					if errors.Is(err, storage.ErrNotFound) {
						return HandleErrorRespectJSON("parent issue %s not found", dependsOnID)
					}
					return HandleErrorRespectJSON("failed to check parent issue: %v", err)
				}
				if isAutoClosingParentType(depParent) && depParent.Status == types.StatusClosed {
					return HandleErrorRespectJSON("cannot create a child under closed parent %s (its status is closed; reopen the parent first or use --force to override)", dependsOnID)
				}
			}

			edgeSpecs = append(edgeSpecs, edgeSpec{
				target:  dependsOnID,
				depType: depType,
				swap:    swapDirection,
			})
		}

		if waitsFor != "" {
			gate := waitsForGate
			if gate == "" {
				gate = types.WaitsForAllChildren
			}
			if gate != types.WaitsForAllChildren && gate != types.WaitsForAnyChildren {
				return HandleErrorRespectJSON("invalid --waits-for-gate value '%s' (valid: all-children, any-children)", gate)
			}

			meta := types.WaitsForMeta{
				Gate: gate,
			}
			metaJSON, err := json.Marshal(meta)
			if err != nil {
				return HandleErrorRespectJSON("failed to serialize waits-for metadata: %v", err)
			}

			edgeSpecs = append(edgeSpecs, edgeSpec{
				target:   waitsFor,
				depType:  types.DepWaitsFor,
				metadata: string(metaJSON),
			})
		}

		// Create the issue and all its edges atomically. transactHonoringAutoCommit
		// preserves the prior commit semantics exactly: a Dolt version commit in
		// server mode and embedded-autocommit-on mode, and SQL-only (no version
		// commit) in embedded-autocommit-off mode — the same behavior the old
		// shouldCommitCreatePostWrites gate produced — while also setting
		// commandDidExplicitDoltCommit so PersistentPostRun does not double-commit.
		//
		// The edge structs are built INSIDE the closure, after tx.CreateIssue mints
		// an auto-generated issue.ID (write-back onto the struct), so an auto-gen
		// create's edges reference the real id, not an empty string.
		commitMsg := fmt.Sprintf("bd: create %s", issue.ID)
		if commitMsg == "bd: create " {
			// issue.ID not yet minted (bare auto-gen); use the title so the Dolt
			// version commit message is non-empty (an empty message makes
			// StageAndCommit skip the version commit). Nothing parses the message.
			commitMsg = fmt.Sprintf("bd: create %q", title)
		}
		if err := transactHonoringAutoCommit(ctx, store, commitMsg, func(tx storage.Transaction) error {
			if err := tx.CreateIssue(ctx, issue, actor); err != nil {
				return err
			}
			for _, spec := range edgeSpecs {
				dep := &types.Dependency{
					IssueID:     issue.ID,
					DependsOnID: spec.target,
					Type:        spec.depType,
					Metadata:    spec.metadata,
				}
				if spec.swap {
					dep.IssueID = spec.target
					dep.DependsOnID = issue.ID
				}
				if err := tx.AddDependency(ctx, dep, actor); err != nil {
					return fmt.Errorf("failed to add dependency %s -> %s: %w", dep.IssueID, dep.DependsOnID, err)
				}
			}
			return nil
		}); err != nil {
			return HandleErrorRespectJSON("%v", err)
		}

		if repoPath != "." && targetStore != nil {
			if err := commitPendingIfEmbedded(ctx, targetStore, actor, doltAutoCommitParams{
				Command:  "create",
				IssueIDs: []string{issue.ID},
			}); err != nil {
				debug.Logf("warning: failed to commit routed repo: %v", err)
			}
		}

		if remoteCache != nil {
			if pushErr := remoteCache.Push(rootCtx, repoPath); pushErr != nil {
				return HandleErrorRespectJSON("failed to push to %s: %v\nThe issue was created locally but not synced to the remote.", repoPath, pushErr)
			}
		}

		if jsonOutput {
			if err := outputJSON(issue); err != nil {
				return err
			}
		} else if silent {
			fmt.Println(issue.ID)
		} else {
			fmt.Printf("%s Created issue: %s\n", ui.RenderPass("✓"), formatFeedbackID(issue.ID, issue.Title))
			fmt.Printf("  Priority: P%d\n", issue.Priority)
			fmt.Printf("  Status: %s\n", issue.Status)

			maybeShowTip(store)
		}

		SetLastTouchedID(issue.ID)
		return nil
	},
}

type createIssueParams struct {
	ID                 string
	Title              string
	Description        string
	Design             string
	AcceptanceCriteria string
	Notes              string
	SpecID             string
	Priority           int
	IssueType          types.IssueType
	Assignee           string
	ExternalRef        string
	EstimatedMinutes   *int
	Ephemeral          bool
	NoHistory          bool
	CreatedBy          string
	Owner              string
	Labels             []string
	MolType            types.MolType
	WispType           types.WispType
	EventKind          string
	Actor              string
	Target             string
	Payload            string
	DueAt              *time.Time
	DeferUntil         *time.Time
	Metadata           json.RawMessage
}

func buildCreateIssue(params createIssueParams) *types.Issue {
	var externalRefPtr *string
	if params.ExternalRef != "" {
		externalRefPtr = &params.ExternalRef
	}

	status := types.StatusOpen
	if params.DeferUntil != nil && params.DeferUntil.After(time.Now()) {
		status = types.StatusDeferred
	}

	return &types.Issue{
		ID:                 params.ID,
		Title:              params.Title,
		Description:        params.Description,
		Design:             params.Design,
		AcceptanceCriteria: params.AcceptanceCriteria,
		Notes:              params.Notes,
		SpecID:             params.SpecID,
		Status:             status,
		Priority:           params.Priority,
		IssueType:          params.IssueType,
		Assignee:           params.Assignee,
		ExternalRef:        externalRefPtr,
		EstimatedMinutes:   params.EstimatedMinutes,
		Ephemeral:          params.Ephemeral,
		NoHistory:          params.NoHistory,
		CreatedBy:          params.CreatedBy,
		Owner:              params.Owner,
		Labels:             append([]string(nil), params.Labels...),
		MolType:            params.MolType,
		WispType:           params.WispType,
		EventKind:          params.EventKind,
		Actor:              params.Actor,
		Target:             params.Target,
		Payload:            params.Payload,
		DueAt:              params.DueAt,
		DeferUntil:         params.DeferUntil,
		Metadata:           params.Metadata,
	}
}

func mergeCreateLabels(labels, inheritedLabels []string) []string {
	merged := make([]string, 0, len(labels)+len(inheritedLabels))
	seen := make(map[string]struct{}, len(labels)+len(inheritedLabels))
	for _, label := range labels {
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		merged = append(merged, label)
	}
	for _, label := range inheritedLabels {
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		merged = append(merged, label)
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func renderCreateDryRunPreview(issue *types.Issue, labels, deps []string) {
	idDisplay := issue.ID
	if idDisplay == "" {
		idDisplay = "(will be generated)"
	}
	fmt.Printf("%s [DRY RUN] Would create issue:\n", ui.RenderWarn("⚠"))
	fmt.Printf("  ID: %s\n", idDisplay)
	fmt.Printf("  Title: %s\n", ui.SanitizeForTerminal(issue.Title))
	fmt.Printf("  Type: %s\n", issue.IssueType)
	fmt.Printf("  Priority: P%d\n", issue.Priority)
	fmt.Printf("  Status: %s\n", issue.Status)
	if issue.Assignee != "" {
		fmt.Printf("  Assignee: %s\n", ui.SanitizeForTerminal(issue.Assignee))
	}
	if issue.Description != "" {
		fmt.Printf("  Description: %s\n", ui.SanitizeForTerminal(issue.Description))
	}
	if len(labels) > 0 {
		// beads-tt13r: sanitize each label at the display site. Dry-run labels
		// come from --label / inherited labels, and validateLabelValue permits
		// ESC/OSC/CSI bytes, so a poisoned label would inject terminal control
		// sequences here — mirroring the Title/Description (ihaw), Assignee
		// (i8dsb) and EventKind (k86xm) sanitize already applied in this same
		// preview function. Display-only: the stored/created labels are untouched.
		sanitizedLabels := make([]string, len(labels))
		for i, l := range labels {
			sanitizedLabels[i] = ui.SanitizeForTerminal(l)
		}
		fmt.Printf("  Labels: %s\n", strings.Join(sanitizedLabels, ", "))
	}
	if len(deps) > 0 {
		fmt.Printf("  Dependencies: %s\n", strings.Join(deps, ", "))
	}
	if issue.EventKind != "" {
		// Sanitize the event-kind at the display site (beads-k86xm): it can carry
		// terminal escapes from an untrusted source, same axis as the Assignee sink
		// sanitized above; the stored value is untouched.
		fmt.Printf("  Event category: %s\n", ui.SanitizeForTerminal(issue.EventKind))
	}
}

func shouldCommitCreatePostWrites(_ *types.Issue, _ bool) (bool, error) {
	if isEmbeddedMode() {
		if strings.TrimSpace(doltAutoCommit) == "" {
			return true, nil
		}
		mode, err := getDoltAutoCommitMode()
		if err != nil {
			return false, err
		}
		return mode == doltAutoCommitOn, nil
	}
	return false, nil
}

func createDepsAcceptedTypeList() string {
	names := []string{"blocked-by", "depends-on"}
	for _, depType := range types.WellKnownDependencyTypes() {
		names = append(names, string(depType))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func init() {
	createCmd.Flags().StringP("file", "f", "", "Create multiple issues from markdown file")
	createCmd.Flags().String("graph", "", "Create a graph of issues with dependencies from JSON plan file")
	createCmd.Flags().String("title", "", "Issue title (alternative to positional argument)")
	createCmd.Flags().Bool("silent", false, "Output only the issue ID (for scripting)")
	createCmd.Flags().Bool("dry-run", false, "Preview what would be created without actually creating")
	registerPriorityFlag(createCmd, "2")
	createCmd.Flags().StringP("type", "t", "task", "Issue type (bug|feature|task|epic|chore|decision); custom types require types.custom config; aliases: enhancement/feat→feature, dec/adr→decision")
	registerCommonIssueFlags(createCmd)
	createCmd.Flags().String("spec-id", "", "Link to specification document")
	createCmd.Flags().StringSliceP("labels", "l", []string{}, "Labels (comma-separated)")
	createCmd.Flags().String("skills", "", "Required skills for this issue")
	createCmd.Flags().String("context", "", "Additional context for the issue")
	createCmd.Flags().StringSlice("label", []string{}, "Alias for --labels")
	_ = createCmd.Flags().MarkHidden("label") // Only fails if flag missing (caught in tests)
	createCmd.Flags().String("id", "", "Explicit issue ID (e.g., 'bd-42' for partitioning)")
	createCmd.Flags().String("parent", "", "Parent issue ID for hierarchical child (e.g., 'bd-a3f8e9')")
	createCmd.Flags().Bool("no-inherit-labels", false, "Don't inherit labels from parent issue")
	createCmd.Flags().StringSlice("deps", []string{}, "Dependencies in format 'type:id' or 'id' (e.g., 'discovered-from:bd-20,blocks:bd-15' or 'bd-20')")
	createCmd.Flags().String("waits-for", "", "Spawner issue ID to wait for (creates waits-for dependency for fanout gate)")
	createCmd.Flags().String("waits-for-gate", "all-children", "Gate type: all-children (wait for all) or any-children (wait for first)")
	createCmd.Flags().Bool("force", false, "Force creation even if prefix doesn't match database prefix")
	createCmd.Flags().String("repo", "", "Target repository for issue (overrides auto-routing)")
	createCmd.Flags().IntP("estimate", "e", 0, "Time estimate in minutes (e.g., 60 for 1 hour)")
	createCmd.Flags().Bool("ephemeral", false, "Create as ephemeral (short-lived, subject to TTL compaction)")
	createCmd.Flags().Bool("no-history", false, "Skip Dolt commit history without making GC-eligible (for permanent agent beads)")
	createCmd.Flags().String("mol-type", "", "Molecule type: swarm (multi-agent), patrol (recurring ops), work (default)")
	createCmd.Flags().String("wisp-type", "", "Wisp type for TTL-based compaction: heartbeat, ping, patrol, gc_report, recovery, error, escalation")
	createCmd.Flags().Bool("validate", false, "Validate description contains required sections for issue type")
	// Event-specific flags (only valid when --type=event)
	createCmd.Flags().String("event-category", "", "Event category (e.g., patrol.muted, agent.started) (requires --type=event)")
	createCmd.Flags().String("event-actor", "", "Entity URI who caused this event (requires --type=event)")
	createCmd.Flags().String("event-target", "", "Entity URI or bead ID affected (requires --type=event)")
	createCmd.Flags().String("event-payload", "", "Event-specific JSON data (requires --type=event)")
	// Time-based scheduling flags (GH#820)
	// Examples:
	//   --due=+6h           Due in 6 hours
	//   --due=tomorrow      Due tomorrow
	//   --due="next monday" Due next Monday
	//   --due=2025-01-15    Due on specific date
	//   --defer=+1h         Hidden from bd ready for 1 hour
	//   --defer=tomorrow    Hidden until tomorrow
	createCmd.Flags().String("due", "", "Due date/time. Formats: +6h, +1d, +2w, tomorrow, next monday, 2025-01-15")
	createCmd.Flags().String("defer", "", "Defer until date (issue hidden from bd ready until then). Same formats as --due")
	createCmd.Flags().String("metadata", "", "Set custom metadata (JSON string or @file.json to read from file)")
	// Note: --json flag is defined as a persistent flag in main.go, not here
	rootCmd.AddCommand(createCmd)
}

// formatTimeForRPC converts a *time.Time to RFC3339 string for RPC calls.
// Returns empty string if t is nil, to distinguish "not set" from "set to zero".
func formatTimeForRPC(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

func openDryRunTargetStore(ctx context.Context, repoPath string) (storage.DoltStorage, error) {
	if remotecache.IsRemoteURL(repoPath) {
		cache, err := remotecache.DefaultCache()
		if err != nil {
			return nil, fmt.Errorf("failed to initialize remote cache: %w", err)
		}
		// The dry-run parent lookup only reads from this cached remote store.
		// Do not add writes here; dry-runs must not mutate cached remotes.
		store, err := cache.OpenStore(ctx, repoPath, newDoltStoreFromConfig)
		if err != nil {
			return nil, fmt.Errorf("dry-run parent lookup requires an existing cached remote store for %s: %w", repoPath, err)
		}
		return store, nil
	}

	targetPath := routing.ExpandPath(repoPath)
	beadsDir := filepath.Join(targetPath, ".beads")
	metadataPath := filepath.Join(beadsDir, "metadata.json")
	if _, err := os.Stat(metadataPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("target repo %s is not initialized; refusing to initialize it during dry-run", targetPath)
		}
		return nil, fmt.Errorf("failed to inspect target repo %s: %w", targetPath, err)
	}

	store, err := newDoltStoreFromConfig(ctx, beadsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open target store for dry-run: %w", err)
	}
	return store, nil
}

// ensureBeadsDirForPath ensures a beads directory exists at the target path.
// If the .beads directory doesn't exist, it creates it and initializes with
// the same prefix as the source store (T010, T012: prefix inheritance).
func ensureBeadsDirForPath(ctx context.Context, targetPath string, sourceStore storage.DoltStorage) error {
	beadsDir := filepath.Join(targetPath, ".beads")
	metadataPath := filepath.Join(beadsDir, "metadata.json")

	// Check if beads directory already exists with a Dolt database.
	// metadata.json is the canonical marker for an initialized beads dir.
	if _, err := os.Stat(metadataPath); err == nil {
		return nil
	}

	// Create .beads directory
	if err := os.MkdirAll(beadsDir, 0750); err != nil {
		return fmt.Errorf("cannot create .beads directory: %w", err)
	}

	// Initialize database via NewFromConfigWithOptions to respect Dolt config.
	// Set the prefix if source store has one (T012: prefix inheritance).
	if sourceStore != nil {
		sourcePrefix, err := sourceStore.GetConfig(ctx, "issue_prefix")
		if err == nil && sourcePrefix != "" {
			// Sanitize prefix for SQL database name (same as bd init).
			dbName := strings.ReplaceAll(sourcePrefix, "-", "_")

			// Open target store temporarily to set prefix.
			// Use newDoltStore with explicit config since the target .beads
			// directory was just created and has no metadata.json yet.
			tempStore, err := newDoltStore(ctx, &dolt.Config{
				BeadsDir:        beadsDir,
				Database:        dbName,
				CreateIfMissing: true,
			})
			if err != nil {
				return fmt.Errorf("failed to initialize target database: %w", err)
			}
			if err := tempStore.SetConfig(ctx, "issue_prefix", sourcePrefix); err != nil {
				_ = tempStore.Close() // Best effort cleanup on error path
				return fmt.Errorf("failed to set prefix in target store: %w", err)
			}
			if err := tempStore.Close(); err != nil {
				return fmt.Errorf("failed to close target store: %w", err)
			}

			// Write metadata.json so newDoltStoreFromConfig can find the
			// correct database name on subsequent opens (GH#2988).
			cfg := configfile.DefaultConfig()
			cfg.Backend = configfile.BackendDolt
			cfg.DoltDatabase = dbName
			cfg.DoltMode = configfile.DoltModeEmbedded
			cfg.ProjectID = configfile.GenerateProjectID()
			if err := cfg.Save(beadsDir); err != nil {
				return fmt.Errorf("failed to write metadata.json: %w", err)
			}
		}
	}

	return nil
}
