package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

// These tests exercise the previously-uncovered tracker methods and field-mapper
// helpers in internal/jira. They are hermetic: a Client backed by httptest.Server
// (no live Jira, no network egress, no DB).

func TestProjectKeyAccessors(t *testing.T) {
	tr := &Tracker{}
	if got := tr.PrimaryProjectKey(); got != "" {
		t.Errorf("PrimaryProjectKey() with no keys = %q, want empty", got)
	}
	if got := tr.ProjectKeys(); len(got) != 0 {
		t.Errorf("ProjectKeys() with no keys = %v, want empty", got)
	}

	tr.SetProjectKeys([]string{"ALPHA", "BETA"})
	if got := tr.ProjectKeys(); len(got) != 2 || got[0] != "ALPHA" || got[1] != "BETA" {
		t.Errorf("ProjectKeys() = %v, want [ALPHA BETA]", got)
	}
	if got := tr.PrimaryProjectKey(); got != "ALPHA" {
		t.Errorf("PrimaryProjectKey() = %q, want %q", got, "ALPHA")
	}
}

func TestValidate(t *testing.T) {
	// Uninitialized tracker (nil client) is invalid.
	if err := (&Tracker{}).Validate(); err == nil {
		t.Error("Validate() on nil-client tracker should error")
	}
	// Tracker with a client validates OK.
	tr := &Tracker{client: newTestClient("https://example.atlassian.net", "3")}
	if err := tr.Validate(); err != nil {
		t.Errorf("Validate() on initialized tracker = %v, want nil", err)
	}
}

