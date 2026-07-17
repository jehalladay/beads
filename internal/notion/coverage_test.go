package notion

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	itracker "github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

// --- FieldMapper (fieldmapper.go was entirely uncovered) ---

// TestFieldMapperNilConfigUsesDefault verifies NewFieldMapper substitutes the
// default mapping when passed a nil config.
func TestFieldMapperNilConfigUsesDefault(t *testing.T) {
	t.Parallel()
	m := NewFieldMapper(nil)
	if m == nil || m.config == nil {
		t.Fatal("NewFieldMapper(nil) must supply a non-nil default config")
	}
	// A default config maps "high" -> 1.
	if got := m.PriorityToBeads("high"); got != 1 {
		t.Fatalf("PriorityToBeads(high) = %d, want 1", got)
	}
}

// TestFieldMapperScalarConversions drives every scalar mapper method through a
// hit, a fallback (unknown value), and the non-string type-assertion fallback.
func TestFieldMapperScalarConversions(t *testing.T) {
	t.Parallel()
	m := NewFieldMapper(DefaultMappingConfig())

	// Priority
	if got := m.PriorityToBeads("critical"); got != 0 {
		t.Errorf("PriorityToBeads(critical) = %d, want 0", got)
	}
	if got := m.PriorityToBeads("nonsense"); got != 2 {
		t.Errorf("PriorityToBeads(unknown) = %d, want default 2", got)
	}
	if got := m.PriorityToBeads(42); got != 2 {
		t.Errorf("PriorityToBeads(non-string) = %d, want default 2", got)
	}
	if got := m.PriorityToTracker(1); got != "High" {
		t.Errorf("PriorityToTracker(1) = %v, want High", got)
	}

	// Status
	if got := m.StatusToBeads("closed"); got != types.StatusClosed {
		t.Errorf("StatusToBeads(closed) = %v, want closed", got)
	}
	if got := m.StatusToBeads("nonsense"); got != types.StatusOpen {
		t.Errorf("StatusToBeads(unknown) = %v, want open", got)
	}
	if got := m.StatusToBeads(nil); got != types.StatusOpen {
		t.Errorf("StatusToBeads(non-string) = %v, want open", got)
	}
	if got := m.StatusToTracker(types.StatusInProgress); got != "In Progress" {
		t.Errorf("StatusToTracker(in_progress) = %v, want 'In Progress'", got)
	}

	// Type
	if got := m.TypeToBeads("bug"); got != types.TypeBug {
		t.Errorf("TypeToBeads(bug) = %v, want bug", got)
	}
	if got := m.TypeToBeads("nonsense"); got != types.TypeTask {
		t.Errorf("TypeToBeads(unknown) = %v, want task", got)
	}
	if got := m.TypeToBeads(3.14); got != types.TypeTask {
		t.Errorf("TypeToBeads(non-string) = %v, want task", got)
	}
	if got := m.TypeToTracker(types.TypeEpic); got != "Epic" {
		t.Errorf("TypeToTracker(epic) = %v, want Epic", got)
	}
}

// TestFieldMapperIssueToBeads covers the *PulledIssue, value PulledIssue, nil,
// and unrecognized-Raw branches of IssueToBeads.
func TestFieldMapperIssueToBeads(t *testing.T) {
	t.Parallel()
	m := NewFieldMapper(DefaultMappingConfig())

	if m.IssueToBeads(nil) != nil {
		t.Error("IssueToBeads(nil) should be nil")
	}

	pulled := PulledIssue{ID: "bd-1", Title: "Task one", Status: "open", Priority: "medium", Type: "task"}

	ptrConv := m.IssueToBeads(&itracker.TrackerIssue{Raw: &pulled})
	if ptrConv == nil || ptrConv.Issue == nil || ptrConv.Issue.Title != "Task one" {
		t.Fatalf("IssueToBeads(*PulledIssue) = %+v, want issue with title 'Task one'", ptrConv)
	}

	valConv := m.IssueToBeads(&itracker.TrackerIssue{Raw: pulled})
	if valConv == nil || valConv.Issue == nil || valConv.Issue.Title != "Task one" {
		t.Fatalf("IssueToBeads(PulledIssue) = %+v, want issue with title 'Task one'", valConv)
	}

	if got := m.IssueToBeads(&itracker.TrackerIssue{Raw: "not-a-pulled-issue"}); got != nil {
		t.Errorf("IssueToBeads(unknown Raw) = %+v, want nil", got)
	}
}

