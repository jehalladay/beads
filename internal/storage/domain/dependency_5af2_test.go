package domain

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// fakeDepRepo is a hermetic DependencySQLRepository. Every method has an
// optional error hook so the wrapper error branches are reachable, and the
// slice-backed state lets the reparent/list happy paths exercise real data.
type fakeDepRepo struct {
	// canned returns
	hasCycle bool
	deleted  DepDeleteResult
	bulk     DepBulkResult
	wispBulk *DepBulkResult // when set, returned for UseWispsTable ListByIssueIDs calls
	meta     []*types.IssueWithDependencyMetadata
	iter     storage.Iter[types.IssueWithDependencyMetadata]
	countN   int64
	counts   map[string]*types.DependencyCounts
	blocking BlockingInfo
	isBlk    bool
	blockers []string
	cycles   [][]*types.Issue
	tree     []*types.TreeNode
	cyclePth string
	records  map[string][]*types.Dependency

	// error hooks
	hasCycleErr  error
	insertErr    error
	deleteErr    error
	listErr      error
	wispListErr  error // fails only the UseWispsTable ListByIssueIDs call
	metaErr      error
	iterErr      error
	countErr     error
	countsErr    error
	blockingErr  error
	isBlkErr     error
	cyclesErr    error
	treeErr      error
	cycleEdgeErr error
	recordsErr   error
	wispRecErr   error

	inserted []*types.Dependency
	deletes  [][2]string
}

func (f *fakeDepRepo) Insert(ctx context.Context, dep *types.Dependency, actor string, opts DepInsertOpts) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	f.inserted = append(f.inserted, dep)
	return nil
}

func (f *fakeDepRepo) Delete(ctx context.Context, issueID, dependsOnID, actor string, opts DepInsertOpts) (DepDeleteResult, error) {
	if f.deleteErr != nil {
		return DepDeleteResult{}, f.deleteErr
	}
	f.deletes = append(f.deletes, [2]string{issueID, dependsOnID})
	return f.deleted, nil
}

func (f *fakeDepRepo) HasCycle(ctx context.Context, issueID, dependsOnID string) (bool, error) {
	if f.hasCycleErr != nil {
		return false, f.hasCycleErr
	}
	return f.hasCycle, nil
}

func (f *fakeDepRepo) ListByIssueIDs(ctx context.Context, issueIDs []string, opts DepListOpts) (DepBulkResult, error) {
	if opts.UseWispsTable && f.wispListErr != nil {
		return DepBulkResult{}, f.wispListErr
	}
	if f.listErr != nil {
		return DepBulkResult{}, f.listErr
	}
	if opts.UseWispsTable && f.wispBulk != nil {
		return *f.wispBulk, nil
	}
	return f.bulk, nil
}

func (f *fakeDepRepo) ListWithIssueMetadata(ctx context.Context, sourceID string, opts DepListOpts) ([]*types.IssueWithDependencyMetadata, error) {
	if f.metaErr != nil {
		return nil, f.metaErr
	}
	return f.meta, nil
}

func (f *fakeDepRepo) IterWithIssueMetadata(ctx context.Context, sourceID string, opts DepListOpts) (storage.Iter[types.IssueWithDependencyMetadata], error) {
	if f.iterErr != nil {
		return nil, f.iterErr
	}
	return f.iter, nil
}

func (f *fakeDepRepo) CountByID(ctx context.Context, sourceID string, opts DepListOpts) (int64, error) {
	if f.countErr != nil {
		return 0, f.countErr
	}
	return f.countN, nil
}

func (f *fakeDepRepo) CountsByIssueIDs(ctx context.Context, issueIDs []string, opts DepCountsOpts) (map[string]*types.DependencyCounts, error) {
	if f.countsErr != nil {
		return nil, f.countsErr
	}
	return f.counts, nil
}

func (f *fakeDepRepo) GetBlockingInfo(ctx context.Context, issueIDs []string, opts DepListOpts) (BlockingInfo, error) {
	return f.blocking, f.blockingErr
}

