//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedServerExport proves bd export is proxied-server-aware (beads-948qg).
// Before the fix, runExport called store.SearchIssues (+ 4 relational bulk-loads
// and GetInfraTypes/GetAllConfig) directly on the nil global `store` in
// proxiedServerMode (export is not a noDbCommand and had NO usesProxiedServer()
// routing) → a hard nil-pointer PANIC on hub-connected crew, the worst-case
// failure mode the aocj/i2v77 umbrella flagged for merge-slot. Because export is
// a pure READ fully satisfiable via the proxied UOW (SearchIssues + the label/
// dependency/comment use-cases already work proxied — see searchGatesProxied /
// list_proxied), the disposition is interface-ext: export WORKS hub-connected,
// mirroring beads-mh3e (diff), not fail-loud like the mutation members.
func TestProxiedServerExport(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("export_does_not_nil_panic_and_emits_jsonl", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "xpe")
		i1 := bdProxiedCreate(t, bd, p.dir, "Export proxied one", "--type", "task", "--priority", "2")
		i2 := bdProxiedCreate(t, bd, p.dir, "Export proxied two", "--type", "bug", "--priority", "1")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "export")
		combined := stdout + stderr
		if strings.Contains(combined, "storage is nil") {
			t.Fatalf("bd export hit 'storage is nil' in proxied mode (not proxied-server-aware):\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if strings.Contains(combined, "nil pointer dereference") || strings.Contains(combined, "panic:") {
			t.Fatalf("bd export PANICKED in proxied mode (nil store deref) — not proxied-server-aware:\nerr:%v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if err != nil {
			t.Fatalf("bd export failed in proxied mode: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}

		// Every non-empty stdout line must be a valid JSON object (JSONL), and the
		// two created issues must appear as _type:"issue" records — proving the
		// proxied SearchIssues + bulk-load path produced a real export, not just a
		// non-panicking empty result.
		seen := map[string]bool{}
		var lines int
		for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			lines++
			var rec map[string]interface{}
			if uerr := json.Unmarshal([]byte(line), &rec); uerr != nil {
				t.Fatalf("export emitted a non-JSON line in proxied mode: %q\nerr: %v", line, uerr)
			}
			if rec["_type"] == "issue" {
				if id, ok := rec["id"].(string); ok {
					seen[id] = true
				}
			}
		}
		if lines == 0 {
			t.Fatalf("bd export produced NO JSONL lines in proxied mode:\nstdout:\n%s", stdout)
		}
		if !seen[i1.ID] || !seen[i2.ID] {
			t.Fatalf("expected both created issues %s and %s in the proxied export, got ids: %v\nstdout:\n%s", i1.ID, i2.ID, seen, stdout)
		}
	})

	t.Run("export_relational_data_via_proxied_bulk_loads", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "xpr")
		// A canonical + a blocking dep + a label + a comment exercise every
		// relational bulk-load adapter (labels/deps/comments/counts) on the
		// proxied path, not just SearchIssues.
		blocker := bdProxiedCreate(t, bd, p.dir, "Export blocker", "--type", "task")
		main := bdProxiedCreate(t, bd, p.dir, "Export main", "--type", "task", "--label", "xpr-tag")
		if _, err := bdProxiedRun(t, bd, p.dir, "dep", "add", main.ID, blocker.ID, "--type", "blocks"); err != nil {
			t.Fatalf("dep add failed: %v", err)
		}
		if _, err := bdProxiedRun(t, bd, p.dir, "comment", main.ID, "an export comment"); err != nil {
			t.Fatalf("comment add failed: %v", err)
		}

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "export", "--all")
		if err != nil {
			t.Fatalf("bd export --all failed in proxied mode: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		combined := stdout + stderr
		if strings.Contains(combined, "nil pointer dereference") || strings.Contains(combined, "panic:") || strings.Contains(combined, "storage is nil") {
			t.Fatalf("bd export --all nil-panicked in proxied mode:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}

		var mainRec map[string]interface{}
		for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var rec map[string]interface{}
			if uerr := json.Unmarshal([]byte(line), &rec); uerr != nil {
				t.Fatalf("non-JSON export line: %q (%v)", line, uerr)
			}
			if rec["_type"] == "issue" && rec["id"] == main.ID {
				mainRec = rec
			}
		}
		if mainRec == nil {
			t.Fatalf("main issue %s missing from proxied export --all:\nstdout:\n%s", main.ID, stdout)
		}
		// The label bulk-load must have populated the record.
		labels, _ := mainRec["labels"].([]interface{})
		var haveTag bool
		for _, l := range labels {
			if s, ok := l.(string); ok && s == "xpr-tag" {
				haveTag = true
			}
		}
		if !haveTag {
			t.Errorf("expected label 'xpr-tag' on the exported main issue via the proxied label bulk-load, got labels: %v", labels)
		}
	})

	// NOTE: no proxied memory-export subtest — `bd remember` is direct-mode-only
	// (memory.go ensureDirectMode), so a memory can't be SEEDED on the proxied
	// path to assert its round-trip. The memory-read leg (GetAllConfig via
	// ConfigUseCase) is still exercised: --include-memories runs its GetAllConfig
	// call above without panicking, and the same ConfigUseCase.GetInfraTypes
	// adapter is exercised by the default export (infra-type exclusion).
	t.Run("export_include_memories_flag_does_not_panic", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "xpm")
		bdProxiedCreate(t, bd, p.dir, "Export mem issue", "--type", "task")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "export", "--include-memories")
		if err != nil {
			t.Fatalf("bd export --include-memories failed in proxied mode: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "panic:") || strings.Contains(stdout+stderr, "storage is nil") || strings.Contains(stdout+stderr, "nil pointer dereference") {
			t.Fatalf("bd export --include-memories nil-panicked in proxied mode (GetAllConfig on nil store):\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	})
}
