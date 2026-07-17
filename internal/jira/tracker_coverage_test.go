package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

// --- Tracker project-key accessors -----------------------------------------

func TestProjectKeyAccessors(t *testing.T) {
	tr := &Tracker{}

	// Empty state.
	if got := tr.ProjectKeys(); len(got) != 0 {
		t.Errorf("ProjectKeys() on empty tracker = %v, want empty", got)
	}
	if got := tr.PrimaryProjectKey(); got != "" {
		t.Errorf("PrimaryProjectKey() on empty tracker = %q, want \"\"", got)
	}

	// After SetProjectKeys.
	tr.SetProjectKeys([]string{"ALPHA", "BETA"})
	if got := tr.ProjectKeys(); len(got) != 2 || got[0] != "ALPHA" || got[1] != "BETA" {
		t.Errorf("ProjectKeys() = %v, want [ALPHA BETA]", got)
	}
	if got := tr.PrimaryProjectKey(); got != "ALPHA" {
		t.Errorf("PrimaryProjectKey() = %q, want ALPHA", got)
	}
}

// --- Validate / Close -------------------------------------------------------

func TestValidate(t *testing.T) {
	// Uninitialized (nil client) → error.
	uninit := &Tracker{}
	if err := uninit.Validate(); err == nil {
		t.Error("Validate() on uninitialized tracker = nil, want error")
	}

	// Initialized (client set) → nil.
	inited := &Tracker{client: NewClient("https://x.atlassian.net", "u", "t")}
	if err := inited.Validate(); err != nil {
		t.Errorf("Validate() on initialized tracker = %v, want nil", err)
	}
}

