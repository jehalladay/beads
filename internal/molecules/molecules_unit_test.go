package molecules

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// fakeStore is a minimal storage.DoltStorage that only implements the two
// methods LoadAll/loadMolecules actually use (GetIssue and
// CreateIssuesWithFullOptions). It embeds the interface so it satisfies the
// full method set without a real Dolt server — every other method is nil and
// would panic if called, which is the point: these tests must not touch them.
type fakeStore struct {
	storage.DoltStorage

	// existing holds IDs that GetIssue should report as already present.
	existing map[string]*types.Issue
	// getErr, when set for an ID, is returned by GetIssue for that ID.
	getErr map[string]error
	// createErr, when non-nil, is returned by CreateIssuesWithFullOptions.
	createErr error

	// created accumulates every issue passed to CreateIssuesWithFullOptions.
	created []*types.Issue
	// createCalls counts how many times CreateIssuesWithFullOptions ran.
	createCalls int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		existing: make(map[string]*types.Issue),
		getErr:   make(map[string]error),
	}
}

func (f *fakeStore) GetIssue(_ context.Context, id string) (*types.Issue, error) {
	if err, ok := f.getErr[id]; ok {
		return nil, err
	}
	if iss, ok := f.existing[id]; ok {
		return iss, nil
	}
	// Not found: mirror the real store's contract of (nil, error) so the
	// caller's "err == nil && existing != nil" guard treats it as absent.
	return nil, errors.New("not found")
}

func (f *fakeStore) CreateIssuesWithFullOptions(_ context.Context, issues []*types.Issue, _ string, _ storage.BatchCreateOptions) error {
	f.createCalls++
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, issues...)
	return nil
}

// writeJSONL writes a molecules.jsonl file at dir/.beads/molecules.jsonl and
// returns the .beads directory path (the beadsDir LoadAll expects).
func writeProjectMolecules(t *testing.T, root, content string) string {
	t.Helper()
	beadsDir := filepath.Join(root, ".beads")
	if err := os.MkdirAll(beadsDir, 0750); err != nil {
		t.Fatalf("mkdir beads dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, MoleculeFileName), []byte(content), 0600); err != nil {
		t.Fatalf("write molecules file: %v", err)
	}
	return beadsDir
}

// --- getTownMoleculesPath ---

func TestGetTownMoleculesPath_NoGTRoot(t *testing.T) {
	t.Setenv("GT_ROOT", "")
	if got := getTownMoleculesPath(); got != "" {
		t.Errorf("getTownMoleculesPath() = %q, want \"\" when GT_ROOT unset", got)
	}
}

func TestGetTownMoleculesPath_GTRootSetButNoFile(t *testing.T) {
	t.Setenv("GT_ROOT", t.TempDir()) // dir exists, but no .beads/molecules.jsonl
	if got := getTownMoleculesPath(); got != "" {
		t.Errorf("getTownMoleculesPath() = %q, want \"\" when file absent", got)
	}
}

func TestGetTownMoleculesPath_FileExists(t *testing.T) {
	root := t.TempDir()
	writeProjectMolecules(t, root, `{"id":"mol-t","title":"T","issue_type":"molecule","status":"open"}`)
	t.Setenv("GT_ROOT", root)

	want := filepath.Join(root, ".beads", MoleculeFileName)
	if got := getTownMoleculesPath(); got != want {
		t.Errorf("getTownMoleculesPath() = %q, want %q", got, want)
	}
}

// --- getUserMoleculesPath ---

func TestGetUserMoleculesPath_NoFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // home exists, but no ~/.beads/molecules.jsonl
	if got := getUserMoleculesPath(); got != "" {
		t.Errorf("getUserMoleculesPath() = %q, want \"\" when file absent", got)
	}
}

func TestGetUserMoleculesPath_FileExists(t *testing.T) {
	home := t.TempDir()
	writeProjectMolecules(t, home, `{"id":"mol-u","title":"U","issue_type":"molecule","status":"open"}`)
	t.Setenv("HOME", home)

	want := filepath.Join(home, ".beads", MoleculeFileName)
	if got := getUserMoleculesPath(); got != want {
		t.Errorf("getUserMoleculesPath() = %q, want %q", got, want)
	}
}

