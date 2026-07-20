package main

import (
	"context"

	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
)

// beads-948qg: exportSource abstracts the read surface bd export needs so the
// command works both on the direct/embedded path (global `store`) AND on
// hub-connected (proxied-server) crew where `store` is nil and reads must route
// through a UOW. This is the aocj interface-ext disposition for the READ leg
// (export is a pure read, so it WORKS proxied rather than fail-loud — unlike the
// mutation members branch/merge-slot/vc/compact). Mirror of beads-mh3e (diff)
// and list_proxied_server: the proxied adapter delegates to the same domain
// use-cases the direct store methods call (which reuse the same issueops query
// helpers), so the emitted JSONL is byte-identical regardless of deployment.
type exportSource interface {
	// GetInfraTypes returns the configured infra types (agents/roles/messages).
	// A nil/empty result lets the caller fall back to domain.DefaultInfraTypes().
	GetInfraTypes(ctx context.Context) (map[string]bool, error)
	SearchIssues(ctx context.Context, query string, filter types.IssueFilter) ([]*types.Issue, error)
	GetLabelsForIssues(ctx context.Context, issueIDs []string) (map[string][]string, error)
	GetDependencyRecordsForIssues(ctx context.Context, issueIDs []string) (map[string][]*types.Dependency, error)
	GetCommentsForIssues(ctx context.Context, issueIDs []string) (map[string][]*types.Comment, error)
	GetCommentCounts(ctx context.Context, issueIDs []string) (map[string]int, error)
	GetDependencyCounts(ctx context.Context, issueIDs []string) (map[string]*types.DependencyCounts, error)
	GetAllConfig(ctx context.Context) (map[string]string, error)
}

// openExportSource picks the direct-store adapter or, in proxied-server mode,
// the UOW-backed adapter. The proxied UOW must be closed via closeExportSource
// (read-only: it never commits, so Close rolls back the empty read tx).
func openExportSource(ctx context.Context) (exportSource, error) {
	if usesProxiedServer() {
		if uowProvider == nil {
			FatalError("proxied-server UOW provider not initialized")
		}
		uw, err := uowProvider.NewUOW(ctx)
		if err != nil {
			return nil, HandleErrorRespectJSON("open unit of work: %v", err)
		}
		return &proxiedExportSource{uw: uw}, nil
	}
	return &directExportSource{}, nil
}

// closeExportSource rolls back the proxied read UOW. A no-op for the direct
// adapter (nothing to release; the global store outlives the command).
func closeExportSource(ctx context.Context, src exportSource) {
	if p, ok := src.(*proxiedExportSource); ok {
		p.uw.Close(ctx)
	}
}

// directExportSource reads from the global `store` (direct/embedded path). It
// preserves the pre-948qg behavior exactly, including the nil-store guard on
// GetInfraTypes (the only optional read: a nil store falls through to the
// default infra-type set rather than erroring).
type directExportSource struct{}

func (d *directExportSource) GetInfraTypes(ctx context.Context) (map[string]bool, error) {
	if store == nil {
		return nil, nil
	}
	return store.GetInfraTypes(ctx), nil
}

func (d *directExportSource) SearchIssues(ctx context.Context, query string, filter types.IssueFilter) ([]*types.Issue, error) {
	return store.SearchIssues(ctx, query, filter)
}

func (d *directExportSource) GetLabelsForIssues(ctx context.Context, issueIDs []string) (map[string][]string, error) {
	return store.GetLabelsForIssues(ctx, issueIDs)
}

func (d *directExportSource) GetDependencyRecordsForIssues(ctx context.Context, issueIDs []string) (map[string][]*types.Dependency, error) {
	return store.GetDependencyRecordsForIssues(ctx, issueIDs)
}

func (d *directExportSource) GetCommentsForIssues(ctx context.Context, issueIDs []string) (map[string][]*types.Comment, error) {
	return store.GetCommentsForIssues(ctx, issueIDs)
}

func (d *directExportSource) GetCommentCounts(ctx context.Context, issueIDs []string) (map[string]int, error) {
	return store.GetCommentCounts(ctx, issueIDs)
}

func (d *directExportSource) GetDependencyCounts(ctx context.Context, issueIDs []string) (map[string]*types.DependencyCounts, error) {
	return store.GetDependencyCounts(ctx, issueIDs)
}

func (d *directExportSource) GetAllConfig(ctx context.Context) (map[string]string, error) {
	return store.GetAllConfig(ctx)
}

// proxiedExportSource reads via a proxied UOW for hub-connected crew. Every
// method delegates to the domain use-case that backs the corresponding direct
// store method (same issueops helpers underneath), so the export is identical.
type proxiedExportSource struct {
	uw uow.UnitOfWork
}

func (p *proxiedExportSource) GetInfraTypes(ctx context.Context) (map[string]bool, error) {
	return p.uw.ConfigUseCase().GetInfraTypes(ctx)
}

func (p *proxiedExportSource) SearchIssues(ctx context.Context, query string, filter types.IssueFilter) ([]*types.Issue, error) {
	page, err := p.uw.IssueUseCase().SearchIssues(ctx, query, filter)
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (p *proxiedExportSource) GetLabelsForIssues(ctx context.Context, issueIDs []string) (map[string][]string, error) {
	return p.uw.LabelUseCase().GetLabelsForIssues(ctx, issueIDs)
}

func (p *proxiedExportSource) GetDependencyRecordsForIssues(ctx context.Context, issueIDs []string) (map[string][]*types.Dependency, error) {
	return p.uw.DependencyUseCase().GetIssueDependencyRecords(ctx, issueIDs)
}

func (p *proxiedExportSource) GetCommentsForIssues(ctx context.Context, issueIDs []string) (map[string][]*types.Comment, error) {
	return p.uw.CommentUseCase().GetCommentsForIssues(ctx, issueIDs)
}

func (p *proxiedExportSource) GetCommentCounts(ctx context.Context, issueIDs []string) (map[string]int, error) {
	return p.uw.CommentUseCase().GetCommentCounts(ctx, issueIDs)
}

func (p *proxiedExportSource) GetDependencyCounts(ctx context.Context, issueIDs []string) (map[string]*types.DependencyCounts, error) {
	return p.uw.DependencyUseCase().CountsByIssueIDs(ctx, issueIDs)
}

func (p *proxiedExportSource) GetAllConfig(ctx context.Context) (map[string]string, error) {
	return p.uw.ConfigUseCase().GetAllConfig(ctx)
}
