//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerCreateFormNilStore is the teeth for beads-xwoug: `bd
// create-form` has no proxied-server (_proxied_server.go) companion, and its
// shared helper CreateIssueFromFormValues derefs the global `store`
// unconditionally (store.CreateIssue / store.GetIssue / store.GetNextChildID).
//
// GROUND TRUTH (cmd/bd @origin/main pre-fix): in proxiedServerMode main.go's
// PersistentPreRun sets the UOW provider and RETURNS before newDoltStore
// (main.go:1155), so the global `store` stays nil. create-form is NOT in
// noDbCommands and has no ensureStoreActive()/usesProxiedServer() branch, so it
// fits none of the aocj sweep's 4 safe buckets — the aocj top-level sweep
// missed it because the nil deref lives in a helper reached only after
// form.Run(), not at the RunE's first deref site a token-grep would catch.
//
// The guard the fix adds sits at the TOP of runCreateForm, BEFORE form.Run(),
// so it fires deterministically in this non-TTY subprocess. That placement is
// exactly what makes this test DISCRIMINATING despite the non-TTY:
//
//   - WITHOUT the guard, control reaches form.Run() first, which in a non-TTY
//     fails with "form error: ... could not open TTY" (rc=1, no panic). On a
//     real pty it would instead SUCCEED and then nil-deref store → SIGSEGV.
//   - WITH the guard, ensureStoreActive() fails first in proxied config with
//     "failed to open database: proxy server store should be uow provider"
//     (rc=1) — before the form is ever shown.
//
// So a bare "non-zero exit + no panic" assertion would FALSE-GREEN (the
// pre-fix TTY error also satisfies it). The load-bearing assertion is that the
// failure is the STORE guard, NOT the form/TTY error: WITH fix the message
// says "database"/"uow provider"/"beads database"; the pre-fix TTY wording
// ("could not open TTY" / "form error") must be ABSENT. Mutation-verify:
// delete the ensureStoreActive block and this test RED-flips to the TTY
// message (and on a pty the pre-fix code SIGSEGVs — the real-world defect).
func TestProxiedServerCreateFormNilStore(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdProxiedInit(t, bd, "xwoug")

	// --parent forces the parent-status path in CreateIssueFromFormValues that
	// derefs store even before the CreateIssue call; the guard must precede it.
	stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "create-form", "--parent", "xwoug-1")
	combined := stdout + stderr

	if err == nil {
		t.Fatalf("beads-xwoug: create-form unexpectedly SUCCEEDED in proxied mode (must fail loud):\nstdout:\n%s", stdout)
	}
	// Must NOT panic / nil-crash.
	for _, bad := range []string{"panic", "nil pointer", "invalid memory address", "SIGSEGV", "runtime error"} {
		if strings.Contains(combined, bad) {
			t.Fatalf("beads-xwoug: create-form PANICKED/nil-crashed in proxied mode (want a clean fail-loud error), saw %q:\nstdout:\n%s\nstderr:\n%s", bad, stdout, stderr)
		}
	}
	// DISCRIMINATOR: the failure must be the store guard, fired BEFORE the form.
	// The pre-fix code reaches form.Run() and fails on the TTY instead — that
	// wording proves the guard did not run (and on a pty would nil-deref).
	if strings.Contains(combined, "could not open TTY") || strings.Contains(combined, "form error") {
		t.Fatalf("beads-xwoug: create-form reached form.Run() before guarding the nil store in proxied mode (store guard missing — nil deref on a real pty):\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(combined, "database") && !strings.Contains(combined, "uow provider") && !strings.Contains(combined, "beads database") {
		t.Fatalf("beads-xwoug: expected the store-activation guard message (database / uow provider), got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}
