package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-knab: `bd pour --dry-run` printed proto issue.Title (via
// substituteVariables) and attachment issue.Title RAW via fmt.Printf
// (pour.go), bypassing the ui.SanitizeForTerminal sanitize that human-readable
// output applies. A proto/attachment title can originate from an untrusted
// import (JSONL/markdown/SCM) carrying OSC/CSI terminal-control escapes (OSC 0
// window-title / OSC 52 clipboard), so the dry-run preview injected control
// sequences onto those lines. The fix routes both sinks through displayTitle.
// 7n9y sink-class slice.
func TestPrintPourDryRun_sanitize_knab(t *testing.T) {
	const osc = "\x1b]0;pwned\x07"
	const csi = "\x1b[31m"

	root := &types.Issue{
		ID:     "proto-root",
		Title:  "Root Title" + osc,
		Status: types.StatusOpen,
	}
	child := &types.Issue{
		ID:     "proto-child",
		Title:  "Child" + csi + osc + "Title",
		Status: types.StatusOpen,
	}
	subgraph := &TemplateSubgraph{
		Root:   root,
		Issues: []*types.Issue{root, child},
	}
	attachProto := &types.Issue{
		ID:     "proto-attach",
		Title:  "Attach" + osc + "Proto",
		Status: types.StatusOpen,
	}
	attachments := []attachmentInfo{
		{
			id:       "proto-attach",
			issue:    attachProto,
			subgraph: &TemplateSubgraph{Root: attachProto, Issues: []*types.Issue{attachProto}},
		},
	}

	out := captureStdout(t, func() error {
		printPourDryRun(subgraph, attachments, map[string]string{}, "alice", "hard", "proto-root")
		return nil
	})

	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("dry-run leaked a raw ESC (\\x1b): %q", out)
	}
	if strings.ContainsRune(out, '\x07') {
		t.Errorf("dry-run leaked a raw BEL (\\x07): %q", out)
	}
	// Visible text must survive sanitizing (escapes stripped, chars kept).
	for _, want := range []string{"Root Title", "ChildTitle", "AttachProto"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run dropped visible title text %q: %q", want, out)
		}
	}
	// Structural output must still render: IDs, attachment header, assignee.
	for _, want := range []string{"proto-root", "proto-child", "Attachments", "assignee: alice"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run dropped structural output %q: %q", want, out)
		}
	}
}
