// Package storage — merge_slot_test.go
//
// Hermetic unit tests for the non-transactional merge-slot helpers.
// These avoid a real Dolt backend by using a partial Storage fake that
// embeds the Storage interface (so it satisfies the type) and overrides
// only the handful of methods the covered impls actually call. The pure
// parseSlotMeta/encodeSlotMeta helpers need no fake at all.
package storage

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// fakeSlotStore embeds Storage so it satisfies the 100+ method interface,
// then overrides just the methods the merge-slot impls call. Any un-overridden
// method panics (embedded nil), which is fine — the covered impls never call
// them, and a panic makes an accidental call loud.
type fakeSlotStore struct {
	Storage
	issues      map[string]*types.Issue
	config      map[string]string
	createErr   error
	addLabelErr error
	created     []*types.Issue
	labelsAdded []string
}

func newFakeSlotStore() *fakeSlotStore {
	return &fakeSlotStore{
		issues: map[string]*types.Issue{},
		config: map[string]string{},
	}
}

func (f *fakeSlotStore) GetConfig(_ context.Context, key string) (string, error) {
	v, ok := f.config[key]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (f *fakeSlotStore) GetIssue(_ context.Context, id string) (*types.Issue, error) {
	iss, ok := f.issues[id]
	if !ok {
		return nil, ErrNotFound
	}
	return iss, nil
}

func (f *fakeSlotStore) CreateIssue(_ context.Context, issue *types.Issue, _ string) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, issue)
	f.issues[issue.ID] = issue
	return nil
}

func (f *fakeSlotStore) AddLabel(_ context.Context, issueID, label, _ string) error {
	if f.addLabelErr != nil {
		return f.addLabelErr
	}
	f.labelsAdded = append(f.labelsAdded, issueID+":"+label)
	return nil
}

