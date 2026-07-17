package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// beads-sxsk: hermetic tests for readBulkDepEdges + bulkDepValidationError
// (dep.go) and collectLabelArgs (label.go). Verified 0% + no test references.

func TestBulkDepValidationError(t *testing.T) {
	err := bulkDepValidationError([]string{"line 1: bad", "line 2: worse"})
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "bulk dependency validation failed") ||
		!strings.Contains(msg, "line 1: bad") || !strings.Contains(msg, "line 2: worse") {
		t.Errorf("error should aggregate all messages, got %q", msg)
	}
}

func writeBulkFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "edges.ndjson")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write bulk file: %v", err)
	}
	return p
}

func TestReadBulkDepEdges(t *testing.T) {
	t.Run("parses valid edges with from/to and default type", func(t *testing.T) {
		content := `{"from":"a","to":"b","type":"blocks"}
{"issue_id":"c","depends_on_id":"d"}

{"from":"e","to":"f"}
`
		edges, err := readBulkDepEdges(writeBulkFile(t, content), "related")
		if err != nil {
			t.Fatalf("readBulkDepEdges: %v", err)
		}
		if len(edges) != 3 {
			t.Fatalf("expected 3 edges (blank line skipped), got %d", len(edges))
		}
		// Line 1: explicit blocks.
		if edges[0].IssueID != "a" || edges[0].DependsOnID != "b" || edges[0].Type != types.DepBlocks {
			t.Errorf("edge0 = %+v", edges[0])
		}
		// Line 2: from/to via issue_id/depends_on_id aliases.
		if edges[1].IssueID != "c" || edges[1].DependsOnID != "d" {
			t.Errorf("edge1 alias fields = %+v", edges[1])
		}
		// Line 4: type falls back to the provided default.
		if string(edges[2].Type) != "related" {
			t.Errorf("edge2 default type = %q, want related", edges[2].Type)
		}
		// Line numbers track the source lines (blank line 3 was skipped).
		if edges[2].Line != 4 {
			t.Errorf("edge2.Line = %d, want 4", edges[2].Line)
		}
	})

	t.Run("invalid JSON line is reported", func(t *testing.T) {
		_, err := readBulkDepEdges(writeBulkFile(t, "{not json}\n"), "blocks")
		if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
			t.Fatalf("expected invalid-JSON error, got %v", err)
		}
	})

	t.Run("missing from/to is reported", func(t *testing.T) {
		_, err := readBulkDepEdges(writeBulkFile(t, `{"from":"a"}`+"\n"), "blocks")
		if err == nil || !strings.Contains(err.Error(), "missing to") {
			t.Fatalf("expected missing-to error, got %v", err)
		}
	})

	t.Run("invalid dependency type is reported", func(t *testing.T) {
		// A >50-char type is invalid per DependencyType.IsValid.
		long := strings.Repeat("x", 51)
		_, err := readBulkDepEdges(writeBulkFile(t, `{"from":"a","to":"b","type":"`+long+`"}`+"\n"), "blocks")
		if err == nil || !strings.Contains(err.Error(), "invalid dependency type") {
			t.Fatalf("expected invalid-type error, got %v", err)
		}
	})

	t.Run("nonexistent file errors", func(t *testing.T) {
		if _, err := readBulkDepEdges(filepath.Join(t.TempDir(), "nope.ndjson"), "blocks"); err == nil {
			t.Error("expected open error for a missing file")
		}
	})

	t.Run("empty file yields no edges, no error", func(t *testing.T) {
		edges, err := readBulkDepEdges(writeBulkFile(t, "\n\n"), "blocks")
		if err != nil {
			t.Fatalf("empty file: %v", err)
		}
		if len(edges) != 0 {
			t.Errorf("expected 0 edges, got %d", len(edges))
		}
	})
}

func TestCollectLabelArgs(t *testing.T) {
	t.Run("flag labels present → all args are issue IDs", func(t *testing.T) {
		ids, labels := collectLabelArgs([]string{"id1", "id2"}, []string{"bug", "urgent"})
		if len(ids) != 2 || ids[0] != "id1" {
			t.Errorf("ids = %v, want [id1 id2]", ids)
		}
		if len(labels) != 2 || labels[0] != "bug" || labels[1] != "urgent" {
			t.Errorf("labels = %v, want [bug urgent]", labels)
		}
	})

	t.Run("no flag labels → last arg is the label", func(t *testing.T) {
		ids, labels := collectLabelArgs([]string{"id1", "id2", "mylabel"}, nil)
		if len(ids) != 2 || ids[1] != "id2" {
			t.Errorf("ids = %v, want [id1 id2]", ids)
		}
		if len(labels) != 1 || labels[0] != "mylabel" {
			t.Errorf("labels = %v, want [mylabel]", labels)
		}
	})

	t.Run("dedups and trims labels, drops empties", func(t *testing.T) {
		ids, labels := collectLabelArgs([]string{"id1"}, []string{" bug ", "bug", "", "urgent"})
		if len(ids) != 1 {
			t.Errorf("ids = %v, want [id1]", ids)
		}
		if len(labels) != 2 || labels[0] != "bug" || labels[1] != "urgent" {
			t.Errorf("labels = %v, want deduped [bug urgent]", labels)
		}
	})

	t.Run("empty args and no flags → nothing", func(t *testing.T) {
		ids, labels := collectLabelArgs(nil, nil)
		if len(ids) != 0 || len(labels) != 0 {
			t.Errorf("expected empty, got ids=%v labels=%v", ids, labels)
		}
	})
}
