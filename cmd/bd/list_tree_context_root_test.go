package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// capturePrettyFooter renders via the given fn and returns captured stdout.
func capturePrettyFooter(t *testing.T, fn func()) string {
	t.Helper()
	stdioMutex.Lock()
	defer stdioMutex.Unlock()

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	fn()
	w.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r)
	os.Stdout = oldStdout
	return buf.String()
}

// beads-bubp: `bd children`/`bd list --parent --pretty` prepend the queried
// parent as the tree root for hierarchy context; the footer counted it, so the
// human "Total: N issues" was +1 vs --json (children only). The context-root
// variant must exclude the parent from the footer count so human == json.
func TestDisplayPrettyListFooter_ExcludesContextRoot(t *testing.T) {
	parent := &types.Issue{ID: "bubp-parent", Title: "Parent", Status: types.StatusOpen, Priority: 2}
	kids := []*types.Issue{
		{ID: "bubp-c1", Title: "Child 1", Status: types.StatusOpen, Priority: 2},
		{ID: "bubp-c2", Title: "Child 2", Status: types.StatusOpen, Priority: 2},
		{ID: "bubp-c3", Title: "Child 3", Status: types.StatusInProgress, Priority: 2},
	}
	// treeIssues as getHierarchicalChildren builds it: children + the parent root.
	treeIssues := append([]*types.Issue{parent}, kids...)

	t.Run("context-root excluded → footer counts children only (matches --json len 3)", func(t *testing.T) {
		out := capturePrettyFooter(t, func() {
			displayPrettyListWithDepsContextRoot(treeIssues, false, nil, false, "bubp-parent")
		})
		if !strings.Contains(out, "Total: 3 issues (2 open, 1 in progress)") {
			t.Errorf("footer should count 3 children (parent excluded), got:\n%s", out)
		}
		if strings.Contains(out, "Total: 4 issues") {
			t.Errorf("footer must not count the context-root parent, got:\n%s", out)
		}
	})

	t.Run("no context root → all rows counted (plain bd list unaffected)", func(t *testing.T) {
		out := capturePrettyFooter(t, func() {
			displayPrettyListWithDepsContextRoot(treeIssues, false, nil, false, "")
		})
		if !strings.Contains(out, "Total: 4 issues (3 open, 1 in progress)") {
			t.Errorf("with no context root all 4 rows count, got:\n%s", out)
		}
	})

	t.Run("backward-compat wrapper counts all rows (no context root)", func(t *testing.T) {
		out := capturePrettyFooter(t, func() {
			displayPrettyListWithDepsTruncated(treeIssues, false, nil, false)
		})
		if !strings.Contains(out, "Total: 4 issues") {
			t.Errorf("legacy wrapper should count all rows, got:\n%s", out)
		}
	})
}
