//go:build cgo

package embeddeddolt_test

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/steveyegge/beads/internal/types"
	"golang.org/x/sync/errgroup"
)

// TestUpdateMetadataFieldsConcurrentDifferentKeys is the beads-fnp6 teeth check:
// two concurrent writers each set a DIFFERENT metadata key on the SAME issue.
// With the atomic server-side JSON_SET path, BOTH keys survive. The old
// read-modify-write-the-whole-blob path (SlotSet/CLI applyMetadataEdits →
// UpdateIssue(metadata=blob)) lost one key to last-writer-wins.
//
// REVERT-GUARD: to confirm this test has teeth, make
// EmbeddedDoltStore.UpdateMetadataFields do a whole-blob read-modify-write
// instead of ApplyMetadataKeyEditsInTx and this test goes RED (one key lost).
func TestUpdateMetadataFieldsConcurrentDifferentKeys(t *testing.T) {
	skipUnlessEmbeddedDolt(t)

	te := newTestEnv(t, "mf")
	ctx := t.Context()

	issue := &types.Issue{
		Title:     "concurrent metadata target",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		Metadata:  json.RawMessage(`{"seed":"v0"}`),
	}
	if err := te.store.CreateIssue(ctx, issue, "tester"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	id := issue.ID

	// N concurrent writers, each owning a distinct key. Under last-writer-wins
	// blob clobber, only a subset of keys would survive.
	const writers = 8
	var start sync.WaitGroup
	start.Add(1)
	g, gctx := errgroup.WithContext(ctx)
	for i := 0; i < writers; i++ {
		key := fmt.Sprintf("k%d", i)
		val := json.RawMessage(fmt.Sprintf(`"val%d"`, i))
		g.Go(func() error {
			start.Wait() // maximize overlap
			return te.store.UpdateMetadataFields(gctx, id,
				map[string]json.RawMessage{key: val}, nil, "tester")
		})
	}
	start.Done()
	if err := g.Wait(); err != nil {
		t.Fatalf("concurrent UpdateMetadataFields: %v", err)
	}

	got, err := te.store.GetIssue(ctx, id)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(got.Metadata, &meta); err != nil {
		t.Fatalf("unmarshal metadata %q: %v", string(got.Metadata), err)
	}

	// The pre-existing key must be preserved (JSON_SET is additive).
	if _, ok := meta["seed"]; !ok {
		t.Errorf("pre-existing key %q was clobbered; metadata=%s", "seed", string(got.Metadata))
	}
	// Every concurrent writer's key must survive.
	for i := 0; i < writers; i++ {
		key := fmt.Sprintf("k%d", i)
		want := fmt.Sprintf(`"val%d"`, i)
		v, ok := meta[key]
		if !ok {
			t.Errorf("key %q lost to concurrent clobber; metadata=%s", key, string(got.Metadata))
			continue
		}
		if string(v) != want {
			t.Errorf("key %q = %s, want %s", key, string(v), want)
		}
	}
}

// TestUpdateMetadataFieldsSetUnset verifies per-key set + unset semantics in a
// single call: sets land, unsets remove, and a key present in both is removed
// (unset wins), matching the client-side applyMetadataEdits contract.
func TestUpdateMetadataFieldsSetUnset(t *testing.T) {
	skipUnlessEmbeddedDolt(t)

	te := newTestEnv(t, "mu")
	ctx := t.Context()

	issue := &types.Issue{
		Title:     "set/unset target",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		Metadata:  json.RawMessage(`{"keep":"y","drop":"old"}`),
	}
	if err := te.store.CreateIssue(ctx, issue, "tester"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	err := te.store.UpdateMetadataFields(ctx, issue.ID,
		map[string]json.RawMessage{"added": json.RawMessage(`"new"`), "num": json.RawMessage(`42`)},
		[]string{"drop", "absent"}, "tester")
	if err != nil {
		t.Fatalf("UpdateMetadataFields: %v", err)
	}

	got, err := te.store.GetIssue(ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(got.Metadata, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	assertKey := func(k, want string) {
		v, ok := meta[k]
		if !ok {
			t.Errorf("key %q missing; metadata=%s", k, string(got.Metadata))
			return
		}
		if string(v) != want {
			t.Errorf("key %q = %s, want %s", k, string(v), want)
		}
	}
	assertKey("keep", `"y"`)
	assertKey("added", `"new"`)
	assertKey("num", `42`)
	if _, ok := meta["drop"]; ok {
		t.Errorf("key %q should have been removed; metadata=%s", "drop", string(got.Metadata))
	}
}

// TestMergeMetadataWithCASConcurrent is the beads-fnp6 teeth check for the
// --metadata (whole-blob MERGE) path: N concurrent merges each add a distinct
// key. The server-side read-merge-write-in-one-tx (with commit-conflict retry)
// keeps ALL keys; the old client-side read-modify-write lost concurrent edits.
//
// REVERT-GUARD: make EmbeddedDoltStore.MergeMetadataWithCAS do a client-side
// GetIssue → merge → UpdateIssue(metadata=blob) and this test goes RED.
func TestMergeMetadataWithCASConcurrent(t *testing.T) {
	skipUnlessEmbeddedDolt(t)

	te := newTestEnv(t, "mc")
	ctx := t.Context()

	issue := &types.Issue{
		Title:     "concurrent merge target",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		Metadata:  json.RawMessage(`{"seed":"v0"}`),
	}
	if err := te.store.CreateIssue(ctx, issue, "tester"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	id := issue.ID

	const writers = 8
	var start sync.WaitGroup
	start.Add(1)
	g, gctx := errgroup.WithContext(ctx)
	for i := 0; i < writers; i++ {
		blob := json.RawMessage(fmt.Sprintf(`{"m%d":"val%d"}`, i, i))
		g.Go(func() error {
			start.Wait()
			return te.store.MergeMetadataWithCAS(gctx, id, blob, "tester")
		})
	}
	start.Done()
	if err := g.Wait(); err != nil {
		t.Fatalf("concurrent MergeMetadataWithCAS: %v", err)
	}

	got, err := te.store.GetIssue(ctx, id)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(got.Metadata, &meta); err != nil {
		t.Fatalf("unmarshal metadata %q: %v", string(got.Metadata), err)
	}
	if _, ok := meta["seed"]; !ok {
		t.Errorf("pre-existing key %q clobbered; metadata=%s", "seed", string(got.Metadata))
	}
	for i := 0; i < writers; i++ {
		key := fmt.Sprintf("m%d", i)
		if _, ok := meta[key]; !ok {
			t.Errorf("merge key %q lost to concurrent clobber; metadata=%s", key, string(got.Metadata))
		}
	}
}
