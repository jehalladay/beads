package sqlite

import (
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestHashIssueContent(t *testing.T) {
	issue1 := &types.Issue{
		Title:       "Issue from clone A",
		Description: "",
		Priority:    1,
		IssueType:   "task",
		Status:      "open",
	}
	
	issue2 := &types.Issue{
		Title:       "Issue from clone B",
		Description: "",
		Priority:    1,
		IssueType:   "task",
		Status:      "open",
	}
	
	hash1 := hashIssueContent(issue1)
	hash2 := hashIssueContent(issue2)
	
	// Hashes should be different
	if hash1 == hash2 {
		t.Errorf("Expected different hashes, got same: %s", hash1)
	}
	
	// Hashes should be deterministic
	hash1Again := hashIssueContent(issue1)
	if hash1 != hash1Again {
		t.Errorf("Hash not deterministic: %s != %s", hash1, hash1Again)
	}
	
	t.Logf("Hash A: %s", hash1)
	t.Logf("Hash B: %s", hash2)
	t.Logf("A < B: %v (B wins if true)", hash1 < hash2)
}

func TestScoreCollisions_Deterministic(t *testing.T) {
	existingIssue := &types.Issue{
		ID:          "test-1",
		Title:       "Issue from clone B",
		Description: "",
		Priority:    1,
		IssueType:   "task",
		Status:      "open",
	}
	
	incomingIssue := &types.Issue{
		ID:          "test-1",
		Title:       "Issue from clone A",
		Description: "",
		Priority:    1,
		IssueType:   "task",
		Status:      "open",
	}
	
	collision := &CollisionDetail{
		ID:            "test-1",
		ExistingIssue: existingIssue,
		IncomingIssue: incomingIssue,
	}
	
	// Run scoring
	err := ScoreCollisions(nil, nil, []*CollisionDetail{collision}, nil)
	if err != nil {
		t.Fatalf("ScoreCollisions failed: %v", err)
	}
	
	existingHash := hashIssueContent(existingIssue)
	incomingHash := hashIssueContent(incomingIssue)
	
	t.Logf("Existing hash (B): %s", existingHash)
	t.Logf("Incoming hash (A): %s", incomingHash)
	t.Logf("Existing < Incoming: %v", existingHash < incomingHash)
	
	// Clone B has lower hash, so it should win
	// This means: RemapIncoming should be TRUE (remap incoming A, keep existing B)
	if !collision.RemapIncoming {
		t.Errorf("Expected RemapIncoming=true (remap incoming A, keep existing B with lower hash), got false")
	} else {
		t.Logf("✓ Correct: RemapIncoming=true, will remap incoming 'clone A' and keep existing 'clone B'")
	}
}
