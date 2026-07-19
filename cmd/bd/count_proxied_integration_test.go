//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestProxiedServerCount proves bd count is proxied-server-aware (beads-2om1):
// count.go used the nil global `store` (CountIssues/CountIssuesByGroup +
// loadDirectListFilterConfig) in proxiedServerMode → "storage is nil". The fix
// is an interface-extension leg — CountIssues/CountIssuesByGroup added to the
// domain IssueUseCase (backed by issueops widened *sql.Tx→DBTX) + a
// usesProxiedServer() gate routing through a countBackend over the UOW.
func TestProxiedServerCount(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("count_total", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cnt")
		bdProxiedCreate(t, bd, p.dir, "one", "--type", "task")
		bdProxiedCreate(t, bd, p.dir, "two", "--type", "task")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "count")
		if err != nil {
			t.Fatalf("bd count failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd count hit 'storage is nil' in proxied mode:\n%s\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, "2") {
			t.Errorf("expected count 2:\n%s", stdout)
		}
	})

	t.Run("count_json", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cnj")
		bdProxiedCreate(t, bd, p.dir, "a", "--type", "task")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "count", "--json")
		if err != nil {
			t.Fatalf("bd count --json failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd count --json hit 'storage is nil':\n%s\n%s", stdout, stderr)
		}
		start := strings.Index(stdout, "{")
		if start < 0 {
			t.Fatalf("no JSON object in count output:\n%s", stdout)
		}
		var out struct {
			Count int64 `json:"count"`
		}
		if err := json.Unmarshal([]byte(stdout[start:]), &out); err != nil {
			t.Fatalf("parse count JSON: %v\nraw: %s", err, stdout[start:])
		}
		if out.Count != 1 {
			t.Errorf("expected count 1, got %d", out.Count)
		}
	})

	t.Run("count_by_status", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cbs")
		i1 := bdProxiedCreate(t, bd, p.dir, "open one", "--type", "task")
		_ = i1
		i2 := bdProxiedCreate(t, bd, p.dir, "to close", "--type", "task")
		bdProxiedClose(t, bd, p.dir, i2.ID)

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "count", "--by-status")
		if err != nil {
			t.Fatalf("bd count --by-status failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("bd count --by-status hit 'storage is nil':\n%s\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, "Total:") {
			t.Errorf("expected grouped output with Total:\n%s", stdout)
		}
	})

	t.Run("count_invalid_status_fails", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "cif")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "count", "--status", "bogusstatus")
		if err == nil {
			t.Fatalf("expected bd count with an invalid status to fail; got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "storage is nil") {
			t.Fatalf("invalid-status path hit 'storage is nil' rather than a validation error:\n%s\n%s", stdout, stderr)
		}
	})
}
