package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestUpdateIssueErrorsWhenNoTransitionAvailable is the beads-p9oq regression:
// a status change whose target the jira workflow does NOT permit must surface a
// non-nil error, not silently succeed. Previously applyTransition returned nil
// when no transition matched, so UpdateIssue reported success and the sync
// engine counted the issue as Updated while jira's status never changed —
// silent status-sync loss.
func TestUpdateIssueErrorsWhenNoTransitionAvailable(t *testing.T) {
	const key = "PROJ-1"
	issuePath := "/rest/api/3/issue/" + key
	transitionsPath := issuePath + "/transitions"

	var transitionPosted bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == issuePath:
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == issuePath:
			// Current jira status is "To Do" — differs from the desired
			// "In Progress", so a transition IS needed.
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(issueResponse(key, "To Do"))
		case r.Method == http.MethodGet && r.URL.Path == transitionsPath:
			// The workflow offers NO transition whose To.Name is the desired
			// state — only an unrelated one.
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(TransitionsResult{
				Transitions: []Transition{
					{ID: "31", Name: "Resolve", To: StatusField{Name: "Done"}},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == transitionsPath:
			transitionPosted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	tr := newTrackerWithServer(srv.URL, "3")
	_, err := tr.UpdateIssue(context.Background(), key, &types.Issue{
		Title:  "Test",
		Status: types.StatusInProgress, // no transition path to this state
	})

	if err == nil {
		t.Fatal("expected an error when no workflow transition reaches the desired status, got nil (silent status-sync loss)")
	}
	if transitionPosted {
		t.Error("no transition should have been POSTed when none matched")
	}
	// The error must name the desired status so the operator can act.
	if !strings.Contains(err.Error(), "In Progress") {
		t.Errorf("error should mention the unreachable target status, got: %v", err)
	}
}
