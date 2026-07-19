package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/ui"
)

// beads-8fm2: proxied-server handler for `bd edit`.
//
// The direct path resolves the issue and writes the edited field via the global
// `store`, which is NIL in proxiedServerMode (main.go PersistentPreRun returns
// after uowProvider setup, before store init; newDoltStoreFromConfig refuses to
// build a store in proxied mode). So a hub-connected crew running `bd edit`
// failed "storage is nil" — the aocj/1zuh class. Route the LOAD (current field
// value) through the UOW and the WRITE through the shared applyUpdateProxiedOne
// core (same field allowlist as `bd update`: title/description/design/
// notes/acceptance_criteria per issueops/update.go), keeping $EDITOR interaction
// identical. Mirrors beads-rejl (priority) / beads-qwez (assign/tag).
func runEditProxiedServer(ctx context.Context, id, fieldToEdit string) error {
	if uowProvider == nil {
		return HandleErrorRespectJSON("proxied-server UOW provider not initialized")
	}

	// Load the current field value via the UOW (read-only).
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("open unit of work: %v", err)
	}
	issue, _, err := proxiedGetIssueOrWisp(ctx, uw, id)
	if err != nil || issue == nil {
		uw.Close(ctx)
		return HandleErrorRespectJSON("resolving %s: %v", id, err)
	}
	resolvedID := issue.ID
	if verr := validateIssueUpdatable(resolvedID, issue); verr != nil {
		uw.Close(ctx)
		return HandleErrorRespectJSON("%s", verr)
	}
	currentValue := editFieldValue(issue, fieldToEdit)
	uw.Close(ctx)

	editor, err := resolveEditor()
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}

	newValue, tmpPath, changed, err := runEditorForField(editor, fieldToEdit, currentValue)
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}
	if !changed {
		fmt.Println("No changes made")
		return nil
	}
	if fieldToEdit == "title" && newValue == "" {
		return HandleErrorRespectJSON("title cannot be empty")
	}

	// Write via the shared proxied update core (same field allowlist + audit).
	// force=false: edit is a plain single-field mutation with no force-override
	// semantics (matches assign/tag/defer/label/priority callers).
	in := &updateInput{fields: map[string]any{fieldToEdit: newValue}}
	updated, ok := applyUpdateProxiedOne(ctx, resolvedID, in, false)
	if !ok {
		fmt.Fprintf(os.Stderr, "Your edits are preserved in: %s\n", tmpPath)
		return &exitError{Code: 1}
	}
	_ = os.Remove(tmpPath)

	SetLastTouchedID(updated.ID)
	displayTitle := updated.Title
	fieldName := strings.ReplaceAll(fieldToEdit, "_", " ")
	fmt.Printf("%s Updated %s for issue: %s\n", ui.RenderPass("✓"), fieldName, formatFeedbackID(updated.ID, displayTitle))
	return nil
}

// editFieldValue returns the current value of the named edit field on issue.
func editFieldValue(issue *types.Issue, fieldToEdit string) string {
	switch fieldToEdit {
	case "title":
		return issue.Title
	case "description":
		return issue.Description
	case "design":
		return issue.Design
	case "notes":
		return issue.Notes
	case "acceptance_criteria":
		return issue.AcceptanceCriteria
	}
	return ""
}

// resolveEditor finds the editor to use ($EDITOR, $VISUAL, then common defaults).
func resolveEditor() (string, error) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		for _, defaultEditor := range []string{"vim", "vi", "nano", "emacs"} {
			if _, err := exec.LookPath(defaultEditor); err == nil {
				editor = defaultEditor
				break
			}
		}
	}
	if editor == "" {
		return "", fmt.Errorf("no editor found. Set $EDITOR or $VISUAL environment variable")
	}
	return editor, nil
}

// runEditorForField opens the editor on a temp file seeded with currentValue and
// returns the trimmed new value, the temp path (for edit-preservation on write
// failure), and whether it changed. The temp file is removed on a clean no-op.
func runEditorForField(editor, fieldToEdit, currentValue string) (newValue, tmpPath string, changed bool, err error) {
	tmpFile, err := os.CreateTemp("", fmt.Sprintf("bd-edit-%s-*.txt", fieldToEdit))
	if err != nil {
		return "", "", false, fmt.Errorf("creating temp file: %v", err)
	}
	tmpPath = tmpFile.Name()
	if _, werr := tmpFile.WriteString(currentValue); werr != nil {
		_ = tmpFile.Close()
		return "", tmpPath, false, fmt.Errorf("writing to temp file: %v", werr)
	}
	_ = tmpFile.Close()

	editorParts := strings.Fields(editor)
	editorArgs := append(editorParts[1:], tmpPath)
	editorCmd := exec.Command(editorParts[0], editorArgs...) //nolint:gosec // G204: editor from trusted $EDITOR/$VISUAL env or known defaults
	editorCmd.Stdin = os.Stdin
	editorCmd.Stdout = os.Stdout
	editorCmd.Stderr = os.Stderr
	if rerr := editorCmd.Run(); rerr != nil {
		return "", tmpPath, false, fmt.Errorf("running editor: %v", rerr)
	}

	// #nosec G304 -- tmpPath was created just above in this function
	editedContent, rerr := os.ReadFile(tmpPath)
	if rerr != nil {
		return "", tmpPath, false, fmt.Errorf("reading edited file: %v", rerr)
	}
	newValue = strings.TrimSpace(string(editedContent))
	if newValue == currentValue {
		_ = os.Remove(tmpPath)
		return newValue, tmpPath, false, nil
	}
	return newValue, tmpPath, true, nil
}

// editFieldFromFlags maps the edit command flags to the field name.
func editFieldFromFlags(hasTitle, hasDesign, hasNotes, hasAcceptance bool) string {
	switch {
	case hasTitle:
		return "title"
	case hasDesign:
		return "design"
	case hasNotes:
		return "notes"
	case hasAcceptance:
		return "acceptance_criteria"
	default:
		return "description"
	}
}
