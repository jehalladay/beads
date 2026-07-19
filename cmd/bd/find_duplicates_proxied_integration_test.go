//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerFindDuplicates proves bd find-duplicates is
// proxied-server-aware (beads-zawz). Before the fix, find_duplicates.go called
// store.SearchIssues() with a nil global `store` in proxiedServerMode
// (find-duplicates is not a noDbCommand) → nil pointer dereference panic on
// hub-connected crew. This is a clean-mirror leg (iq3i/mtgy class): SearchIssues
// is already on the UOW IssueUseCase, so the fix routes the issue fetch through
// the proxied UOW stack (fetchFindDuplicatesIssuesProxied) and reuses the
// store-free pairing/render.
func TestProxiedServerFindDuplicates(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("find_duplicates_does_not_nil_panic", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "fdp")
		// Two similar issues so the mechanical pass has something to pair.
		bdProxiedCreate(t, bd, p.dir, "Login button is broken on mobile", "--type", "bug")
		bdProxiedCreate(t, bd, p.dir, "Login button broken on mobile devices", "--type", "bug")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "find-duplicates")
		combined := stdout + stderr
		if strings.Contains(combined, "storage is nil") {
			t.Fatalf("bd find-duplicates hit 'storage is nil' in proxied mode (not proxied-server-aware):\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if strings.Contains(combined, "nil pointer dereference") || strings.Contains(combined, "panic:") {
			t.Fatalf("bd find-duplicates PANICKED in proxied mode (nil store deref):\nerr:%v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if err != nil {
			t.Fatalf("bd find-duplicates failed in proxied mode: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
	})

	t.Run("find_duplicates_json_does_not_nil_panic", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "fdj")
		bdProxiedCreate(t, bd, p.dir, "Cannot save settings page", "--type", "bug")
		bdProxiedCreate(t, bd, p.dir, "Unable to save the settings page", "--type", "bug")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "find-duplicates", "--json")
		combined := stdout + stderr
		if strings.Contains(combined, "storage is nil") {
			t.Fatalf("bd find-duplicates --json hit 'storage is nil' in proxied mode:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if strings.Contains(combined, "nil pointer dereference") || strings.Contains(combined, "panic:") {
			t.Fatalf("bd find-duplicates --json PANICKED in proxied mode:\nerr:%v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if err != nil {
			t.Fatalf("bd find-duplicates --json failed in proxied mode: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		// Success path emits a JSON object with a pairs array.
		if !strings.Contains(stdout, "\"pairs\"") {
			t.Errorf("expected a \"pairs\" key in --json output:\n%s", stdout)
		}
	})

	t.Run("find_duplicates_status_filter_does_not_nil_panic", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "fds")
		bdProxiedCreate(t, bd, p.dir, "Crash on startup with empty config", "--type", "bug")
		bdProxiedCreate(t, bd, p.dir, "Startup crash when config is empty", "--type", "bug")

		// --status exercises the loadProxiedListFilterConfig custom-status path.
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "find-duplicates", "--status", "open")
		combined := stdout + stderr
		if strings.Contains(combined, "storage is nil") || strings.Contains(combined, "nil pointer dereference") || strings.Contains(combined, "panic:") {
			t.Fatalf("bd find-duplicates --status open hit nil store in proxied mode:\nerr:%v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if err != nil {
			t.Fatalf("bd find-duplicates --status open failed in proxied mode: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
	})
}
