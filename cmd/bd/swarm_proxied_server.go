package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

// swarm_proxied_server.go makes the `bd swarm` subcommand family
// (validate/status/create/list) proxied-server-aware (beads-2n2f, aocj/qppc/1zuh
// class). In proxiedServerMode the global `store` is nil (main.go PersistentPreRun
// wires uowProvider but returns before newDoltStore), so every swarm subcommand —
// which reaches storage only through the SwarmStorage interface (GetIssue,
// GetDependents, GetDependencyRecords) plus store.SearchIssues (list) and the
// create writes (CreateIssue/AddDependency) — failed "no database connection" /
// "storage is nil" on hub-connected crew.
//
// The prior scope note filed 2n2f as domain-interface-extension-gated on the
// belief that the UOW had no reverse-edge dependents accessor. That is obsolete:
// the reverse edge is reachable through the EXISTING DependencyUseCase via
// ListWithIssueMetadata(id, DepListFilter{Direction: DepDirectionIn}) (the same
// path show/dep proxied handlers already use), and singular dependency records
// map onto GetForIssueIDs([]string{id}). So swarm is a clean-mirror leg: no new
// interface method, and every shared helper (findExistingSwarm / getEpicChildren
// / analyzeEpicForSwarm / getSwarmStatus / renderSwarmAnalysis / renderSwarmStatus)
// is reused unchanged against a UOW-backed SwarmStorage adapter.

// swarmProxiedStorage adapts a UOW to the SwarmStorage interface so the shared,
// well-tested swarm analysis/status helpers run unchanged in proxied-server mode.
type swarmProxiedStorage struct {
	uw uow.UnitOfWork
}

// GetIssue mirrors store.GetIssue via the UOW. It normalizes a not-found (nil,
// nil) to (nil, storage.ErrNotFound) so callers using errors.Is(err,
// storage.ErrNotFound) behave the same as on the direct path.
func (s *swarmProxiedStorage) GetIssue(ctx context.Context, id string) (*types.Issue, error) {
	issue, err := s.uw.IssueUseCase().GetIssue(ctx, id)
	if err != nil {
		return nil, err
	}
	if issue == nil {
		return nil, storage.ErrNotFound
	}
	return issue, nil
}

// GetDependents returns the reverse-edge dependents of issueID (issues that
// depend ON issueID), mirroring DoltStore.GetDependents. The direct store returns
// []*types.Issue with dependency type dropped; the UOW reverse-edge accessor
// returns []*IssueWithDependencyMetadata whose embedded Issue is exactly what the
// swarm helpers consume (they re-fetch the full issue via GetIssue when they need
// mol_type, matching the direct path's documented behavior at swarm.go findExistingSwarm).
func (s *swarmProxiedStorage) GetDependents(ctx context.Context, issueID string) ([]*types.Issue, error) {
	deps, err := s.uw.DependencyUseCase().ListWithIssueMetadata(ctx, issueID, domain.DepListFilter{
		Direction: domain.DepDirectionIn,
	})
	if err != nil {
		return nil, err
	}
	out := make([]*types.Issue, 0, len(deps))
	for _, d := range deps {
		issue := d.Issue // copy the embedded value; take its address
		out = append(out, &issue)
	}
	return out, nil
}

// GetDependencyRecords returns the forward dependency records for issueID,
// mirroring DoltStore.GetDependencyRecords via the batch UOW accessor.
func (s *swarmProxiedStorage) GetDependencyRecords(ctx context.Context, issueID string) ([]*types.Dependency, error) {
	byID, err := s.uw.DependencyUseCase().GetForIssueIDs(ctx, []string{issueID})
	if err != nil {
		return nil, err
	}
	return byID[issueID], nil
}