// --- loadMolecules (via a fake store, no Dolt) ---

func TestLoadMolecules_MarksTemplatesAndCreates(t *testing.T) {
	store := newFakeStore()
	l := NewLoader(store)

	mols := []*types.Issue{
		{ID: "mol-a", Title: "A", Status: types.StatusOpen},
		{ID: "mol-b", Title: "B", Status: types.StatusOpen},
	}
	n, err := l.loadMolecules(context.Background(), mols)
	if err != nil {
		t.Fatalf("loadMolecules: %v", err)
	}
	if n != 2 {
		t.Errorf("loaded = %d, want 2", n)
	}
	if store.createCalls != 1 {
		t.Errorf("createCalls = %d, want 1 (single batch)", store.createCalls)
	}
	for _, m := range store.created {
		if !m.IsTemplate {
			t.Errorf("molecule %s not marked IsTemplate", m.ID)
		}
	}
}

func TestLoadMolecules_SkipsExisting(t *testing.T) {
	store := newFakeStore()
	store.existing["mol-dup"] = &types.Issue{ID: "mol-dup", Title: "Existing"}
	l := NewLoader(store)

	mols := []*types.Issue{
		{ID: "mol-dup", Title: "Dup", Status: types.StatusOpen},
		{ID: "mol-fresh", Title: "Fresh", Status: types.StatusOpen},
	}
	n, err := l.loadMolecules(context.Background(), mols)
	if err != nil {
		t.Fatalf("loadMolecules: %v", err)
	}
	if n != 1 {
		t.Errorf("loaded = %d, want 1 (existing skipped)", n)
	}
	if len(store.created) != 1 || store.created[0].ID != "mol-fresh" {
		t.Errorf("created = %+v, want only mol-fresh", store.created)
	}
}

func TestLoadMolecules_AllExisting_NoCreateCall(t *testing.T) {
	store := newFakeStore()
	store.existing["mol-x"] = &types.Issue{ID: "mol-x"}
	l := NewLoader(store)

	n, err := l.loadMolecules(context.Background(), []*types.Issue{{ID: "mol-x", Title: "X"}})
	if err != nil {
		t.Fatalf("loadMolecules: %v", err)
	}
	if n != 0 {
		t.Errorf("loaded = %d, want 0", n)
	}
	if store.createCalls != 0 {
		t.Errorf("createCalls = %d, want 0 (nothing new to create)", store.createCalls)
	}
}

func TestLoadMolecules_CreateError_Propagates(t *testing.T) {
	store := newFakeStore()
	store.createErr = errors.New("boom")
	l := NewLoader(store)

	_, err := l.loadMolecules(context.Background(), []*types.Issue{{ID: "mol-e", Title: "E"}})
	if err == nil {
		t.Fatal("loadMolecules = nil error, want wrapped create error")
	}
}

// --- LoadAll hierarchical branches (no Dolt) ---

