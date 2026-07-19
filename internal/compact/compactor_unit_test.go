package compact

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

type stubStore struct {
	checkEligibilityFn func(context.Context, string, int) (bool, string, error)
	getIssueFn         func(context.Context, string) (*types.Issue, error)
	snapshotIssueFn    func(context.Context, string, int) error
	updateIssueFn      func(context.Context, string, map[string]interface{}, string) error
	applyCompactionFn  func(context.Context, string, int, int, int, string) error
	compactOverwriteFn func(context.Context, string, map[string]interface{}, int, int, string, string) error
	addCommentFn       func(context.Context, string, string, string) error
}

func (s *stubStore) CheckEligibility(ctx context.Context, issueID string, tier int) (bool, string, error) {
	if s.checkEligibilityFn != nil {
		return s.checkEligibilityFn(ctx, issueID, tier)
	}
	return false, "", nil
}

func (s *stubStore) GetIssue(ctx context.Context, issueID string) (*types.Issue, error) {
	if s.getIssueFn != nil {
		return s.getIssueFn(ctx, issueID)
	}
	return nil, fmt.Errorf("GetIssue not stubbed")
}

func (s *stubStore) SnapshotIssue(ctx context.Context, issueID string, tier int) error {
	if s.snapshotIssueFn != nil {
		return s.snapshotIssueFn(ctx, issueID, tier)
	}
	return nil
}

func (s *stubStore) UpdateIssue(ctx context.Context, issueID string, updates map[string]interface{}, actor string) error {
	if s.updateIssueFn != nil {
		return s.updateIssueFn(ctx, issueID, updates, actor)
	}
	return nil
}

func (s *stubStore) ApplyCompaction(ctx context.Context, issueID string, tier int, originalSize int, compactedSize int, commitHash string, _ string) error {
	if s.applyCompactionFn != nil {
		return s.applyCompactionFn(ctx, issueID, tier, originalSize, compactedSize, commitHash)
	}
	return nil
}

func (s *stubStore) CompactOverwrite(ctx context.Context, issueID string, updates map[string]interface{}, tier int, originalSize int, commitHash string, actor string) error {
	if s.compactOverwriteFn != nil {
		return s.compactOverwriteFn(ctx, issueID, updates, tier, originalSize, commitHash, actor)
	}
	return nil
}

func (s *stubStore) AddComment(ctx context.Context, issueID, actor, comment string) error {
	if s.addCommentFn != nil {
		return s.addCommentFn(ctx, issueID, actor, comment)
	}
	return nil
}

type stubSummarizer struct {
	summary string
	err     error
	calls   int
	mu      sync.Mutex
}

func (s *stubSummarizer) SummarizeTier1(ctx context.Context, issue *types.Issue) (string, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return s.summary, s.err
}

func (s *stubSummarizer) getCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func stubIssue() *types.Issue {
	return &types.Issue{
		ID:                 "bd-123",
		Title:              "Fix login",
		Description:        strings.Repeat("A", 20),
		Design:             strings.Repeat("B", 10),
		Notes:              strings.Repeat("C", 5),
		AcceptanceCriteria: "done",
		Status:             types.StatusClosed,
	}
}

func withGitHash(t *testing.T, hash string) func() {
	orig := gitExec
	gitExec = func(string, ...string) ([]byte, error) {
		return []byte(hash), nil
	}
	return func() { gitExec = orig }
}