// searchIssues mirrors store.SearchIssues via the UOW (used by swarm list).
func (s *swarmProxiedStorage) searchIssues(ctx context.Context, query string, filter types.IssueFilter) ([]*types.Issue, error) {
	page, err := s.uw.IssueUseCase().SearchIssues(ctx, query, filter)
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

// openSwarmProxiedUOW opens a UOW for the proxied swarm handlers.
func openSwarmProxiedUOW(ctx context.Context) (uow.UnitOfWork, error) {
	if uowProvider == nil {
		return nil, fmt.Errorf("proxied-server UOW provider not initialized")
	}
	return uowProvider.NewUOW(ctx)
}

// runSwarmValidateProxied mirrors swarmValidateCmd's RunE via the UOW.
func runSwarmValidateProxied(ctx context.Context, epicRef string, verbose bool) error {
	uw, err := openSwarmProxiedUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}
	defer uw.Close(ctx)
	s := &swarmProxiedStorage{uw: uw}

	epic, err := s.GetIssue(ctx, epicRef)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return HandleErrorRespectJSON("epic '%s' not found", epicRef)
		}
		return HandleErrorRespectJSON("failed to get epic: %v", err)
	}

	if epic.IssueType != types.TypeEpic && epic.IssueType != "molecule" {
		return HandleErrorRespectJSON("'%s' is not an epic or molecule (type: %s)", epic.ID, epic.IssueType)
	}

	analysis, err := analyzeEpicForSwarm(ctx, s, epic)
	if err != nil {
		return HandleErrorRespectJSON("failed to analyze epic: %v", err)
	}

	if !verbose {
		analysis.Issues = nil
	}

	if jsonOutput {
		if jerr := outputJSON(analysis); jerr != nil {
			return jerr
		}
		if !analysis.Swarmable {
			return SilentExit()
		}
		return nil
	}

	renderSwarmAnalysis(analysis)

	if !analysis.Swarmable {
		return SilentExit()
	}
	return nil
}

// runSwarmStatusProxied mirrors swarmStatusCmd's RunE via the UOW.
func runSwarmStatusProxied(ctx context.Context, issueRef string) error {
	uw, err := openSwarmProxiedUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}
	defer uw.Close(ctx)
	s := &swarmProxiedStorage{uw: uw}

	issue, err := s.GetIssue(ctx, issueRef)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return HandleErrorRespectJSON("issue '%s' not found", issueRef)
		}
		return HandleErrorRespectJSON("failed to get issue: %v", err)
	}

	var epic *types.Issue

	if issue.IssueType == "molecule" && issue.MolType == types.MolTypeSwarm {
		deps, derr := s.GetDependencyRecords(ctx, issue.ID)
		if derr != nil {
			return HandleErrorRespectJSON("failed to get swarm dependencies: %v", derr)
		}
		for _, dep := range deps {
			if dep.Type == types.DepRelatesTo {
				epic, err = s.GetIssue(ctx, dep.DependsOnID)
				if err != nil {
					return HandleErrorRespectJSON("failed to get linked epic: %v", err)
				}
				break
			}
		}
		if epic == nil {
			return HandleErrorRespectJSON("swarm molecule '%s' has no linked epic", issue.ID)
		}
	} else if issue.IssueType == types.TypeEpic || issue.IssueType == "molecule" {
		epic = issue
	} else {
		return HandleErrorRespectJSON("'%s' is not an epic or swarm molecule (type: %s)", issue.ID, issue.IssueType)
	}

	status, err := getSwarmStatus(ctx, s, epic)
	if err != nil {
		return HandleErrorRespectJSON("failed to get swarm status: %v", err)
	}

	if jsonOutput {
		return outputJSON(status)
	}

	renderSwarmStatus(status)
	return nil
}

