package linear

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClientForExternalID_TransientErrorDoesNotFallBackToWrongTeam is the
// beads-x498 regression. In a multi-team config, clientForExternalID probed
// each team's client and treated ANY error (including a transient 5xx/network
// error on the CORRECT team) as "not this team", then fell back to
// primaryClient(). That misroutes an update to the wrong team's client (and
// resolves stateId against the wrong workflow). A transient error means the
// owner is UNKNOWN, not "belongs to primary" — so the resolver must return nil
// (callers already turn nil into a clean error) rather than blindly falling
// back. A confirmed not-found in every team (clean empty results) still
// legitimately routes nowhere/primary.
func TestClientForExternalID_TransientErrorDoesNotFallBackToWrongTeam(t *testing.T) {
	// team-1 (primary) errors transiently on the identifier lookup.
	team1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"errors":[{"message":"upstream hiccup"}]}`))
	}))
	defer team1.Close()

	// team-2 returns a clean empty result (issue genuinely not in team-2).
	team2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"issues":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}`))
	}))
	defer team2.Close()

	tr := &Tracker{
		teamIDs: []string{"team-1", "team-2"},
		clients: map[string]*Client{
			"team-1": NewClient("key", "team-1").WithEndpoint(team1.URL),
			"team-2": NewClient("key", "team-2").WithEndpoint(team2.URL),
		},
		config: DefaultMappingConfig(),
	}

	client := tr.clientForExternalID(context.Background(), "ENG-1")
	if client != nil {
		t.Fatalf("clientForExternalID returned a non-nil client after a TRANSIENT error on team-1 — it must NOT fall back to primary when ownership is unknown (would misroute the update to the wrong team); got a client, want nil")
	}
}

// TestClientForExternalID_AllCleanNotFoundFallsBack confirms the fix does NOT
// over-correct: when every team returns a clean not-found (no errors), the
// owner is genuinely undetermined-but-confirmed-absent, and falling back to
// primaryClient() remains acceptable (a global issue id still targets the
// right issue). We assert a non-nil client here so a benign "not in any team"
// case is unchanged.
func TestClientForExternalID_AllCleanNotFoundFallsBack(t *testing.T) {
	empty := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"issues":{"nodes":[],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}`))
		}))
	}
	t1, t2 := empty(), empty()
	defer t1.Close()
	defer t2.Close()

	tr := &Tracker{
		teamIDs: []string{"team-1", "team-2"},
		clients: map[string]*Client{
			"team-1": NewClient("key", "team-1").WithEndpoint(t1.URL),
			"team-2": NewClient("key", "team-2").WithEndpoint(t2.URL),
		},
		config: DefaultMappingConfig(),
	}

	if client := tr.clientForExternalID(context.Background(), "ENG-1"); client == nil {
		t.Fatal("all-clean-not-found must still fall back to primaryClient (non-nil); got nil")
	}
}
