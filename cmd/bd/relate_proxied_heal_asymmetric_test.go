//go:build cgo

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/storage/uow"
	"github.com/steveyegge/beads/internal/types"
)

// TestRelateProxiedHealsAsymmetricLink covers the proxied-path twin of
// beads-ri535 (the direct-path asymmetric-heal fix). ri535 changed runRelate to
// gate the "already related, no change" no-op on relatesToLinkFullyBidirectional
// (BOTH directions) so `bd relate` on an ASYMMETRIC relates-to link (one
// direction present — from an imported one-sided row or legacy pre-oyy1 data)
// falls through to the idempotent 2-dep add and HEALS the missing reciprocal.
// But ri535 only touched relate.go — the proxied handler
// (runRelateProxiedServer) still gated its no-op on the EITHER-direction
// proxiedRelatesToLinkExists (beads-hwgq), so every hub-connected (proxied) crew
// still false-no-op'd an asymmetric link and never healed it.
//
// The fix adds proxiedRelatesToLinkFullyBidirectional and gates the no-op on it,
// mirroring ri535. This test drives runRelateProxiedServer against a seam-level
// fake UOW (the proxied integration harness is a real-dolt subprocess with no
// way to SEED a one-sided relates-to edge — every CLI path refuses it: dep add
// rejects relates_to (hf1c6), relate always writes both directions — so an
// asymmetric link is only reachable via imported/legacy data; the fake models
// exactly that state). The observable contract: on an asymmetric link the
// handler must NOT short-circuit "no change" — it must call AddDependencies with
// BOTH edges (the heal). MUTATION-VERIFY: revert the no-op guard to
// proxiedRelatesToLinkExists → the asymmetric case short-circuits, AddDependencies
// is never called, and this test FAILS.

// fakeRelateHealUOW embeds uow.UnitOfWork (unused methods panic if hit, which the
// test would surface) and returns fake Issue + Dependency use-cases.
type fakeRelateHealUOW struct {
	uow.UnitOfWork
	issue *fakeRelateHealIssueUC
	dep   *fakeRelateHealDepUC
}

func (u *fakeRelateHealUOW) IssueUseCase() domain.IssueUseCase           { return u.issue }
func (u *fakeRelateHealUOW) DependencyUseCase() domain.DependencyUseCase { return u.dep }
func (u *fakeRelateHealUOW) Commit(context.Context, string) error        { return nil }
func (u *fakeRelateHealUOW) Close(context.Context)                       {}

type fakeRelateHealProvider struct{ uw *fakeRelateHealUOW }

func (p *fakeRelateHealProvider) NewUOW(context.Context) (uow.UnitOfWork, error) {
	return p.uw, nil
}
func (p *fakeRelateHealProvider) Close(context.Context) error { return nil }

// fakeRelateHealIssueUC: both endpoints exist (so proxiedIssuesExist passes).
type fakeRelateHealIssueUC struct {
	domain.IssueUseCase
}

func (u *fakeRelateHealIssueUC) GetIssue(_ context.Context, id string) (*types.Issue, error) {
	return &types.Issue{ID: id, IssueType: types.TypeTask, Status: types.StatusOpen}, nil
}

// SearchIssues honors the exact-ID fast path that proxiedResolvePartialID
// (beads-mrz0u) now runs BEFORE the relate logic — utils.ResolvePartialIDVia
// first queries SearchIssues with an exact IDs filter. The handler resolves
// both args via this path, so the fake must model a store that resolves a
// full ID to itself; otherwise resolution falls through to GetConfig and this
// stub-free fake panics. Returning the requested ID leaves the downstream
// heal/no-op logic under test operating on the same canonical IDs as before.
func (u *fakeRelateHealIssueUC) SearchIssues(_ context.Context, _ string, filter types.IssueFilter) (domain.SearchPage, error) {
	var items []*types.Issue
	for _, id := range filter.IDs {
		items = append(items, &types.Issue{ID: id, IssueType: types.TypeTask, Status: types.StatusOpen})
	}
	return domain.SearchPage{Items: items}, nil
}

// fakeRelateHealDepUC returns an ASYMMETRIC records map (only id1->id2 present)
// and records the deps passed to AddDependencies so the test can assert the heal.
type fakeRelateHealDepUC struct {
	domain.DependencyUseCase
	records     map[string][]*types.Dependency
	addedDeps   []*types.Dependency
	addDepsCall int
}

