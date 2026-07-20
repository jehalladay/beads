//go:build cgo

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

// bdFederation runs "bd federation" with extra args. Returns combined output.
func bdFederation(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"federation"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	stdout, stderr, err := runCommandBuffers(t, cmd)
	if err != nil {
		t.Fatalf("bd federation %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// bdFederationFail runs "bd federation" expecting failure.
func bdFederationFail(t *testing.T, bd, dir string, args ...string) string {
	t.Helper()
	fullArgs := append([]string{"federation"}, args...)
	cmd := exec.Command(bd, fullArgs...)
	cmd.Dir = dir
	cmd.Env = bdEnv(dir)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("bd federation %s should have failed, got: %s", strings.Join(args, " "), out)
	}
	return string(out)
}

func TestFormatFederationPeerListJSONPreservesLegacyKeys(t *testing.T) {
	formatted := formatFederationPeerListJSON([]storage.RemoteInfo{{
		Name: "town-beta",
		URL:  "file:///tmp/town-beta",
	}})

	raw, err := json.Marshal(formatted)
	if err != nil {
		t.Fatalf("marshal federation peer JSON: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, `"Name":"town-beta"`) || !strings.Contains(body, `"URL":"file:///tmp/town-beta"`) {
		t.Fatalf("formatted JSON should preserve legacy Name/URL keys, got %s", body)
	}
	if strings.Contains(body, `"name"`) || strings.Contains(body, `"url"`) {
		t.Fatalf("formatted JSON should not expose lowercase RemoteInfo storage tags, got %s", body)
	}
}

func TestEmbeddedFederation(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt federation tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	t.Run("list_peers_empty", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "fdlst0")

		out := bdFederation(t, bd, dir, "list-peers")
		if !strings.Contains(out, "No federation peers") {
			t.Errorf("expected 'No federation peers', got: %s", out)
		}
	})

	t.Run("list_peers_empty_json", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "fdlstj")

		out := bdFederation(t, bd, dir, "list-peers", "--json")
		// Should be valid JSON (empty array or null)
		out = strings.TrimSpace(out)
		if out != "null" && out != "[]" {
			// Try parsing as JSON array
			var result []interface{}
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatalf("expected valid JSON array, got: %s", out)
			}
		}
	})

	t.Run("add_peer_simple", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "fdadd")

		out := bdFederation(t, bd, dir, "add-peer", "test-peer", "file:///tmp/fake-peer")
		if !strings.Contains(out, "test-peer") {
			t.Errorf("expected peer name in output, got: %s", out)
		}

		// Verify it appears in list
		listOut := bdFederation(t, bd, dir, "list-peers")
		if !strings.Contains(listOut, "test-peer") {
			t.Errorf("expected 'test-peer' in list, got: %s", listOut)
		}
	})

	t.Run("add_peer_json", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "fdaddj")

		out := bdFederation(t, bd, dir, "add-peer", "json-peer", "file:///tmp/json-peer", "--json")
		var result map[string]interface{}
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatalf("failed to parse JSON: %v\n%s", err, out)
		}
		if added, _ := result["added"].(string); added != "json-peer" {
			t.Errorf("expected added='json-peer', got %q", added)
		}
	})

	t.Run("add_peer_with_credentials", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "fdcred")

		out := bdFederation(t, bd, dir, "add-peer", "auth-peer", "file:///tmp/auth-peer",
			"--user", "admin", "--password", "secret", "--json")
		var result map[string]interface{}
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatalf("failed to parse JSON: %v\n%s", err, out)
		}
		if hasAuth, _ := result["has_auth"].(bool); !hasAuth {
			t.Error("expected has_auth=true")
		}
	})

	t.Run("add_peer_with_sovereignty", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "fdsov")

		out := bdFederation(t, bd, dir, "add-peer", "sov-peer", "file:///tmp/sov-peer",
			"--sovereignty", "T2", "--json")
		var result map[string]interface{}
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatalf("failed to parse JSON: %v\n%s", err, out)
		}
		if sov, _ := result["sovereignty"].(string); sov != "T2" {
			t.Errorf("expected sovereignty='T2', got %q", sov)
		}
	})

	t.Run("remove_peer", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "fdrm")

		bdFederation(t, bd, dir, "add-peer", "removable", "file:///tmp/removable")

		// Verify it exists
		listOut := bdFederation(t, bd, dir, "list-peers")
		if !strings.Contains(listOut, "removable") {
			t.Fatalf("peer should exist before removal, got: %s", listOut)
		}

		// Remove it
		out := bdFederation(t, bd, dir, "remove-peer", "removable")
		if !strings.Contains(out, "removable") {
			t.Errorf("expected peer name in removal output, got: %s", out)
		}

		// Verify it's gone
		listOut = bdFederation(t, bd, dir, "list-peers")
		if strings.Contains(listOut, "removable") {
			t.Errorf("peer should be gone after removal, got: %s", listOut)
		}
	})

	t.Run("remove_peer_json", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "fdrmj")

		bdFederation(t, bd, dir, "add-peer", "rm-json", "file:///tmp/rm-json")

		out := bdFederation(t, bd, dir, "remove-peer", "rm-json", "--json")
		var result map[string]interface{}
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatalf("failed to parse JSON: %v\n%s", err, out)
		}
		if removed, _ := result["removed"].(string); removed != "rm-json" {
			t.Errorf("expected removed='rm-json', got %q", removed)
		}
	})

	t.Run("status_no_peers", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "fdst0")

		out := bdFederation(t, bd, dir, "status")
		if !strings.Contains(out, "No federation peers") {
			t.Errorf("expected 'No federation peers', got: %s", out)
		}
	})

	t.Run("status_json_no_peers", func(t *testing.T) {
		dir, _, _ := bdInit(t, bd, "--prefix", "fdstj")

		out := bdFederation(t, bd, dir, "status", "--json")
		var result map[string]interface{}
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatalf("failed to parse JSON: %v\n%s", err, out)
		}
		if _, ok := result["peers"]; !ok {
			t.Error("missing 'peers' in status JSON")
		}
		// beads-7mm8: the pending-changes key must be snake_case per the JSON
		// contract, not the old camelCase "pendingChanges".
		if _, ok := result["pending_changes"]; !ok {
			t.Errorf("expected snake_case 'pending_changes' key, got: %s", out)
		}
		if _, ok := result["pendingChanges"]; ok {
			t.Errorf("camelCase 'pendingChanges' key must be gone (snake_case contract), got: %s", out)
		}
	})
}

