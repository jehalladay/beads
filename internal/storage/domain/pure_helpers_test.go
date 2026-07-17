package domain

import (
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/types"
)

// These tests cover the pure, hermetically-testable helpers in the domain
// package (no SQL repo, no filesystem, no network). The larger use-case
// methods on issue.go/dependency.go require a live store and are exercised by
// the integration suite; this file targets the 0%-covered pure logic.

func TestDefaultAdaptiveConfig(t *testing.T) {
	cfg := DefaultAdaptiveConfig()
	if cfg.MaxCollisionProbability != 0.25 {
		t.Errorf("MaxCollisionProbability = %v, want 0.25", cfg.MaxCollisionProbability)
	}
	if cfg.MinLength != 3 {
		t.Errorf("MinLength = %d, want 3", cfg.MinLength)
	}
	if cfg.MaxLength != 8 {
		t.Errorf("MaxLength = %d, want 8", cfg.MaxLength)
	}
}

func TestComputeAdaptiveLength(t *testing.T) {
	cfg := DefaultAdaptiveConfig()

	tests := []struct {
		name      string
		numIssues int
		want      int
	}{
		{"zero issues picks min", 0, cfg.MinLength},
		{"one issue picks min", 1, cfg.MinLength},
		{"small count stays at min", 100, cfg.MinLength},
		{"huge count saturates at max", 1 << 30, cfg.MaxLength},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeAdaptiveLength(tt.numIssues, cfg)
			if got != tt.want {
				t.Errorf("ComputeAdaptiveLength(%d) = %d, want %d", tt.numIssues, got, tt.want)
			}
		})
	}
}

func TestComputeAdaptiveLength_Monotonic(t *testing.T) {
	// Length must be non-decreasing as the issue count grows, and always
	// bounded by [MinLength, MaxLength].
	cfg := DefaultAdaptiveConfig()
	prev := 0
	for _, n := range []int{0, 10, 50, 200, 1000, 10000, 100000, 1000000} {
		got := ComputeAdaptiveLength(n, cfg)
		if got < cfg.MinLength || got > cfg.MaxLength {
			t.Fatalf("ComputeAdaptiveLength(%d) = %d out of bounds [%d,%d]", n, got, cfg.MinLength, cfg.MaxLength)
		}
		if got < prev {
			t.Fatalf("ComputeAdaptiveLength not monotonic: n=%d got %d < prev %d", n, got, prev)
		}
		prev = got
	}
}

func TestComputeAdaptiveLength_CustomConfig(t *testing.T) {
	// A permissive collision threshold should always return MinLength; a
	// strict one should climb toward MaxLength for large counts.
	permissive := AdaptiveIDConfig{MaxCollisionProbability: 1.0, MinLength: 4, MaxLength: 6}
	if got := ComputeAdaptiveLength(1000, permissive); got != 4 {
		t.Errorf("permissive ComputeAdaptiveLength = %d, want 4", got)
	}

	// A strict threshold with a large count climbs well above MinLength (the
	// first length whose collision probability drops under the threshold),
	// bounded by MaxLength.
	strict := AdaptiveIDConfig{MaxCollisionProbability: 0.0001, MinLength: 2, MaxLength: 10}
	got := ComputeAdaptiveLength(100000, strict)
	if got <= strict.MinLength || got > strict.MaxLength {
		t.Errorf("strict ComputeAdaptiveLength = %d, want in (%d,%d]", got, strict.MinLength, strict.MaxLength)
	}
}