func TestCompactTier1_Success(t *testing.T) {
	cleanup := withGitHash(t, "deadbeef\n")
	t.Cleanup(cleanup)

	overwriteCalled := false
	store := &stubStore{
		checkEligibilityFn: func(context.Context, string, int) (bool, string, error) { return true, "", nil },
		getIssueFn:         func(context.Context, string) (*types.Issue, error) { return stubIssue(), nil },
		// CompactTier1 now applies the overwrite + compaction mark ATOMICALLY via
		// CompactOverwrite (beads-pj38), not separate UpdateIssue+ApplyCompaction.
		compactOverwriteFn: func(ctx context.Context, id string, updates map[string]interface{}, tier, original int, hash, actor string) error {
			overwriteCalled = true
			if updates["description"].(string) != "short" {
				t.Fatalf("expected summarized description")
			}
			if updates["design"].(string) != "" {
				t.Fatalf("design should be cleared")
			}
			if hash != "deadbeef" {
				t.Fatalf("unexpected hash %q", hash)
			}
			return nil
		},
		addCommentFn: func(ctx context.Context, id, actor, comment string) error {
			if !strings.Contains(comment, "saved") {
				t.Fatalf("unexpected comment %q", comment)
			}
			return nil
		},
	}
	summary := &stubSummarizer{summary: "short"}
	c := &Compactor{store: store, summarizer: summary, config: &Config{}}

	if err := c.CompactTier1(context.Background(), "bd-123"); err != nil {
		t.Fatalf("CompactTier1 unexpected error: %v", err)
	}
	if summary.calls != 1 {
		t.Fatalf("expected summarizer used once, got %d", summary.calls)
	}
	if !overwriteCalled {
		t.Fatalf("expected CompactOverwrite (atomic overwrite+mark) to be called")
	}
}

// TestCompactTier1_SnapshotBeforeOverwrite is the data-safety guard: the
// pre-compaction snapshot must be taken BEFORE the destructive UpdateIssue, so
// compaction is always reversible.
func TestCompactTier1_SnapshotBeforeOverwrite(t *testing.T) {
	cleanup := withGitHash(t, "deadbeef\n")
	t.Cleanup(cleanup)

	var order []string
	store := &stubStore{
		checkEligibilityFn: func(context.Context, string, int) (bool, string, error) { return true, "", nil },
		getIssueFn:         func(context.Context, string) (*types.Issue, error) { return stubIssue(), nil },
		snapshotIssueFn: func(ctx context.Context, id string, tier int) error {
			if tier != 1 {
				t.Fatalf("expected snapshot tier 1, got %d", tier)
			}
			order = append(order, "snapshot")
			return nil
		},
		compactOverwriteFn: func(context.Context, string, map[string]interface{}, int, int, string, string) error {
			order = append(order, "overwrite")
			return nil
		},
	}
	summary := &stubSummarizer{summary: "short"}
	c := &Compactor{store: store, summarizer: summary, config: &Config{}}

	if err := c.CompactTier1(context.Background(), "bd-123"); err != nil {
		t.Fatalf("CompactTier1 unexpected error: %v", err)
	}
	if len(order) != 2 || order[0] != "snapshot" || order[1] != "overwrite" {
		t.Fatalf("expected snapshot before overwrite, got %v", order)
	}
}

// TestCompactTier1_SnapshotError verifies that a failed archive aborts the
// compaction so the original content is never overwritten without a snapshot.
func TestCompactTier1_SnapshotError(t *testing.T) {
	overwriteCalled := false
	store := &stubStore{
		checkEligibilityFn: func(context.Context, string, int) (bool, string, error) { return true, "", nil },
		getIssueFn:         func(context.Context, string) (*types.Issue, error) { return stubIssue(), nil },
		snapshotIssueFn:    func(context.Context, string, int) error { return errors.New("disk full") },
		compactOverwriteFn: func(context.Context, string, map[string]interface{}, int, int, string, string) error {
			overwriteCalled = true
			return nil
		},
	}
	summary := &stubSummarizer{summary: "short"}
	c := &Compactor{store: store, summarizer: summary, config: &Config{}}

	err := c.CompactTier1(context.Background(), "bd-123")
	if err == nil || !strings.Contains(err.Error(), "archive pre-compaction snapshot") {
		t.Fatalf("expected snapshot error, got %v", err)
	}
	if overwriteCalled {
		t.Fatalf("issue was overwritten despite snapshot failure")
	}
}

func TestCompactTier1_Ineligible(t *testing.T) {
	store := &stubStore{
		checkEligibilityFn: func(context.Context, string, int) (bool, string, error) { return false, "recently compacted", nil },
	}
	c := &Compactor{store: store, config: &Config{}}

	err := c.CompactTier1(context.Background(), "bd-123")
	if err == nil || !strings.Contains(err.Error(), "recently compacted") {
		t.Fatalf("expected ineligible error, got %v", err)
	}
}