func TestMergeSlotID(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name   string
		prefix string // "" means the config key is absent
		want   string
	}{
		{"default fallback when unset", "", "bd-merge-slot"},
		{"plain prefix", "gt", "gt-merge-slot"},
		{"prefix with trailing dash is trimmed", "beads-", "beads-merge-slot"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeSlotStore()
			if tc.prefix != "" {
				f.config["issue_prefix"] = tc.prefix
			}
			if got := MergeSlotID(ctx, f); got != tc.want {
				t.Fatalf("MergeSlotID() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMergeSlotCheckImpl_NotFound(t *testing.T) {
	f := newFakeSlotStore() // no slot issue present
	_, err := MergeSlotCheckImpl(context.Background(), f)
	if err == nil {
		t.Fatal("MergeSlotCheckImpl() err = nil, want not-found error")
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("MergeSlotCheckImpl() err = %v, want errors.Is ErrNotFound", err)
	}
}

func TestMergeSlotCheckImpl_AvailableAndHeld(t *testing.T) {
	ctx := context.Background()

	// Open slot with no metadata → available, empty holder/waiters.
	f := newFakeSlotStore()
	f.issues["bd-merge-slot"] = &types.Issue{ID: "bd-merge-slot", Status: types.StatusOpen}
	st, err := MergeSlotCheckImpl(ctx, f)
	if err != nil {
		t.Fatalf("MergeSlotCheckImpl() err = %v, want nil", err)
	}
	if !st.Available || st.Holder != "" || len(st.Waiters) != 0 {
		t.Fatalf("open slot: got %+v, want available/empty", st)
	}
	if st.SlotID != "bd-merge-slot" {
		t.Fatalf("SlotID = %q, want bd-merge-slot", st.SlotID)
	}

	// Non-open slot carrying holder + waiters → unavailable, parsed metadata.
	meta, _ := json.Marshal(map[string]any{"holder": "alice", "waiters": []string{"bob", "carol"}})
	f.issues["bd-merge-slot"] = &types.Issue{
		ID:       "bd-merge-slot",
		Status:   types.StatusInProgress,
		Metadata: meta,
	}
	st, err = MergeSlotCheckImpl(ctx, f)
	if err != nil {
		t.Fatalf("MergeSlotCheckImpl() err = %v, want nil", err)
	}
	if st.Available {
		t.Fatal("held slot reported Available = true, want false")
	}
	if st.Holder != "alice" {
		t.Fatalf("Holder = %q, want alice", st.Holder)
	}
	if len(st.Waiters) != 2 || st.Waiters[0] != "bob" || st.Waiters[1] != "carol" {
		t.Fatalf("Waiters = %v, want [bob carol]", st.Waiters)
	}
}

func TestMergeSlotCreateImpl_Idempotent(t *testing.T) {
	f := newFakeSlotStore()
	existing := &types.Issue{ID: "bd-merge-slot", Title: "Merge Slot"}
	f.issues["bd-merge-slot"] = existing

	got, err := MergeSlotCreateImpl(context.Background(), f, "actor")
	if err != nil {
		t.Fatalf("MergeSlotCreateImpl() err = %v, want nil", err)
	}
	if got != existing {
		t.Fatalf("MergeSlotCreateImpl() returned a new issue, want the existing one")
	}
	if len(f.created) != 0 {
		t.Fatalf("CreateIssue called %d times on idempotent path, want 0", len(f.created))
	}
}

func TestMergeSlotCreateImpl_CreatesWithLabel(t *testing.T) {
	f := newFakeSlotStore()
	got, err := MergeSlotCreateImpl(context.Background(), f, "actor")
	if err != nil {
		t.Fatalf("MergeSlotCreateImpl() err = %v, want nil", err)
	}
	if got == nil || got.ID != "bd-merge-slot" {
		t.Fatalf("MergeSlotCreateImpl() = %+v, want the new bd-merge-slot issue", got)
	}
	if len(f.created) != 1 || f.created[0].IssueType != types.TypeTask || f.created[0].Priority != 0 {
		t.Fatalf("created issue wrong: %+v", f.created)
	}
	if len(f.labelsAdded) != 1 || f.labelsAdded[0] != "bd-merge-slot:gt:slot" {
		t.Fatalf("labelsAdded = %v, want [bd-merge-slot:gt:slot]", f.labelsAdded)
	}
}

func TestMergeSlotCreateImpl_LabelFailureIsNonFatal(t *testing.T) {
	f := newFakeSlotStore()
	f.addLabelErr = errors.New("label backend down")
	got, err := MergeSlotCreateImpl(context.Background(), f, "actor")
	if err != nil {
		t.Fatalf("MergeSlotCreateImpl() err = %v, want nil (label failure is non-fatal)", err)
	}
	if got == nil || got.ID != "bd-merge-slot" {
		t.Fatalf("MergeSlotCreateImpl() = %+v, want the created slot despite label failure", got)
	}
}

func TestMergeSlotCreateImpl_CreateErrorPropagates(t *testing.T) {
	f := newFakeSlotStore()
	f.createErr = errors.New("create backend down")
	_, err := MergeSlotCreateImpl(context.Background(), f, "actor")
	if err == nil {
		t.Fatal("MergeSlotCreateImpl() err = nil, want the create error wrapped")
	}
}

func TestParseSlotMeta(t *testing.T) {
	tests := []struct {
		name        string
		metadata    json.RawMessage
		wantHolder  string
		wantWaiters int
	}{
		{"empty metadata", nil, "", 0},
		{"populated", json.RawMessage(`{"holder":"x","waiters":["a","b"]}`), "x", 2},
		{"holder only", json.RawMessage(`{"holder":"solo"}`), "solo", 0},
		{"malformed json is tolerated", json.RawMessage(`{not json`), "", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			meta := parseSlotMeta(&types.Issue{Metadata: tc.metadata})
			if meta.Holder != tc.wantHolder {
				t.Fatalf("Holder = %q, want %q", meta.Holder, tc.wantHolder)
			}
			if len(meta.Waiters) != tc.wantWaiters {
				t.Fatalf("len(Waiters) = %d, want %d", len(meta.Waiters), tc.wantWaiters)
			}
		})
	}
}

func TestEncodeSlotMeta_RoundTrip(t *testing.T) {
	in := slotMeta{Holder: "alice", Waiters: []string{"bob"}}
	s, err := encodeSlotMeta(in)
	if err != nil {
		t.Fatalf("encodeSlotMeta() err = %v, want nil", err)
	}
	out := parseSlotMeta(&types.Issue{Metadata: json.RawMessage(s)})
	if out.Holder != "alice" || len(out.Waiters) != 1 || out.Waiters[0] != "bob" {
		t.Fatalf("round-trip = %+v, want {alice [bob]}", out)
	}

	// omitempty: an empty slotMeta encodes to "{}".
	empty, err := encodeSlotMeta(slotMeta{})
	if err != nil {
		t.Fatalf("encodeSlotMeta(empty) err = %v, want nil", err)
	}
	if empty != "{}" {
		t.Fatalf("encodeSlotMeta(empty) = %q, want {}", empty)
	}
}
