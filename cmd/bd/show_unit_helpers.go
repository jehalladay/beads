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
	// Set labels (replaces all existing labels) via the atomic diff-based
	// SetLabels: unchanged labels untouched, the whole replace in ONE
	// transaction. This lifts the churn-free diff into the shared storage seam
	// (issueops.SetLabelsInTx) so the CLI direct path and the proxied path
	// (domain labelUseCase.setMany) run ONE implementation (beads-idvy). hu8z
	// previously did the same diff here but across N separate transactions
	// (partial-failure could strip old labels without completing the new set);
	// SetLabels makes it atomic.
	if len(setLabels) > 0 {
		if err := st.SetLabels(ctx, issueID, setLabels, actor); err != nil {
			return err
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
