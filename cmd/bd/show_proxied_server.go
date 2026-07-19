package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
	"github.com/steveyegge/beads/internal/uimd"
)

type showProxiedInput struct {
	ids             []string
	thread          bool
	shortMode       bool
	longMode        bool
	refs            bool
	children        bool
	asOfRef         string
	localTime       bool
	watchMode       bool
	currentMode     bool
	includeDepends  bool
	includeComments bool
}

func gatherShowProxiedInput(cmd *cobra.Command, args []string) *showProxiedInput {
	in := &showProxiedInput{}
	in.thread, _ = cmd.Flags().GetBool("thread")
	in.shortMode, _ = cmd.Flags().GetBool("short")
	in.longMode, _ = cmd.Flags().GetBool("long")
	in.refs, _ = cmd.Flags().GetBool("refs")
	in.children, _ = cmd.Flags().GetBool("children")
	in.asOfRef, _ = cmd.Flags().GetString("as-of")
	in.localTime, _ = cmd.Flags().GetBool("local-time")
	in.watchMode, _ = cmd.Flags().GetBool("watch")
	in.currentMode, _ = cmd.Flags().GetBool("current")
	in.includeDepends, _ = cmd.Flags().GetBool("include-dependents")
	in.includeComments, _ = cmd.Flags().GetBool("include-comments")

	idFlags, _ := cmd.Flags().GetStringArray("id")
	in.ids = append(in.ids, args...)
	in.ids = append(in.ids, idFlags...)
	return in
}

func proxiedOpenReadUOW(ctx context.Context) uow.UnitOfWork {
	if uowProvider == nil {
		FatalError("proxied-server UOW provider not initialized")
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		FatalErrorRespectJSON("open unit of work: %v", err)
	}
	return uw
}

func runShowProxiedServer(cmd *cobra.Command, ctx context.Context, args []string) {
	in := gatherShowProxiedInput(cmd, args)

	if in.watchMode {
		FatalErrorRespectJSON("watch mode not supported in proxied-server mode")
	}

	uw := proxiedOpenReadUOW(ctx)
	defer uw.Close(ctx)

	if in.currentMode {
		if len(in.ids) > 0 {
			FatalErrorRespectJSON("--current cannot be combined with explicit issue IDs")
		}
		currentID := resolveCurrentIssueIDProxied(ctx, uw)
		if currentID == "" {
			FatalErrorRespectJSON("no current issue found (no in-progress, hooked, or recently touched issues)")
		}
		in.ids = []string{currentID}
	}

	if len(in.ids) == 0 {
		FatalErrorRespectJSON("at least one issue ID is required (use positional args, --id flag, or --current)")
	}

	// beads-qtw9: the as-of/refs/children legs render the ids that DID resolve
	// but must still signal a non-zero exit on ANY id-resolution failure, to
	// match the direct paths (show_refs.go/show_children.go/show.go all return
	// exitError{Code:1} on any failed id) and the proxied default/thread legs
	// (which FatalError/os.Exit). Those three return a failed-id count; the
	// others FatalError/os.Exit internally so they report 0 here.
	failedCount := 0
	switch {
	case in.asOfRef != "":
		failedCount = runShowProxiedAsOf(ctx, uw, in)
	case in.thread:
		runShowProxiedThread(ctx, uw, in)
	case in.refs:
		failedCount = runShowProxiedRefs(ctx, uw, in)
	case in.children:
		failedCount = runShowProxiedChildren(ctx, uw, in)
	default:
		failedCount = runShowProxiedDefault(ctx, uw, in)
	}

	// beads-qtw9: any id-resolution failure -> rc!=0 (partial success still
	// printed the resolved items above), matching the direct-path contract.
	if failedCount > 0 {
		os.Exit(1)
	}

	// beads-87i2 (supersedes the beads-kuv1 show-sets-last-touched behavior): a
	// read-only view must NOT arm the last-touched close/update target — a later
	// bare `bd close`/`bd update` would otherwise silently hit the merely-viewed
	// issue. Only create/update/close set last-touched; `bd show --current` still
	// resolves via in-progress/hooked (primary) + that create/update/close-set
	// fallback. Kept in lockstep with the direct path (cmd/bd/show.go).
}