// TestFieldMapperIssueToTracker covers the success path (map assembly) and the
// error path (unsupported issue type -> empty map).
func TestFieldMapperIssueToTracker(t *testing.T) {
	t.Parallel()
	m := NewFieldMapper(DefaultMappingConfig())

	issue := &types.Issue{
		ID:        "bd-9",
		Title:     "Ship it",
		Status:    types.StatusOpen,
		Priority:  1,
		IssueType: types.TypeFeature,
		Labels:    []string{"a", "b"},
	}
	out := m.IssueToTracker(issue)
	if out["id"] != "bd-9" || out["title"] != "Ship it" {
		t.Fatalf("IssueToTracker = %+v, want id/title populated", out)
	}
	if out["status"] != "Open" || out["priority"] != "High" || out["issue_type"] != "Feature" {
		t.Fatalf("IssueToTracker mapped fields = %+v", out)
	}

	// "decision" is not in the default TypeToNotion map -> PushIssueFromIssue
	// rejects it, exercising the error branch that returns an empty map.
	bad := &types.Issue{ID: "bd-10", IssueType: types.TypeDecision}
	if got := m.IssueToTracker(bad); len(got) != 0 {
		t.Errorf("IssueToTracker(unsupported type) = %+v, want empty map", got)
	}
}

// --- refs.go (BuildNotionExternalRef fallbacks) ---

// TestBuildNotionExternalRefBranches covers the nil guard and each successive
// fallback source in BuildNotionExternalRef.
func TestBuildNotionExternalRefBranches(t *testing.T) {
	t.Parallel()
	const canonical = "https://www.notion.so/0123456789abcdef0123456789abcdef"

	if got := BuildNotionExternalRef(nil); got != "" {
		t.Errorf("BuildNotionExternalRef(nil) = %q, want empty", got)
	}

	tests := []struct {
		name  string
		issue *PulledIssue
		want  string
	}{
		{
			name:  "from external ref page id",
			issue: &PulledIssue{ExternalRef: "0123456789abcdef0123456789abcdef"},
			want:  canonical,
		},
		{
			name:  "from notion page id when external ref empty",
			issue: &PulledIssue{NotionPageID: "0123456789abcdef0123456789abcdef"},
			want:  canonical,
		},
		{
			name:  "from external-ref URL fallback",
			issue: &PulledIssue{ExternalRef: "https://www.notion.so/Task-0123456789abcdef0123456789abcdef"},
			want:  canonical,
		},
		{
			name:  "no resolvable id",
			issue: &PulledIssue{ExternalRef: "not-a-page", NotionPageID: ""},
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildNotionExternalRef(tt.issue); got != tt.want {
				t.Errorf("BuildNotionExternalRef = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- types.go NullableString.UnmarshalJSON ---

// TestNullableStringUnmarshalJSON covers the null branch, a normal string, and
// the decode-error branch.
func TestNullableStringUnmarshalJSON(t *testing.T) {
	t.Parallel()

	var s NullableString
	if err := json.Unmarshal([]byte("null"), &s); err != nil {
		t.Fatalf("unmarshal null: %v", err)
	}
	if s != "" {
		t.Errorf("null -> %q, want empty", s)
	}

	if err := json.Unmarshal([]byte(`"hello"`), &s); err != nil {
		t.Fatalf("unmarshal string: %v", err)
	}
	if s != "hello" {
		t.Errorf("string -> %q, want hello", s)
	}

	if err := json.Unmarshal([]byte(`{bad`), &s); err == nil {
		t.Error("unmarshal invalid JSON should error")
	}
}

// --- auth.go ResolveAuth ---

// configStore is a storage.Storage whose only meaningful method is GetConfig;
// every other method is inherited from the embedded interface and never called
// by ResolveAuth. A nil embedded interface is safe because ResolveAuth only
// invokes GetConfig.
type configStore struct {
	storage.Storage
	token string
	err   error
}

func (c configStore) GetConfig(context.Context, string) (string, error) {
	return c.token, c.err
}

func TestResolveAuth(t *testing.T) {
	ctx := context.Background()

	t.Run("config token wins", func(t *testing.T) {
		t.Setenv("NOTION_TOKEN", "env-token")
		auth, err := ResolveAuth(ctx, configStore{token: "  cfg-token  "})
		if err != nil {
			t.Fatalf("ResolveAuth error: %v", err)
		}
		if auth == nil || auth.Token != "cfg-token" || auth.Source != AuthSourceConfigToken {
			t.Fatalf("ResolveAuth = %+v, want trimmed config token", auth)
		}
	})

	t.Run("falls back to env when store empty", func(t *testing.T) {
		t.Setenv("NOTION_TOKEN", "env-token")
		auth, err := ResolveAuth(ctx, configStore{token: "   "})
		if err != nil {
			t.Fatalf("ResolveAuth error: %v", err)
		}
		if auth == nil || auth.Token != "env-token" || auth.Source != AuthSourceEnv {
			t.Fatalf("ResolveAuth = %+v, want env token", auth)
		}
	})

	t.Run("nil store falls back to env", func(t *testing.T) {
		t.Setenv("NOTION_TOKEN", "env-only")
		auth, err := ResolveAuth(ctx, nil)
		if err != nil {
			t.Fatalf("ResolveAuth error: %v", err)
		}
		if auth == nil || auth.Source != AuthSourceEnv {
			t.Fatalf("ResolveAuth = %+v, want env source", auth)
		}
	})

	t.Run("no config and no env yields nil", func(t *testing.T) {
		t.Setenv("NOTION_TOKEN", "")
		auth, err := ResolveAuth(ctx, configStore{err: context.Canceled})
		if err != nil {
			t.Fatalf("ResolveAuth error: %v", err)
		}
		if auth != nil {
			t.Fatalf("ResolveAuth = %+v, want nil when nothing configured", auth)
		}
	})
}

// --- client.go write methods + GetCurrentUser + WithHTTPClient ---

func TestClientGetCurrentUser(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/me" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"object":"user","id":"u_1","name":"Ada","type":"person"}`)
	}))
	defer server.Close()

	client := NewClient("tok").WithBaseURL(server.URL)
	user, err := client.GetCurrentUser(context.Background())
	if err != nil {
		t.Fatalf("GetCurrentUser error: %v", err)
	}
	if user.ID != "u_1" || user.Name != "Ada" {
		t.Fatalf("user = %+v", user)
	}
}

func TestClientCreatePage(t *testing.T) {
	t.Parallel()
	var gotBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/pages" {
			t.Fatalf("method/path = %s %q", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = io.WriteString(w, `{"id":"pg_1","url":"https://www.notion.so/pg_1"}`)
	}))
	defer server.Close()

	client := NewClient("tok").WithBaseURL(server.URL)
	page, err := client.CreatePage(context.Background(), "ds_9", map[string]interface{}{"Name": "x"})
	if err != nil {
		t.Fatalf("CreatePage error: %v", err)
	}
	if page.ID != "pg_1" {
		t.Fatalf("page = %+v", page)
	}
	parent, _ := gotBody["parent"].(map[string]interface{})
	if parent["data_source_id"] != "ds_9" {
		t.Fatalf("request parent = %+v, want data_source_id ds_9", parent)
	}
}

