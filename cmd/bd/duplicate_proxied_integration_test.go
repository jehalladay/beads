//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerDuplicate proves `bd duplicate <id> --of <canonical>` and
// `bd supersede <id> --with <new>` are proxied-server-aware (beads-crys).
//
// Before the fix, runDuplicate/runSupersede (cmd/bd/duplicate.go) called the
// global `store` (ResolvePartialID + GetIssue + LinkAndClose) which is NIL in
// proxiedServerMode — neither command is a noDbCommand — so both nil-panicked
// (or reported "storage is nil") for hub-connected crew, unlike bd close /
// bd update which route to a proxied UOW handler.
//
// This is the clean-mirror leg of the beads-crys bead (whose original
// description was stale — it cited GetDependentsWithMetadata, but njnw
// refactored the handler to the atomic store.LinkAndClose). The mirror needs
// only GetIssue + AddDependency + CloseIssue, all on the UOW; the atomicity
// njnw guarantees (edge only durable when the close also commits) is preserved
// by staging both on a single UOW and committing once.
func TestProxiedServerDuplicate(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("duplicate_does_not_nil_panic_and_closes", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "dup")
		canonical := bdProxiedCreate(t, bd, p.dir, "Canonical login bug", "--type", "bug")
		dup := bdProxiedCreate(t, bd, p.dir, "Same login bug reported again", "--type", "bug")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "duplicate", dup.ID, "--of", canonical.ID)
		combined := stdout + stderr
		if strings.Contains(combined, "storage is nil") {
			t.Fatalf("bd duplicate hit 'storage is nil' in proxied mode (not proxied-server-aware):\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if strings.Contains(combined, "nil pointer dereference") || strings.Contains(combined, "panic:") {
			t.Fatalf("bd duplicate PANICKED in proxied mode (nil store deref):\nerr:%v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if err != nil {
			t.Fatalf("bd duplicate failed in proxied mode: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}

		// The duplicate must now be closed (the whole point of the verb).
		showOut, _, _ := bdProxiedRunBuffers(t, bd, p.dir, "show", dup.ID)
		if !strings.Contains(strings.ToLower(showOut), "closed") {
			t.Errorf("expected duplicate %s to be closed after bd duplicate, show output:\n%s", dup.ID, showOut)
		}
	})

	t.Run("duplicate_json_does_not_nil_panic", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "duj")
		canonical := bdProxiedCreate(t, bd, p.dir, "Canonical header bug", "--type", "bug")
		dup := bdProxiedCreate(t, bd, p.dir, "Header renders twice", "--type", "bug")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "duplicate", dup.ID, "--of", canonical.ID, "--json")
		combined := stdout + stderr
		if strings.Contains(combined, "storage is nil") || strings.Contains(combined, "nil pointer dereference") || strings.Contains(combined, "panic:") {
			t.Fatalf("bd duplicate --json hit nil store in proxied mode:\nerr:%v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if err != nil {
			t.Fatalf("bd duplicate --json failed in proxied mode: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if !strings.Contains(stdout, "\"canonical\"") || !strings.Contains(stdout, "\"duplicate\"") {
			t.Errorf("expected duplicate/canonical keys in --json output:\n%s", stdout)
		}
	})

	t.Run("duplicate_of_missing_canonical_errors_no_panic", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "dum")
		dup := bdProxiedCreate(t, bd, p.dir, "A bug", "--type", "bug")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "duplicate", dup.ID, "--of", "dum-nonexistent")
		combined := stdout + stderr
		if strings.Contains(combined, "nil pointer dereference") || strings.Contains(combined, "panic:") {
			t.Fatalf("bd duplicate with missing canonical PANICKED in proxied mode:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if err == nil {
			t.Fatalf("expected bd duplicate with a nonexistent canonical to error, got success:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	})

	t.Run("supersede_does_not_nil_panic_and_closes", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "sup")
		newIssue := bdProxiedCreate(t, bd, p.dir, "New replacement spec", "--type", "task")
		old := bdProxiedCreate(t, bd, p.dir, "Old spec superseded", "--type", "task")

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "supersede", old.ID, "--with", newIssue.ID)
		combined := stdout + stderr
		if strings.Contains(combined, "storage is nil") {
			t.Fatalf("bd supersede hit 'storage is nil' in proxied mode (not proxied-server-aware):\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if strings.Contains(combined, "nil pointer dereference") || strings.Contains(combined, "panic:") {
			t.Fatalf("bd supersede PANICKED in proxied mode (nil store deref):\nerr:%v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if err != nil {
			t.Fatalf("bd supersede failed in proxied mode: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		showOut, _, _ := bdProxiedRunBuffers(t, bd, p.dir, "show", old.ID)
		if !strings.Contains(strings.ToLower(showOut), "closed") {
			t.Errorf("expected superseded %s to be closed after bd supersede, show output:\n%s", old.ID, showOut)
		}
	})
}