// TestFederationStatusPeerJSONSnakeCase pins the beads-7mm8 fix: the per-peer
// status object marshals snake_case keys, not the PascalCase Go field names.
// peerStatus is local to runFederationStatus, so assert the contract via the
// same field/tag shape here (a regression in the source struct tags would be
// caught by the embedded status test with a live peer; this documents the
// intended keys without requiring a reachable peer).
func TestFederationStatusPeerJSONSnakeCase(t *testing.T) {
	type peerStatus struct {
		Status      *storage.SyncStatus `json:"status"`
		URL         string              `json:"url"`
		Reachable   bool                `json:"reachable"`
		ReachError  string              `json:"reach_error"`
		StatusError string              `json:"status_error"`
	}
	raw, err := json.Marshal(peerStatus{URL: "file:///tmp/p", Reachable: true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(raw)
	for _, want := range []string{`"status"`, `"url"`, `"reachable"`, `"reach_error"`, `"status_error"`} {
		if !strings.Contains(body, want) {
			t.Errorf("expected snake_case key %s, got %s", want, body)
		}
	}
	for _, bad := range []string{`"URL"`, `"Reachable"`, `"ReachError"`, `"StatusError"`} {
		if strings.Contains(body, bad) {
			t.Errorf("PascalCase key %s must not appear (snake_case contract), got %s", bad, body)
		}
	}
}

// TestFederationStatusNestedSyncStatusJSONSnakeCase is the beads-ugb99 teeth.
//
// The `bd federation status --json` payload embeds the per-peer sync state as
// the nested "status" object (peerStatus.Status *storage.SyncStatus). At
// runtime that field is non-nil (the render loop always sets it), but
// storage.SyncStatus had NO json tags, so json.Marshal emitted its fields as
// PascalCase (Peer/LastSync/LocalAhead/LocalBehind/HasConflicts) — a snake_case
// contract violation nested inside the otherwise-snake_case payload that
// beads-7mm8 fixed on the outer wrapper. 7mm8's test used Status:nil so the
// nested object was never marshaled and the leak was missed.
func TestFederationStatusNestedSyncStatusJSONSnakeCase(t *testing.T) {
	type peerStatus struct {
		Status      *storage.SyncStatus `json:"status"`
		URL         string              `json:"url"`
		Reachable   bool                `json:"reachable"`
		ReachError  string              `json:"reach_error"`
		StatusError string              `json:"status_error"`
	}
	raw, err := json.Marshal(peerStatus{
		URL:       "file:///tmp/p",
		Reachable: true,
		Status:    &storage.SyncStatus{Peer: "p", LocalAhead: 1, LocalBehind: 2, HasConflicts: true},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(raw)
	for _, want := range []string{`"peer"`, `"last_sync"`, `"local_ahead"`, `"local_behind"`, `"has_conflicts"`} {
		if !strings.Contains(body, want) {
			t.Errorf("expected snake_case nested status key %s, got %s", want, body)
		}
	}
	for _, bad := range []string{`"Peer"`, `"LastSync"`, `"LocalAhead"`, `"LocalBehind"`, `"HasConflicts"`} {
		if strings.Contains(body, bad) {
			t.Errorf("PascalCase nested status key %s must not appear (snake_case contract), got %s", bad, body)
		}
	}
}

func TestEmbeddedFederationConcurrent(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt federation tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "fdconc")

	const numWorkers = 10

	type result struct {
		worker int
		out    string
		err    error
	}

	results := make([]result, numWorkers)
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for w := 0; w < numWorkers; w++ {
		go func(worker int) {
			defer wg.Done()
			peerName := fmt.Sprintf("conc-peer-%d", worker)
			peerURL := fmt.Sprintf("file:///tmp/conc-peer-%d", worker)
			cmd := exec.Command(bd, "federation", "add-peer", peerName, peerURL)
			cmd.Dir = dir
			cmd.Env = bdEnv(dir)
			out, err := cmd.CombinedOutput()
			results[worker] = result{worker: worker, out: string(out), err: err}
		}(w)
	}
	wg.Wait()

	successes := 0
	for _, r := range results {
		if strings.Contains(r.out, "panic") {
			t.Errorf("worker %d panicked:\n%s", r.worker, r.out)
		}
		if r.err == nil {
			successes++
		} else if !strings.Contains(r.out, "one writer at a time") {
			t.Errorf("worker %d failed with unexpected error: %v\n%s", r.worker, r.err, r.out)
		}
	}
	if successes < 1 {
		t.Errorf("expected at least 1 successful add-peer, got %d", successes)
	}
	t.Logf("%d/%d federation workers succeeded", successes, numWorkers)
}
