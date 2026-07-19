package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEmbeddedImportParseErrorLineNumber proves bd import parse errors name the
// offending 1-based line (beads-ovc4). Before the fix the message was
// "failed to parse JSONL line: <err>" with no line index, forcing an operator
// to bisect a large export/migration file by hand.
func TestEmbeddedImportParseErrorLineNumber(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt import tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "iln")

	// valid, MALFORMED (line 2), valid.
	content := `{"id":"iln-1","title":"ok one","issue_type":"task","status":"open","priority":3}
not json at all
{"id":"iln-3","title":"ok three","issue_type":"task","status":"open","priority":3}
`
	path := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write jsonl: %v", err)
	}

	cmd := exec.Command(bd, "import", path)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	out := stdout.String() + stderr.String()
	if err == nil {
		t.Fatalf("expected import of a malformed line to fail; got success:\n%s", out)
	}
	if !strings.Contains(out, "line 2") {
		t.Errorf("import parse error should name the offending line (line 2), got:\n%s", out)
	}
}
