//go:build cgo

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEditJSONContract_8872 is the end-to-end regression for beads-8872:
// `bd edit <id> --json` ignored the JSON flag on BOTH terminal paths — the
// success case printed the plaintext "✓ Updated <field> for issue: <id>" glyph
// line and the no-op case printed "No changes made", each with rc=0 and NO
// outputJSON call. A script consuming `bd edit ... --json` therefore got
// unparseable plaintext on stdout.
//
// The fix emits the (mutated or unchanged) issue as a one-element JSON array on
// stdout, matching the update/priority/assign/tag/note single-issue mutation
// contract (ARRAY shape, beads-yrtx/utby/bjyq). Asserts: stdout is exactly one
// JSON array holding the target issue, no plaintext glyph leaks onto stdout.
func TestEditJSONContract_8872(t *testing.T) {
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ej")

	t.Run("changed_field_emits_json_array", func(t *testing.T) {
		iss := bdCreate(t, bd, dir, "edit target", "--type", "task", "-d", "old body")
		stdout, stderr, err := runEditJSON(t, bd, dir, "brand new body", iss.ID)
		if err != nil {
			t.Fatalf("bd edit --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		id := assertSingleEditIssueArray(t, "edit description", stdout)
		if id != iss.ID {
			t.Fatalf("edit --json returned issue_id=%s, want %s\nstdout:\n%s", id, iss.ID, stdout)
		}
		// The mutated field must be persisted (guards against a JSON-of-stale-issue).
		if desc := jsonFieldOfShow(t, bd, dir, iss.ID, "description"); desc != "brand new body" {
			t.Fatalf("edited description did not persist: got %q via show --json", desc)
		}
	})

	t.Run("no_change_emits_json_array_not_plaintext", func(t *testing.T) {
		iss := bdCreate(t, bd, dir, "noop target", "--type", "task", "-d", "unchanged body")
		// Feed the editor the SAME content → a no-op edit.
		stdout, stderr, err := runEditJSON(t, bd, dir, "unchanged body", iss.ID)
		if err != nil {
			t.Fatalf("bd edit --json (no-op) failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout, "No changes made") {
			t.Fatalf("no-op edit --json leaked the plaintext 'No changes made' onto stdout (beads-8872 regression)\nstdout:\n%s", stdout)
		}
		id := assertSingleEditIssueArray(t, "edit no-op", stdout)
		if id != iss.ID {
			t.Fatalf("no-op edit --json returned issue_id=%s, want %s\nstdout:\n%s", id, iss.ID, stdout)
		}
	})
}

// runEditJSON runs `bd edit <args> --json` with EDITOR pointed at a fake editor
// that writes newContent, capturing stdout and stderr SEPARATELY (the bug is
// stdout-specific — CombinedOutput could not distinguish a plaintext-on-stdout
// leak from a legitimate stderr line).
func runEditJSON(t *testing.T, bd, dir, newContent string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	editor := writeDirectFakeEditor(t, dir, newContent)
	full := append([]string{"edit"}, args...)
	full = append(full, "--json")
	cmd := exec.Command(bd, full...)
	cmd.Dir = dir
	cmd.Env = append(bdEnv(dir), "EDITOR="+editor, "VISUAL=")
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	err = cmd.Run()
	return so.String(), se.String(), err
}

// writeDirectFakeEditor writes a non-interactive $EDITOR stub that overwrites
// its target file ($1) with newContent (uniquely named to avoid colliding with
// the proxied-integration writeFakeEditor helper).
func writeDirectFakeEditor(t *testing.T, dir, newContent string) string {
	t.Helper()
	script := filepath.Join(dir, "fake-editor-direct.sh")
	body := "#!/bin/sh\ncat > \"$1\" <<'BD_EDIT_EOF'\n" + newContent + "\nBD_EDIT_EOF\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake editor: %v", err)
	}
	return script
}

// assertSingleEditIssueArray verifies stdout is exactly one JSON array holding a
// single issue object, and returns that object's issue id ("id"). The
// mutation-sensitive assertions are (1) stdout starts with '[' and (2) the
// decoder reports no trailing document — an un-fixed edit emits plaintext (fails
// the '[' check) rather than a JSON array.
func assertSingleEditIssueArray(t *testing.T, label, stdout string) string {
	t.Helper()
	s := strings.TrimSpace(stdout)
	if !strings.HasPrefix(s, "[") {
		t.Fatalf("%s --json stdout is not a JSON array (edit ignored --json, emitted plaintext?)\nstdout:\n%s", label, stdout)
	}
	dec := json.NewDecoder(strings.NewReader(s))
	var arr []map[string]interface{}
	if derr := dec.Decode(&arr); derr != nil {
		t.Fatalf("%s --json stdout is not a decodable array: %v\nstdout:\n%s", label, derr, stdout)
	}
	if dec.More() {
		t.Fatalf("%s --json stdout has a trailing second document\nstdout:\n%s", label, stdout)
	}
	if len(arr) != 1 {
		t.Fatalf("%s --json array = %d elements, want 1\nstdout:\n%s", label, len(arr), stdout)
	}
	id, _ := arr[0]["id"].(string)
	return id
}

// jsonFieldOfShow returns a top-level string field from `bd show <id> --json`.
func jsonFieldOfShow(t *testing.T, bd, dir, id, field string) string {
	t.Helper()
	cmd := exec.Command(bd, "show", id, "--json")
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bd show --json failed: %v\n%s", err, out)
	}
	s := strings.TrimSpace(string(out))
	// show --json emits a single-element array.
	dec := json.NewDecoder(strings.NewReader(s))
	var arr []map[string]interface{}
	if derr := dec.Decode(&arr); derr != nil || len(arr) == 0 {
		t.Fatalf("bd show --json not a non-empty array (%v): %s", derr, s)
	}
	v, _ := arr[0][field].(string)
	return v
}
