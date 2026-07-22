package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// beads-4sfae: beads-o70m1 added providesLabelError to reject a hand-set
// 'provides:<cap>' capability label at `bd create` + `bd create --graph`, but
// the reserved-identity-label family (reservedIdentityLabelError) is enforced at
// ~7 seams and o70m1 mirrored provides: into only 2. This pins the extension to
// the remaining create-authoring + mutation seams that already guard identity
// labels but let a hand-set provides: through:
//   - create-form  (validateCreateFormValues)
//   - markdown      (createIssuesFromMarkdown, `bd create --file`)
//   - quick         (`bd q`/`bd quick`)
//   - update        (`bd update --add-label`/`--set-labels`)
//   - tag           (`bd tag`)
//
// 'provides:<cap>' marks a single-provider cross-project capability; `bd ship`
// is the only sanctioned applier (it enforces closed-requirement + single-
// provider before stamping the label via storage). A hand-set provides: on any
// of these seams minted/attached the reserved label outside `bd ship`.
// providesLabelError has NO GT_INTERNAL bypass (unlike the identity guard) — the
// label is applied by ship via the storage layer, not these CLI seams.
//
// The create-form + markdown legs are unit-testable directly (no store); the
// quick/update/tag legs are exercised end-to-end via the embedded-dolt harness.
// MUTATION-VERIFY: remove the providesLabelError call at any seam → that leg
// goes RED (the create/attach succeeds, RC=0, label lands).

// ── create-form (unit; validateCreateFormValues) ──
func TestValidateCreateFormValues_RejectsProvidesLabel_4sfae(t *testing.T) {
	// providesLabelError is not GT_INTERNAL-gated, but set it empty so the test
	// is unambiguous about which guard fires.
	t.Setenv(gtInternalEnv, "")
	fv := parseCreateFormInput(&createFormRawInput{Title: "cap via form", IssueType: "task", Priority: "2", Labels: "provides:formcap"})
	err := validateCreateFormValues(fv)
	if err == nil {
		t.Fatal("beads-4sfae: validateCreateFormValues must reject a hand-set 'provides:' label (create-form parity with single create), got nil")
	}
	if !strings.Contains(err.Error(), "provides:") || !strings.Contains(err.Error(), "bd ship") {
		t.Errorf("reject message should name 'provides:' and hint 'bd ship'; got %q", err.Error())
	}
}

func TestValidateCreateFormValues_ProvidesNotGTInternalExempt_4sfae(t *testing.T) {
	// provides: is reserved for `bd ship` regardless of GT_INTERNAL — even a
	// gt-internal write must not hand-set it here.
	t.Setenv(gtInternalEnv, gtInternalValue)
	fv := parseCreateFormInput(&createFormRawInput{Title: "cap via form gt", IssueType: "task", Priority: "2", Labels: "provides:formcap"})
	if err := validateCreateFormValues(fv); err == nil {
		t.Error("beads-4sfae: 'provides:' must be rejected at create-form even under GT_INTERNAL (ship applies it via storage, not this seam)")
	}
}

func TestValidateCreateFormValues_NonProvidesLabelUnaffected_4sfae(t *testing.T) {
	t.Setenv(gtInternalEnv, "")
	fv := parseCreateFormInput(&createFormRawInput{Title: "ordinary", IssueType: "task", Priority: "2", Labels: "area:cli, needs review"})
	if err := validateCreateFormValues(fv); err != nil {
		t.Errorf("beads-4sfae: ordinary labels must pass create-form; got %v", err)
	}
}

// ── markdown (unit; createIssuesFromMarkdown --dry-run) ──
func TestCreateIssuesFromMarkdown_RejectsProvidesLabel_4sfae(t *testing.T) {
	t.Setenv(gtInternalEnv, "")
	tmpDir := t.TempDir()
	md := "## Cap Task\n\nBody.\n\n### Labels\n\nprovides:mdcap\n"
	mdPath := filepath.Join(tmpDir, "cap.md")
	if err := os.WriteFile(mdPath, []byte(md), 0644); err != nil {
		t.Fatal(err)
	}
	// dry-run: the guard fires before the store is needed, so a nil store is
	// fine (the NonProvidesLabelUnaffected twin below proves the same shape
	// returns nil when the label is ordinary). markdown surfaces the guard via
	// HandleErrorRespectJSON, which prints the message to os.Stderr (non-JSON) or
	// stdout (JSON) and returns an opaque *exitError whose .Error() is just
	// "exit code 1" — and the destination stream depends on the global jsonOutput
	// left over from prior suite tests. So assert on the return value (err !=
	// nil) rather than a captured stream or err.Error() text: err != nil here can
	// only come from the providesLabelError guard (mutation-verified: remove the
	// guard → err becomes nil → RED). The message text ("provides:"/"bd ship") is
	// verified against the shared providesLabelError via the create-form leg above.
	err := createIssuesFromMarkdown(nil, mdPath, true)
	if err == nil {
		t.Fatal("beads-4sfae: createIssuesFromMarkdown must reject a hand-set 'provides:' label (markdown-create parity), got nil")
	}
}

func TestCreateIssuesFromMarkdown_NonProvidesLabelUnaffected_4sfae(t *testing.T) {
	t.Setenv(gtInternalEnv, "")
	tmpDir := t.TempDir()
	md := "## Normal Task\n\nBody.\n\n### Labels\n\narea:cli, needs review\n"
	mdPath := filepath.Join(tmpDir, "normal.md")
	if err := os.WriteFile(mdPath, []byte(md), 0644); err != nil {
		t.Fatal(err)
	}
	if err := createIssuesFromMarkdown(nil, mdPath, true); err != nil {
		t.Errorf("beads-4sfae: ordinary markdown labels must pass; got %v", err)
	}
}
