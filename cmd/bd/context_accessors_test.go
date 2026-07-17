package main

import "testing"

// beads-bk6i: hermetic tests for the context.go accessor functions (both the
// legacy-globals branch and the cmdCtx branch) plus backup_export truncateHash.
// All verified 0% + no test refs. Each accessor branches on shouldUseGlobals()
// (testModeUseGlobals || cmdCtx == nil).

func TestContextAccessors_GlobalsBranch(t *testing.T) {
	// Save/restore all touched globals + context state.
	origActor, origVerbose, origQuiet, origJSON, origRO := actor, verboseFlag, quietFlag, jsonOutput, readonlyMode
	origTestMode, origCtx := testModeUseGlobals, cmdCtx
	t.Cleanup(func() {
		actor, verboseFlag, quietFlag, jsonOutput, readonlyMode = origActor, origVerbose, origQuiet, origJSON, origRO
		testModeUseGlobals, cmdCtx = origTestMode, origCtx
	})

	// Force the legacy-globals path.
	testModeUseGlobals = true
	cmdCtx = nil

	if !shouldUseGlobals() {
		t.Fatal("shouldUseGlobals should be true with testModeUseGlobals set")
	}
	actor = "global-actor"
	verboseFlag, quietFlag, jsonOutput, readonlyMode = true, true, true, true

	if getActor() != "global-actor" {
		t.Errorf("getActor = %q, want global-actor", getActor())
	}
	if !isVerbose() || !isQuiet() || !isJSONOutput() || !isReadonlyMode() {
		t.Errorf("globals-branch flags: verbose=%v quiet=%v json=%v ro=%v (want all true)",
			isVerbose(), isQuiet(), isJSONOutput(), isReadonlyMode())
	}
}

func TestContextAccessors_CmdCtxBranch(t *testing.T) {
	origTestMode, origCtx := testModeUseGlobals, cmdCtx
	t.Cleanup(func() { testModeUseGlobals, cmdCtx = origTestMode, origCtx })

	// Force the cmdCtx path: not test-mode, and a non-nil context.
	testModeUseGlobals = false
	cmdCtx = &CommandContext{
		Actor:        "ctx-actor",
		Verbose:      true,
		Quiet:        false,
		JSONOutput:   true,
		ReadonlyMode: false,
	}

	if shouldUseGlobals() {
		t.Fatal("shouldUseGlobals should be false with a non-nil cmdCtx and test-mode off")
	}
	if getActor() != "ctx-actor" {
		t.Errorf("getActor = %q, want ctx-actor", getActor())
	}
	if !isVerbose() {
		t.Error("isVerbose should read cmdCtx.Verbose=true")
	}
	if isQuiet() {
		t.Error("isQuiet should read cmdCtx.Quiet=false")
	}
	if !isJSONOutput() {
		t.Error("isJSONOutput should read cmdCtx.JSONOutput=true")
	}
	if isReadonlyMode() {
		t.Error("isReadonlyMode should read cmdCtx.ReadonlyMode=false")
	}
}

func TestTruncateHash(t *testing.T) {
	cases := map[string]string{
		"":                 "",
		"abc":              "abc",      // <= 8 unchanged
		"12345678":         "12345678", // exactly 8 unchanged
		"123456789":        "12345678", // > 8 truncated to first 8
		"deadbeefcafebabe": "deadbeef",
	}
	for in, want := range cases {
		if got := truncateHash(in); got != want {
			t.Errorf("truncateHash(%q) = %q, want %q", in, got, want)
		}
	}
}
