//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
)

// TestMergeSlotCheckJSONStableKeySet is the teeth for beads-8qf2q: `bd
// merge-slot check --json` emitted a DIFFERENT key-set by outcome — not-found →
// {id,available,error}, found → {id,available,holder,waiters} — so a consumer
// keying on .holder/.waiters got missing keys on not-found (and vice-versa on
// .error). The fix emits a STABLE key-set {id,available,holder,waiters,error}
// on BOTH legs (nulls for the inapplicable fields). This asserts the two legs
// carry the identical key-set.
func TestMergeSlotCheckJSONStableKeySet(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "ms")

	runCheck := func(t *testing.T) map[string]interface{} {
		t.Helper()
		cmd := exec.Command(bd, "merge-slot", "check", "--json")
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		// check is rc0 on both not-found and found (not-found available:false is a
		// valid result, not an error).
		if err != nil {
			t.Fatalf("merge-slot check --json failed: %v\n%s", err, out)
		}
		s := strings.TrimSpace(string(out))
		start := strings.Index(s, "{")
		if start < 0 {
			t.Fatalf("merge-slot check --json emitted no JSON object:\n%s", s)
		}
		var obj map[string]interface{}
		if e := json.Unmarshal([]byte(s[start:]), &obj); e != nil {
			t.Fatalf("merge-slot check --json output is not valid JSON: %v\n%s", e, s)
		}
		return obj
	}

	keysOf := func(m map[string]interface{}) []string {
		ks := make([]string, 0, len(m))
		for k := range m {
			// schema_version is injected by the envelope wrapper on every --json
			// payload; it is not part of the per-command key-set under test.
			if k == "schema_version" {
				continue
			}
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return ks
	}

	// Leg 1: no slot created yet → not-found outcome.
	notFound := runCheck(t)
	if av, ok := notFound["available"].(bool); !ok || av {
		t.Errorf("expected available:false on the not-found leg, got %v", notFound["available"])
	}

	// Leg 2: create the slot → found outcome.
	create := exec.Command(bd, "merge-slot", "create")
	create.Dir = dir
	create.Env = bdEnv(dir)
	if out, err := create.CombinedOutput(); err != nil {
		t.Fatalf("merge-slot create failed: %v\n%s", err, out)
	}
	found := runCheck(t)

	// The two legs must carry the IDENTICAL key-set (beads-8qf2q).
	nfKeys := keysOf(notFound)
	fKeys := keysOf(found)
	if strings.Join(nfKeys, ",") != strings.Join(fKeys, ",") {
		t.Errorf("merge-slot check --json key-set flips by outcome (beads-8qf2q):\n  not-found keys: %v\n  found keys:     %v", nfKeys, fKeys)
	}
	// And that stable key-set must include all of the union fields.
	for _, want := range []string{"id", "available", "holder", "waiters", "error"} {
		if _, ok := notFound[want]; !ok {
			t.Errorf("not-found leg missing stable key %q; keys=%v", want, nfKeys)
		}
		if _, ok := found[want]; !ok {
			t.Errorf("found leg missing stable key %q; keys=%v", want, fKeys)
		}
	}
}
