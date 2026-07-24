package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// TestFederationAddPeerRejectsReservedOrigin guards beads-jkbyt: `bd federation
// add-peer origin <url>` must be REJECTED before any store dispatch, because a
// peer named "origin" routes through AddFederationPeer -> UpsertRemote (or the
// plain AddRemote branch) and CLOBBERS the backing "origin" Dolt remote that
// bd dolt push/pull target — silently misdirecting the primary git-backed
// issue-sync path. beads-j785d then hides such a peer from status/list-peers,
// making the damage un-diagnosable.
//
// This is load-bearing: the guard runs at the command layer BEFORE the branch
// that picks AddFederationPeer vs AddRemote, so BOTH dispatch paths are covered.
// The assertion that NEITHER store method was called proves the origin remote
// is never touched. Reverting the ValidatePeerName reserved-name check (or the
// command-layer call) makes this RED — the fake would record a clobbering call.
func TestFederationAddPeerRejectsReservedOrigin(t *testing.T) {
	// Covers the plain (no-flag) branch AND the auth/sovereignty branch, plus
	// case-insensitivity: every form must be refused with neither store method
	// touched.
	cases := []struct {
		name string
		user string
		sov  string
		peer string
	}{
		{name: "plain lowercase", peer: "origin"},
		{name: "plain uppercase", peer: "ORIGIN"},
		{name: "plain mixed case", peer: "Origin"},
		{name: "with sovereignty", peer: "origin", sov: "T2"},
		{name: "with user", peer: "origin", user: "sync-bot"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			fake := &federationAddPeerFakeStore{}

			prevStore, prevActive, prevJSON, prevProxied := store, storeActive, jsonOutput, proxiedServerMode
			prevUser, prevPassword, prevSov := federationUser, federationPassword, federationSov
			store = fake
			storeActive = true
			jsonOutput = false
			proxiedServerMode = false
			federationUser = tc.user
			// A password is supplied inline so the --user branch never prompts
			// on stdin during the test (the guard rejects before that anyway).
			federationPassword = ""
			if tc.user != "" {
				federationPassword = "secret"
			}
			federationSov = tc.sov
			t.Cleanup(func() {
				store, storeActive, jsonOutput, proxiedServerMode = prevStore, prevActive, prevJSON, prevProxied
				federationUser, federationPassword, federationSov = prevUser, prevPassword, prevSov
			})

			// HandleError writes the human message to stderr and returns a
			// sentinel exitError (.Error() == "exit code 1"), so capture stderr
			// to assert the message names the reserved-name reason.
			origStderr := os.Stderr
			r, w, pipeErr := os.Pipe()
			if pipeErr != nil {
				t.Fatalf("os.Pipe: %v", pipeErr)
			}
			os.Stderr = w

			err := runFederationAddPeer(federationAddPeerCmd, []string{tc.peer, "file:///tmp/evilhub"})

			w.Close()
			os.Stderr = origStderr
			stderrOut, _ := io.ReadAll(r)

			if err == nil {
				t.Fatalf("add-peer %q must be rejected (reserved name), got nil error", tc.peer)
			}
			if !strings.Contains(string(stderrOut), "reserved") {
				t.Errorf("stderr should explain the name is reserved; got: %q", string(stderrOut))
			}

			// The clobber can only happen through a store write. Neither branch
			// must have been reached.
			if len(fake.addRemoteCalls) != 0 {
				t.Errorf("reserved-name add-peer must NOT call AddRemote (would clobber origin); got %v", fake.addRemoteCalls)
			}
			if len(fake.addFederationPeerPeers) != 0 {
				t.Errorf("reserved-name add-peer must NOT call AddFederationPeer (would clobber origin via UpsertRemote); got %d calls",
					len(fake.addFederationPeerPeers))
			}
		})
	}
}
