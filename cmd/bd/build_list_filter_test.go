package main

import (
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// baseCfg is a config with no custom statuses/types so the built-in
// validity checks apply and the domain infra defaults are used.
func baseCfg() listFilterConfig { return listFilterConfig{} }

func TestBuildListFilter_ReadyFlagForcesOpen(t *testing.T) {
	f, err := buildListFilter(listInput{readyFlag: true}, baseCfg())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if f.Status == nil || *f.Status != types.StatusOpen {
		t.Fatalf("ready should force Status=open, got %v", f.Status)
	}
	// ready doesn't set the default ExcludeStatus block (status=="" but readyFlag).
	if len(f.ExcludeStatus) != 0 {
		t.Errorf("ready ExcludeStatus = %v, want none", f.ExcludeStatus)
	}
}

func TestBuildListFilter_SingleStatus(t *testing.T) {
	f, err := buildListFilter(listInput{status: "in_progress"}, baseCfg())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if f.Status == nil || *f.Status != types.StatusInProgress {
		t.Fatalf("Status = %v, want in_progress", f.Status)
	}
	if len(f.Statuses) != 0 {
		t.Errorf("Statuses = %v, want empty for single status", f.Statuses)
	}
}

func TestBuildListFilter_SingleStatusInvalid(t *testing.T) {
	_, err := buildListFilter(listInput{status: "bogus"}, baseCfg())
	if err == nil {
		t.Fatal("expected error for invalid single status")
	}
}

func TestBuildListFilter_MultiStatus(t *testing.T) {
	f, err := buildListFilter(listInput{status: "open,in_progress"}, baseCfg())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if f.Status != nil {
		t.Errorf("Status = %v, want nil for multi-status", f.Status)
	}
	if len(f.Statuses) != 2 || f.Statuses[0] != types.StatusOpen || f.Statuses[1] != types.StatusInProgress {
		t.Errorf("Statuses = %v, want [open in_progress]", f.Statuses)
	}
}

func TestBuildListFilter_MultiStatusInvalidPart(t *testing.T) {
	_, err := buildListFilter(listInput{status: "open,bogus"}, baseCfg())
	if err == nil {
		t.Fatal("expected error for invalid part in multi-status")
	}
}

func TestBuildListFilter_CustomStatusAccepted(t *testing.T) {
	cfg := listFilterConfig{customStatuses: []types.CustomStatus{{Name: "triage", Category: types.CategoryActive}}}
	f, err := buildListFilter(listInput{status: "triage"}, cfg)
	if err != nil {
		t.Fatalf("custom status should be valid: %v", err)
	}
	if f.Status == nil || string(*f.Status) != "triage" {
		t.Errorf("Status = %v, want triage", f.Status)
	}
}

func TestBuildListFilter_DefaultExcludesClosedAndPinned(t *testing.T) {
	// No status, no all/ready/pinned → default exclusion block.
	f, err := buildListFilter(listInput{}, baseCfg())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	var sawClosed, sawPinned bool
	for _, s := range f.ExcludeStatus {
		if s == types.StatusClosed {
			sawClosed = true
		}
		if s == types.StatusPinned {
			sawPinned = true
		}
	}
	if !sawClosed || !sawPinned {
		t.Errorf("default ExcludeStatus = %v, want closed+pinned", f.ExcludeStatus)
	}
	// Default also excludes non-pinned via Pinned=false.
	if f.Pinned == nil || *f.Pinned != false {
		t.Errorf("default Pinned = %v, want false", f.Pinned)
	}
}

func TestBuildListFilter_DefaultExcludesDoneAndFrozenCustom(t *testing.T) {
	cfg := listFilterConfig{customStatuses: []types.CustomStatus{
		{Name: "shipped", Category: types.CategoryDone},
		{Name: "iced", Category: types.CategoryFrozen},
		{Name: "triage", Category: types.CategoryActive},
	}}
	f, err := buildListFilter(listInput{}, cfg)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	got := map[types.Status]bool{}
	for _, s := range f.ExcludeStatus {
		got[s] = true
	}
	if !got["shipped"] || !got["iced"] {
		t.Errorf("ExcludeStatus = %v, want to include shipped+iced", f.ExcludeStatus)
	}
	if got["triage"] {
		t.Errorf("active custom status should NOT be excluded: %v", f.ExcludeStatus)
	}
}

func TestBuildListFilter_PinnedFlag(t *testing.T) {
	f, err := buildListFilter(listInput{pinnedFlag: true}, baseCfg())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if f.Pinned == nil || *f.Pinned != true {
		t.Errorf("Pinned = %v, want true", f.Pinned)
	}
}

func TestBuildListFilter_ScalarFilters(t *testing.T) {
	in := listInput{
		prioritySet:    true,
		priority:       1,
		assignee:       "alice",
		titleSearch:    "widget",
		specPrefix:     "SPEC-",
		titleContains:  "abc",
		descContains:   "def",
		notesContains:  "ghi",
		priorityMinSet: true,
		priorityMin:    0,
		priorityMaxSet: true,
		priorityMax:    3,
		parentID:       "bd-parent",
		hasMetadataKey: "owner",
	}
	f, err := buildListFilter(in, baseCfg())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if f.Priority == nil || *f.Priority != 1 {
		t.Errorf("Priority = %v, want 1", f.Priority)
	}
	if f.Assignee == nil || *f.Assignee != "alice" {
		t.Errorf("Assignee = %v", f.Assignee)
	}
	if f.TitleSearch != "widget" || f.SpecIDPrefix != "SPEC-" {
		t.Errorf("TitleSearch/SpecIDPrefix = %q/%q", f.TitleSearch, f.SpecIDPrefix)
	}
	if f.TitleContains != "abc" || f.DescriptionContains != "def" || f.NotesContains != "ghi" {
		t.Errorf("contains filters wrong: %q %q %q", f.TitleContains, f.DescriptionContains, f.NotesContains)
	}
	if f.PriorityMin == nil || *f.PriorityMin != 0 || f.PriorityMax == nil || *f.PriorityMax != 3 {
		t.Errorf("priority range wrong: min=%v max=%v", f.PriorityMin, f.PriorityMax)
	}
	if f.ParentID == nil || *f.ParentID != "bd-parent" {
		t.Errorf("ParentID = %v", f.ParentID)
	}
	if f.HasMetadataKey != "owner" {
		t.Errorf("HasMetadataKey = %q", f.HasMetadataKey)
	}
}

func TestBuildListFilter_LabelFilters(t *testing.T) {
	in := listInput{
		labels:         []string{"a", "b"},
		labelsAny:      []string{"c"},
		excludeLabels:  []string{"d"},
		labelPattern:   "tech-*",
		labelRegex:     "tech-(debt|legacy)",
		idFilter:       "bd-1, bd-2",
		metadataFields: map[string]string{"k": "v"},
	}
	f, err := buildListFilter(in, baseCfg())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(f.Labels) != 2 || len(f.LabelsAny) != 1 || len(f.ExcludeLabels) != 1 {
		t.Errorf("label slices wrong: %v %v %v", f.Labels, f.LabelsAny, f.ExcludeLabels)
	}
	if f.LabelPattern != "tech-*" || f.LabelRegex != "tech-(debt|legacy)" {
		t.Errorf("pattern/regex wrong: %q %q", f.LabelPattern, f.LabelRegex)
	}
	if len(f.IDs) != 2 {
		t.Errorf("IDs = %v, want 2 normalized ids", f.IDs)
	}
	if f.MetadataFields["k"] != "v" {
		t.Errorf("MetadataFields = %v", f.MetadataFields)
	}
}

func TestBuildListFilter_BoolFlags(t *testing.T) {
	in := listInput{
		emptyDesc:  true,
		noAssignee: true,
		noLabels:   true,
		skipLabels: true,
		noParent:   true,
	}
	f, err := buildListFilter(in, baseCfg())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !f.EmptyDescription || !f.NoAssignee || !f.NoLabels || !f.SkipLabels || !f.NoParent {
		t.Errorf("bool flags not all set: %+v", f)
	}
}

func TestBuildListFilter_IssueTypeValidAndInvalid(t *testing.T) {
	f, err := buildListFilter(listInput{issueType: "bug"}, baseCfg())
	if err != nil {
		t.Fatalf("bug should be valid: %v", err)
	}
	if f.IssueType == nil || *f.IssueType != types.IssueType("bug") {
		t.Errorf("IssueType = %v, want bug", f.IssueType)
	}

	if _, err := buildListFilter(listInput{issueType: "not-a-type"}, baseCfg()); err == nil {
		t.Fatal("expected error for invalid issue type")
	}
}

func TestBuildListFilter_CustomIssueTypeAccepted(t *testing.T) {
	cfg := listFilterConfig{customTypes: []string{"spike"}}
	f, err := buildListFilter(listInput{issueType: "spike"}, cfg)
	if err != nil {
		t.Fatalf("custom type should be valid: %v", err)
	}
	if f.IssueType == nil || string(*f.IssueType) != "spike" {
		t.Errorf("IssueType = %v, want spike", f.IssueType)
	}
}

func TestBuildListFilter_TemplateAndGateExclusion(t *testing.T) {
	// Defaults: templates excluded, gates excluded.
	f, err := buildListFilter(listInput{}, baseCfg())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if f.IsTemplate == nil || *f.IsTemplate != false {
		t.Errorf("IsTemplate = %v, want false (exclude templates)", f.IsTemplate)
	}
	var sawGate bool
	for _, x := range f.ExcludeTypes {
		if x == "gate" {
			sawGate = true
		}
	}
	if !sawGate {
		t.Errorf("ExcludeTypes = %v, want to include gate", f.ExcludeTypes)
	}

	// include-templates + include-gates removes those exclusions.
	f2, err := buildListFilter(listInput{includeTemplates: true, includeGates: true, includeInfra: true}, baseCfg())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if f2.IsTemplate != nil {
		t.Errorf("include-templates: IsTemplate = %v, want nil", f2.IsTemplate)
	}
	for _, x := range f2.ExcludeTypes {
		if x == "gate" {
			t.Errorf("include-gates: gate still excluded: %v", f2.ExcludeTypes)
		}
	}
}

func TestBuildListFilter_InfraExclusionAndInclusion(t *testing.T) {
	// Default: infra types excluded, and SkipWisps set.
	f, err := buildListFilter(listInput{}, baseCfg())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	var sawAgent bool
	for _, x := range f.ExcludeTypes {
		if x == "agent" {
			sawAgent = true
		}
	}
	if !sawAgent {
		t.Errorf("default ExcludeTypes = %v, want to include infra type agent", f.ExcludeTypes)
	}
	if !f.SkipWisps {
		t.Error("default SkipWisps = false, want true")
	}

	// Asking for an infra type directly flips Ephemeral=true and does not skip
	// wisps. The type must also validate, so register "agent" as a custom type
	// (matches how infra types are configured in practice).
	f2, err := buildListFilter(listInput{issueType: "agent"}, listFilterConfig{customTypes: []string{"agent"}})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if f2.Ephemeral == nil || *f2.Ephemeral != true {
		t.Errorf("infra-type query Ephemeral = %v, want true", f2.Ephemeral)
	}
	if f2.SkipWisps {
		t.Error("infra-type query SkipWisps = true, want false")
	}
}

func TestBuildListFilter_ExcludeTypeStrsSplitAndNormalize(t *testing.T) {
	f, err := buildListFilter(listInput{excludeTypeStrs: []string{"bug, feature", " chore "}}, baseCfg())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	got := map[types.IssueType]bool{}
	for _, x := range f.ExcludeTypes {
		got[x] = true
	}
	for _, want := range []types.IssueType{"bug", "feature", "chore"} {
		if !got[want] {
			t.Errorf("ExcludeTypes missing %q: %v", want, f.ExcludeTypes)
		}
	}
}

func TestBuildListFilter_MolAndWispType(t *testing.T) {
	mt := types.MolTypeSwarm
	wt := types.WispTypeHeartbeat
	f, err := buildListFilter(listInput{molType: &mt, wispType: &wt}, baseCfg())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if f.MolType == nil || *f.MolType != types.MolTypeSwarm {
		t.Errorf("MolType = %v", f.MolType)
	}
	if f.WispType == nil || *f.WispType != types.WispTypeHeartbeat {
		t.Errorf("WispType = %v", f.WispType)
	}
}

func TestBuildListFilter_TimeRangesAndScheduling(t *testing.T) {
	now := time.Now()
	in := listInput{
		createdAfter:  &now,
		createdBefore: &now,
		updatedAfter:  &now,
		updatedBefore: &now,
		closedAfter:   &now,
		closedBefore:  &now,
		deferAfter:    &now,
		deferBefore:   &now,
		dueAfter:      &now,
		dueBefore:     &now,
		deferredFlag:  true,
		overdueFlag:   true,
	}
	f, err := buildListFilter(in, baseCfg())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if f.CreatedAfter == nil || f.ClosedBefore == nil || f.DueAfter == nil {
		t.Error("time ranges not propagated")
	}
	if !f.Deferred || !f.Overdue {
		t.Errorf("Deferred=%v Overdue=%v, want both true", f.Deferred, f.Overdue)
	}
}

func TestBuildListFilter_SortAndPagination(t *testing.T) {
	f, err := buildListFilter(listInput{sqlLimit: 25, offset: 10, sortBy: "priority", reverse: true}, baseCfg())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if f.Limit != 25 || f.Offset != 10 {
		t.Errorf("Limit/Offset = %d/%d, want 25/10", f.Limit, f.Offset)
	}
	if f.SortBy != "priority" || !f.SortDesc {
		t.Errorf("SortBy/SortDesc = %q/%v", f.SortBy, f.SortDesc)
	}
}

func TestBuildListFilter_AllFlagKeepsPinnedNil(t *testing.T) {
	// --all with no status leaves Pinned unset (nil) so pinned rows are included.
	f, err := buildListFilter(listInput{allFlag: true, includeInfra: true, includeTemplates: true, includeGates: true}, baseCfg())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if f.Pinned != nil {
		t.Errorf("--all Pinned = %v, want nil", f.Pinned)
	}
	if len(f.ExcludeStatus) != 0 {
		t.Errorf("--all ExcludeStatus = %v, want empty", f.ExcludeStatus)
	}
}
