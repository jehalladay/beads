//go:build cgo

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// TestCompactDoltGCFailureRespectsJSON is the teeth for beads-906um: when
// `dolt gc` fails under `bd compact --dolt --json`, runCompactDolt must emit a
// structured {error} object on stdout (via FatalErrorRespectJSON), not
// plaintext on stderr with an empty stdout. It uses the subprocess re-exec
// idiom (beads-4yi7) because the path exits via os.Exit — an in-process call
// would kill the test binary.
//
// The child drives runCompactDolt() directly with:
//   - BEADS_DIR pointing at a temp .beads containing a dolt/ subdir (so
//     FindBeadsDir + the doltPath stat both resolve without a real DB), and
//   - a fake `dolt` on PATH that exits 1 (so the gc exec fails
//     deterministically, hermetically, with no real Dolt needed).
func TestCompactDoltGCFailureRespectsJSON(t *testing.T) {
	if os.Getenv("BD_COMPACT_GC_CHILD") == "1" {
		runCompactDoltGCChild()
		return // unreachable — child os.Exits inside runCompactDolt
	}

	tmp := t.TempDir()

	// Hermetic .beads with a dolt/ dir so FindBeadsDir + doltPath stat pass.
	beadsDir := filepath.Join(tmp, ".beads")
	if err := os.MkdirAll(filepath.Join(beadsDir, "dolt"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Fake `dolt` binary that always fails, on an isolated PATH.
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeDolt := filepath.Join(binDir, "dolt")
	if err := os.WriteFile(fakeDolt, []byte("#!/bin/sh\necho 'boom: simulated gc failure' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run", "TestCompactDoltGCFailureRespectsJSON") // #nosec G204 -- test self-reexec
	cmd.Env = append(os.Environ(),
		"BD_COMPACT_GC_CHILD=1",
		"BEADS_DIR="+beadsDir,
		"PATH="+binDir, // ONLY the fake dolt is resolvable
	)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	// The gc-failed leg must exit non-zero (it's a fatal error).
	if err == nil {
		t.Fatalf("expected child to exit non-zero on gc failure; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	// The fix: under --json, stdout MUST carry a parseable JSON error object.
	// Pre-fix (bare os.Exit after Fprintf to stderr) leaves stdout empty.
	out := strings.TrimSpace(stdout.String())
	if out == "" {
		t.Fatalf("--json gc failure emitted EMPTY stdout (json contract broken); stderr=%q", stderr.String())
	}
	var parsed map[string]interface{}
	if jerr := json.Unmarshal([]byte(out), &parsed); jerr != nil {
		t.Fatalf("--json gc failure stdout is not valid JSON: %v\nstdout=%q", jerr, out)
	}
	if _, ok := parsed["error"]; !ok {
		t.Fatalf("--json gc failure JSON has no \"error\" key: %v", parsed)
	}
}

// runCompactDoltGCChild installs the package globals runCompactDolt needs and
// invokes it. The fake `dolt` on PATH makes the gc exec fail, driving the
// beads-906um error leg, which os.Exits.
func runCompactDoltGCChild() {
	jsonOutput = true
	compactDryRun = false
	runCompactDolt()
}

func TestCompactSuite(t *testing.T) {
	// Compaction is now implemented for Dolt backend
	tmpDir := t.TempDir()
	testDB := filepath.Join(tmpDir, ".beads", "beads.db")
	s := newTestStore(t, testDB)
	ctx := context.Background()

	t.Run("DryRun", func(t *testing.T) {
		// Create a closed issue
		issue := &types.Issue{
			ID:          "test-dryrun-1",
			Title:       "Test Issue",
			Description: "This is a long description that should be compacted. " + string(make([]byte, 500)),
			Status:      types.StatusClosed,
			Priority:    2,
			IssueType:   types.TypeTask,
			CreatedAt:   time.Now().Add(-60 * 24 * time.Hour),
			ClosedAt:    ptrTime(time.Now().Add(-35 * 24 * time.Hour)),
		}

		if err := s.CreateIssue(ctx, issue, "test"); err != nil {
			t.Fatal(err)
		}

		// Test dry run - should check eligibility without error even without API key
		eligible, reason, err := s.CheckEligibility(ctx, "test-dryrun-1", 1)
		if err != nil {
			t.Fatalf("CheckEligibility failed: %v", err)
		}

		if !eligible {
			t.Fatalf("Issue should be eligible for compaction: %s", reason)
		}
	})

	t.Run("Stats", func(t *testing.T) {
		// Create mix of issues - some eligible, some not
		issues := []*types.Issue{
			{
				ID:          "test-stats-1",
				Title:       "Old closed",
				Description: "Content that makes this issue eligible for compaction.",
				Status:      types.StatusClosed,
				Priority:    2,
				IssueType:   types.TypeTask,
				CreatedAt:   time.Now().Add(-60 * 24 * time.Hour),
				ClosedAt:    ptrTime(time.Now().Add(-35 * 24 * time.Hour)),
			},
			{
				ID:          "test-stats-2",
				Title:       "Recent closed",
				Description: "Some content here too.",
				Status:      types.StatusClosed,
				Priority:    2,
				IssueType:   types.TypeTask,
				CreatedAt:   time.Now().Add(-10 * 24 * time.Hour),
				ClosedAt:    ptrTime(time.Now().Add(-5 * 24 * time.Hour)),
			},
			{
				ID:        "test-stats-3",
				Title:     "Still open",
				Status:    types.StatusOpen,
				Priority:  2,
				IssueType: types.TypeTask,
				CreatedAt: time.Now().Add(-40 * 24 * time.Hour),
			},
		}

		for _, issue := range issues {
			if err := s.CreateIssue(ctx, issue, "test"); err != nil {
				t.Fatal(err)
			}
		}

		// Verify issues were created
		allIssues, err := s.SearchIssues(ctx, "", types.IssueFilter{})
		if err != nil {
			t.Fatalf("SearchIssues failed: %v", err)
		}

		// Count issues with stats prefix
		statCount := 0
		for _, issue := range allIssues {
			if len(issue.ID) >= 11 && issue.ID[:11] == "test-stats-" {
				statCount++
			}
		}

		if statCount != 3 {
			t.Errorf("Expected 3 stats issues, got %d", statCount)
		}

		// Test eligibility check for old closed issue
		eligible, _, err := s.CheckEligibility(ctx, "test-stats-1", 1)
		if err != nil {
			t.Fatalf("CheckEligibility failed: %v", err)
		}
		if !eligible {
			t.Error("Old closed issue should be eligible for Tier 1")
		}
	})

	t.Run("RunCompactStats", func(t *testing.T) {
		// Create some closed issues
		for i := 1; i <= 3; i++ {
			id := fmt.Sprintf("test-runstats-%d", i)
			issue := &types.Issue{
				ID:          id,
				Title:       "Test Issue",
				Description: string(make([]byte, 500)),
				Status:      types.StatusClosed,
				Priority:    2,
				IssueType:   types.TypeTask,
				CreatedAt:   time.Now().Add(-60 * 24 * time.Hour),
				ClosedAt:    ptrTime(time.Now().Add(-35 * 24 * time.Hour)),
			}
			if err := s.CreateIssue(ctx, issue, "test"); err != nil {
				t.Fatal(err)
			}
		}

		// Test stats - should work without API key
		savedJSONOutput := jsonOutput
		jsonOutput = false
		defer func() { jsonOutput = savedJSONOutput }()

		// Actually call runCompactStats to increase coverage
		runCompactStats(ctx, s)

		// Also test with JSON output
		jsonOutput = true
		runCompactStats(ctx, s)
	})

	t.Run("CompactStatsJSON", func(t *testing.T) {
		// Create a closed issue eligible for Tier 1
		issue := &types.Issue{
			ID:          "test-json-1",
			Title:       "Test Issue",
			Description: string(make([]byte, 500)),
			Status:      types.StatusClosed,
			Priority:    2,
			IssueType:   types.TypeTask,
			CreatedAt:   time.Now().Add(-60 * 24 * time.Hour),
			ClosedAt:    ptrTime(time.Now().Add(-35 * 24 * time.Hour)),
		}
		if err := s.CreateIssue(ctx, issue, "test"); err != nil {
			t.Fatal(err)
		}

		// Test with JSON output
		savedJSONOutput := jsonOutput
		jsonOutput = true
		defer func() { jsonOutput = savedJSONOutput }()

		// Should not panic and should execute JSON path
		runCompactStats(ctx, s)
	})

	t.Run("RunCompactSingleDryRun", func(t *testing.T) {
		// Create a closed issue eligible for compaction
		issue := &types.Issue{
			ID:          "test-single-1",
			Title:       "Test Compact Issue",
			Description: string(make([]byte, 500)),
			Status:      types.StatusClosed,
			Priority:    2,
			IssueType:   types.TypeTask,
			CreatedAt:   time.Now().Add(-60 * 24 * time.Hour),
			ClosedAt:    ptrTime(time.Now().Add(-35 * 24 * time.Hour)),
		}
		if err := s.CreateIssue(ctx, issue, "test"); err != nil {
			t.Fatal(err)
		}

		// Test eligibility in dry run mode
		eligible, _, err := s.CheckEligibility(ctx, "test-single-1", 1)
		if err != nil {
			t.Fatalf("CheckEligibility failed: %v", err)
		}
		if !eligible {
			t.Error("Issue should be eligible for Tier 1 compaction")
		}
	})

	t.Run("RunCompactAllDryRun", func(t *testing.T) {
		// Create multiple closed issues
		for i := 1; i <= 3; i++ {
			issue := &types.Issue{
				ID:          fmt.Sprintf("test-all-%d", i),
				Title:       "Test Issue",
				Description: string(make([]byte, 500)),
				Status:      types.StatusClosed,
				Priority:    2,
				IssueType:   types.TypeTask,
				CreatedAt:   time.Now().Add(-60 * 24 * time.Hour),
				ClosedAt:    ptrTime(time.Now().Add(-35 * 24 * time.Hour)),
			}
			if err := s.CreateIssue(ctx, issue, "test"); err != nil {
				t.Fatal(err)
			}
		}

		// Verify issues eligible for compaction
		closedStatus := types.StatusClosed
		issues, err := s.SearchIssues(ctx, "", types.IssueFilter{Status: &closedStatus})
		if err != nil {
			t.Fatalf("SearchIssues failed: %v", err)
		}

		eligibleCount := 0
		for _, issue := range issues {
			// Only count our test-all issues
			if len(issue.ID) < 9 || issue.ID[:9] != "test-all-" {
				continue
			}
			eligible, _, err := s.CheckEligibility(ctx, issue.ID, 1)
			if err != nil {
				t.Fatalf("CheckEligibility failed for %s: %v", issue.ID, err)
			}
			if eligible {
				eligibleCount++
			}
		}

		if eligibleCount != 3 {
			t.Errorf("Expected 3 eligible issues, got %d", eligibleCount)
		}
	})
}

func TestCompactValidation(t *testing.T) {
	tests := []struct {
		name       string
		compactID  string
		compactAll bool
		dryRun     bool
		force      bool
		wantError  bool
	}{
		{
			name:       "both id and all",
			compactID:  "test-1",
			compactAll: true,
			wantError:  true,
		},
		{
			name:      "force without id",
			force:     true,
			wantError: true,
		},
		{
			name:      "no flags",
			wantError: true,
		},
		{
			name:      "dry run only",
			dryRun:    true,
			wantError: false,
		},
		{
			name:      "id only",
			compactID: "test-1",
			wantError: false,
		},
		{
			name:       "all only",
			compactAll: true,
			wantError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.compactID != "" && tt.compactAll {
				// Should fail
				if !tt.wantError {
					t.Error("Expected error for both --id and --all")
				}
			}

			if tt.force && tt.compactID == "" {
				// Should fail
				if !tt.wantError {
					t.Error("Expected error for --force without --id")
				}
			}

			if tt.compactID == "" && !tt.compactAll && !tt.dryRun {
				// Should fail
				if !tt.wantError {
					t.Error("Expected error when no action specified")
				}
			}
		})
	}
}

func TestCompactProgressBar(t *testing.T) {
	// Test progress bar formatting
	pb := progressBar(50, 100)
	if len(pb) == 0 {
		t.Error("Progress bar should not be empty")
	}

	pb = progressBar(100, 100)
	if len(pb) == 0 {
		t.Error("Full progress bar should not be empty")
	}

	pb = progressBar(0, 100)
	if len(pb) == 0 {
		t.Error("Zero progress bar should not be empty")
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func TestCompactInitCommand(t *testing.T) {
	if compactCmd == nil {
		t.Fatal("compactCmd should be initialized")
	}

	if compactCmd.Use != "compact" {
		t.Errorf("Expected Use='compact', got %q", compactCmd.Use)
	}

	if len(compactCmd.Long) == 0 {
		t.Error("compactCmd should have Long description")
	}

	// Verify --json is honored on compact via the ROOT persistent flag.
	// beads-9fww intentionally removed the command-LOCAL --json flag: a local
	// binding shadowed the persistent flag and left --json non-functional (see
	// compact.go init() NOTE). So --json must resolve as an INHERITED flag, and
	// must NOT be re-registered locally (re-adding a local shadow regresses 9fww).
	if compactCmd.InheritedFlags().Lookup("json") == nil {
		t.Error("compact command should inherit the persistent --json flag")
	}
	if local := compactCmd.LocalFlags().Lookup("json"); local != nil {
		t.Error("compact command must not register a LOCAL --json flag (shadows the persistent flag; see beads-9fww)")
	}
}