func TestLoadAll_ProjectOnly(t *testing.T) {
	// Isolate from ambient town/user catalogs so only the project path loads.
	t.Setenv("GT_ROOT", "")
	t.Setenv("HOME", t.TempDir())

	store := newFakeStore()
	l := NewLoader(store)

	beadsDir := writeProjectMolecules(t, t.TempDir(),
		`{"id":"mol-p1","title":"P1","issue_type":"molecule","status":"open"}
{"id":"mol-p2","title":"P2","issue_type":"molecule","status":"open"}`)

	result, err := l.LoadAll(context.Background(), beadsDir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if result.Loaded != 2 {
		t.Errorf("Loaded = %d, want 2", result.Loaded)
	}
	if len(result.Sources) != 1 {
		t.Errorf("Sources = %v, want a single project source", result.Sources)
	}
}

func TestLoadAll_TownAndProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no user catalog

	// Town catalog via GT_ROOT.
	townRoot := t.TempDir()
	writeProjectMolecules(t, townRoot, `{"id":"mol-town","title":"Town","issue_type":"molecule","status":"open"}`)
	t.Setenv("GT_ROOT", townRoot)

	// Distinct project catalog.
	beadsDir := writeProjectMolecules(t, t.TempDir(), `{"id":"mol-proj","title":"Proj","issue_type":"molecule","status":"open"}`)

	store := newFakeStore()
	l := NewLoader(store)

	result, err := l.LoadAll(context.Background(), beadsDir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if result.Loaded != 2 {
		t.Errorf("Loaded = %d, want 2 (town + project)", result.Loaded)
	}
	// Town source should be listed before the project source.
	if len(result.Sources) != 2 {
		t.Fatalf("Sources = %v, want 2", result.Sources)
	}
	wantTown := filepath.Join(townRoot, ".beads", MoleculeFileName)
	if result.Sources[0] != wantTown {
		t.Errorf("Sources[0] = %q, want town path %q", result.Sources[0], wantTown)
	}
}

func TestLoadAll_UserCatalog(t *testing.T) {
	t.Setenv("GT_ROOT", "") // no town catalog

	home := t.TempDir()
	writeProjectMolecules(t, home, `{"id":"mol-user","title":"User","issue_type":"molecule","status":"open"}`)
	t.Setenv("HOME", home)

	store := newFakeStore()
	l := NewLoader(store)

	// No project dir.
	result, err := l.LoadAll(context.Background(), "")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if result.Loaded != 1 {
		t.Errorf("Loaded = %d, want 1 (user catalog)", result.Loaded)
	}
	wantUser := filepath.Join(home, ".beads", MoleculeFileName)
	if len(result.Sources) != 1 || result.Sources[0] != wantUser {
		t.Errorf("Sources = %v, want [%q]", result.Sources, wantUser)
	}
}

func TestLoadAll_EmptyBeadsDir_NoProjectLoad(t *testing.T) {
	t.Setenv("GT_ROOT", "")
	t.Setenv("HOME", t.TempDir())

	store := newFakeStore()
	l := NewLoader(store)

	result, err := l.LoadAll(context.Background(), "")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if result.Loaded != 0 {
		t.Errorf("Loaded = %d, want 0 when no catalogs exist", result.Loaded)
	}
	if len(result.Sources) != 0 {
		t.Errorf("Sources = %v, want empty", result.Sources)
	}
}

func TestLoadAll_LoadErrorIsLoggedNotFatal(t *testing.T) {
	// A store whose batch-create fails must not fail LoadAll: the error is
	// logged and that source is omitted from Sources, but LoadAll returns nil.
	t.Setenv("HOME", t.TempDir()) // no user catalog

	townRoot := t.TempDir()
	writeProjectMolecules(t, townRoot, `{"id":"mol-town","title":"Town","issue_type":"molecule","status":"open"}`)
	t.Setenv("GT_ROOT", townRoot)

	store := newFakeStore()
	store.createErr = errors.New("create boom")
	l := NewLoader(store)

	result, err := l.LoadAll(context.Background(), "")
	if err != nil {
		t.Fatalf("LoadAll returned error, want nil (load errors are non-fatal): %v", err)
	}
	if result.Loaded != 0 {
		t.Errorf("Loaded = %d, want 0 when the store rejects the batch", result.Loaded)
	}
	if len(result.Sources) != 0 {
		t.Errorf("Sources = %v, want empty (failed source not recorded)", result.Sources)
	}
}

func TestLoadAll_UserEqualsTown_NotDoubleLoaded(t *testing.T) {
	// When HOME and GT_ROOT resolve the same catalog path, LoadAll must not
	// load it twice (the userPath != townPath guard).
	shared := t.TempDir()
	writeProjectMolecules(t, shared, `{"id":"mol-shared","title":"Shared","issue_type":"molecule","status":"open"}`)
	t.Setenv("GT_ROOT", shared)
	t.Setenv("HOME", shared)

	store := newFakeStore()
	l := NewLoader(store)

	result, err := l.LoadAll(context.Background(), "")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	// Loaded once via town; user path is skipped because it equals townPath.
	if result.Loaded != 1 {
		t.Errorf("Loaded = %d, want 1 (dedup town==user)", result.Loaded)
	}
	if len(result.Sources) != 1 {
		t.Errorf("Sources = %v, want single source (no double-load)", result.Sources)
	}
}