func TestCompactTier1_SummaryNotSmaller(t *testing.T) {
	commentCalled := false
	store := &stubStore{
		checkEligibilityFn: func(context.Context, string, int) (bool, string, error) { return true, "", nil },
		getIssueFn:         func(context.Context, string) (*types.Issue, error) { return stubIssue(), nil },
		addCommentFn: func(ctx context.Context, id, actor, comment string) error {
			commentCalled = true
			if !strings.Contains(comment, "Tier 1 compaction skipped") {
				t.Fatalf("unexpected comment %q", comment)
			}
			return nil
		},
	}
	summary := &stubSummarizer{summary: strings.Repeat("X", 40)}
	c := &Compactor{store: store, summarizer: summary, config: &Config{}}

	err := c.CompactTier1(context.Background(), "bd-123")
	if err == nil || !strings.Contains(err.Error(), "compaction would increase size") {
		t.Fatalf("expected size error, got %v", err)
	}
	if !commentCalled {
		t.Fatalf("expected warning comment to be recorded")
	}
}

func TestCompactTier1_UpdateError(t *testing.T) {
	// The overwrite runs via the atomic CompactOverwrite seam now (beads-pj38);
	// an overwrite failure surfaces (and rolls back inside the tx).
	store := &stubStore{
		checkEligibilityFn: func(context.Context, string, int) (bool, string, error) { return true, "", nil },
		getIssueFn:         func(context.Context, string) (*types.Issue, error) { return stubIssue(), nil },
		compactOverwriteFn: func(context.Context, string, map[string]interface{}, int, int, string, string) error {
			return errors.New("boom")
		},
	}
	summary := &stubSummarizer{summary: "short"}
	c := &Compactor{store: store, summarizer: summary, config: &Config{}}

	err := c.CompactTier1(context.Background(), "bd-123")
	if err == nil || !strings.Contains(err.Error(), "failed to overwrite+mark compaction") {
		t.Fatalf("expected overwrite error, got %v", err)
	}
}

// --- New constructor tests ---