func TestClientUpdatePage(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/pages/pg_1" {
			t.Fatalf("method/path = %s %q", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":"pg_1"}`)
	}))
	defer server.Close()

	client := NewClient("tok").WithBaseURL(server.URL)
	page, err := client.UpdatePage(context.Background(), "pg_1", map[string]interface{}{"Status": "done"})
	if err != nil {
		t.Fatalf("UpdatePage error: %v", err)
	}
	if page.ID != "pg_1" {
		t.Fatalf("page = %+v", page)
	}
}

func TestClientArchivePage(t *testing.T) {
	t.Parallel()
	var body map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/pages/pg_2" {
			t.Fatalf("method/path = %s %q", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{"id":"pg_2","in_trash":true}`)
	}))
	defer server.Close()

	client := NewClient("tok").WithBaseURL(server.URL)
	page, err := client.ArchivePage(context.Background(), "pg_2", true)
	if err != nil {
		t.Fatalf("ArchivePage error: %v", err)
	}
	if page.ID != "pg_2" {
		t.Fatalf("page = %+v", page)
	}
	if body["in_trash"] != true {
		t.Fatalf("request body = %+v, want in_trash true", body)
	}
}

// TestClientWithHTTPClient verifies WithHTTPClient returns a clone that uses the
// supplied http.Client (and leaves the original untouched).
func TestClientWithHTTPClient(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"object":"user","id":"u_2"}`)
	}))
	defer server.Close()

	base := NewClient("tok").WithBaseURL(server.URL)
	custom := &http.Client{}
	clone := base.WithHTTPClient(custom)
	if clone.HTTPClient != custom {
		t.Fatal("WithHTTPClient did not set the supplied client")
	}
	if base.HTTPClient == custom {
		t.Fatal("WithHTTPClient mutated the receiver instead of cloning")
	}
	if _, err := clone.GetCurrentUser(context.Background()); err != nil {
		t.Fatalf("clone request error: %v", err)
	}
}