func resolveCurrentIssueIDProxied(ctx context.Context, uw uow.UnitOfWork) string {
	currentActor := getActorWithGit()
	if currentActor != "" {
		for _, status := range []types.Status{types.StatusInProgress, types.StatusHooked} {
			st := status
			filter := types.IssueFilter{Status: &st, Assignee: &currentActor}
			page, err := uw.IssueUseCase().SearchIssues(ctx, "", filter)
			if err == nil && len(page.Items) > 0 {
				return page.Items[0].ID
			}
		}
	}
	// beads-kuv1: fall back to the last-touched issue, matching the direct path
	// (cmd/bd/show.go resolveCurrentIssueID step 3). GetLastTouchedID is
	// file-based (reads .beads/), so it works in the proxied/subprocess context
	// too — dropping it made proxied `--current` strictly less capable (it
	// FatalError'd right after `bd show <id>` / `bd update`, the common case).
	return GetLastTouchedID()
}

func proxiedGetIssueOrWisp(ctx context.Context, uw uow.UnitOfWork, id string) (issue *types.Issue, isWisp bool, err error) {
	issue, err = uw.IssueUseCase().GetIssue(ctx, id)
	if err == nil && issue != nil {
		return issue, false, nil
	}
	wispIssue, wispErr := uw.IssueUseCase().GetWisp(ctx, id)
	if wispErr == nil && wispIssue != nil {
		return wispIssue, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	return nil, false, nil
}

func proxiedListDeps(ctx context.Context, uw uow.UnitOfWork, id string, isWisp bool, filter domain.DepListFilter) ([]*types.IssueWithDependencyMetadata, error) {
	if isWisp {
		return uw.DependencyUseCase().ListWispWithIssueMetadata(ctx, id, filter)
	}
	return uw.DependencyUseCase().ListWithIssueMetadata(ctx, id, filter)
}

func proxiedCountDeps(ctx context.Context, uw uow.UnitOfWork, id string, isWisp bool, filter domain.DepListFilter) (int64, error) {
	if isWisp {
		return uw.DependencyUseCase().CountByWispID(ctx, id, filter)
	}
	return uw.DependencyUseCase().CountByIssueID(ctx, id, filter)
}

func proxiedGetComments(ctx context.Context, uw uow.UnitOfWork, id string, isWisp bool) ([]*types.Comment, error) {
	if isWisp {
		return uw.CommentUseCase().GetCommentsForWisp(ctx, id)
	}
	return uw.CommentUseCase().GetCommentsForIssue(ctx, id)
}

func proxiedCountComments(ctx context.Context, uw uow.UnitOfWork, id string, isWisp bool) (int64, error) {
	if isWisp {
		return uw.CommentUseCase().CountCommentsForWisp(ctx, id)
	}
	return uw.CommentUseCase().CountCommentsForIssue(ctx, id)
}

func runShowProxiedAsOf(ctx context.Context, uw uow.UnitOfWork, in *showProxiedInput) int {
	failedCount := 0
	var jsonIssues []*types.Issue
	for idx, id := range in.ids {
		issue, err := uw.IssueUseCase().AsOf(ctx, id, in.asOfRef)
		if err != nil {
			reportItemError("Error fetching %s as of %s: %v", id, in.asOfRef, err)
			failedCount++
			continue
		}
		if issue == nil {
			reportItemError("Issue %s did not exist at %s", id, in.asOfRef)
			failedCount++
			continue
		}

		if in.shortMode {
			fmt.Println(formatShortIssue(issue))
			continue
		}
		if jsonOutput {
			jsonIssues = append(jsonIssues, issue)
			continue
		}

		if idx > 0 {
			fmt.Println("\n" + ui.RenderMuted(strings.Repeat("-", 60)))
		}
		fmt.Printf("\n%s (as of %s)\n", formatIssueHeader(issue), ui.RenderMuted(in.asOfRef))
		fmt.Println(formatIssueMetadata(issue))
		if issue.Description != "" {
			fmt.Printf("\n%s\n%s\n", ui.RenderBold("DESCRIPTION"), uimd.RenderMarkdown(issue.Description))
		}
		fmt.Println()
	}
	if jsonOutput && len(jsonIssues) > 0 {
		_ = outputJSON(jsonIssues)
	}
	return failedCount
}

func runShowProxiedRefs(ctx context.Context, uw uow.UnitOfWork, in *showProxiedInput) int {
	failedCount := 0
	allRefs := make(map[string][]*types.IssueWithDependencyMetadata, len(in.ids))
	for _, id := range in.ids {
		issue, isWisp, err := proxiedGetIssueOrWisp(ctx, uw, id)
		if err != nil {
			reportItemError("Error resolving %s: %v", id, err)
			failedCount++
			continue
		}
		if issue == nil {
			reportItemError("Issue %s not found", id)
			failedCount++
			continue
		}
		refs, err := proxiedListDeps(ctx, uw, id, isWisp, domain.DepListFilter{Direction: domain.DepDirectionIn})
		if err != nil {
			reportItemError("Error getting refs for %s: %v", id, err)
			failedCount++
			continue
		}
		allRefs[id] = refs
	}

	if jsonOutput {
		_ = outputJSON(allRefs)
		return failedCount
	}
	for id, refs := range allRefs {
		if len(refs) == 0 {
			fmt.Printf("\n%s: No references found\n", ui.RenderAccent(id))
			continue
		}
		fmt.Printf("\n%s References to %s:\n", ui.RenderAccent("📎"), id)
		refsByType := make(map[types.DependencyType][]*types.IssueWithDependencyMetadata)
		for _, ref := range refs {
			refsByType[ref.DependencyType] = append(refsByType[ref.DependencyType], ref)
		}
		typeOrder := []types.DependencyType{
			types.DepUntil, types.DepCausedBy, types.DepValidates,
			types.DepBlocks, types.DepParentChild, types.DepRelatesTo,
			types.DepTracks, types.DepDiscoveredFrom, types.DepRelated,
			types.DepSupersedes, types.DepDuplicates, types.DepRepliesTo,
			types.DepApprovedBy, types.DepAuthoredBy, types.DepAssignedTo,
		}
		shown := make(map[types.DependencyType]bool)
		for _, depType := range typeOrder {
			if grp, ok := refsByType[depType]; ok {
				displayRefGroup(depType, grp)
				shown[depType] = true
			}
		}
		for depType, grp := range refsByType {
			if !shown[depType] {
				displayRefGroup(depType, grp)
			}
		}
		fmt.Println()
	}
	return failedCount
}

func runShowProxiedChildren(ctx context.Context, uw uow.UnitOfWork, in *showProxiedInput) int {
	failedCount := 0
	allChildren := make(map[string][]*types.IssueWithDependencyMetadata, len(in.ids))
	for _, id := range in.ids {
		issue, isWisp, err := proxiedGetIssueOrWisp(ctx, uw, id)
		if err != nil {
			reportItemError("Error resolving %s: %v", id, err)
			failedCount++
			continue
		}
		if issue == nil {
			reportItemError("Issue %s not found", id)
			failedCount++
			continue
		}
		kids, err := proxiedListDeps(ctx, uw, id, isWisp, domain.DepListFilter{
			Types:     []types.DependencyType{types.DepParentChild},
			Direction: domain.DepDirectionIn,
		})
		if err != nil {
			reportItemError("Error getting children for %s: %v", id, err)
			failedCount++
			continue
		}
		if kids == nil {
			kids = []*types.IssueWithDependencyMetadata{}
		}
		allChildren[id] = kids
	}

	if jsonOutput {
		_ = outputJSON(allChildren)
		return failedCount
	}
	for id, kids := range allChildren {
		if len(kids) == 0 {
			fmt.Printf("%s: No children found\n", ui.RenderAccent(id))
			continue
		}
		fmt.Printf("%s Children of %s (%d):\n", ui.RenderAccent("↳"), id, len(kids))
		for _, c := range kids {
			if in.shortMode {
				fmt.Printf("  %s\n", formatShortIssue(&c.Issue))
			} else {
				fmt.Println(formatDependencyLine("↳", c))
			}
		}
		fmt.Println()
	}
	return failedCount
}

func runShowProxiedThread(ctx context.Context, uw uow.UnitOfWork, in *showProxiedInput) {
	if len(in.ids) == 0 {
		return
	}
	startMsg, _, err := proxiedGetIssueOrWisp(ctx, uw, in.ids[0])
	if err != nil {
		FatalErrorRespectJSON("fetching message %s: %v", in.ids[0], err)
	}
	if startMsg == nil {
		FatalErrorRespectJSON("message %s not found", in.ids[0])
		return // unreachable (FatalErrorRespectJSON exits); makes non-nil startMsg explicit
	}

	rootMsg := startMsg
	seen := map[string]bool{rootMsg.ID: true}
	for {
		parentID := proxiedFindRepliesTo(ctx, uw, rootMsg.ID)
		if parentID == "" || seen[parentID] {
			break
		}
		seen[parentID] = true
		parentMsg, _ := uw.IssueUseCase().GetIssue(ctx, parentID)
		if parentMsg == nil {
			parentMsg, _ = uw.IssueUseCase().GetWisp(ctx, parentID)
		}
		if parentMsg == nil {
			break
		}
		rootMsg = parentMsg
	}

	threadMessages := []*types.Issue{rootMsg}
	threadIDs := map[string]bool{rootMsg.ID: true}
	repliesTo := map[string]string{}
	queue := []string{rootMsg.ID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		replies := proxiedFindReplies(ctx, uw, current)
		for _, reply := range replies {
			if threadIDs[reply.ID] {
				continue
			}
			r := reply
			threadMessages = append(threadMessages, &r)
			threadIDs[reply.ID] = true
			repliesTo[reply.ID] = current
			queue = append(queue, reply.ID)
		}
	}

	slices.SortFunc(threadMessages, func(a, b *types.Issue) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})

	if jsonOutput {
		_ = outputJSON(threadMessages)
		return
	}

	printProxiedThread(threadMessages, repliesTo, rootMsg)
}

