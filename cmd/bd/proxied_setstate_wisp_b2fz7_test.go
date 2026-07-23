//go:build cgo

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// beads-b2fz7 (PROXIED set-state parity for WISP targets).
//
// runSetStateProxiedServer (cmd/bd/state_proxied_server.go) previously guarded
// existence with a bare `issueUC.GetIssue(id)` — the ISSUES table only — so a
// `bd set-state <wisp> <dim> <val>` on an ephemeral WISP target was rejected
// outright ("resolving <wisp>: not found") for hub-connected (proxied,
// store==nil) crew. Both the DIRECT path (store.* auto-routes on isActiveWisp)
// AND this same file's READ leg (proxiedStateLabels → GetWispLabels) already
// treat a wisp as a first-class state target, so the WRITE leg was at a parity
// deficit. The fix resolves issue-or-wisp (proxiedGetIssueOrWisp) and routes the
// label read/remove/add to the wisp variants when the target is a wisp.
//
// Driven END-TO-END through the ACTUAL proxied `bd` subprocess (a UOW-level
// helper would false-green by skipping the CLI resolve/guard plumbing).
// MUTATION-VERIFIED: revert the proxiedGetIssueOrWisp guard back to the bare
// GetIssue and set_state_on_wisp_succeeds goes RED (guard rejects the wisp);
// revert the wisp label routing and the read-back assertion goes RED (label
// written to the issues table, invisible to the wisp-aware read).
func TestProxiedSetStateWisp_b2fz7(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	t.Run("set_state_on_wisp_succeeds", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "ssw")
		wisp := bdProxiedCreate(t, bd, p.dir, "wisp state target", "--ephemeral")

		// set-state must SUCCEED on the wisp (the bare guard rejected it before).
		// CLI signature: `set-state <id> <dimension>=<value>` (cobra ExactArgs(2)).
		out, err := bdProxiedRun(t, bd, p.dir, "set-state", "--json", wisp.ID, "review=approved")
		if err != nil {
			t.Fatalf("REGRESSION (beads-b2fz7): proxied `bd set-state` on WISP %s failed (the direct path + read leg support wisps): %v\n%s", wisp.ID, err, out)
		}
		var res map[string]interface{}
		s := string(out)
		start := strings.Index(s, "{")
		if start < 0 {
			t.Fatalf("no JSON object in set-state output:\n%s", s)
		}
		if err := json.Unmarshal([]byte(s[start:]), &res); err != nil {
			t.Fatalf("parse set-state --json: %v\nraw: %s", err, s[start:])
		}
		if res["changed"] != true {
			t.Errorf("expected changed:true on first set-state, got %v", res["changed"])
		}
		if res["new_value"] != "approved" {
			t.Errorf("expected new_value:approved, got %v", res["new_value"])
		}

		// The state must be READ-BACK from the WISP label table — proves the
		// remove/add label ops routed to the wisp variants, not the issues table.
		rout, rerr := bdProxiedRun(t, bd, p.dir, "state", "--json", wisp.ID, "review")
		if rerr != nil {
			t.Fatalf("proxied `bd state` on wisp failed: %v\n%s", rerr, rout)
		}
		var rres map[string]interface{}
		rs := string(rout)
		rstart := strings.Index(rs, "{")
		if rstart < 0 {
			t.Fatalf("no JSON object in state output:\n%s", rs)
		}
		if err := json.Unmarshal([]byte(rs[rstart:]), &rres); err != nil {
			t.Fatalf("parse state --json: %v\nraw: %s", err, rs[rstart:])
		}
		if rres["value"] != "approved" {
			t.Errorf("REGRESSION (beads-b2fz7): set-state on WISP %s did not persist to the wisp label table (state read-back value=%v, want approved) — label ops wrote to the wrong (issues) table", wisp.ID, rres["value"])
		}
	})
}