// --- tracker.go accessors + FetchIssue + Close + delegating helpers ---

// TestTrackerAccessors covers the trivial identity accessors and delegating
// helpers that had no direct coverage.
func TestTrackerAccessors(t *testing.T) {
	t.Parallel()
	tr := &Tracker{config: DefaultMappingConfig()}

	if tr.Name() != "notion" {
		t.Errorf("Name() = %q", tr.Name())
	}
	if tr.DisplayName() != "Notion" {
		t.Errorf("DisplayName() = %q", tr.DisplayName())
	}
	if tr.ConfigPrefix() != "notion" {
		t.Errorf("ConfigPrefix() = %q", tr.ConfigPrefix())
	}
	if err := tr.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
	if tr.FieldMapper() == nil {
		t.Error("FieldMapper() = nil")
	}

	const canonical = "https://www.notion.so/0123456789abcdef0123456789abcdef"
	ref := "https://www.notion.so/Task-0123456789abcdef0123456789abcdef"
	if !tr.IsExternalRef(ref) {
		t.Errorf("IsExternalRef(%q) = false, want true", ref)
	}
	if tr.IsExternalRef("https://example.com/x") {
		t.Error("IsExternalRef(non-notion) = true, want false")
	}
	// ExtractIdentifier returns the normalized hyphenated 8-4-4-4-12 form.
	if got := tr.ExtractIdentifier(ref); got != "01234567-89ab-cdef-0123-456789abcdef" {
		t.Errorf("ExtractIdentifier = %q, want hyphenated page id", got)
	}
	if got := tr.BuildExternalRef(&itracker.TrackerIssue{URL: ref}); got != canonical {
		t.Errorf("BuildExternalRef = %q, want %q", got, canonical)
	}
	if got := tr.BuildExternalRef(nil); got != "" {
		t.Errorf("BuildExternalRef(nil) = %q, want empty", got)
	}
}

// TestTrackerFetchIssue drives FetchIssue for a page-id hit, a local-id hit, and
// a miss, exercising the remote-index build via the fakeAPI.
func TestTrackerFetchIssue(t *testing.T) {
	t.Parallel()
	api := &fakeAPI{
		pages: []Page{
			{
				ID:  "01234567-89ab-cdef-0123-456789abcdef",
				URL: "https://www.notion.so/Task-0123456789abcdef0123456789abcdef",
				Properties: map[string]PageProperty{
					PropertyTitle:    {Title: []RichText{{PlainText: "Findable"}}},
					PropertyBeadsID:  {RichText: []RichText{{PlainText: "bd-77"}}},
					PropertyStatus:   {Select: &SelectOption{Name: "Open"}},
					PropertyPriority: {Select: &SelectOption{Name: "Medium"}},
					PropertyType:     {Select: &SelectOption{Name: "Task"}},
				},
			},
		},
	}
	tr := &Tracker{client: api, config: DefaultMappingConfig(), dataSourceID: "ds_1"}
	ctx := context.Background()

	byPage, err := tr.FetchIssue(ctx, "01234567-89ab-cdef-0123-456789abcdef")
	if err != nil {
		t.Fatalf("FetchIssue(page id) error: %v", err)
	}
	if byPage == nil || byPage.Title != "Findable" {
		t.Fatalf("FetchIssue(page id) = %+v, want Findable", byPage)
	}

	byLocal, err := tr.FetchIssue(ctx, "bd-77")
	if err != nil {
		t.Fatalf("FetchIssue(local id) error: %v", err)
	}
	if byLocal == nil || byLocal.Title != "Findable" {
		t.Fatalf("FetchIssue(local id) = %+v, want Findable", byLocal)
	}

	miss, err := tr.FetchIssue(ctx, "bd-does-not-exist")
	if err != nil {
		t.Fatalf("FetchIssue(miss) error: %v", err)
	}
	if miss != nil {
		t.Fatalf("FetchIssue(miss) = %+v, want nil", miss)
	}
}

// TestTrackerValidate covers the not-initialized guard and the success path.
func TestTrackerValidate(t *testing.T) {
	t.Parallel()

	uninit := &Tracker{}
	if err := uninit.Validate(); err == nil {
		t.Error("Validate() on uninitialized tracker should error")
	}

	ok := &Tracker{client: &fakeAPI{dataSource: &DataSource{ID: "ds_1"}}, dataSourceID: "ds_1"}
	if err := ok.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}