// printProxiedThread renders the human-readable thread view for the proxied
// 'bd show <id> --thread' path. The thread header title (rootMsg.Title) and
// each message's Subject (msg.Title) are routed through displayTitle
// (ui.SanitizeForTerminal): a message Subject is settable by other actors
// (e.g. via --stdin) and a title imported from JSONL/markdown/SCM carries its
// value verbatim, so an OSC/CSI terminal-control escape (OSC 0 window-title /
// OSC 52 clipboard) would otherwise reach the terminal. Display-only — the
// stored title and the JSON path (outputJSON above) are unchanged. This is the
// proxied twin of the direct show_thread.go sinks fixed in beads-s3qhv.
func printProxiedThread(threadMessages []*types.Issue, repliesTo map[string]string, rootMsg *types.Issue) {
	fmt.Printf("\n%s Thread: %s\n", ui.RenderAccent("📬"), displayTitle(rootMsg.Title))
	fmt.Println(strings.Repeat("─", 66))
	for _, msg := range threadMessages {
		depth := 0
		parent := repliesTo[msg.ID]
		for parent != "" && depth < 5 {
			depth++
			parent = repliesTo[parent]
		}
		indent := strings.Repeat("  ", depth)
		timeStr := msg.CreatedAt.Format("2006-01-02 15:04")
		statusIcon := "📧"
		if msg.Status == types.StatusClosed {
			statusIcon = "✓"
		}
		fmt.Printf("%s%s %s %s\n", indent, statusIcon, ui.RenderAccent(msg.ID), ui.RenderMuted(timeStr))
		fmt.Printf("%s  From: %s  To: %s\n", indent, msg.Sender, msg.Assignee)
		if parentID := repliesTo[msg.ID]; parentID != "" {
			fmt.Printf("%s  Re: %s\n", indent, parentID)
		}
		fmt.Printf("%s  %s: %s\n", indent, ui.RenderMuted("Subject"), displayTitle(msg.Title))
		if msg.Description != "" {
			for _, line := range strings.Split(msg.Description, "\n") {
				fmt.Printf("%s  %s\n", indent, line)
			}
		}
		fmt.Println()
	}
	fmt.Printf("Total: %d messages in thread\n\n", len(threadMessages))
}