func TestDefaultInfraTypes(t *testing.T) {
	got := DefaultInfraTypes()
	want := []string{"agent", "role", "message"}
	if len(got) != len(want) {
		t.Fatalf("DefaultInfraTypes() len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("DefaultInfraTypes()[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// Must return a defensive copy: mutating the result must not affect a
	// subsequent call.
	got[0] = "mutated"
	again := DefaultInfraTypes()
	if again[0] != "agent" {
		t.Errorf("DefaultInfraTypes not defensively copied: got %q after caller mutation", again[0])
	}
}

func TestIsInfraType(t *testing.T) {
	tests := []struct {
		t    types.IssueType
		want bool
	}{
		{types.IssueType("agent"), true},
		{types.IssueType("role"), true},
		{types.TypeMessage, true},
		{types.TypeBug, false},
		{types.TypeTask, false},
		{types.TypeEpic, false},
		{types.IssueType(""), false},
		{types.IssueType("Agent"), false}, // case-sensitive
	}
	for _, tt := range tests {
		if got := IsInfraType(tt.t); got != tt.want {
			t.Errorf("IsInfraType(%q) = %v, want %v", tt.t, got, tt.want)
		}
	}
}

func TestIsBlockingDep(t *testing.T) {
	tests := []struct {
		t    types.DependencyType
		want bool
	}{
		{types.DepBlocks, true},
		{types.DepConditionalBlocks, true},
		{types.DepParentChild, false},
		{types.DepWaitsFor, false},
		{types.DepRelated, false},
		{types.DepDiscoveredFrom, false},
		{types.DependencyType(""), false},
	}
	for _, tt := range tests {
		if got := isBlockingDep(tt.t); got != tt.want {
			t.Errorf("isBlockingDep(%q) = %v, want %v", tt.t, got, tt.want)
		}
	}
}

func TestCycleRelevantDepType(t *testing.T) {
	// cycleRelevantDepType is the blocking-only predicate; must match
	// isBlockingDep exactly (blocks + conditional-blocks).
	tests := []struct {
		t    types.DependencyType
		want bool
	}{
		{types.DepBlocks, true},
		{types.DepConditionalBlocks, true},
		{types.DepWaitsFor, false},
		{types.DepParentChild, false},
		{types.DepRelated, false},
		{types.DependencyType(""), false},
	}
	for _, tt := range tests {
		if got := cycleRelevantDepType(tt.t); got != tt.want {
			t.Errorf("cycleRelevantDepType(%q) = %v, want %v", tt.t, got, tt.want)
		}
	}
}

func TestReadyPathDepType(t *testing.T) {
	// readyPathDepType is the broad predicate delegating to AffectsReadyWork:
	// blocks + parent-child + conditional-blocks + waits-for.
	trueTypes := []types.DependencyType{
		types.DepBlocks, types.DepParentChild, types.DepConditionalBlocks, types.DepWaitsFor,
	}
	for _, dt := range trueTypes {
		if !readyPathDepType(dt) {
			t.Errorf("readyPathDepType(%q) = false, want true", dt)
		}
	}
	falseTypes := []types.DependencyType{
		types.DepRelated, types.DepDiscoveredFrom, types.DependencyType(""),
	}
	for _, dt := range falseTypes {
		if readyPathDepType(dt) {
			t.Errorf("readyPathDepType(%q) = true, want false", dt)
		}
	}
}

func TestReadyPathDepType_BroaderThanCycleRelevant(t *testing.T) {
	// The two predicates must stay distinct: parent-child affects ready-work
	// but is NOT blocking-cycle-relevant.
	if !readyPathDepType(types.DepParentChild) {
		t.Error("readyPathDepType(parent-child) should be true")
	}
	if cycleRelevantDepType(types.DepParentChild) {
		t.Error("cycleRelevantDepType(parent-child) should be false")
	}
}

func TestDepPairKeyRoundTrip(t *testing.T) {
	tests := []struct {
		from, to string
	}{
		{"beads-abc", "beads-def"},
		{"", ""},
		{"a", ""},
		{"", "b"},
		{"has-dash-123", "other_underscore"},
	}
	for _, tt := range tests {
		key := depPairKey(tt.from, tt.to)
		gotFrom, gotTo, ok := depPairIDs(key)
		if !ok {
			t.Errorf("depPairIDs(depPairKey(%q,%q)) not ok", tt.from, tt.to)
			continue
		}
		if gotFrom != tt.from || gotTo != tt.to {
			t.Errorf("round-trip (%q,%q) = (%q,%q)", tt.from, tt.to, gotFrom, gotTo)
		}
	}
}

func TestDepPairIDs_NoSeparator(t *testing.T) {
	from, to, ok := depPairIDs("no-nul-here")
	if ok {
		t.Errorf("depPairIDs without separator: ok=true, want false (from=%q to=%q)", from, to)
	}
	if from != "" || to != "" {
		t.Errorf("depPairIDs without separator returned (%q,%q), want empty", from, to)
	}
}

func TestGraphParentDepPairs(t *testing.T) {
	keyToID := map[string]string{
		"childKey":  "beads-child",
		"parentKey": "beads-parent",
		"orphanKey": "beads-orphan",
	}
	nodes := []GraphNode{
		// Resolved via ParentKey.
		{Key: "childKey", ParentKey: "parentKey"},
		// Resolved via explicit ParentID.
		{Key: "orphanKey", ParentID: "beads-explicit"},
		// No parent at all → skipped.
		{Key: "childKey"},
		// Child key unresolvable → skipped.
		{Key: "missingKey", ParentKey: "parentKey"},
		// Parent key unresolvable → skipped.
		{Key: "childKey", ParentKey: "missingParent"},
	}
	got := graphParentDepPairs(nodes, keyToID)

	if !got[depPairKey("beads-child", "beads-parent")] {
		t.Error("expected (child→parent) pair via ParentKey")
	}
	if !got[depPairKey("beads-orphan", "beads-explicit")] {
		t.Error("expected (orphan→explicit) pair via ParentID")
	}
	if len(got) != 2 {
		t.Errorf("graphParentDepPairs returned %d pairs, want 2: %v", len(got), got)
	}
}

func TestGraphParentDepPairs_ParentKeyOverridesParentID(t *testing.T) {
	// When both ParentKey and ParentID are set, ParentKey (resolved via
	// keyToID) wins.
	keyToID := map[string]string{"c": "beads-c", "p": "beads-p"}
	nodes := []GraphNode{{Key: "c", ParentKey: "p", ParentID: "beads-ignored"}}
	got := graphParentDepPairs(nodes, keyToID)
	if !got[depPairKey("beads-c", "beads-p")] {
		t.Error("ParentKey should override ParentID")
	}
	if got[depPairKey("beads-c", "beads-ignored")] {
		t.Error("ParentID should have been ignored when ParentKey is set")
	}
}

func TestResolveEdgeRef(t *testing.T) {
	keyToID := map[string]string{"k1": "beads-1"}
	tests := []struct {
		name string
		key  string
		id   string
		want string
	}{
		{"key resolves", "k1", "ignored-id", "beads-1"},
		{"key set but unresolvable yields empty", "missing", "fallback", ""},
		{"no key uses explicit id", "", "beads-explicit", "beads-explicit"},
		{"neither yields empty", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveEdgeRef(tt.key, tt.id, keyToID); got != tt.want {
				t.Errorf("resolveEdgeRef(%q,%q) = %q, want %q", tt.key, tt.id, got, tt.want)
			}
		})
	}
}

func TestResolveDoltDatabaseName(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *configfile.Config
		prefix string
		dbFlag string
		want   string
	}{
		{"explicit flag wins over everything", &configfile.Config{DoltDatabase: "fromcfg"}, "pfx", "fromflag", "fromflag"},
		{"config db used when no flag", &configfile.Config{DoltDatabase: "fromcfg"}, "pfx", "", "fromcfg"},
		{"prefix normalizes dashes to underscores", nil, "my-cool-rig", "", "my_cool_rig"},
		{"nil config + empty prefix falls to default", nil, "", "", configfile.DefaultDoltDatabase},
		{"empty-field config + empty prefix falls to default", &configfile.Config{}, "", "", configfile.DefaultDoltDatabase},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveDoltDatabaseName(tt.cfg, tt.prefix, tt.dbFlag); got != tt.want {
				t.Errorf("resolveDoltDatabaseName(%+v,%q,%q) = %q, want %q", tt.cfg, tt.prefix, tt.dbFlag, got, tt.want)
			}
		})
	}
}

func TestResolveProjectID(t *testing.T) {
	// Explicit project ID passes through unchanged.
	if got := resolveProjectID(&configfile.Config{ProjectID: "fixed-id"}); got != "fixed-id" {
		t.Errorf("resolveProjectID with set ID = %q, want fixed-id", got)
	}

	// nil config and empty-field config both generate a fresh (non-empty)
	// project ID.
	for _, cfg := range []*configfile.Config{nil, {}} {
		got := resolveProjectID(cfg)
		if got == "" {
			t.Errorf("resolveProjectID(%v) = empty, want a generated ID", cfg)
		}
	}
}
