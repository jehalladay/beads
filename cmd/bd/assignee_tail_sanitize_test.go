//go:build cgo

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestAssigneeTailSanitize_i8dsb is the sanitize teeth for the tail of the
// Assignee display-sink axis (beads-i8dsb): markdown.go:349 (bd create --file
// <md> --dry-run template preview). The template Assignee comes from an
// untrusted markdown file and is not control-char validated (normalizeAssignee
// only trims), so it rendered raw. restore.go:217, ready_proxied_server.go:197
// and swarm.go:840 receive the identical one-line ui.SanitizeForTerminal wrap
// but are not e2e-asserted here: restore.go prints a pre-compaction snapshot
// (needs a compacted issue), the proxied-ready path needs BEADS_TEST_PROXIED_
// SERVER wiring, and swarm status needs a live swarm epic — all three are
// mechanical mirrors of the verified sites.
func TestAssigneeTailSanitize_i8dsb(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}

	bd := buildEmbeddedBD(t)
	dir, beadsDir, _ := bdInit(t, bd)

	const csi = "\x1b[31m"
	const osc52 = "\x1b]52;c;cHduZWQ=\x07"
	evilAssignee := "evilA" + csi + osc52 + "userZ"

	run := func(t *testing.T, args ...string) string {
		t.Helper()
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bd %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	assertClean := func(t *testing.T, label, got string) {
		t.Helper()
		if strings.ContainsRune(got, '\x1b') {
			t.Errorf("%s leaked a raw ESC (0x1b) — assignee not sanitized:\n%q", label, got)
		}
		if strings.ContainsRune(got, '\x07') {
			t.Errorf("%s leaked a raw BEL (0x07) — assignee not sanitized:\n%q", label, got)
		}
		if !strings.Contains(got, "userZ") {
			t.Errorf("%s dropped expected visible username; output:\n%s", label, got)
		}
	}

	// markdown.go:349 — bd create --file <md> --dry-run template preview.
	t.Run("markdown-dryrun", func(t *testing.T) {
		md := "## Task from markdown\n\n### Assignee\n" + evilAssignee + "\n"
		mdPath := filepath.Join(beadsDir, "tmpl.md")
		if err := os.WriteFile(mdPath, []byte(md), 0o644); err != nil {
			t.Fatalf("write markdown: %v", err)
		}
		assertClean(t, "bd create --file --dry-run", run(t, "create", "--file", mdPath, "--dry-run"))
	})
}
