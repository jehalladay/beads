package notion

import (
	"context"
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// pullStore is a storage.Storage fake exposing GetConfig + SearchIssues, the
// only methods the covered tracker paths invoke. Other methods are inherited
// from the embedded nil interface and never called.
type pullStore struct {
	storage.Storage
	token     string
	tokenErr  error
	issues    []*types.Issue
	searchErr error
}

func (s pullStore) GetConfig(_ context.Context, key string) (string, error) {
	if key == configKeyToken {
		return s.token, s.tokenErr
	}
	return "", nil
}

func (s pullStore) SearchIssues(_ context.Context, _ string, _ types.IssueFilter) ([]*types.Issue, error) {
	return s.issues, s.searchErr
}

func TestInit_Branches(t *testing.T) {
	ctx := context.Background()

	t.Run("missing auth -> error", func(t *testing.T) {
		t.Setenv("NOTION_TOKEN", "")
		t.Setenv("NOTION_DATA_SOURCE_ID", "ds-1")
		tr := &Tracker{}
		err := tr.Init(ctx, pullStore{})
		if err == nil {
			t.Fatal("expected auth-not-configured error")
		}
	})

	t.Run("missing data source -> error", func(t *testing.T) {
		t.Setenv("NOTION_TOKEN", "tok")
		t.Setenv("NOTION_DATA_SOURCE_ID", "")
		tr := &Tracker{}
		err := tr.Init(ctx, pullStore{})
		if err == nil {
			t.Fatal("expected data-source-not-configured error")
		}
	})

	t.Run("success sets auth source, client, and default config", func(t *testing.T) {
		t.Setenv("NOTION_TOKEN", "tok")
		t.Setenv("NOTION_DATA_SOURCE_ID", "ds-1")
		tr := &Tracker{client: &fakeAPI{}} // pre-set client avoids a real client
		if err := tr.Init(ctx, pullStore{}); err != nil {
			t.Fatalf("Init: %v", err)
		}
		if tr.dataSourceID != "ds-1" {
			t.Errorf("dataSourceID = %q, want ds-1", tr.dataSourceID)
		}
		if tr.authSource != AuthSourceEnv {
			t.Errorf("authSource = %v, want env", tr.authSource)
		}
		if tr.config == nil {
			t.Error("expected default config to be set")
		}
	})

	t.Run("token from store sets config-token auth source", func(t *testing.T) {
		t.Setenv("NOTION_TOKEN", "")
		t.Setenv("NOTION_DATA_SOURCE_ID", "ds-1")
		tr := &Tracker{client: &fakeAPI{}}
		if err := tr.Init(ctx, pullStore{token: "cfg-tok"}); err != nil {
			t.Fatalf("Init: %v", err)
		}
		if tr.authSource != AuthSourceConfigToken {
			t.Errorf("authSource = %v, want config-token", tr.authSource)
		}
	})
}

func TestBuildLocalPullIndexes_Branches(t *testing.T) {
	ctx := context.Background()

	t.Run("nil store -> empty indexes", func(t *testing.T) {
		tr := &Tracker{}
		byExt, byID, err := tr.buildLocalPullIndexes(ctx)
		if err != nil || len(byExt) != 0 || len(byID) != 0 {
			t.Fatalf("nil store = (%v,%v,%v), want empty,empty,nil", byExt, byID, err)
		}
	})

	t.Run("search error is wrapped", func(t *testing.T) {
		tr := &Tracker{store: pullStore{searchErr: errors.New("boom")}}
		if _, _, err := tr.buildLocalPullIndexes(ctx); err == nil {
			t.Fatal("expected wrapped search error")
		}
	})

	t.Run("indexes by ID and by external identifier", func(t *testing.T) {
		ref := "https://www.notion.so/0123456789abcdef0123456789abcdef"
		issues := []*types.Issue{
			nil, // skipped
			{ID: "bd-1"},
			{ID: "bd-2", ExternalRef: &ref},
			{ID: "bd-3", ExternalRef: strptr("not-a-notion-ref")}, // no identifier extracted
		}
		tr := &Tracker{store: pullStore{issues: issues}}
		byExt, byID, err := tr.buildLocalPullIndexes(ctx)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		for _, id := range []string{"bd-1", "bd-2", "bd-3"} {
			if _, ok := byID[id]; !ok {
				t.Errorf("byID missing %q", id)
			}
		}
		if len(byExt) != 1 {
			t.Fatalf("byExt = %v, want exactly one (bd-2's notion ref)", byExt)
		}
	})
}

func strptr(s string) *string { return &s }

func TestUpdateIssue_ErrorBranches(t *testing.T) {
	ctx := context.Background()
	tr := &Tracker{client: &fakeAPI{}, config: DefaultMappingConfig(), dataSourceID: "ds-1"}

	t.Run("empty external ID and no ExternalRef -> invalid page id error", func(t *testing.T) {
		_, err := tr.UpdateIssue(ctx, "", &types.Issue{ID: "bd-1"})
		if err == nil {
			t.Fatal("expected invalid page id error")
		}
	})

	t.Run("empty external ID with unusable ExternalRef -> invalid page id error", func(t *testing.T) {
		_, err := tr.UpdateIssue(ctx, "", &types.Issue{ID: "bd-1", ExternalRef: strptr("not-a-page")})
		if err == nil {
			t.Fatal("expected invalid page id error (ExternalRef yields no page id)")
		}
	})
}
