package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// beads-sx1i: hermetic tests for the marker + response-writer helpers in
// codex_hook.go (verified 0% + no test refs). The marker dir is redirected via
// codexHookMarkerDirOverride; response writers take an io.Writer.

func TestCodexHookMarkerBaseDir_Override(t *testing.T) {
	orig := codexHookMarkerDirOverride
	t.Cleanup(func() { codexHookMarkerDirOverride = orig })

	codexHookMarkerDirOverride = "/custom/marker/dir"
	if got := codexHookMarkerBaseDir(); got != "/custom/marker/dir" {
		t.Errorf("override not honored: %q", got)
	}

	// Without an override it falls back to a cache/temp path (non-empty, contains
	// the beads/codex-hooks segment or the temp fallback).
	codexHookMarkerDirOverride = ""
	base := codexHookMarkerBaseDir()
	if base == "" {
		t.Error("expected a non-empty default marker dir")
	}
}

func TestCodexHookRefreshMarkerPath(t *testing.T) {
	orig := codexHookMarkerDirOverride
	t.Cleanup(func() { codexHookMarkerDirOverride = orig })
	codexHookMarkerDirOverride = "/base"

	a := codexHookRefreshMarkerPath(codexHookInput{SessionID: "s1", CWD: "/ws"})
	b := codexHookRefreshMarkerPath(codexHookInput{SessionID: "s2", CWD: "/ws"})
	c := codexHookRefreshMarkerPath(codexHookInput{SessionID: "s1", CWD: "/other"})

	// Deterministic + under the base dir + .refresh suffix.
	if !strings.HasPrefix(a, "/base/") || !strings.HasSuffix(a, ".refresh") {
		t.Errorf("unexpected marker path: %q", a)
	}
	if a != codexHookRefreshMarkerPath(codexHookInput{SessionID: "s1", CWD: "/ws"}) {
		t.Error("marker path should be deterministic for the same inputs")
	}
	// Different session or workspace → different path.
	if a == b || a == c {
		t.Error("marker path should differ by session and by workspace")
	}

	// Empty session/workspace still produce a stable path (defaults applied).
	if p := codexHookRefreshMarkerPath(codexHookInput{}); !strings.HasSuffix(p, ".refresh") {
		t.Errorf("empty input should still yield a .refresh path, got %q", p)
	}
}

func TestCodexHookMarkNeedsRefresh(t *testing.T) {
	orig := codexHookMarkerDirOverride
	t.Cleanup(func() { codexHookMarkerDirOverride = orig })
	codexHookMarkerDirOverride = filepath.Join(t.TempDir(), "markers")

	in := codexHookInput{SessionID: "sess", CWD: "/ws"}
	if err := codexHookMarkNeedsRefresh(in); err != nil {
		t.Fatalf("markNeedsRefresh: %v", err)
	}
	path := codexHookRefreshMarkerPath(in)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("marker file not written: %v", err)
	}
	if strings.TrimSpace(string(data)) != "1" {
		t.Errorf("marker content = %q, want 1", data)
	}
}

func TestWriteCodexHookSystemMessage(t *testing.T) {
	var buf bytes.Buffer
	if err := writeCodexHookSystemMessage(&buf, "hello world"); err != nil {
		t.Fatalf("write: %v", err)
	}
	var resp codexHookResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, buf.String())
	}
	if !resp.Continue || resp.SystemMessage != "hello world" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestWriteCodexHookAdditionalContext(t *testing.T) {
	var buf bytes.Buffer
	if err := writeCodexHookAdditionalContext(&buf, "SessionStart", "primed context"); err != nil {
		t.Fatalf("write: %v", err)
	}
	var resp codexHookResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("output not valid JSON: %v\n%s", err, buf.String())
	}
	if !resp.Continue {
		t.Error("expected Continue=true")
	}
	if resp.HookSpecificOutput.HookEventName != "SessionStart" ||
		resp.HookSpecificOutput.AdditionalContext != "primed context" {
		t.Errorf("unexpected hookSpecificOutput: %+v", resp.HookSpecificOutput)
	}
}