func TestClose(t *testing.T) {
	if err := (&Tracker{}).Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

func TestFetchIssueFound(t *testing.T) {
	const key = "PROJ-7"
	issuePath := "/rest/api/3/issue/" + key

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == issuePath {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(Issue{
				ID:  "10007",
				Key: key,
				Fields: IssueFields{
					Summary: "Fetched issue",
					Status:  &StatusField{Name: "To Do"},
				},
			})
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	tr := newTrackerWithServer(srv.URL, "3")
	ti, err := tr.FetchIssue(context.Background(), key)
	if err != nil {
		t.Fatalf("FetchIssue error: %v", err)
	}
	if ti == nil {
		t.Fatal("FetchIssue returned nil for an existing issue")
	}
	if ti.Identifier != key {
		t.Errorf("FetchIssue Identifier = %q, want %q", ti.Identifier, key)
	}
	if ti.Title != "Fetched issue" {
		t.Errorf("FetchIssue Title = %q, want %q", ti.Title, "Fetched issue")
	}
}

func TestFetchIssueError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	tr := newTrackerWithServer(srv.URL, "3")
	if _, err := tr.FetchIssue(context.Background(), "PROJ-404"); err == nil {
		t.Error("FetchIssue should return an error on a 404 response")
	}
}

func TestCreateIssueInjectsProjectAndRoundTrips(t *testing.T) {
	const key = "PROJ-100"
	createPath := "/rest/api/3/issue"
	getPath := createPath + "/" + key

	var postedProjectKey string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == createPath:
			var payload struct {
				Fields struct {
					Project struct {
						Key string `json:"key"`
					} `json:"project"`
				} `json:"fields"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			postedProjectKey = payload.Fields.Project.Key
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id":   "10100",
				"key":  key,
				"self": srv0Self(r) + getPath,
			})
		case r.Method == http.MethodGet && r.URL.Path == getPath:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(Issue{
				ID:  "10100",
				Key: key,
				Fields: IssueFields{
					Summary: "Created issue",
					Status:  &StatusField{Name: "To Do"},
				},
			})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	tr := newTrackerWithServer(srv.URL, "3")
	tr.SetProjectKeys([]string{"PROJ", "SECOND"})

	ti, err := tr.CreateIssue(context.Background(), &types.Issue{
		Title:     "Created issue",
		IssueType: types.TypeBug,
		Priority:  1,
	})
	if err != nil {
		t.Fatalf("CreateIssue error: %v", err)
	}
	if postedProjectKey != "PROJ" {
		t.Errorf("CreateIssue posted project key = %q, want primary %q", postedProjectKey, "PROJ")
	}
	if ti == nil || ti.Identifier != key {
		t.Fatalf("CreateIssue returned %+v, want identifier %q", ti, key)
	}
	if ti.Title != "Created issue" {
		t.Errorf("CreateIssue Title = %q, want %q", ti.Title, "Created issue")
	}
}

// srv0Self returns the scheme+host of the test server for building a Self URL.
func srv0Self(r *http.Request) string {
	return "http://" + r.Host
}

func TestJiraPriorityToNumeric(t *testing.T) {
	// Defaults (case-insensitive), plus unknown → 2.
	defaults := map[string]int{
		"Highest": 0, "high": 1, "MEDIUM": 2, "Low": 3, "lowest": 4,
		"": 2, "Unknown": 2,
	}
	for name, want := range defaults {
		if got := jiraPriorityToNumeric(name, nil); got != want {
			t.Errorf("jiraPriorityToNumeric(%q, nil) = %d, want %d", name, got, want)
		}
	}

	// Custom map: inverted lookup (beads key → Jira name), case-insensitive.
	custom := map[string]string{"0": "Critical", "3": "Minor"}
	if got := jiraPriorityToNumeric("critical", custom); got != 0 {
		t.Errorf("jiraPriorityToNumeric(critical, custom) = %d, want 0", got)
	}
	if got := jiraPriorityToNumeric("Minor", custom); got != 3 {
		t.Errorf("jiraPriorityToNumeric(Minor, custom) = %d, want 3", got)
	}
	// A name not in the custom map falls through to defaults.
	if got := jiraPriorityToNumeric("High", custom); got != 1 {
		t.Errorf("jiraPriorityToNumeric(High, custom) = %d, want default 1", got)
	}

	// Custom map with an out-of-range/invalid beads key is ignored → default path.
	bad := map[string]string{"9": "Weird", "notanint": "Odd"}
	if got := jiraPriorityToNumeric("Weird", bad); got != 2 {
		t.Errorf("jiraPriorityToNumeric(Weird, out-of-range) = %d, want default 2", got)
	}
	if got := jiraPriorityToNumeric("Odd", bad); got != 2 {
		t.Errorf("jiraPriorityToNumeric(Odd, non-int key) = %d, want default 2", got)
	}
}

func TestTypeToBeadsDefaults(t *testing.T) {
	m := &jiraFieldMapper{}
	cases := map[string]types.IssueType{
		"Bug":      types.TypeBug,
		"Story":    types.TypeFeature,
		"Feature":  types.TypeFeature,
		"Epic":     types.TypeEpic,
		"Task":     types.TypeTask,
		"Sub-task": types.TypeTask,
		"Unknown":  types.TypeTask,
	}
	for in, want := range cases {
		if got := m.TypeToBeads(in); got != want {
			t.Errorf("TypeToBeads(%q) = %q, want %q", in, got, want)
		}
	}

	// Non-string input → TypeTask.
	if got := m.TypeToBeads(42); got != types.TypeTask {
		t.Errorf("TypeToBeads(non-string) = %q, want %q", got, types.TypeTask)
	}

	// Custom map takes precedence (case-insensitive).
	mm := &jiraFieldMapper{typeMap: map[string]string{"chore": "Maintenance"}}
	if got := mm.TypeToBeads("maintenance"); got != types.IssueType("chore") {
		t.Errorf("TypeToBeads with custom map = %q, want %q", got, "chore")
	}
}

func TestFieldExtractionHelpersNilPaths(t *testing.T) {
	// All fields nil → helpers return empty strings (the uncovered branch).
	empty := &Issue{}
	if got := priorityName(empty); got != "" {
		t.Errorf("priorityName(nil field) = %q, want empty", got)
	}
	if got := statusName(empty); got != "" {
		t.Errorf("statusName(nil field) = %q, want empty", got)
	}
	if got := typeName(empty); got != "" {
		t.Errorf("typeName(nil field) = %q, want empty", got)
	}

	// Populated → returns the names.
	full := &Issue{Fields: IssueFields{
		Priority:  &PriorityField{Name: "High"},
		Status:    &StatusField{Name: "Done"},
		IssueType: &IssueTypeField{Name: "Bug"},
	}}
	if got := priorityName(full); got != "High" {
		t.Errorf("priorityName = %q, want High", got)
	}
	if got := statusName(full); got != "Done" {
		t.Errorf("statusName = %q, want Done", got)
	}
	if got := typeName(full); got != "Bug" {
		t.Errorf("typeName = %q, want Bug", got)
	}
}

func TestExtractBrowseURL(t *testing.T) {
	// Happy path.
	ji := &Issue{
		Key:  "PROJ-9",
		Self: "https://company.atlassian.net/rest/api/3/issue/10009",
	}
	if got := extractBrowseURL(ji); got != "https://company.atlassian.net/browse/PROJ-9" {
		t.Errorf("extractBrowseURL = %q, want browse URL", got)
	}

	// Empty Self → "".
	if got := extractBrowseURL(&Issue{Key: "PROJ-9"}); got != "" {
		t.Errorf("extractBrowseURL(empty Self) = %q, want empty", got)
	}
	// Empty Key → "".
	if got := extractBrowseURL(&Issue{Self: "https://x/rest/api/3/issue/1"}); got != "" {
		t.Errorf("extractBrowseURL(empty Key) = %q, want empty", got)
	}
	// Self without a /rest/api/ segment → "".
	if got := extractBrowseURL(&Issue{Key: "PROJ-9", Self: "https://company.atlassian.net/other/path"}); got != "" {
		t.Errorf("extractBrowseURL(no rest path) = %q, want empty", got)
	}
}

// TestIssueToBeadsSetsExternalRefAndPriority exercises IssueToBeads end-to-end,
// which routes through priorityName/statusName/typeName + extractBrowseURL +
// jiraPriorityToNumeric, tying the helpers together on a realistic issue.
func TestIssueToBeadsSetsExternalRefAndPriority(t *testing.T) {
	ji := &Issue{
		ID:   "10001",
		Key:  "PROJ-1",
		Self: "https://company.atlassian.net/rest/api/3/issue/10001",
		Fields: IssueFields{
			Summary:   "Round trip",
			Priority:  &PriorityField{Name: "Lowest"},
			Status:    &StatusField{Name: "Done"},
			IssueType: &IssueTypeField{Name: "Epic"},
		},
	}
	ti := jiraToTrackerIssue(ji, nil)
	conv := (&jiraFieldMapper{}).IssueToBeads(&ti)
	if conv == nil {
		t.Fatal("IssueToBeads returned nil")
	}
	if conv.Issue.Priority != 4 {
		t.Errorf("Priority = %d, want 4 (Lowest)", conv.Issue.Priority)
	}
	if conv.Issue.Status != types.StatusClosed {
		t.Errorf("Status = %q, want closed (Done)", conv.Issue.Status)
	}
	if conv.Issue.IssueType != types.TypeEpic {
		t.Errorf("IssueType = %q, want epic", conv.Issue.IssueType)
	}
	if conv.Issue.ExternalRef == nil || *conv.Issue.ExternalRef != "https://company.atlassian.net/browse/PROJ-1" {
		t.Errorf("ExternalRef = %v, want browse URL", conv.Issue.ExternalRef)
	}
}

// Guard against an unused-import style false positive on the tracker import in
// case future refactors trim usage.
var _ tracker.IssueTracker = (*Tracker)(nil)