func proxiedFindRepliesTo(ctx context.Context, uw uow.UnitOfWork, id string) string {
	deps, err := uw.DependencyUseCase().ListWithIssueMetadata(ctx, id, domain.DepListFilter{
		Types:     []types.DependencyType{types.DepRepliesTo},
		Direction: domain.DepDirectionOut,
	})
	if err != nil || len(deps) == 0 {
		return ""
	}
	return deps[0].ID
}

func proxiedFindReplies(ctx context.Context, uw uow.UnitOfWork, id string) []types.Issue {
	deps, err := uw.DependencyUseCase().ListWithIssueMetadata(ctx, id, domain.DepListFilter{
		Types:     []types.DependencyType{types.DepRepliesTo},
		Direction: domain.DepDirectionIn,
	})
	if err != nil {
		return nil
	}
	out := make([]types.Issue, 0, len(deps))
	for _, d := range deps {
		out = append(out, d.Issue)
	}
	return out
}

func runShowProxiedDefault(ctx context.Context, uw uow.UnitOfWork, in *showProxiedInput) int {
	formatTime := func(t time.Time) string {
		if in.localTime {
			t = t.Local()
		}
		return t.Format("2006-01-02 15:04")
	}

	var allDetails []interface{}
	foundCount := 0
	failedCount := 0
	for idx, id := range in.ids {
		issue, isWisp, err := proxiedGetIssueOrWisp(ctx, uw, id)
		if err != nil {
			reportItemError("Error fetching %s: %v", id, err)
			failedCount++
			continue
		}
		if issue == nil {
			reportItemError("Issue %s not found", id)
			failedCount++
			continue
		}
		foundCount++

		if in.shortMode {
			fmt.Println(formatShortIssue(issue))
			continue
		}

		if jsonOutput {
			details := proxiedBuildDetails(ctx, uw, issue, isWisp, in)
			allDetails = append(allDetails, details)
			continue
		}

		proxiedRenderIssue(ctx, uw, issue, isWisp, in, idx, formatTime)
	}

	if jsonOutput {
		if len(allDetails) > 0 {
			// beads-ej2f: partial success — the found issues are the stdout JSON
			// array; the failedCount is returned so runShowProxiedServer exits
			// non-zero (matching the direct show.go:469 failedCount>0 contract).
			_ = outputJSON(allDetails)
		} else {
			// All failed: preserve the single stdout JSON error object (beads-92tz).
			FatalErrorRespectJSON("no issues found matching the provided IDs")
		}
	} else if foundCount == 0 {
		os.Exit(1)
	}
	// beads-ej2f: any failed id drives a non-zero exit even on partial success,
	// mirroring the as-of/refs/children legs (qtw9) and the direct default path
	// (cmd/bd/show.go returns exitError{Code:1} on failedCount>0).
	return failedCount
}

