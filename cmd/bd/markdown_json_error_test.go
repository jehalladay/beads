//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// beads-1z1l: createIssuesFromMarkdown emits outputJSON on success (markdown.go),
// so its error paths must honor the --json contract too — a bare HandleError
// prints plain text to stderr, breaking `bd create --file foo.md --json`
// consumers doing json.load on stdout. Same 8lqh class as landed beads-xwjg.
//
// Both legs below fire in createIssuesFromMarkdown BEFORE any store use (the
// parse error before parsing succeeds; the empty-file "no issues found" guard
// before the store==nil check), so they are deterministic and server-free.
func TestCreateMarkdownJSONErrorContract_1z1l(t *testing.T) {
	prevJSON := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = prevJSON })

	t.Run("parse_error_missing_file", func(t *testing.T) {
		// A .md path that does not exist → parseMarkdownFile errors (os.Stat
		// fails) → the parsing-markdown-file error path.
		missing := filepath.Join(t.TempDir(), "does-not-exist.md")
		out, err := captureStdoutExpectErr(t, func() error {
			return createIssuesFromMarkdown(nil, missing, false)
		})
		assertJSONErrorObject(t, out, err)
	})

	t.Run("empty_file_no_issues", func(t *testing.T) {
		// A valid, readable .md file with no H2 issue headings → 0 templates →
		// the "no issues found in markdown file" path, still before store use.
		empty := filepath.Join(t.TempDir(), "empty.md")
		if werr := os.WriteFile(empty, []byte("just some prose, no headings\n"), 0o600); werr != nil {
			t.Fatalf("write empty.md: %v", werr)
		}
		out, err := captureStdoutExpectErr(t, func() error {
			return createIssuesFromMarkdown(nil, empty, false)
		})
		assertJSONErrorObject(t, out, err)
	})
}
