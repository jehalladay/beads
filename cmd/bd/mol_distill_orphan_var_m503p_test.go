package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-m503p: `bd mol distill --var X=Y` where the value Y appears ONLY in the
// root epic TITLE previously emitted a formula declaring vars.<name>.required=true
// even though {{name}} landed in NO emitted field. Cause: the validation scope
// (collectSubgraphText, which includes Root.Title) diverges from the emit scope
// (the root becomes the formula itself, so its Title is carried into no emitted
// field). The declared-required var is then orphaned — referenced by zero
// placeholders, and pour (which keys required vars off {{...}} occurrences in
// the emitted text, not the flag) silently ignores it. The fix declares a var
// ONLY if its {{placeholder}} actually appears in an emitted field.
func TestDistillDropsOrphanedRootTitleVar_m503p(t *testing.T) {
	// Value "ACMEDB" lives ONLY in the root epic Title — nowhere in the root
	// Description or any child step. It must NOT become a declared var.
	subgraph := &TemplateSubgraph{
		Root: &types.Issue{
			ID:          "root",
			Title:       "Migrate ACMEDB cluster",
			Description: "Migration epic",
		},
		Issues: []*types.Issue{
			{ID: "root", Title: "Migrate ACMEDB cluster", Description: "Migration epic"},
			{ID: "step", Title: "backup data", Description: "take a snapshot"},
		},
	}
	// replacements is map[VALUE]VARNAME (subgraphToFormula's caller convention).
	f := subgraphToFormula(subgraph, "titlecase", map[string]string{"ACMEDB": "db"})
	if f == nil {
		t.Fatal("subgraphToFormula returned nil")
	}

	// The orphaned var must NOT be declared.
	if _, ok := f.Vars["db"]; ok {
		t.Errorf("var 'db' declared but its value lived only in the root title (orphaned required var, beads-m503p): Vars=%v", f.Vars)
	}

	// Sanity: {{db}} must appear in NO emitted field (steps + description),
	// which is exactly why it should not be declared.
	var emitted strings.Builder
	for _, s := range f.Steps {
		emitted.WriteString(s.Title)
		emitted.WriteByte(' ')
		emitted.WriteString(s.Description)
		emitted.WriteByte(' ')
	}
	emitted.WriteString(f.Description)
	if strings.Contains(emitted.String(), "{{db}}") {
		t.Fatalf("test premise broken: {{db}} unexpectedly present in an emitted field: %q", emitted.String())
	}
}

// Positive control: a --var value that DOES appear in an emitted field (a step
// title) must still be declared required — the fix prunes only orphans, it does
// not drop legitimately-referenced vars.
func TestDistillKeepsReferencedVar_m503p(t *testing.T) {
	subgraph := &TemplateSubgraph{
		Root: &types.Issue{ID: "root", Title: "Root epic", Description: "root desc"},
		Issues: []*types.Issue{
			{ID: "root", Title: "Root epic", Description: "root desc"},
			{ID: "step", Title: "deploy to prod", Description: "ship it"},
		},
	}
	// "prod" appears in the step title → {{env}} lands in the emitted step.
	f := subgraphToFormula(subgraph, "deploy", map[string]string{"prod": "env"})
	if f == nil {
		t.Fatal("subgraphToFormula returned nil")
	}
	vd, ok := f.Vars["env"]
	if !ok {
		t.Fatalf("var 'env' NOT declared, but {{env}} lands in an emitted step title (regression): Vars=%v", f.Vars)
	}
	if !vd.Required {
		t.Errorf("var 'env' should be Required=true; got %+v", vd)
	}
	// Guard the substitution actually landed in the emitted step title.
	if !strings.Contains(f.Steps[0].Title, "{{env}}") {
		t.Errorf("step title should contain {{env}} after substitution; got %q", f.Steps[0].Title)
	}
}

// A value referenced in the root DESCRIPTION (an emitted field, unlike the
// title) must still be declared — proves the fix keys on the emit scope, not
// on "is it the root" (only the title is dropped, because only the title is
// non-emitted).
func TestDistillKeepsRootDescriptionVar_m503p(t *testing.T) {
	subgraph := &TemplateSubgraph{
		Root: &types.Issue{ID: "root", Title: "Root epic", Description: "target region us-east"},
		Issues: []*types.Issue{
			{ID: "root", Title: "Root epic", Description: "target region us-east"},
			{ID: "step", Title: "provision", Description: "spin up"},
		},
	}
	f := subgraphToFormula(subgraph, "provision", map[string]string{"us-east": "region"})
	if f == nil {
		t.Fatal("subgraphToFormula returned nil")
	}
	if _, ok := f.Vars["region"]; !ok {
		t.Errorf("var 'region' NOT declared, but {{region}} lands in the emitted formula Description: Description=%q Vars=%v", f.Description, f.Vars)
	}
	if !strings.Contains(f.Description, "{{region}}") {
		t.Errorf("formula Description should carry {{region}} after substitution; got %q", f.Description)
	}
}
