//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestParseCreateFormInput_TrimsTitle verifies beads-1077e: `bd create-form`
// trims the title, matching single `bd create` (create.go: TrimSpace). Without
// it a padded form title was stored verbatim and became unsearchable.
func TestParseCreateFormInput_TrimsTitle(t *testing.T) {
	fv := parseCreateFormInput(&createFormRawInput{
		Title:     "  padded title  ",
		IssueType: "task",
		Priority:  "2",
	})
	if fv.Title != "padded title" {
		t.Errorf("parseCreateFormInput title = %q, want trimmed %q", fv.Title, "padded title")
	}
}

// TestParseCreateFormInput_NormalizesAssignee verifies beads-1077e: the form
// normalizes the assignee (TrimSpace + fold the "none" sentinel to ""),
// matching normalizeAssignee used by single create/assign/update. Without it a
// padded or "none" form assignee orphaned the work from `bd ready --assignee`.
func TestParseCreateFormInput_NormalizesAssignee(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  alice  ", "alice"},
		{"none", ""},
		{"None", ""},
		{"  none  ", ""},
		{"bob", "bob"},
	}
	for _, c := range cases {
		fv := parseCreateFormInput(&createFormRawInput{Title: "t", IssueType: "task", Priority: "2", Assignee: c.in})
		if fv.Assignee != c.want {
			t.Errorf("parseCreateFormInput assignee %q -> %q, want %q", c.in, fv.Assignee, c.want)
		}
	}
}

// TestValidateCreateFormValues_RejectsEmptyTitle verifies beads-1077e: a
// whitespace-only form title is rejected (after trim), matching single create
// ("title cannot be empty").
func TestValidateCreateFormValues_RejectsEmptyTitle(t *testing.T) {
	t.Setenv(gtInternalEnv, "")
	fv := parseCreateFormInput(&createFormRawInput{Title: "   ", IssueType: "task", Priority: "2"})
	if err := validateCreateFormValues(fv); err == nil {
		t.Error("validateCreateFormValues should reject a whitespace-only title, got nil")
	}
}

// TestValidateCreateFormValues_RejectsReservedIdentityLabel verifies beads-1077e:
// a reserved gt identity label (gt:agent/gt:role/gt:rig) set via the form is
// rejected on a non-gt-internal write, matching single create / label add /
// graph (f8fvh) / markdown --file (kvq0v) — the beads-3c4g spoof vector.
func TestValidateCreateFormValues_RejectsReservedIdentityLabel(t *testing.T) {
	t.Setenv(gtInternalEnv, "")
	for _, label := range []string{"gt:agent", "gt:role", "gt:rig"} {
		fv := parseCreateFormInput(&createFormRawInput{Title: "spoof", IssueType: "task", Priority: "2", Labels: label})
		err := validateCreateFormValues(fv)
		if err == nil {
			t.Errorf("validateCreateFormValues should reject reserved identity label %q, got nil", label)
			continue
		}
		if !strings.Contains(err.Error(), label) {
			t.Errorf("error for %q = %q, should name the reserved label", label, err.Error())
		}
	}
}

// TestValidateCreateFormValues_AllowsReservedWithGTInternal verifies the fix
// does not break gt's own registration writes.
func TestValidateCreateFormValues_AllowsReservedWithGTInternal(t *testing.T) {
	t.Setenv(gtInternalEnv, gtInternalValue)
	fv := parseCreateFormInput(&createFormRawInput{Title: "gt reg", IssueType: "task", Priority: "2", Labels: "gt:agent, gt:role"})
	if err := validateCreateFormValues(fv); err != nil {
		t.Errorf("validateCreateFormValues with GT_INTERNAL set should allow reserved identity labels, got %v", err)
	}
}

// TestValidateCreateFormValues_AllowsNormalInput verifies the guard does not
// over-reach: a normal titled form with ordinary labels passes.
func TestValidateCreateFormValues_AllowsNormalInput(t *testing.T) {
	t.Setenv(gtInternalEnv, "")
	fv := parseCreateFormInput(&createFormRawInput{Title: "real work", IssueType: "task", Priority: "2", Labels: "area:cli, needs review"})
	if err := validateCreateFormValues(fv); err != nil {
		t.Errorf("validateCreateFormValues should accept normal input, got %v", err)
	}
}
