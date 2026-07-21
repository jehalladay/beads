package doctor

import (
	"context"
	"os/exec"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// orphanTestProvider is a minimal types.IssueProvider for exercising the REAL
// FindOrphanedIssues git scan (the existing cmd/bd/orphans_test.go mocks the
// function out, so it never covers the scan itself).
type orphanTestProvider struct {
	issues []*types.Issue
	prefix string
}

func (p *orphanTestProvider) GetOpenIssues(context.Context) ([]*types.Issue, error) {
	return p.issues, nil
}

func (p *orphanTestProvider) GetIssuePrefix() string { return p.prefix }

func gitCommitAllowEmpty(t *testing.T, dir, message string) {
	t.Helper()
	cmd := exec.Command("git", "commit", "--allow-empty", "-m", message)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit failed: %v\n%s", err, out)
	}
}

func gitCommitAllowEmptyFile(t *testing.T, dir, message string) {
	t.Helper()
	// Use -F - so a multi-line body (subject + blank line + footer) is preserved
	// exactly as a real "Refs:/Closes: (id)" trailer would be.
	cmd := exec.Command("git", "commit", "--allow-empty", "-F", "-")
	cmd.Dir = dir
	cmd.Stdin = nil
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("git commit start: %v", err)
	}
	if _, err := stdin.Write([]byte(message)); err != nil {
		t.Fatalf("write commit body: %v", err)
	}
	stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("git commit -F - failed: %v", err)
	}
}

// TestFindOrphanedIssues_DetectsBodyRef is the teeth for beads-3tjtm: an issue
// id referenced only in the commit BODY/footer (Refs:/Closes: convention) must
// be detected, not only ids in the subject line. Pre-fix the scan used
// `git log --oneline` (subject only), so the body-ref orphan was silently
// dropped. Mutation-verify: revert git.go to `--oneline` and this test RED-flips
// on the body-ref assertion (op-body stays undetected).
func TestFindOrphanedIssues_DetectsBodyRef(t *testing.T) {
	dir := setupGitRepo(t)

	subjectIssue := &types.Issue{ID: "op-subj", Title: "subject ref", Status: types.StatusOpen}
	bodyIssue := &types.Issue{ID: "op-body", Title: "body ref", Status: types.StatusOpen}
	provider := &orphanTestProvider{
		issues: []*types.Issue{subjectIssue, bodyIssue},
		prefix: "op",
	}

	// Commit 1: ref in the SUBJECT (already worked pre-fix).
	gitCommitAllowEmpty(t, dir, "do the thing (op-subj)")
	// Commit 2: ref ONLY in the body/footer trailer — the beads-3tjtm case.
	gitCommitAllowEmptyFile(t, dir, "refactor module\n\nImplements the feature.\nRefs: (op-body)\n")

	orphans, err := FindOrphanedIssues(dir, provider)
	if err != nil {
		t.Fatalf("FindOrphanedIssues: %v", err)
	}

	found := map[string]OrphanIssue{}
	for _, o := range orphans {
		found[o.IssueID] = o
	}

	if _, ok := found["op-subj"]; !ok {
		t.Fatalf("subject-ref orphan op-subj not detected (regression): %#v", orphans)
	}
	bodyOrphan, ok := found["op-body"]
	if !ok {
		t.Fatalf("beads-3tjtm: body/footer-ref orphan op-body NOT detected — scan missed the commit body (want full-message scan, not --oneline): %#v", orphans)
	}
	// LatestCommitMessage must stay the SUBJECT line, not the whole body
	// (rendering-unaffected contract from the bead).
	if bodyOrphan.LatestCommitMessage != "refactor module" {
		t.Fatalf("beads-3tjtm: LatestCommitMessage should be the subject %q, got %q", "refactor module", bodyOrphan.LatestCommitMessage)
	}
	if bodyOrphan.LatestCommit == "" {
		t.Fatalf("beads-3tjtm: body-ref orphan has empty LatestCommit: %#v", bodyOrphan)
	}
}