func (f *fakeDepRepo) GetBlockingInfoAcrossIssuesAndWisps(ctx context.Context, issueIDs []string) (BlockingInfo, error) {
	if f.blockingErr != nil {
		return BlockingInfo{}, f.blockingErr
	}
	return f.blocking, nil
}

func (f *fakeDepRepo) IsBlocked(ctx context.Context, issueID string, opts DepListOpts) (bool, []string, error) {
	if f.isBlkErr != nil {
		return false, nil, f.isBlkErr
	}
	return f.isBlk, f.blockers, nil
}

func (f *fakeDepRepo) DeleteAllForIDs(ctx context.Context, ids []string, opts DepInsertOpts) (int, error) {
	return 0, nil
}

func (f *fakeDepRepo) CountAllForIDs(ctx context.Context, ids []string, opts DepCountsOpts) (int, error) {
	return 0, nil
}

func (f *fakeDepRepo) DetectCycles(ctx context.Context) ([][]*types.Issue, error) {
	if f.cyclesErr != nil {
		return nil, f.cyclesErr
	}
	return f.cycles, nil
}

func (f *fakeDepRepo) GetTree(ctx context.Context, rootID string, opts DepTreeOpts) ([]*types.TreeNode, error) {
	if f.treeErr != nil {
		return nil, f.treeErr
	}
	return f.tree, nil
}

func (f *fakeDepRepo) CycleThroughEdges(ctx context.Context, edges [][2]string) (string, error) {
	if f.cycleEdgeErr != nil {
		return "", f.cycleEdgeErr
	}
	return f.cyclePth, nil
}

func (f *fakeDepRepo) GetDependencyRecordsForIssues(ctx context.Context, issueIDs []string) (map[string][]*types.Dependency, error) {
	if f.recordsErr != nil {
		return nil, f.recordsErr
	}
	return f.records, nil
}

func (f *fakeDepRepo) GetWispDependencyRecordsForIDs(ctx context.Context, wispIDs []string) (map[string][]*types.Dependency, error) {
	if f.wispRecErr != nil {
		return nil, f.wispRecErr
	}
	return f.records, nil
}

var _ DependencySQLRepository = (*fakeDepRepo)(nil)