func TestNew_NilConfig(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	store := &stubStore{}
	c, err := New(store, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.config.Concurrency != defaultConcurrency {
		t.Errorf("expected default concurrency %d, got %d", defaultConcurrency, c.config.Concurrency)
	}
	if !c.config.DryRun {
		t.Error("expected DryRun to be set when no API key")
	}
}

func TestNew_DryRunExplicit(t *testing.T) {
	store := &stubStore{}
	c, err := New(store, "", &Config{DryRun: true, Concurrency: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.config.Concurrency != 3 {
		t.Errorf("expected concurrency 3, got %d", c.config.Concurrency)
	}
	if c.summarizer != nil {
		t.Error("expected nil summarizer in dry run")
	}
}

func TestNew_NoAPIKeyFallsToDryRun(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	store := &stubStore{}
	c, err := New(store, "", &Config{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !c.config.DryRun {
		t.Error("expected DryRun to be set when no API key")
	}
}

func TestNew_WithAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	store := &stubStore{}
	c, err := New(store, "test-key-123", &Config{Concurrency: 2, AuditEnabled: true, Actor: "testbot"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.config.Concurrency != 2 {
		t.Errorf("expected concurrency 2, got %d", c.config.Concurrency)
	}
	if c.summarizer == nil {
		t.Error("expected non-nil summarizer with API key")
	}
	if c.config.APIKey != "test-key-123" {
		t.Errorf("expected API key to be set, got %q", c.config.APIKey)
	}
}

func TestNew_ZeroConcurrency(t *testing.T) {
	store := &stubStore{}
	c, err := New(store, "", &Config{DryRun: true, Concurrency: 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.config.Concurrency != defaultConcurrency {
		t.Errorf("expected default concurrency %d for zero value, got %d", defaultConcurrency, c.config.Concurrency)
	}
}

func TestNew_NegativeConcurrency(t *testing.T) {
	store := &stubStore{}
	c, err := New(store, "", &Config{DryRun: true, Concurrency: -1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.config.Concurrency != defaultConcurrency {
		t.Errorf("expected default concurrency %d for negative value, got %d", defaultConcurrency, c.config.Concurrency)
	}
}

func TestNew_EnvKeyOverridesParam(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	store := &stubStore{}
	c, err := New(store, "param-key", &Config{Concurrency: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.summarizer == nil {
		t.Error("expected non-nil summarizer when env key set")
	}
}

// --- CompactTier1 additional error path tests ---

func TestCompactTier1_CancelledContext(t *testing.T) {
	c := &Compactor{store: &stubStore{}, config: &Config{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := c.CompactTier1(ctx, "bd-123")
	if err == nil {
		t.Fatal("expected error")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestCompactTier1_EligibilityCheckError(t *testing.T) {
	store := &stubStore{
		checkEligibilityFn: func(context.Context, string, int) (bool, string, error) {
			return false, "", errors.New("db error")
		},
	}
	c := &Compactor{store: store, config: &Config{}}

	err := c.CompactTier1(context.Background(), "bd-123")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to verify eligibility") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCompactTier1_IneligibleNoReason(t *testing.T) {
	store := &stubStore{
		checkEligibilityFn: func(context.Context, string, int) (bool, string, error) { return false, "", nil },
	}
	c := &Compactor{store: store, config: &Config{}}

	err := c.CompactTier1(context.Background(), "bd-123")
	if err == nil {
		t.Fatal("expected error")
	}
	expected := "issue bd-123 is not eligible for Tier 1 compaction"
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
}

func TestCompactTier1_GetIssueFetchError(t *testing.T) {
	store := &stubStore{
		checkEligibilityFn: func(context.Context, string, int) (bool, string, error) { return true, "", nil },
		getIssueFn: func(context.Context, string) (*types.Issue, error) {
			return nil, errors.New("fetch error")
		},
	}
	c := &Compactor{store: store, config: &Config{}}

	err := c.CompactTier1(context.Background(), "bd-123")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to fetch issue") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCompactTier1_SummarizerError(t *testing.T) {
	store := &stubStore{
		checkEligibilityFn: func(context.Context, string, int) (bool, string, error) { return true, "", nil },
		getIssueFn:         func(context.Context, string) (*types.Issue, error) { return stubIssue(), nil },
	}
	summary := &stubSummarizer{err: errors.New("API error")}
	c := &Compactor{store: store, summarizer: summary, config: &Config{}}

	err := c.CompactTier1(context.Background(), "bd-123")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to summarize") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCompactTier1_ApplyCompactionError(t *testing.T) {
	cleanup := withGitHash(t, "abc\n")
	t.Cleanup(cleanup)

	// The overwrite+mark now runs atomically via CompactOverwrite (beads-pj38);
	// a failure there (e.g. the ApplyCompaction leg inside the tx) surfaces as
	// the overwrite error and rolls back the content overwrite.
	store := &stubStore{
		checkEligibilityFn: func(context.Context, string, int) (bool, string, error) { return true, "", nil },
		getIssueFn:         func(context.Context, string) (*types.Issue, error) { return stubIssue(), nil },
		compactOverwriteFn: func(context.Context, string, map[string]interface{}, int, int, string, string) error {
			return errors.New("apply failed")
		},
	}
	summary := &stubSummarizer{summary: "short"}
	c := &Compactor{store: store, summarizer: summary, config: &Config{}}

	err := c.CompactTier1(context.Background(), "bd-123")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to overwrite+mark compaction") {
		t.Errorf("unexpected error: %v", err)
	}
}

// beads-ezng: a post-commit AddComment failure must NOT fail CompactTier1. The
// comment is a COSMETIC event log emitted AFTER CompactOverwrite already
// committed the compaction durably (atomicity is scoped to CompactOverwrite per
// beads-pj38). Returning an error here reports a false-FAILURE for an issue that
// WAS compacted. The overwrite must still have happened (compaction committed),
// and the operation must report success.
func TestCompactTier1_AddCommentError(t *testing.T) {
	cleanup := withGitHash(t, "abc\n")
	t.Cleanup(cleanup)

	overwritten := false
	store := &stubStore{
		checkEligibilityFn: func(context.Context, string, int) (bool, string, error) { return true, "", nil },
		getIssueFn:         func(context.Context, string) (*types.Issue, error) { return stubIssue(), nil },
		compactOverwriteFn: func(context.Context, string, map[string]interface{}, int, int, string, string) error {
			overwritten = true
			return nil
		},
		addCommentFn: func(context.Context, string, string, string) error { return errors.New("comment failed") },
	}
	summary := &stubSummarizer{summary: "short"}
	c := &Compactor{store: store, summarizer: summary, config: &Config{}}

	err := c.CompactTier1(context.Background(), "bd-123")
	if err != nil {
		t.Fatalf("beads-ezng: post-commit comment failure must not fail the compaction; got %v", err)
	}
	if !overwritten {
		t.Fatal("expected CompactOverwrite to have committed the compaction")
	}
}

func TestCompactTier1_SummaryNotSmaller_CommentError(t *testing.T) {
	store := &stubStore{
		checkEligibilityFn: func(context.Context, string, int) (bool, string, error) { return true, "", nil },
		getIssueFn:         func(context.Context, string) (*types.Issue, error) { return stubIssue(), nil },
		addCommentFn:       func(context.Context, string, string, string) error { return errors.New("comment failed") },
	}
	summary := &stubSummarizer{summary: strings.Repeat("X", 40)}
	c := &Compactor{store: store, summarizer: summary, config: &Config{}}

	err := c.CompactTier1(context.Background(), "bd-123")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed to record warning") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- CompactTier1Batch additional tests ---

func TestCompactTier1Batch_GetIssueError(t *testing.T) {
	store := &stubStore{
		getIssueFn: func(ctx context.Context, id string) (*types.Issue, error) {
			return nil, errors.New("not found")
		},
	}
	c := &Compactor{store: store, config: &Config{Concurrency: 1}}

	results, err := c.CompactTier1Batch(context.Background(), []string{"bd-1"})
	if err != nil {
		t.Fatalf("batch should not return top-level error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Err == nil {
		t.Error("expected error in result")
	}
}

func TestCompactTier1Batch_Empty(t *testing.T) {
	c := &Compactor{store: &stubStore{}, config: &Config{Concurrency: 1}}

	results, err := c.CompactTier1Batch(context.Background(), []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestCompactTier1Batch_MixedResults(t *testing.T) {
	cleanup := withGitHash(t, "cafebabe\n")
	t.Cleanup(cleanup)

	var mu sync.Mutex
	overwritten := make(map[string]int)
	store := &stubStore{
		checkEligibilityFn: func(ctx context.Context, id string, tier int) (bool, string, error) {
			switch id {
			case "bd-1":
				return true, "", nil
			case "bd-2":
				return false, "not eligible", nil
			default:
				return false, "", fmt.Errorf("unexpected id %s", id)
			}
		},
		getIssueFn: func(ctx context.Context, id string) (*types.Issue, error) {
			issue := stubIssue()
			issue.ID = id
			return issue, nil
		},
		compactOverwriteFn: func(ctx context.Context, id string, updates map[string]interface{}, tier, original int, hash, actor string) error {
			mu.Lock()
			overwritten[id]++
			mu.Unlock()
			return nil
		},
		addCommentFn: func(context.Context, string, string, string) error { return nil },
	}
	summary := &stubSummarizer{summary: "short"}
	c := &Compactor{store: store, summarizer: summary, config: &Config{Concurrency: 2}}

	results, err := c.CompactTier1Batch(context.Background(), []string{"bd-1", "bd-2"})
	if err != nil {
		t.Fatalf("CompactTier1Batch unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	resMap := map[string]*BatchResult{}
	for _, r := range results {
		resMap[r.IssueID] = &r
	}

	if res := resMap["bd-1"]; res == nil || res.Err != nil || res.CompactedSize == 0 {
		t.Fatalf("expected success result for bd-1, got %+v", res)
	}
	if res := resMap["bd-2"]; res == nil || res.Err == nil || !strings.Contains(res.Err.Error(), "not eligible") {
		t.Fatalf("expected ineligible error for bd-2, got %+v", res)
	}
	if overwritten["bd-1"] != 1 {
		t.Fatalf("expected atomic CompactOverwrite for bd-1 exactly once, got %d", overwritten["bd-1"])
	}
	if overwritten["bd-2"] != 0 {
		t.Fatalf("bd-2 should not be processed")
	}
	if summary.calls != 1 {
		t.Fatalf("summarizer should run once; got %d", summary.calls)
	}
}
