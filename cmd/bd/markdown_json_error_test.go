//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// beads-1z1l: bd create --file <markdown> --json (createIssuesFromMarkdown)
// emits outputJSON(createdIssues) on success, so its error paths must honor the
// --json error contract — a parseable JSON {error} object on stdout
// (HandleErrorRespectJSON), matching the landed xwjg/8lqh class — not a plain
// HandleError that cobra prints as stderr text (which leaves a `bd create --file
// foo.md --json` consumer with empty stdout). The "no issues found" (empty /
// headingless file) leg is deterministic and fires BEFORE any store use, so no
// embedded dolt is needed to exercise it.
func TestMarkdownCreateJSONErrorContract_1z1l(t *testing.T) {
	writeMD := func(t *testing.T, body string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "issues.md")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	prevJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = prevJSON })

	assertJSONError := func(t *testing.T, label, stdout string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: expected a non-nil error, got nil (stdout=%q)", label, stdout)
		}
		out := strings.TrimSpace(stdout)
		if out == "" {
			t.Fatalf("%s: stdout empty on a --json error — must emit a JSON {error} object (beads-1z1l), err=%v", label, err)
		}
		var obj map[string]any
		if jerr := json.Unmarshal([]byte(out), &obj); jerr != nil {
			t.Fatalf("%s: stdout is not a JSON object on --json error: %v\nstdout:\n%s", label, jerr, out)
		}
		if _, ok := obj["error"]; !ok {
			t.Errorf("%s: expected an \"error\" field in the --json stdout object, got: %s", label, out)
		}
	}

	// A file with no H2 (## Title) heading makes parseMarkdownFile itself return
	// an error ("no issues found ... expected ## Issue Title format"), hitting the
	// parse-error leg (markdown.go:329). This is reached BEFORE any store access,
	// so it's deterministic + server-free — under --json it must be a stdout
	// {error} object, not plain stderr. (RED-proven: reverting the :329 fix →
	// "stdout empty on a --json error".)
	t.Run("parse_error_json_error", func(t *testing.T) {
		mdPath := writeMD(t, "just some prose, no ## heading anywhere\n")
		out, err := captureStdoutExpectErr(t, func() error {
			return createIssuesFromMarkdown(nil, mdPath, false)
		})
		assertJSONError(t, "parse error", out, err)
	})
}