func TestDependencyUseCase_Add(t *testing.T) {
	ctx := context.Background()

	// nil dep and empty IDs are rejected before any repo call.
	uc := NewDependencyUseCase(&fakeDepRepo{})
	if err := uc.AddDependency(ctx, nil, "a"); err == nil {
		t.Error("nil dep accepted")
	}
	if err := uc.AddDependency(ctx, &types.Dependency{}, "a"); err == nil {
		t.Error("empty IDs accepted")
	}

	// blocking dep triggers a cycle check; cycle detected -> error.
	cyc := &fakeDepRepo{hasCycle: true}
	if err := NewDependencyUseCase(cyc).AddDependency(ctx,
		&types.Dependency{IssueID: "a", DependsOnID: "b", Type: types.DepBlocks}, "act"); err == nil {
		t.Error("cycle not rejected")
	}

	// cycle-check repo error is wrapped.
	ce := &fakeDepRepo{hasCycleErr: errors.New("cyc-boom")}
	if err := NewDependencyUseCase(ce).AddDependency(ctx,
		&types.Dependency{IssueID: "a", DependsOnID: "b", Type: types.DepBlocks}, "act"); !errors.Is(err, ce.hasCycleErr) {
		t.Errorf("err = %v, want wrapped cycle-check error", err)
	}

	// insert error is wrapped.
	ie := &fakeDepRepo{insertErr: errors.New("ins-boom")}
	if err := NewDependencyUseCase(ie).AddDependency(ctx,
		&types.Dependency{IssueID: "a", DependsOnID: "b", Type: types.DepRelated}, "act"); !errors.Is(err, ie.insertErr) {
		t.Errorf("err = %v, want wrapped insert error", err)
	}

	// happy path (non-blocking type skips cycle check).
	ok := &fakeDepRepo{}
	if err := NewDependencyUseCase(ok).AddDependency(ctx,
		&types.Dependency{IssueID: "a", DependsOnID: "b", Type: types.DepRelated}, "act"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	if len(ok.inserted) != 1 {
		t.Errorf("insert not called: %d", len(ok.inserted))
	}

	// wisp variant reaches add() via the useWisp path.
	okw := &fakeDepRepo{}
	if err := NewDependencyUseCase(okw).AddWispDependency(ctx,
		&types.Dependency{IssueID: "w1", DependsOnID: "w2", Type: types.DepBlocks}, "act"); err != nil {
		t.Fatalf("AddWispDependency: %v", err)
	}
}

func TestDependencyUseCase_Remove(t *testing.T) {
	ctx := context.Background()

	uc := NewDependencyUseCase(&fakeDepRepo{})
	if err := uc.RemoveDependency(ctx, "", "b", "act"); err == nil {
		t.Error("empty sourceID accepted")
	}

	de := &fakeDepRepo{deleteErr: errors.New("del-boom")}
	if err := NewDependencyUseCase(de).RemoveDependency(ctx, "a", "b", "act"); !errors.Is(err, de.deleteErr) {
		t.Errorf("err = %v, want wrapped delete error", err)
	}

	ok := &fakeDepRepo{}
	if err := NewDependencyUseCase(ok).RemoveDependency(ctx, "a", "b", "act"); err != nil {
		t.Fatalf("RemoveDependency: %v", err)
	}
	if len(ok.deletes) != 1 {
		t.Errorf("delete not called")
	}

	// wisp variant.
	if err := NewDependencyUseCase(&fakeDepRepo{}).RemoveWispDependency(ctx, "w1", "w2", "act"); err != nil {
		t.Fatalf("RemoveWispDependency: %v", err)
	}
}

func TestDependencyUseCase_Reparent(t *testing.T) {
	ctx := context.Background()

	uc := NewDependencyUseCase(&fakeDepRepo{})
	if err := uc.Reparent(ctx, "", "p", "act"); err == nil {
		t.Error("empty childID accepted")
	}
	if err := uc.Reparent(ctx, "c", "c", "act"); err == nil {
		t.Error("self-parent accepted")
	}

	// list-current-parent error is wrapped.
	le := &fakeDepRepo{listErr: errors.New("list-boom")}
	if err := NewDependencyUseCase(le).Reparent(ctx, "c", "p", "act"); !errors.Is(err, le.listErr) {
		t.Errorf("err = %v, want wrapped list error", err)
	}

	// old parent already equals new parent -> no-op (short-circuit).
	same := &fakeDepRepo{bulk: DepBulkResult{Outgoing: map[string][]*types.Dependency{
		"c": {{IssueID: "c", DependsOnID: "p", Type: types.DepParentChild}},
	}}}
	if err := NewDependencyUseCase(same).Reparent(ctx, "c", "p", "act"); err != nil {
		t.Fatalf("Reparent no-op: %v", err)
	}
	if len(same.deletes) != 0 || len(same.inserted) != 0 {
		t.Error("no-op reparent touched repo")
	}

	// existing old parent gets deleted, new parent inserted (full path).
	full := &fakeDepRepo{bulk: DepBulkResult{Outgoing: map[string][]*types.Dependency{
		"c": {{IssueID: "c", DependsOnID: "oldp", Type: types.DepParentChild}},
	}}}
	if err := NewDependencyUseCase(full).Reparent(ctx, "c", "newp", "act"); err != nil {
		t.Fatalf("Reparent full: %v", err)
	}
	if len(full.deletes) != 1 || full.deletes[0] != [2]string{"c", "oldp"} {
		t.Errorf("old parent not deleted: %v", full.deletes)
	}
	if len(full.inserted) != 1 || full.inserted[0].DependsOnID != "newp" {
		t.Errorf("new parent not inserted: %v", full.inserted)
	}

	// delete-old-parent error is wrapped.
	de := &fakeDepRepo{
		bulk:      DepBulkResult{Outgoing: map[string][]*types.Dependency{"c": {{IssueID: "c", DependsOnID: "oldp", Type: types.DepParentChild}}}},
		deleteErr: errors.New("del-boom"),
	}
	if err := NewDependencyUseCase(de).Reparent(ctx, "c", "newp", "act"); !errors.Is(err, de.deleteErr) {
		t.Errorf("err = %v, want wrapped delete error", err)
	}

	// insert-new-parent error is wrapped (no old parent to delete).
	ie := &fakeDepRepo{insertErr: errors.New("ins-boom")}
	if err := NewDependencyUseCase(ie).Reparent(ctx, "c", "newp", "act"); !errors.Is(err, ie.insertErr) {
		t.Errorf("err = %v, want wrapped insert error", err)
	}

	// newParentID empty -> orphan (delete old, skip insert).
	orphan := &fakeDepRepo{bulk: DepBulkResult{Outgoing: map[string][]*types.Dependency{
		"c": {{IssueID: "c", DependsOnID: "oldp", Type: types.DepParentChild}},
	}}}
	if err := NewDependencyUseCase(orphan).Reparent(ctx, "c", "", "act"); err != nil {
		t.Fatalf("Reparent orphan: %v", err)
	}
	if len(orphan.deletes) != 1 || len(orphan.inserted) != 0 {
		t.Errorf("orphan reparent wrong: del=%v ins=%v", orphan.deletes, orphan.inserted)
	}

	// wisp variant reaches reparent() via useWisp.
	if err := NewDependencyUseCase(&fakeDepRepo{}).ReparentWisp(ctx, "w", "wp", "act"); err != nil {
		t.Fatalf("ReparentWisp: %v", err)
	}
}

func TestDependencyUseCase_ListAndCount(t *testing.T) {
	ctx := context.Background()
	filter := DepListFilter{Types: []types.DependencyType{types.DepBlocks}, Direction: DepDirectionOut}

	// empty ids -> empty result, no repo call.
	empty := &fakeDepRepo{listErr: errors.New("should-not-be-called")}
	res, err := NewDependencyUseCase(empty).ListByIssueIDs(ctx, nil, filter)
	if err != nil {
		t.Fatalf("ListByIssueIDs empty: %v", err)
	}
	if res.Outgoing == nil || res.Incoming == nil {
		t.Error("empty list result maps not initialized")
	}

	// list error wrapped.
	le := &fakeDepRepo{listErr: errors.New("list-boom")}
	if _, err := NewDependencyUseCase(le).ListByIssueIDs(ctx, []string{"a"}, filter); !errors.Is(err, le.listErr) {
		t.Errorf("err = %v, want wrapped list error", err)
	}

	// happy list.
	ok := &fakeDepRepo{bulk: DepBulkResult{Outgoing: map[string][]*types.Dependency{"a": {}}}}
	if _, err := NewDependencyUseCase(ok).ListByIssueIDs(ctx, []string{"a"}, filter); err != nil {
		t.Fatalf("ListByIssueIDs: %v", err)
	}
	// wisp list variant.
	if _, err := NewDependencyUseCase(ok).ListByWispIDs(ctx, []string{"w"}, filter); err != nil {
		t.Fatalf("ListByWispIDs: %v", err)
	}

	// ListWithIssueMetadata: empty id rejected, error wrapped, happy.
	if _, err := NewDependencyUseCase(&fakeDepRepo{}).ListWithIssueMetadata(ctx, "", filter); err == nil {
		t.Error("empty sourceID accepted")
	}
	me := &fakeDepRepo{metaErr: errors.New("meta-boom")}
	if _, err := NewDependencyUseCase(me).ListWithIssueMetadata(ctx, "a", filter); !errors.Is(err, me.metaErr) {
		t.Errorf("err = %v, want wrapped meta error", err)
	}
	if _, err := NewDependencyUseCase(&fakeDepRepo{}).ListWithIssueMetadata(ctx, "a", filter); err != nil {
		t.Fatalf("ListWithIssueMetadata: %v", err)
	}
	if _, err := NewDependencyUseCase(&fakeDepRepo{}).ListWispWithIssueMetadata(ctx, "w", filter); err != nil {
		t.Fatalf("ListWispWithIssueMetadata: %v", err)
	}

	// Iter variants: empty id, error, happy.
	if _, err := NewDependencyUseCase(&fakeDepRepo{}).IterWithIssueMetadata(ctx, "", filter); err == nil {
		t.Error("iter empty sourceID accepted")
	}
	ige := &fakeDepRepo{iterErr: errors.New("iter-boom")}
	if _, err := NewDependencyUseCase(ige).IterWithIssueMetadata(ctx, "a", filter); !errors.Is(err, ige.iterErr) {
		t.Errorf("err = %v, want wrapped iter error", err)
	}
	it := &fakeDepRepo{iter: storage.NewSliceIter([]*types.IssueWithDependencyMetadata{})}
	if _, err := NewDependencyUseCase(it).IterWithIssueMetadata(ctx, "a", filter); err != nil {
		t.Fatalf("IterWithIssueMetadata: %v", err)
	}
	if _, err := NewDependencyUseCase(it).IterWispWithIssueMetadata(ctx, "w", filter); err != nil {
		t.Fatalf("IterWispWithIssueMetadata: %v", err)
	}

	// CountByID: empty id, error, happy + wisp.
	if _, err := NewDependencyUseCase(&fakeDepRepo{}).CountByIssueID(ctx, "", filter); err == nil {
		t.Error("count empty sourceID accepted")
	}
	ceb := &fakeDepRepo{countErr: errors.New("count-boom")}
	if _, err := NewDependencyUseCase(ceb).CountByIssueID(ctx, "a", filter); !errors.Is(err, ceb.countErr) {
		t.Errorf("err = %v, want wrapped count error", err)
	}
	cn := &fakeDepRepo{countN: 7}
	if n, err := NewDependencyUseCase(cn).CountByIssueID(ctx, "a", filter); err != nil || n != 7 {
		t.Fatalf("CountByIssueID = %d, %v", n, err)
	}
	if _, err := NewDependencyUseCase(cn).CountByWispID(ctx, "w", filter); err != nil {
		t.Fatalf("CountByWispID: %v", err)
	}
}

func TestDependencyUseCase_Counts(t *testing.T) {
	ctx := context.Background()

	// empty -> empty map, no call.
	if m, err := NewDependencyUseCase(&fakeDepRepo{}).CountsByIssueIDs(ctx, nil); err != nil || len(m) != 0 {
		t.Fatalf("CountsByIssueIDs empty = %v, %v", m, err)
	}
	ce := &fakeDepRepo{countsErr: errors.New("counts-boom")}
	if _, err := NewDependencyUseCase(ce).CountsByIssueIDs(ctx, []string{"a"}); !errors.Is(err, ce.countsErr) {
		t.Errorf("err = %v, want wrapped counts error", err)
	}
	ok := &fakeDepRepo{counts: map[string]*types.DependencyCounts{"a": {DependencyCount: 1}}}
	if m, err := NewDependencyUseCase(ok).CountsByIssueIDs(ctx, []string{"a"}); err != nil || len(m) != 1 {
		t.Fatalf("CountsByIssueIDs = %v, %v", m, err)
	}
	if _, err := NewDependencyUseCase(ok).CountsByWispIDs(ctx, []string{"w"}); err != nil {
		t.Fatalf("CountsByWispIDs: %v", err)
	}
}

func TestDependencyUseCase_BlockingAndBlocked(t *testing.T) {
	ctx := context.Background()

	// GetBlockingInfo empty -> initialized maps.
	if bi, err := NewDependencyUseCase(&fakeDepRepo{}).GetBlockingInfo(ctx, nil); err != nil ||
		bi.BlockedBy == nil || bi.Blocks == nil || bi.Parent == nil {
		t.Fatalf("GetBlockingInfo empty = %+v, %v", bi, err)
	}
	be := &fakeDepRepo{blockingErr: errors.New("blk-boom")}
	if _, err := NewDependencyUseCase(be).GetBlockingInfo(ctx, []string{"a"}); !errors.Is(err, be.blockingErr) {
		t.Errorf("err = %v, want wrapped blocking error", err)
	}
	if _, err := NewDependencyUseCase(&fakeDepRepo{}).GetBlockingInfo(ctx, []string{"a"}); err != nil {
		t.Fatalf("GetBlockingInfo: %v", err)
	}

	// IsBlocked empty id, error, happy + wisp.
	if _, _, err := NewDependencyUseCase(&fakeDepRepo{}).IsBlocked(ctx, ""); err == nil {
		t.Error("IsBlocked empty id accepted")
	}
	ie := &fakeDepRepo{isBlkErr: errors.New("isblk-boom")}
	if _, _, err := NewDependencyUseCase(ie).IsBlocked(ctx, "a"); !errors.Is(err, ie.isBlkErr) {
		t.Errorf("err = %v, want wrapped isblocked error", err)
	}
	ok := &fakeDepRepo{isBlk: true, blockers: []string{"x"}}
	if b, who, err := NewDependencyUseCase(ok).IsBlocked(ctx, "a"); err != nil || !b || len(who) != 1 {
		t.Fatalf("IsBlocked = %v %v %v", b, who, err)
	}
	if _, _, err := NewDependencyUseCase(ok).IsWispBlocked(ctx, "w"); err != nil {
		t.Fatalf("IsWispBlocked: %v", err)
	}
}

func TestDependencyUseCase_GetForIssueIDs(t *testing.T) {
	ctx := context.Background()

	// empty ids -> empty map.
	if m, err := NewDependencyUseCase(&fakeDepRepo{}).GetForIssueIDs(ctx, nil); err != nil || len(m) != 0 {
		t.Fatalf("GetForIssueIDs empty = %v, %v", m, err)
	}

	// first (issue) list error wrapped.
	le := &fakeDepRepo{listErr: errors.New("list-boom")}
	if _, err := NewDependencyUseCase(le).GetForIssueIDs(ctx, []string{"a"}); !errors.Is(err, le.listErr) {
		t.Errorf("err = %v, want wrapped list error", err)
	}

	// wisp-list error that is NOT table-not-exist is surfaced.
	we := &fakeDepRepo{
		bulk:        DepBulkResult{Outgoing: map[string][]*types.Dependency{"a": {}}},
		wispListErr: errors.New("wisp-boom"),
	}
	if _, err := NewDependencyUseCase(we).GetForIssueIDs(ctx, []string{"a"}); !errors.Is(err, we.wispListErr) {
		t.Errorf("err = %v, want wrapped wisp-list error", err)
	}

	// wisp-list table-not-exist error is TOLERATED (issue deps still returned).
	tne := &fakeDepRepo{
		bulk:        DepBulkResult{Outgoing: map[string][]*types.Dependency{"a": {{IssueID: "a", DependsOnID: "b", Type: types.DepBlocks}}}},
		wispListErr: errors.New("Table 'beads.wisp_dependencies' doesn't exist"),
	}
	if m, err := NewDependencyUseCase(tne).GetForIssueIDs(ctx, []string{"a"}); err != nil || len(m["a"]) != 1 {
		t.Fatalf("GetForIssueIDs table-not-exist = %v, %v", m, err)
	}

	// issue list returns a nil Outgoing map -> the make() branch runs, wisp merges in.
	nilOut := &fakeDepRepo{bulk: DepBulkResult{Outgoing: nil, Incoming: nil}}
	nilOut.wispBulk = &DepBulkResult{Outgoing: map[string][]*types.Dependency{
		"a": {{IssueID: "a", DependsOnID: "b", Type: types.DepBlocks}},
	}}
	if m, err := NewDependencyUseCase(nilOut).GetForIssueIDs(ctx, []string{"a"}); err != nil || len(m["a"]) != 1 {
		t.Fatalf("GetForIssueIDs nil-Outgoing = %v, %v", m, err)
	}

	// happy path merges issue + wisp outgoing.
	ok := &fakeDepRepo{bulk: DepBulkResult{Outgoing: map[string][]*types.Dependency{
		"a": {{IssueID: "a", DependsOnID: "b", Type: types.DepBlocks}},
	}}}
	m, err := NewDependencyUseCase(ok).GetForIssueIDs(ctx, []string{"a"})
	if err != nil {
		t.Fatalf("GetForIssueIDs: %v", err)
	}
	// issue + wisp queries both return the same fake bulk -> merged 2 entries for "a".
	if len(m["a"]) != 2 {
		t.Errorf("merged deps = %d, want 2", len(m["a"]))
	}
}

func TestDependencyUseCase_TreeCyclesRecords(t *testing.T) {
	ctx := context.Background()

	// DetectCycles error + happy.
	de := &fakeDepRepo{cyclesErr: errors.New("cyc-boom")}
	if _, err := NewDependencyUseCase(de).DetectCycles(ctx); !errors.Is(err, de.cyclesErr) {
		t.Errorf("err = %v, want wrapped cycles error", err)
	}
	if _, err := NewDependencyUseCase(&fakeDepRepo{}).DetectCycles(ctx); err != nil {
		t.Fatalf("DetectCycles: %v", err)
	}

	// GetDependencyTree empty root, error, happy.
	if _, err := NewDependencyUseCase(&fakeDepRepo{}).GetDependencyTree(ctx, "", DepTreeOpts{}); err == nil {
		t.Error("empty rootID accepted")
	}
	te := &fakeDepRepo{treeErr: errors.New("tree-boom")}
	if _, err := NewDependencyUseCase(te).GetDependencyTree(ctx, "r", DepTreeOpts{}); !errors.Is(err, te.treeErr) {
		t.Errorf("err = %v, want wrapped tree error", err)
	}
	if _, err := NewDependencyUseCase(&fakeDepRepo{}).GetDependencyTree(ctx, "r", DepTreeOpts{}); err != nil {
		t.Fatalf("GetDependencyTree: %v", err)
	}

	// GetIssueDependencyRecords empty, error, happy.
	if m, err := NewDependencyUseCase(&fakeDepRepo{}).GetIssueDependencyRecords(ctx, nil); err != nil || len(m) != 0 {
		t.Fatalf("GetIssueDependencyRecords empty = %v, %v", m, err)
	}
	re := &fakeDepRepo{recordsErr: errors.New("rec-boom")}
	if _, err := NewDependencyUseCase(re).GetIssueDependencyRecords(ctx, []string{"a"}); !errors.Is(err, re.recordsErr) {
		t.Errorf("err = %v, want wrapped records error", err)
	}
	if _, err := NewDependencyUseCase(&fakeDepRepo{}).GetIssueDependencyRecords(ctx, []string{"a"}); err != nil {
		t.Fatalf("GetIssueDependencyRecords: %v", err)
	}

	// GetWispDependencyRecords empty, error, happy.
	if m, err := NewDependencyUseCase(&fakeDepRepo{}).GetWispDependencyRecords(ctx, nil); err != nil || len(m) != 0 {
		t.Fatalf("GetWispDependencyRecords empty = %v, %v", m, err)
	}
	we := &fakeDepRepo{wispRecErr: errors.New("wrec-boom")}
	if _, err := NewDependencyUseCase(we).GetWispDependencyRecords(ctx, []string{"w"}); !errors.Is(err, we.wispRecErr) {
		t.Errorf("err = %v, want wrapped wisp-records error", err)
	}
	if _, err := NewDependencyUseCase(&fakeDepRepo{}).GetWispDependencyRecords(ctx, []string{"w"}); err != nil {
		t.Fatalf("GetWispDependencyRecords: %v", err)
	}
}

func TestDependencyUseCase_AddBulk(t *testing.T) {
	ctx := context.Background()
	blk := func(a, b string) *types.Dependency {
		return &types.Dependency{IssueID: a, DependsOnID: b, Type: types.DepBlocks}
	}

	// empty -> empty result, no call.
	if r, err := NewDependencyUseCase(&fakeDepRepo{}).AddDependencies(ctx, nil, "act", BulkAddDepsOpts{}); err != nil || len(r.Added) != 0 {
		t.Fatalf("AddDependencies empty = %v, %v", r, err)
	}

	// nil element rejected.
	if _, err := NewDependencyUseCase(&fakeDepRepo{}).AddDependencies(ctx, []*types.Dependency{nil}, "act", BulkAddDepsOpts{}); err == nil {
		t.Error("nil element accepted")
	}
	// empty IDs rejected.
	if _, err := NewDependencyUseCase(&fakeDepRepo{}).AddDependencies(ctx, []*types.Dependency{{Type: types.DepBlocks}}, "act", BulkAddDepsOpts{}); err == nil {
		t.Error("empty-ID element accepted")
	}

	// per-edge cycle check: repo error wrapped.
	ce := &fakeDepRepo{hasCycleErr: errors.New("cyc-boom")}
	if _, err := NewDependencyUseCase(ce).AddDependencies(ctx, []*types.Dependency{blk("a", "b")}, "act", BulkAddDepsOpts{}); !errors.Is(err, ce.hasCycleErr) {
		t.Errorf("err = %v, want wrapped cycle error", err)
	}
	// per-edge cycle detected.
	cd := &fakeDepRepo{hasCycle: true}
	if _, err := NewDependencyUseCase(cd).AddDependencies(ctx, []*types.Dependency{blk("a", "b")}, "act", BulkAddDepsOpts{}); err == nil {
		t.Error("per-edge cycle not rejected")
	}
	// insert error wrapped.
	ie := &fakeDepRepo{insertErr: errors.New("ins-boom")}
	if _, err := NewDependencyUseCase(ie).AddDependencies(ctx, []*types.Dependency{blk("a", "b")}, "act", BulkAddDepsOpts{}); !errors.Is(err, ie.insertErr) {
		t.Errorf("err = %v, want wrapped insert error", err)
	}

	// happy per-edge path.
	ok := &fakeDepRepo{}
	if r, err := NewDependencyUseCase(ok).AddDependencies(ctx, []*types.Dependency{blk("a", "b")}, "act", BulkAddDepsOpts{}); err != nil || len(r.Added) != 1 {
		t.Fatalf("AddDependencies happy = %v, %v", r, err)
	}

	// SkipPerEdgeCycleCheck: final CycleThroughEdges runs.
	// clean final check.
	skipOK := &fakeDepRepo{}
	if _, err := NewDependencyUseCase(skipOK).AddDependencies(ctx, []*types.Dependency{blk("a", "b")}, "act", BulkAddDepsOpts{SkipPerEdgeCycleCheck: true}); err != nil {
		t.Fatalf("AddDependencies skip clean: %v", err)
	}
	// final check repo error wrapped.
	skipErr := &fakeDepRepo{cycleEdgeErr: errors.New("edge-boom")}
	if _, err := NewDependencyUseCase(skipErr).AddDependencies(ctx, []*types.Dependency{blk("a", "b")}, "act", BulkAddDepsOpts{SkipPerEdgeCycleCheck: true}); !errors.Is(err, skipErr.cycleEdgeErr) {
		t.Errorf("err = %v, want wrapped edge-cycle error", err)
	}
	// final check reports a cycle path.
	skipCyc := &fakeDepRepo{cyclePth: "a -> b -> a"}
	_, err := NewDependencyUseCase(skipCyc).AddDependencies(ctx, []*types.Dependency{blk("a", "b")}, "act", BulkAddDepsOpts{SkipPerEdgeCycleCheck: true})
	if err == nil || !strings.Contains(err.Error(), "cycle would be created") {
		t.Errorf("err = %v, want cycle-would-be-created", err)
	}
	// skip with only non-blocking edges -> no final check, no error.
	skipRel := &fakeDepRepo{cycleEdgeErr: errors.New("should-not-run")}
	if _, err := NewDependencyUseCase(skipRel).AddDependencies(ctx,
		[]*types.Dependency{{IssueID: "a", DependsOnID: "b", Type: types.DepRelated}}, "act",
		BulkAddDepsOpts{SkipPerEdgeCycleCheck: true}); err != nil {
		t.Fatalf("AddDependencies skip non-blocking: %v", err)
	}

	// wisp bulk variant reaches addBulk via useWisp.
	if _, err := NewDependencyUseCase(&fakeDepRepo{}).AddWispDependencies(ctx, []*types.Dependency{blk("w1", "w2")}, "act", BulkAddDepsOpts{}); err != nil {
		t.Fatalf("AddWispDependencies: %v", err)
	}
}
