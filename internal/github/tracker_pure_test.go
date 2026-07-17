package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

// emptyConfigStore is a storage.Storage whose GetConfig always reports "no
// value", so Tracker.getConfig falls through to the environment. It embeds the
// interface so unused methods panic if reached — these tests must not touch them.
type emptyConfigStore struct {
	storage.Storage
}

func (emptyConfigStore) GetConfig(_ context.Context, _ string) (string, error) { return "", nil }

// hermeticGitHubEnv isolates config resolution from any ambient repo config so
// getConfig deterministically falls back to the GITHUB_* env vars we set.
func hermeticGitHubEnv(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("BEADS_DIR", "")
	t.Setenv("BEADS_TEST_IGNORE_REPO_CONFIG", "1")
	t.Setenv("HOME", filepath.Join(tmp, "home"))
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_OWNER", "")
	t.Setenv("GITHUB_REPO", "")
	t.Setenv("GITHUB_REPOSITORY", "")
	t.Setenv("GITHUB_API_URL", "")
}

// initTrackerAgainst spins up a test server and returns a Tracker whose client
// targets it (via GITHUB_API_URL), fully initialized. No real network.
func initTrackerAgainst(t *testing.T, handler http.HandlerFunc) (*Tracker, *httptest.Server) {
	t.Helper()
	hermeticGitHubEnv(t)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	t.Setenv("GITHUB_TOKEN", "tok")
	t.Setenv("GITHUB_OWNER", "owner")
	t.Setenv("GITHUB_REPO", "repo")
	t.Setenv("GITHUB_API_URL", srv.URL)

	tr := &Tracker{}
	if err := tr.Init(context.Background(), emptyConfigStore{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return tr, srv
}

func TestTracker_Identity(t *testing.T) {
	tr := &Tracker{}
	if tr.Name() != "github" || tr.DisplayName() != "GitHub" || tr.ConfigPrefix() != "github" {
		t.Errorf("identity methods: %q %q %q", tr.Name(), tr.DisplayName(), tr.ConfigPrefix())
	}
	if err := tr.Close(); err != nil {
		t.Errorf("Close() = %v", err)
	}
	// Validate before Init fails; FieldMapper is always constructible.
	if err := tr.Validate(); err == nil {
		t.Error("Validate() before Init should error")
	}
	if tr.FieldMapper() == nil {
		t.Error("FieldMapper() returned nil")
	}
}

func TestTracker_Init_Errors(t *testing.T) {
	ctx := context.Background()

	t.Run("missing token", func(t *testing.T) {
		hermeticGitHubEnv(t)
		if err := (&Tracker{}).Init(ctx, emptyConfigStore{}); err == nil {
			t.Fatal("expected error for missing token")
		}
	})

	t.Run("missing owner", func(t *testing.T) {
		hermeticGitHubEnv(t)
		t.Setenv("GITHUB_TOKEN", "tok")
		t.Setenv("GITHUB_REPO", "repo")
		if err := (&Tracker{}).Init(ctx, emptyConfigStore{}); err == nil {
			t.Fatal("expected error for missing owner")
		}
	})

	t.Run("missing repo", func(t *testing.T) {
		hermeticGitHubEnv(t)
		t.Setenv("GITHUB_TOKEN", "tok")
		t.Setenv("GITHUB_OWNER", "owner")
		if err := (&Tracker{}).Init(ctx, emptyConfigStore{}); err == nil {
			t.Fatal("expected error for missing repo")
		}
	})

	t.Run("combined owner/repo via GITHUB_REPOSITORY", func(t *testing.T) {
		hermeticGitHubEnv(t)
		t.Setenv("GITHUB_TOKEN", "tok")
		t.Setenv("GITHUB_REPOSITORY", "octo/hello")
		tr := &Tracker{}
		if err := tr.Init(ctx, emptyConfigStore{}); err != nil {
			t.Fatalf("Init with combined repo: %v", err)
		}
		if err := tr.Validate(); err != nil {
			t.Errorf("Validate after Init: %v", err)
		}
	})
}

func TestTracker_FetchIssues(t *testing.T) {
	tr, _ := initTrackerAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, []Issue{{Number: 1, Title: "a", State: "open"}})
	})
	got, err := tr.FetchIssues(context.Background(), tracker.FetchOptions{})
	if err != nil {
		t.Fatalf("FetchIssues: %v", err)
	}
	if len(got) != 1 || got[0].Identifier != "1" {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestTracker_FetchIssue(t *testing.T) {
	tr, _ := initTrackerAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, Issue{Number: 9, Title: "one"})
	})

	got, err := tr.FetchIssue(context.Background(), "9")
	if err != nil {
		t.Fatalf("FetchIssue: %v", err)
	}
	if got == nil || got.Identifier != "9" {
		t.Fatalf("unexpected issue: %+v", got)
	}

	// Non-numeric identifier is rejected before any request.
	if _, err := tr.FetchIssue(context.Background(), "not-a-number"); err == nil {
		t.Error("expected error for non-numeric identifier")
	}
}

func TestTracker_CreateAndUpdateIssue(t *testing.T) {
	tr, _ := initTrackerAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			writeJSON(t, w, Issue{Number: 100, Title: "created"})
		default:
			writeJSON(t, w, Issue{Number: 100, State: "closed"})
		}
	})
	ctx := context.Background()

	created, err := tr.CreateIssue(ctx, &types.Issue{Title: "created", Description: "b"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if created.Identifier != "100" {
		t.Errorf("created identifier = %q", created.Identifier)
	}

	updated, err := tr.UpdateIssue(ctx, "100", &types.Issue{Status: types.StatusClosed})
	if err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	if updated.State != "closed" {
		t.Errorf("updated state = %q", updated.State)
	}

	// Non-numeric external id is rejected before any request.
	if _, err := tr.UpdateIssue(ctx, "bad", &types.Issue{}); err == nil {
		t.Error("expected error for non-numeric external id")
	}
}
