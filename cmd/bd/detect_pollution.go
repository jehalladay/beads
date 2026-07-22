package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/steveyegge/beads/internal/types"
)

// Pollution detection utilities used by doctor_pollution.go, create.go, and export.go.
// The deprecated 'bd detect-pollution' command has been removed;
// use 'bd doctor --check=pollution' instead.

type pollutionResult struct {
	issue   *types.Issue
	score   float64
	reasons []string
}

// pollutionBackupStore is the narrow slice of the store the pollution backup
// needs to hydrate relational data (beads-nqwl1). Both methods are satisfied by
// storage.DoltStorage (DependencyQueryStore + AnnotationStore).
type pollutionBackupStore interface {
	GetDependencyRecordsForIssues(ctx context.Context, issueIDs []string) (map[string][]*types.Dependency, error)
	GetCommentsForIssues(ctx context.Context, issueIDs []string) (map[string][]*types.Comment, error)
}

// testPrefixPattern matches common test issue title prefixes.
// Compiled once at package level for use in isTestIssue and detectTestPollution.
var testPrefixPattern = regexp.MustCompile(`^(test|benchmark|sample|tmp|temp|debug|dummy)[-_\s]`)

// isTestIssue checks if an issue title looks like a test issue based on common test prefixes.
// This function is used both for warnings during creation and for pollution detection.
func isTestIssue(title string) bool {
	return testPrefixPattern.MatchString(strings.ToLower(title))
}

func detectTestPollution(issues []*types.Issue) []pollutionResult {
	var results []pollutionResult
	sequentialPattern := regexp.MustCompile(`^[a-z]+-\d+$`)

	// Group issues by creation time to detect rapid succession
	issuesByMinute := make(map[int64][]*types.Issue)
	for _, issue := range issues {
		minute := issue.CreatedAt.Unix() / 60
		issuesByMinute[minute] = append(issuesByMinute[minute], issue)
	}

	for _, issue := range issues {
		score := 0.0
		var reasons []string

		title := strings.ToLower(issue.Title)

		// Check for test prefixes (strong signal, but NOT self-sufficient).
		// beads-9y89f: a bare title-prefix match must NOT reach the 0.7 scrub
		// threshold on its own — a legit item titled "Debug ...", "Sample ...",
		// "Temp ...", "Test harness ..." etc. with a real description is not
		// pollution. Contribute 0.6 so a prefix match needs at least one
		// corroborating signal (empty/short description +0.2/+0.1, sequential
		// ID +0.4, rapid batch +0.3, or a generic test title +0.5) to be
		// classified. A prefixed title WITH a substantial description scores
		// 0.6 alone and survives.
		if testPrefixPattern.MatchString(title) {
			score += 0.6
			reasons = append(reasons, "Title starts with test prefix")
		}

		// Check for sequential numbering (medium signal)
		if sequentialPattern.MatchString(issue.ID) && len(issue.Description) < 20 {
			score += 0.4
			reasons = append(reasons, "Sequential ID with minimal description")
		}

		// Check for generic/empty description (weak signal)
		if len(strings.TrimSpace(issue.Description)) == 0 {
			score += 0.2
			reasons = append(reasons, "No description")
		} else if len(issue.Description) < 20 {
			score += 0.1
			reasons = append(reasons, "Very short description")
		}

		// Check for rapid creation (created with many others in same minute)
		minute := issue.CreatedAt.Unix() / 60
		if len(issuesByMinute[minute]) >= 10 {
			score += 0.3
			reasons = append(reasons, fmt.Sprintf("Created with %d other issues in same minute", len(issuesByMinute[minute])-1))
		}

		// Check for generic test titles
		if strings.Contains(title, "issue for testing") ||
			strings.Contains(title, "test issue") ||
			strings.Contains(title, "sample issue") {
			score += 0.5
			reasons = append(reasons, "Generic test title")
		}

		// Only include if score is above threshold
		if score >= 0.7 {
			results = append(results, pollutionResult{
				issue:   issue,
				score:   score,
				reasons: reasons,
			})
		}
	}

	return results
}

// hydrateAndBackupPollutedIssues populates each flagged issue's Dependencies +
// Comments before writing the backup JSONL, then delegates to
// backupPollutedIssues.
//
// beads-nqwl1: doctor --check=pollution --clean tells the user "To restore, run:
// bd init --from-jsonl <backup>", but the backup was asymmetrically lossy vs a
// real export. runPollutionCheck fetches issues via store.SearchIssues with a
// zero-value filter (IncludeDependencies=false), and the search path never
// hydrates comments at all, so the marshaled structs carried id/title/scalars/
// Labels but EMPTY Dependencies and EMPTY Comments. deleteIssue then removes the
// issue AND its dependency/comment rows, so a restore via `bd init --from-jsonl`
// (which DOES re-create deps + comments) silently came back with dependency
// edges and comment history gone — permanent data loss for any wrongly-flagged
// issue. Mirror the real export path (export.go bulk-loads
// GetDependencyRecordsForIssues + GetCommentsForIssues and assigns them before
// marshaling) so backup==export fidelity.
func hydrateAndBackupPollutedIssues(ctx context.Context, st pollutionBackupStore, polluted []pollutionResult, path string) error {
	if len(polluted) > 0 {
		ids := make([]string, len(polluted))
		for i, p := range polluted {
			ids[i] = p.issue.ID
		}
		allDeps, err := st.GetDependencyRecordsForIssues(ctx, ids)
		if err != nil {
			return fmt.Errorf("hydrating dependencies for backup: %w", err)
		}
		commentsMap, err := st.GetCommentsForIssues(ctx, ids)
		if err != nil {
			return fmt.Errorf("hydrating comments for backup: %w", err)
		}
		for _, p := range polluted {
			p.issue.Dependencies = allDeps[p.issue.ID]
			p.issue.Comments = commentsMap[p.issue.ID]
		}
	}
	return backupPollutedIssues(polluted, path)
}

func backupPollutedIssues(polluted []pollutionResult, path string) error {
	// Create backup file
	// nolint:gosec // G304: path is provided by user as explicit backup location
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create backup file: %w", err)
	}
	defer file.Close()

	// Write each issue as JSONL
	for _, p := range polluted {
		data, err := json.Marshal(p.issue)
		if err != nil {
			return fmt.Errorf("failed to marshal issue %s: %w", p.issue.ID, err)
		}

		if _, err := file.WriteString(string(data) + "\n"); err != nil {
			return fmt.Errorf("failed to write issue %s: %w", p.issue.ID, err)
		}
	}

	return nil
}