func proxiedBuildDetails(ctx context.Context, uw uow.UnitOfWork, issue *types.Issue, isWisp bool, in *showProxiedInput) *types.IssueDetails {
	details := &types.IssueDetails{Issue: *issue}

	if isWisp {
		details.Labels, _ = uw.LabelUseCase().GetWispLabels(ctx, issue.ID)
	} else {
		details.Labels, _ = uw.LabelUseCase().GetLabels(ctx, issue.ID)
	}

	deps, _ := proxiedListDeps(ctx, uw, issue.ID, isWisp, domain.DepListFilter{Direction: domain.DepDirectionOut})
	details.Dependencies = deps

	depCount, _ := proxiedCountDeps(ctx, uw, issue.ID, isWisp, domain.DepListFilter{Direction: domain.DepDirectionIn})
	details.DependentCount = &depCount
	depnCount, _ := proxiedCountDeps(ctx, uw, issue.ID, isWisp, domain.DepListFilter{Direction: domain.DepDirectionOut})
	details.DependencyCount = &depnCount
	cmtCount, _ := proxiedCountComments(ctx, uw, issue.ID, isWisp)
	details.CommentCount = &cmtCount

	if in.includeDepends {
		dependents, err := proxiedListDeps(ctx, uw, issue.ID, isWisp, domain.DepListFilter{Direction: domain.DepDirectionIn})
		if err == nil {
			shallow := make([]*types.IssueWithDependencyMetadata, 0, len(dependents))
			for _, item := range dependents {
				shallow = append(shallow, &types.IssueWithDependencyMetadata{
					Issue: types.Issue{
						ID:        item.ID,
						Status:    item.Status,
						IssueType: item.IssueType,
						Priority:  item.Priority,
						Title:     item.Title,
					},
					DependencyType: item.DependencyType,
				})
			}
			details.Dependents = shallow

			if issue.IssueType == types.TypeEpic && len(shallow) > 0 {
				total, closed := 0, 0
				for _, dep := range shallow {
					if dep.DependencyType == types.DepParentChild {
						total++
						if dep.Status == types.StatusClosed {
							closed++
						}
					}
				}
				if total > 0 {
					details.EpicTotalChildren = &total
					details.EpicClosedChildren = &closed
					closeable := total == closed
					details.EpicCloseable = &closeable
				}
			}
		}
	}

	if in.includeComments {
		comments, err := proxiedGetComments(ctx, uw, issue.ID, isWisp)
		if err == nil {
			details.Comments = comments
		}
	}

	for _, dep := range details.Dependencies {
		if dep.DependencyType == types.DepParentChild {
			parentID := dep.ID
			details.Parent = &parentID
			break
		}
	}
	return details
}