// runSwarmCreateProxied mirrors swarmCreateCmd's RunE via the UOW: resolve the
// input, optionally auto-wrap a single issue in a new epic, analyze, create the
// swarm molecule, link it, and commit once.
func runSwarmCreateProxied(ctx context.Context, inputRef, coordinator string, force bool) error {
	uw, err := openSwarmProxiedUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}
	defer uw.Close(ctx)
	s := &swarmProxiedStorage{uw: uw}
	issueUC := uw.IssueUseCase()
	depUC := uw.DependencyUseCase()

	issue, err := s.GetIssue(ctx, inputRef)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return HandleErrorRespectJSON("issue '%s' not found", inputRef)
		}
		return HandleErrorRespectJSON("failed to get issue: %v", err)
	}

	var epicID string
	var epicTitle string

	if issue.IssueType == types.TypeEpic || issue.IssueType == "molecule" {
		epicID = issue.ID
		epicTitle = issue.Title
	} else {
		if !jsonOutput {
			fmt.Printf("Auto-wrapping single issue as epic...\n")
		}

		wrapperEpic := &types.Issue{
			Title:       fmt.Sprintf("Swarm Epic: %s", issue.Title),
			Description: fmt.Sprintf("Auto-generated epic to wrap single issue %s for swarm execution.", issue.ID),
			Status:      types.StatusOpen,
			Priority:    issue.Priority,
			IssueType:   types.TypeEpic,
			CreatedBy:   actor,
		}

		res, cerr := issueUC.CreateIssue(ctx, domain.CreateIssueParams{Issue: wrapperEpic}, actor)
		if cerr != nil {
			return HandleErrorRespectJSON("failed to create wrapper epic: %v", cerr)
		}
		wrapperEpic = res.Issue

		dep := &types.Dependency{
			IssueID:     issue.ID,
			DependsOnID: wrapperEpic.ID,
			Type:        types.DepParentChild,
			CreatedBy:   actor,
		}
		if derr := depUC.AddDependency(ctx, dep, actor); derr != nil {
			return HandleErrorRespectJSON("failed to link issue to epic: %v", derr)
		}

		epicID = wrapperEpic.ID
		epicTitle = wrapperEpic.Title

		if !jsonOutput {
			fmt.Printf("Created wrapper epic: %s\n", epicID)
		}
	}

	existingSwarm, err := findExistingSwarm(ctx, s, epicID)
	if err != nil {
		return HandleErrorRespectJSON("failed to check for existing swarm: %v", err)
	}
	if existingSwarm != nil && !force {
		if jsonOutput {
			if jerr := outputJSON(map[string]interface{}{
				"error":          "swarm already exists",
				"existing_id":    existingSwarm.ID,
				"existing_title": existingSwarm.Title,
			}); jerr != nil {
				return jerr
			}
			return SilentExit()
		}
		fmt.Printf("%s Swarm already exists: %s\n", ui.RenderWarn("⚠"), ui.RenderID(existingSwarm.ID))
		fmt.Printf("   Use --force to create another.\n")
		return SilentExit()
	}

	epic, err := s.GetIssue(ctx, epicID)
	if err != nil {
		return HandleErrorRespectJSON("failed to get epic: %v", err)
	}

	analysis, err := analyzeEpicForSwarm(ctx, s, epic)
	if err != nil {
		return HandleErrorRespectJSON("failed to analyze epic: %v", err)
	}

	if !analysis.Swarmable {
		if jsonOutput {
			if jerr := outputJSON(map[string]interface{}{
				"error":    "epic is not swarmable",
				"analysis": analysis,
			}); jerr != nil {
				return jerr
			}
			return SilentExit()
		}
		fmt.Printf("\n%s Epic is not swarmable. Fix errors first:\n", ui.RenderFail("✗"))
		for _, e := range analysis.Errors {
			fmt.Printf("  • %s\n", e)
		}
		return SilentExit()
	}

	swarmMol := &types.Issue{
		Title:       fmt.Sprintf("Swarm: %s", epicTitle),
		Description: fmt.Sprintf("Swarm molecule orchestrating epic %s.\n\nEpic: %s\nCoordinator: %s", epicID, epicID, coordinator),
		Status:      types.StatusOpen,
		Priority:    epic.Priority,
		IssueType:   "molecule",
		MolType:     types.MolTypeSwarm,
		Assignee:    coordinator,
		CreatedBy:   actor,
	}

	res, err := issueUC.CreateIssue(ctx, domain.CreateIssueParams{Issue: swarmMol}, actor)
	if err != nil {
		return HandleErrorRespectJSON("failed to create swarm molecule: %v", err)
	}
	swarmMol = res.Issue

	dep := &types.Dependency{
		IssueID:     swarmMol.ID,
		DependsOnID: epicID,
		Type:        types.DepRelatesTo,
		CreatedBy:   actor,
	}
	if err := depUC.AddDependency(ctx, dep, actor); err != nil {
		return HandleErrorRespectJSON("failed to link swarm to epic: %v", err)
	}

	commitMsg := fmt.Sprintf("bd: create swarm %s for epic %s", swarmMol.ID, epicID)
	if err := uw.Commit(ctx, commitMsg); err != nil && !isDoltNothingToCommit(err) {
		return HandleErrorRespectJSON("failed to commit: %v", err)
	}

	commandDidWrite.Store(true)

	if jsonOutput {
		return outputJSON(map[string]interface{}{
			"swarm_id":    swarmMol.ID,
			"epic_id":     epicID,
			"coordinator": coordinator,
			"analysis":    analysis,
		})
	}
	printSwarmCreateProxiedSummary(swarmMol.ID, epicID, epicTitle, coordinator, analysis)
	return nil
}

