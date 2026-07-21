//go:build cgo

package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

// TestPopulateSyncResultErrorMsgs_00oy4 is the teeth for beads-00oy4, the
// non-fatal-push twin of beads-o35h0.
//
// `bd federation sync` sets result.PushError when a peer merge succeeds but the
// follow-up push is rejected (dolt/federation.go:402 returns `(result, nil)`
// with PushError set — non-fatal by design). The human path prints
// "○ Push skipped: <err>", but SyncResult.PushError is `json:"-"` (an error
// marshals to {}), so before this fix `--json` emitted {merged:true,
// pushed:false} with NO push-error signal — the same error-less-JSON asymmetry
// o35h0 fixed for the FATAL Error field, still live on the non-fatal push path.
// Federation is the multi-town data path, so a structured consumer could not
// detect that a peer push silently failed (divergence accumulates).
//
// The sync loop routes both mappings through populateSyncResultErrorMsgs, which
// mirrors PushError → PushErrorMsg (`json:"push_error"`) WITHOUT touching the
// fatal `error` field or the RC=1 trigger. This unit-tests that helper (the
// live cmd-layer logic), so reverting the PushError→PushErrorMsg mapping makes
// this test RED. A full merge-succeeds/push-fails subprocess cannot be induced
// deterministically (federation sync's push is step 5, only after a successful
// merge whose local history already contains the peer tip → the push is
// normally a fast-forward; and there is no CLI verb to bootstrap a peer's
// remote branch to arrange otherwise), which is why o35h0 only exercised the
// easy-to-induce fatal fetch path and this pins the mapping at the seam.
func TestPopulateSyncResultErrorMsgs_00oy4(t *testing.T) {
	t.Parallel()

	pushErr := errors.New("failed to push to peer towna: non-fast-forward")

	// ── Non-fatal push failure: merge succeeded, push rejected, no fatal err. ──
	t.Run("push_error_surfaces_in_json_without_fatal", func(t *testing.T) {
		result := &storage.SyncResult{
			Peer:      "towna",
			Fetched:   true,
			Merged:    true,
			Pushed:    false,
			PushError: pushErr,
		}
		// Mirrors the cmd-layer call on the success path: no fatal err.
		populateSyncResultErrorMsgs(result, nil)

		if result.PushErrorMsg == "" {
			t.Fatalf("00oy4: a merged-but-push-failed result must populate PushErrorMsg from PushError")
		}
		if result.PushErrorMsg != pushErr.Error() {
			t.Errorf("PushErrorMsg = %q, want mirror of PushError %q", result.PushErrorMsg, pushErr.Error())
		}
		// Non-fatal: must NOT set the fatal ErrorMsg (that is o35h0's fatal
		// signal + the RC=1 trigger). A push failure keeps RC=0.
		if result.ErrorMsg != "" {
			t.Errorf("00oy4: a non-fatal push failure must not set the fatal ErrorMsg; got %q", result.ErrorMsg)
		}

		// End-to-end JSON contract: push_error is visible + snake_case, merged
		// preserved, no fatal error key.
		raw, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("marshal SyncResult: %v", err)
		}
		var payload struct {
			Merged    bool   `json:"merged"`
			PushError string `json:"push_error"`
			Error     string `json:"error"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("SyncResult must marshal to valid JSON: %v\n%s", err, raw)
		}
		if payload.PushError != pushErr.Error() {
			t.Errorf("00oy4: --json must carry push_error=%q; got %q\n%s", pushErr.Error(), payload.PushError, raw)
		}
		if payload.Error != "" {
			t.Errorf("00oy4: --json must not carry a fatal error for a non-fatal push failure; got %q", payload.Error)
		}
		if !payload.Merged {
			t.Errorf("merge succeeded, so merged:true must survive; got:\n%s", raw)
		}
	})

	// ── Fatal sync failure (o35h0 regression): err → ErrorMsg, not PushError. ──
	t.Run("fatal_error_surfaces_as_error_not_push_error", func(t *testing.T) {
		fatal := errors.New("fetch failed: no such peer")
		result := &storage.SyncResult{Peer: "towna", Merged: false}
		populateSyncResultErrorMsgs(result, fatal)

		if result.ErrorMsg != fatal.Error() {
			t.Errorf("o35h0: fatal err must populate ErrorMsg; got %q", result.ErrorMsg)
		}
		if result.PushErrorMsg != "" {
			t.Errorf("a fatal (non-push) error must not populate PushErrorMsg; got %q", result.PushErrorMsg)
		}
	})

	// ── Clean sync: neither field set, and omitempty keeps push_error absent. ──
	t.Run("clean_sync_omits_both", func(t *testing.T) {
		result := &storage.SyncResult{Peer: "towna", Merged: true, Pushed: true}
		populateSyncResultErrorMsgs(result, nil)

		if result.ErrorMsg != "" || result.PushErrorMsg != "" {
			t.Errorf("a clean sync must leave both *Msg empty; got error=%q push_error=%q", result.ErrorMsg, result.PushErrorMsg)
		}
		raw, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("marshal clean SyncResult: %v", err)
		}
		if strings.Contains(string(raw), "push_error") || strings.Contains(string(raw), `"error"`) {
			t.Errorf("a clean sync must omit both error keys (omitempty); got:\n%s", raw)
		}
	})

	// ── nil result must not panic (defensive; the loop guards the same). ──
	t.Run("nil_result_no_panic", func(t *testing.T) {
		populateSyncResultErrorMsgs(nil, pushErr)
	})
}
