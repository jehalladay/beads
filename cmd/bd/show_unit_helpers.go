package main

import (
	"context"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/validation"
)

// validateIssueUpdatable checks if an issue can be updated.
// Uses the centralized validation package for consistency.
func validateIssueUpdatable(id string, issue *types.Issue) error {
	// Note: We use NotTemplate() directly instead of ForUpdate() to maintain
	// backward compatibility - the original didn't check for nil issues.
	return validation.NotTemplate()(id, issue)
}

// validateIssueClosable checks if an issue can be closed.
// Uses the centralized validation package for consistency.
func validateIssueClosable(id string, issue *types.Issue, force bool) error {
	// Note: We use individual validators instead of ForClose() to maintain
	// backward compatibility - the original didn't check for nil issues.
	return validation.Chain(
		validation.NotTemplate(),
		validation.NotPinned(force),
	)(id, issue)
}

func applyLabelUpdates(ctx context.Context, st storage.DoltStorage, issueID, actor string, setLabels, addLabels, removeLabels []string) error {
	// Set labels (replaces all existing labels). Diff against the current set so
	// unchanged labels are left untouched — remove only current-not-desired, add
	// only desired-not-present. The old churn-all (remove ALL then add ALL) wrote
	// a spurious remove+add history event for every unchanged label and, on a
	// mid-loop failure, could leave the issue with the old labels stripped but the
	// new set incomplete. This matches the atomic diff the proxied path
	// (domain labelUseCase.setMany) already does (beads-hu8z).
	if len(setLabels) > 0 {
		currentLabels, err := st.GetLabels(ctx, issueID)
		if err != nil {
			return err
		}
		desired := make(map[string]bool, len(setLabels))
		for _, label := range setLabels {
			desired[label] = true
		}
		existing := make(map[string]bool, len(currentLabels))
		for _, label := range currentLabels {
			existing[label] = true
			if !desired[label] {
				if err := st.RemoveLabel(ctx, issueID, label, actor); err != nil {
					return err
				}
			}
		}
		for _, label := range setLabels {
			if !existing[label] {
				if err := st.AddLabel(ctx, issueID, label, actor); err != nil {
					return err
				}
			}
		}
	}

	// Add labels
	for _, label := range addLabels {
		if err := st.AddLabel(ctx, issueID, label, actor); err != nil {
			return err
		}
	}

	// Remove labels
	for _, label := range removeLabels {
		if err := st.RemoveLabel(ctx, issueID, label, actor); err != nil {
			return err
		}
	}

	return nil
}
