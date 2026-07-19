//go:build cgo

package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// beads-dycj: the --json SUCCESS payloads of `bd forget` and `bd kv clear`
// emitted their bool-semantic fields (found / deleted) as STRING literals
// ("true"/"false") rather than real JSON booleans, because the handlers built
// a map[string]string. A consumer doing `if result["found"]` reads the
// non-empty string "false" as truthy → misreads not-found as found. The fix
// switches to map[string]interface{} with real booleans, matching the sibling
// `bd recall` in the same file. These teeth decode the JSON and assert the
// value is a Go bool (json.Unmarshal into interface{} yields bool for true JSON
// booleans and string for "true"/"false"), so a regression to string literals
// fails loudly.

// seedMemoryKVTest sets the package store global to a real embedded store,
// marks it active (so ensureDirectMode passes), enables jsonOutput, and returns
// the store + context for seeding keys.
func seedMemoryKVTest(t *testing.T) context.Context {
	t.Helper()
	tmpDir := t.TempDir()
	real := newTestStore(t, filepath.Join(tmpDir, ".beads", "beads.db"))
	ctx := context.Background()

	prevStore, prevCtx, prevJSON, prevActive := store, rootCtx, jsonOutput, isStoreActive()
	rootCtx = ctx
	jsonOutput = true
	setStore(real)
	setStoreActive(true)
	t.Cleanup(func() {
		store, rootCtx, jsonOutput = prevStore, prevCtx, prevJSON
		setStoreActive(prevActive)
	})
	return ctx
}

// decodeBoolField asserts that stdout is a JSON object whose named field is a
// real JSON boolean equal to want (not the string literal "true"/"false").
func decodeBoolField(t *testing.T, out, field string, want bool) {
	t.Helper()
	s := strings.TrimSpace(out)
	if s == "" {
		t.Fatalf("expected a JSON object on stdout, got empty")
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(s), &obj); err != nil {
		t.Fatalf("stdout is not a JSON object: %v\nstdout:\n%s", err, s)
	}
	v, ok := obj[field]
	if !ok {
		t.Fatalf("expected field %q in JSON object, got: %s", field, s)
	}
	b, ok := v.(bool)
	if !ok {
		t.Fatalf("beads-dycj: field %q must be a JSON boolean, got %T (%v) — string-literal bool regression\nstdout:\n%s", field, v, v, s)
	}
	if b != want {
		t.Errorf("field %q = %v, want %v\nstdout:\n%s", field, b, want, s)
	}
}

func TestForgetJSONBoolContract_dycj(t *testing.T) {
	ctx := seedMemoryKVTest(t)

	// forget not-found → {"found": false} (real bool). The not-found path emits
	// the JSON then returns SilentExit() (exit 1), so use the expect-err capture.
	out, err := captureStdoutExpectErr(t, func() error { return forgetCmd.RunE(forgetCmd, []string{"no-such-key"}) })
	if err == nil {
		t.Fatalf("expected SilentExit (non-nil) on forget not-found, got nil")
	}
	decodeBoolField(t, out, "found", false)

	// forget found → {"deleted": true} (real bool)
	if err := store.SetConfig(ctx, kvPrefix+memoryPrefix+"dycj-key", "some value"); err != nil {
		t.Fatalf("seed memory key: %v", err)
	}
	out = captureStdout(t, func() error { return forgetCmd.RunE(forgetCmd, []string{"dycj-key"}) })
	decodeBoolField(t, out, "deleted", true)
}

func TestKVClearJSONBoolContract_dycj(t *testing.T) {
	ctx := seedMemoryKVTest(t)

	// kv clear of an existing key → {"deleted": true} (real bool)
	if err := store.SetConfig(ctx, kvPrefix+"dycj-kv", "v"); err != nil {
		t.Fatalf("seed kv key: %v", err)
	}
	out := captureStdout(t, func() error { return kvClearCmd.RunE(kvClearCmd, []string{"dycj-kv"}) })
	decodeBoolField(t, out, "deleted", true)
}