func proxiedRenderIssue(ctx context.Context, uw uow.UnitOfWork, issue *types.Issue, isWisp bool, in *showProxiedInput, idx int, formatTime func(time.Time) string) {
	if idx > 0 {
		fmt.Println("\n" + ui.RenderMuted(strings.Repeat("─", 60)))
		fmt.Printf("\n%s\n", formatIssueHeader(issue))
	} else {
		fmt.Printf("%s\n", formatIssueHeader(issue))
	}
	fmt.Println(formatIssueMetadata(issue))

	if issue.CompactionLevel > 0 && issue.OriginalSize > 0 {
		currentSize := len(issue.Description) + len(issue.Design) + len(issue.Notes) + len(issue.AcceptanceCriteria)
		saved := issue.OriginalSize - currentSize
		if saved > 0 {
			reduction := float64(saved) / float64(issue.OriginalSize) * 100
			fmt.Println()
			fmt.Printf("📊 %d → %d bytes (%.0f%% reduction)\n", issue.OriginalSize, currentSize, reduction)
		}
	}

	if issue.Description != "" {
		fmt.Printf("\n%s\n%s\n", ui.RenderBold("DESCRIPTION"), uimd.RenderMarkdown(issue.Description))
	} else {
		fmt.Printf("\n%s\n  %s\n", ui.RenderBold("DESCRIPTION"), ui.RenderMuted("(none)"))
	}
	if issue.Design != "" {
		fmt.Printf("\n%s\n%s\n", ui.RenderBold("DESIGN"), uimd.RenderMarkdown(issue.Design))
	}
	if issue.Notes != "" {
		fmt.Printf("\n%s\n%s\n", ui.RenderBold("NOTES"), uimd.RenderMarkdown(issue.Notes))
	}
	if issue.AcceptanceCriteria != "" {
		fmt.Printf("\n%s\n%s\n", ui.RenderBold("ACCEPTANCE CRITERIA"), uimd.RenderMarkdown(issue.AcceptanceCriteria))
	}

	var labels []string
	if isWisp {
		labels, _ = uw.LabelUseCase().GetWispLabels(ctx, issue.ID)
	} else {
		labels, _ = uw.LabelUseCase().GetLabels(ctx, issue.ID)
	}
	if len(labels) > 0 {
		fmt.Printf("\n%s %s\n", ui.RenderBold("LABELS:"), strings.Join(labels, ", "))
	}

	if metaStr := formatIssueCustomMetadata(issue); metaStr != "" {
		fmt.Printf("\n%s\n", metaStr)
	}

	relatedSeen := make(map[string]*types.IssueWithDependencyMetadata)

	depsWithMeta, _ := proxiedListDeps(ctx, uw, issue.ID, isWisp, domain.DepListFilter{Direction: domain.DepDirectionOut})
	if len(depsWithMeta) > 0 {
		var blocks, parent, discovered []*types.IssueWithDependencyMetadata
		for _, dep := range depsWithMeta {
			switch dep.DependencyType {
			case types.DepBlocks:
				blocks = append(blocks, dep)
			case types.DepParentChild:
				parent = append(parent, dep)
			case types.DepRelated, types.DepRelatesTo:
				relatedSeen[dep.ID] = dep
			case types.DepDiscoveredFrom:
				discovered = append(discovered, dep)
			default:
				blocks = append(blocks, dep)
			}
		}
		if len(parent) > 0 {
			fmt.Printf("\n%s\n", ui.RenderBold("PARENT"))
			for _, dep := range parent {
				fmt.Println(formatDependencyLine("↑", dep))
			}
		}
		if len(blocks) > 0 {
			fmt.Printf("\n%s\n", ui.RenderBold("DEPENDS ON"))
			for _, dep := range blocks {
				fmt.Println(formatDependencyLine("→", dep))
			}
		}
		if len(discovered) > 0 {
			fmt.Printf("\n%s\n", ui.RenderBold("DISCOVERED FROM"))
			for _, dep := range discovered {
				fmt.Println(formatDependencyLine("◊", dep))
			}
		}
	}

	dependentsWithMeta, _ := proxiedListDeps(ctx, uw, issue.ID, isWisp, domain.DepListFilter{Direction: domain.DepDirectionIn})
	if len(dependentsWithMeta) > 0 {
		var blocks, children, discovered []*types.IssueWithDependencyMetadata
		for _, dep := range dependentsWithMeta {
			switch dep.DependencyType {
			case types.DepBlocks:
				blocks = append(blocks, dep)
			case types.DepParentChild:
				children = append(children, dep)
			case types.DepRelated, types.DepRelatesTo:
				relatedSeen[dep.ID] = dep
			case types.DepDiscoveredFrom:
				discovered = append(discovered, dep)
			default:
				blocks = append(blocks, dep)
			}
		}
		if len(children) > 0 {
			fmt.Printf("\n%s\n", ui.RenderBold("CHILDREN"))
			for _, dep := range children {
				fmt.Println(formatDependencyLine("↳", dep))
			}
			if issue.IssueType == types.TypeEpic {
				closedCount := 0
				for _, dep := range children {
					if dep.Status == types.StatusClosed {
						closedCount++
					}
				}
				pct := 0
				if len(children) > 0 {
					pct = (closedCount * 100) / len(children)
				}
				if closedCount == len(children) {
					fmt.Printf("  %s %d/%d complete (%d%%) — eligible for close\n", ui.RenderPass("✓"), closedCount, len(children), pct)
				} else {
					fmt.Printf("  %s %d/%d complete (%d%%)\n", ui.RenderMuted("◐"), closedCount, len(children), pct)
				}
			}
		}
		if len(blocks) > 0 {
			fmt.Printf("\n%s\n", ui.RenderBold("BLOCKS"))
			for _, dep := range blocks {
				fmt.Println(formatDependencyLine("←", dep))
			}
		}
		if len(discovered) > 0 {
			fmt.Printf("\n%s\n", ui.RenderBold("DISCOVERED"))
			for _, dep := range discovered {
				fmt.Println(formatDependencyLine("◊", dep))
			}
		}
	}

	if len(relatedSeen) > 0 {
		fmt.Printf("\n%s\n", ui.RenderBold("RELATED"))
		ids := make([]string, 0, len(relatedSeen))
		for k := range relatedSeen {
			ids = append(ids, k)
		}
		sort.Strings(ids)
		for _, k := range ids {
			fmt.Println(formatDependencyLine("↔", relatedSeen[k]))
		}
	}

	comments, _ := proxiedGetComments(ctx, uw, issue.ID, isWisp)
	if len(comments) > 0 {
		fmt.Printf("\n%s\n", ui.RenderBold("COMMENTS"))
		for _, c := range comments {
			fmt.Printf("  %s %s\n", ui.RenderMuted(formatTime(c.CreatedAt)), c.Author)
			rendered := uimd.RenderMarkdown(c.Text)
			for _, line := range strings.Split(strings.TrimRight(rendered, "\n"), "\n") {
				fmt.Printf("    %s\n", line)
			}
		}
	}

	if in.longMode {
		fmt.Print(formatIssueLongExtras(issue, formatTime))
	}

	fmt.Println()
}