func TestClose(t *testing.T) {
	tr := &Tracker{}
	if err := tr.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

// --- FetchIssue -------------------------------------------------------------

func TestFetchIssueReturnsMappedIssue(t *testing.T) {
	const key = "PROJ-7"
	issuePath := "/rest/api/3/issue/" + key

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == issuePath {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(Issue{
				ID:  "10007",
				Key: key,
				Fields: IssueFields{
					Summary:  "A fetched issue",
					Status:   &StatusField{Name: "To Do"},
					Priority: &PriorityField{Name: "High"},
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
		t.Fatal("FetchIssue returned nil issue")
	}
	if ti.Identifier != key {
		t.Errorf("Identifier = %q, want %q", ti.Identifier, key)
	}
	if ti.Title != "A fetched issue" {
		t.Errorf("Title = %q, want %q", ti.Title, "A fetched issue")
	}
	if ti.Priority != 1 { // "High" → 1
		t.Errorf("Priority = %d, want 1", ti.Priority)
	}
}

func TestFetchIssuePropagatesClientError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	tr := newTrackerWithServer(srv.URL, "3")
	if _, err := tr.FetchIssue(context.Background(), "PROJ-1"); err == nil {
		t.Error("FetchIssue on server error = nil, want error")
	}
}

// --- CreateIssue ------------------------------------------------------------

func TestCreateIssueSetsProjectAndReturnsIssue(t *testing.T) {
	const key = "ALPHA-100"
	createPath := "/rest/api/3/issue"
	getPath := createPath + "/" + key

	var capturedProjectKey string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == createPath:
			var payload struct {
				Fields map[string]interface{} `json:"fields"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if proj, ok := payload.Fields["project"].(map[string]interface{}); ok {
				capturedProjectKey, _ = proj["key"].(string)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id":   "10100",
				"key":  key,
				"self": srv0(r) + getPath,
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
	tr.projectKeys = []string{"ALPHA", "BETA"}

	ti, err := tr.CreateIssue(context.Background(), &types.Issue{
		Title:     "Created issue",
		IssueType: types.TypeTask,
		Priority:  2,
	})
	if err != nil {
		t.Fatalf("CreateIssue error: %v", err)
	}
	if ti == nil {
		t.Fatal("CreateIssue returned nil")
	}
	if capturedProjectKey != "ALPHA" {
		t.Errorf("project key sent = %q, want ALPHA (primary)", capturedProjectKey)
	}
	if ti.Identifier != key {
		t.Errorf("Identifier = %q, want %q", ti.Identifier, key)
	}
}

func TestCreateIssuePropagatesClientError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	tr := newTrackerWithServer(srv.URL, "3")
	tr.projectKeys = []string{"ALPHA"}
	if _, err := tr.CreateIssue(context.Background(), &types.Issue{Title: "x"}); err == nil {
		t.Error("CreateIssue on server error = nil, want error")
	}
}

// srv0 returns the scheme+host of the request's server for building self URLs.
func srv0(r *http.Request) string {
	return "http://" + r.Host
}

// --- applyTransition: no matching transition path ---------------------------

func TestApplyTransitionNoMatchingTransition(t *testing.T) {
	const key = "PROJ-9"
	transitionsPath := "/rest/api/3/issue/" + key + "/transitions"

	var transitionPosted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == transitionsPath:
			w.Header().Set("Content-Type", "application/json")
			// Available transitions do NOT include one leading to "Done".
			_ = json.NewEncoder(w).Encode(TransitionsResult{
				Transitions: []Transition{
					{ID: "11", Name: "Start Progress", To: StatusField{Name: "In Progress"}},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == transitionsPath:
			transitionPosted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	tr := newTrackerWithServer(srv.URL, "3")
	// Closed maps to "Done"; no transition targets Done → silent success, no POST.
	if err := tr.applyTransition(context.Background(), key, types.StatusClosed); err != nil {
		t.Fatalf("applyTransition error: %v", err)
	}
	if transitionPosted {
		t.Error("transition POST issued when no matching transition existed")
	}
}

func TestApplyTransitionPropagatesTransitionsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	tr := newTrackerWithServer(srv.URL, "3")
	if err := tr.applyTransition(context.Background(), "PROJ-1", types.StatusClosed); err == nil {
		t.Error("applyTransition on transitions fetch error = nil, want error")
	}
}

// --- jiraPriorityToNumeric --------------------------------------------------

func TestJiraPriorityToNumericDefaults(t *testing.T) {
	tests := []struct {
		name string
		want int
	}{
		{"Highest", 0},
		{"High", 1},
		{"Medium", 2},
		{"Low", 3},
		{"Lowest", 4},
		{"Blocker", 2}, // unknown → default 2
		{"", 2},        // empty → default 2
		{"HIGHEST", 0}, // case-insensitive
	}
	for _, tt := range tests {
		if got := jiraPriorityToNumeric(tt.name, nil); got != tt.want {
			t.Errorf("jiraPriorityToNumeric(%q, nil) = %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestJiraPriorityToNumericCustomMap(t *testing.T) {
	priorityMap := map[string]string{
		"0": "Critical",
		"3": "Trivial",
	}
	// Custom map hit (case-insensitive).
	if got := jiraPriorityToNumeric("critical", priorityMap); got != 0 {
		t.Errorf("jiraPriorityToNumeric(critical, map) = %d, want 0", got)
	}
	if got := jiraPriorityToNumeric("Trivial", priorityMap); got != 3 {
		t.Errorf("jiraPriorityToNumeric(Trivial, map) = %d, want 3", got)
	}
	// Name not in custom map → falls through to defaults.
	if got := jiraPriorityToNumeric("High", priorityMap); got != 1 {
		t.Errorf("jiraPriorityToNumeric(High, map) = %d, want 1 (default fallthrough)", got)
	}
}

func TestJiraPriorityToNumericInvalidMapValueFallsThrough(t *testing.T) {
	// Custom map maps to a non-numeric / out-of-range beads key → ignored, defaults apply.
	priorityMap := map[string]string{
		"notanumber": "High",
		"9":          "Low", // out of 0..4 range
	}
	if got := jiraPriorityToNumeric("High", priorityMap); got != 1 {
		t.Errorf("jiraPriorityToNumeric(High) with bad map key = %d, want 1 (default)", got)
	}
	if got := jiraPriorityToNumeric("Low", priorityMap); got != 3 {
		t.Errorf("jiraPriorityToNumeric(Low) with out-of-range map key = %d, want 3 (default)", got)
	}
}

// --- fieldmapper field extraction + TypeToBeads + extractBrowseURL ----------

func TestTypeToBeadsAllBranches(t *testing.T) {
	m := &jiraFieldMapper{}
	tests := []struct {
		in   interface{}
		want types.IssueType
	}{
		{"Bug", types.TypeBug},
		{"Story", types.TypeFeature},
		{"Feature", types.TypeFeature},
		{"Epic", types.TypeEpic},
		{"Task", types.TypeTask},
		{"Sub-task", types.TypeTask},
		{"Unknown Type", types.TypeTask}, // default
		{42, types.TypeTask},             // non-string → default
	}
	for _, tt := range tests {
		if got := m.TypeToBeads(tt.in); got != tt.want {
			t.Errorf("TypeToBeads(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestTypeToBeadsCustomMapWins(t *testing.T) {
	m := &jiraFieldMapper{typeMap: map[string]string{"story": "User Story"}}
	if got := m.TypeToBeads("User Story"); got != types.IssueType("story") {
		t.Errorf("TypeToBeads(User Story) = %q, want story (custom map)", got)
	}
}

func TestFieldExtractionHelpers(t *testing.T) {
	// nil fields → empty strings.
	empty := &Issue{}
	if got := priorityName(empty); got != "" {
		t.Errorf("priorityName(empty) = %q, want \"\"", got)
	}
	if got := statusName(empty); got != "" {
		t.Errorf("statusName(empty) = %q, want \"\"", got)
	}
	if got := typeName(empty); got != "" {
		t.Errorf("typeName(empty) = %q, want \"\"", got)
	}

	// populated fields.
	full := &Issue{Fields: IssueFields{
		Priority:  &PriorityField{Name: "High"},
		Status:    &StatusField{Name: "Done"},
		IssueType: &IssueTypeField{Name: "Bug"},
	}}
	if got := priorityName(full); got != "High" {
		t.Errorf("priorityName(full) = %q, want High", got)
	}
	if got := statusName(full); got != "Done" {
		t.Errorf("statusName(full) = %q, want Done", got)
	}
	if got := typeName(full); got != "Bug" {
		t.Errorf("typeName(full) = %q, want Bug", got)
	}
}

func TestExtractBrowseURL(t *testing.T) {
	tests := []struct {
		name string
		self string
		key  string
		want string
	}{
		{
			name: "valid",
			self: "https://company.atlassian.net/rest/api/3/issue/10001",
			key:  "PROJ-123",
			want: "https://company.atlassian.net/browse/PROJ-123",
		},
		{"empty self", "", "PROJ-1", ""},
		{"empty key", "https://x.atlassian.net/rest/api/3/issue/1", "", ""},
		{"no rest api marker", "https://x.atlassian.net/weird/1", "PROJ-1", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ji := &Issue{Self: tt.self, Key: tt.key}
			if got := extractBrowseURL(ji); got != tt.want {
				t.Errorf("extractBrowseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- projectKeyFromIssue ----------------------------------------------------

func TestProjectKeyFromIssue(t *testing.T) {
	// From explicit project field.
	withProject := &Issue{
		Key:    "PROJ-5",
		Fields: IssueFields{Project: &ProjectField{Key: "EXPLICIT"}},
	}
	if got := projectKeyFromIssue(withProject); got != "EXPLICIT" {
		t.Errorf("projectKeyFromIssue(withProject) = %q, want EXPLICIT", got)
	}

	// Fallback: derive from key.
	fromKey := &Issue{Key: "DERIVED-42"}
	if got := projectKeyFromIssue(fromKey); got != "DERIVED" {
		t.Errorf("projectKeyFromIssue(fromKey) = %q, want DERIVED", got)
	}

	// No project, no dash in key → "".
	noKey := &Issue{Key: "nodash"}
	if got := projectKeyFromIssue(noKey); got != "" {
		t.Errorf("projectKeyFromIssue(noKey) = %q, want \"\"", got)
	}
}

// --- FetchIssues: multi-project + state + incremental JQL branches ----------

func TestFetchIssuesMultiProjectAndClosedState(t *testing.T) {
	var capturedJQL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/api/3/search/jql" {
			capturedJQL = r.URL.Query().Get("jql")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"issues": []Issue{
					{ID: "1", Key: "ALPHA-1", Fields: IssueFields{Summary: "one", Status: &StatusField{Name: "Done"}}},
				},
				"total": 1,
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	tr := newTrackerWithServer(srv.URL, "3")
	tr.store = &configStore{data: map[string]string{}}
	tr.projectKeys = []string{"ALPHA", "BETA"}

	issues, err := tr.FetchIssues(context.Background(), tracker.FetchOptions{State: "closed"})
	if err != nil {
		t.Fatalf("FetchIssues error: %v", err)
	}
	if len(issues) != 1 || issues[0].Identifier != "ALPHA-1" {
		t.Errorf("FetchIssues returned %d issues, want 1 (ALPHA-1)", len(issues))
	}
	if !strings.Contains(capturedJQL, "project IN (") {
		t.Errorf("multi-project JQL should use IN clause, got: %s", capturedJQL)
	}
	if !strings.Contains(capturedJQL, "statusCategory = Done") {
		t.Errorf("closed-state JQL should filter statusCategory = Done, got: %s", capturedJQL)
	}
}

func TestFetchIssuesOpenStateAndSince(t *testing.T) {
	var capturedJQL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/api/3/search/jql" {
			capturedJQL = r.URL.Query().Get("jql")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"issues": []Issue{}, "total": 0})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	tr := newTrackerWithServer(srv.URL, "3")
	tr.store = &configStore{data: map[string]string{}}
	tr.projectKeys = []string{"SOLO"}

	since := time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)
	_, err := tr.FetchIssues(context.Background(), tracker.FetchOptions{State: "open", Since: &since})
	if err != nil {
		t.Fatalf("FetchIssues error: %v", err)
	}
	if !strings.Contains(capturedJQL, `project = "SOLO"`) {
		t.Errorf("single-project JQL should use equality, got: %s", capturedJQL)
	}
	if !strings.Contains(capturedJQL, "statusCategory != Done") {
		t.Errorf("open-state JQL should filter statusCategory != Done, got: %s", capturedJQL)
	}
	if !strings.Contains(capturedJQL, "updated >=") {
		t.Errorf("incremental JQL should include updated >= filter, got: %s", capturedJQL)
	}
}

func TestFetchIssuesPropagatesSearchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	tr := newTrackerWithServer(srv.URL, "3")
	tr.store = &configStore{data: map[string]string{}}
	tr.projectKeys = []string{"SOLO"}
	if _, err := tr.FetchIssues(context.Background(), tracker.FetchOptions{}); err == nil {
		t.Error("FetchIssues on search error = nil, want error")
	}
}