// printSwarmCreateProxiedSummary renders the human confirmation for a proxied
// 'bd swarm create'. epicTitle derives from a stored issue title (untrusted
// import origin) so it is routed through displayTitle to strip terminal-control
// escapes (beads-ry48z, 7n9y sink class). Pure/display-only: the --json path
// (handled by the caller) stays raw for round-trip fidelity.
func printSwarmCreateProxiedSummary(swarmID, epicID, epicTitle, coordinator string, analysis *SwarmAnalysis) {
	fmt.Printf("\n%s Created swarm molecule: %s\n", ui.RenderPass("✓"), ui.RenderID(swarmID))
	fmt.Printf("   Epic: %s (%s)\n", epicID, displayTitle(epicTitle))
	if coordinator != "" {
		fmt.Printf("   Coordinator: %s\n", coordinator)
	}
	fmt.Printf("   Total issues: %d\n", analysis.TotalIssues)
	fmt.Printf("   Max parallelism: %d\n", analysis.MaxParallelism)
	fmt.Printf("   Waves: %d\n", len(analysis.ReadyFronts))
}

// runSwarmListProxied mirrors swarmListCmd's RunE via the UOW.
func runSwarmListProxied(ctx context.Context) error {
	uw, err := openSwarmProxiedUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}
	defer uw.Close(ctx)
	s := &swarmProxiedStorage{uw: uw}

	swarmType := types.MolTypeSwarm
	filter := types.IssueFilter{
		MolType: &swarmType,
	}
	swarms, err := s.searchIssues(ctx, "", filter)
	if err != nil {
		return HandleErrorRespectJSON("failed to list swarms: %v", err)
	}

	if len(swarms) == 0 {
		if jsonOutput {
			return outputJSON(map[string]interface{}{"swarms": []interface{}{}})
		}
		fmt.Printf("No swarm molecules found.\n")
		return nil
	}

	var items []swarmListItem
	for _, swarm := range swarms {
		item := swarmListItem{
			ID:          swarm.ID,
			Title:       swarm.Title,
			Status:      string(swarm.Status),
			Coordinator: swarm.Assignee,
		}

		deps, derr := s.GetDependencyRecords(ctx, swarm.ID)
		if derr == nil {
			for _, dep := range deps {
				if dep.Type == types.DepRelatesTo {
					item.EpicID = dep.DependsOnID
					epic, eerr := s.GetIssue(ctx, dep.DependsOnID)
					if eerr == nil && epic != nil {
						item.EpicTitle = epic.Title
						status, serr := getSwarmStatus(ctx, s, epic)
						if serr == nil {
							item.Total = status.TotalIssues
							item.Completed = len(status.Completed)
							item.Active = status.ActiveCount
							item.Progress = status.Progress
						}
					}
					break
				}
			}
		}

		items = append(items, item)
	}

	if jsonOutput {
		return outputJSON(map[string]interface{}{"swarms": items})
	}

	renderSwarmList(items)
	return nil
}