func (u *fakeRelateHealDepUC) GetIssueDependencyRecords(_ context.Context, _ []string) (map[string][]*types.Dependency, error) {
	return u.records, nil
}

func (u *fakeRelateHealDepUC) AddDependencies(_ context.Context, deps []*types.Dependency, _ string, _ domain.BulkAddDepsOpts) (domain.BulkAddDepsResult, error) {
	u.addDepsCall++
	u.addedDeps = append(u.addedDeps, deps...)
	return domain.BulkAddDepsResult{Added: deps}, nil
}

func TestRelateProxiedHealsAsymmetricLink(t *testing.T) {
	const id1, id2 = "rh-1", "rh-2"

	// Seed an ASYMMETRIC link: only id1 -> id2 (no reciprocal), modelling an
	// imported one-sided relates-to row / legacy pre-oyy1 data.
	dep := &fakeRelateHealDepUC{
		records: map[string][]*types.Dependency{
			id1: {{IssueID: id1, DependsOnID: id2, Type: types.DepRelatesTo}},
			// id2 has NO reciprocal edge back to id1.
		},
	}
	uw := &fakeRelateHealUOW{
		issue: &fakeRelateHealIssueUC{},
		dep:   dep,
	}

	origProvider, origJSON, origActor := uowProvider, jsonOutput, actor
	t.Cleanup(func() { uowProvider, jsonOutput, actor = origProvider, origJSON, origActor })
	uowProvider = &fakeRelateHealProvider{uw: uw}
	jsonOutput = false
	actor = "test-actor"

	out := captureStdout(t, func() error {
		return runRelateProxiedServer(context.Background(), []string{id1, id2})
	})

	// It must NOT falsely no-op on the asymmetric link.
	if strings.Contains(out, "no change") {
		t.Errorf("proxied relate on an asymmetric link falsely reported 'no change' — did not heal (ri535 proxied twin):\n%s", out)
	}

	// The heal must have written the dependency edges (both directions).
	if dep.addDepsCall == 0 {
		t.Fatalf("AddDependencies was never called — the asymmetric link short-circuited instead of healing the missing reciprocal")
	}
	var hasReciprocal bool
	for _, d := range dep.addedDeps {
		if d.IssueID == id2 && d.DependsOnID == id1 && d.Type == types.DepRelatesTo {
			hasReciprocal = true
		}
	}
	if !hasReciprocal {
		t.Errorf("heal did not write the missing reciprocal edge %s -> %s; added deps: %+v", id2, id1, dep.addedDeps)
	}
}

// TestRelateProxiedFullyBidirectionalStillNoOps guards the other side: a FULLY
// bidirectional link must STILL short-circuit "no change" (the beads-hwgq/57nt
// honest no-op is preserved — the ri535-parity fix only changes the ASYMMETRIC
// case, not the fully-linked one).
func TestRelateProxiedFullyBidirectionalStillNoOps(t *testing.T) {
	const id1, id2 = "rb-1", "rb-2"

	dep := &fakeRelateHealDepUC{
		records: map[string][]*types.Dependency{
			id1: {{IssueID: id1, DependsOnID: id2, Type: types.DepRelatesTo}},
			id2: {{IssueID: id2, DependsOnID: id1, Type: types.DepRelatesTo}},
		},
	}
	uw := &fakeRelateHealUOW{issue: &fakeRelateHealIssueUC{}, dep: dep}

	origProvider, origJSON, origActor := uowProvider, jsonOutput, actor
	t.Cleanup(func() { uowProvider, jsonOutput, actor = origProvider, origJSON, origActor })
	uowProvider = &fakeRelateHealProvider{uw: uw}
	jsonOutput = false
	actor = "test-actor"

	out := captureStdout(t, func() error {
		return runRelateProxiedServer(context.Background(), []string{id1, id2})
	})

	if !strings.Contains(out, "Already related, no change") {
		t.Errorf("proxied relate on a fully-bidirectional link should report the honest no-op (hwgq/57nt), got:\n%s", out)
	}
	if dep.addDepsCall != 0 {
		t.Errorf("fully-bidirectional link should short-circuit before AddDependencies; got %d calls", dep.addDepsCall)
	}
}
