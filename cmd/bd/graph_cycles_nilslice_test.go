package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// TestGraphCheckCyclesEmptyIsNonNullArray_8wyu is the teeth for beads-8wyu.
//
// graphCheckResult.Cycles is a [][]string json field ("cycles") with NO
// omitempty. renderGraphCheck built the payload as graphCheckResult{Clean:true}
// and only appended to Cycles in the cycle loop, so on a CLEAN graph (no cycles
// — the common pass case) Cycles stayed nil and marshaled to `cycles:null`
// instead of `cycles:[]` — the guib/036h/5fv3/jxel/4mkg nil-slice asymmetry.
// renderGraphCheck is the shared root for both the direct and proxied
// (graph_proxied_server.go) paths, so one init at the literal fixes both. The
// fix inits Cycles:[][]string{}.
//
// renderGraphCheck writes to os.Stdout via outputJSON, so the teeth capture
// stdout for the clean-graph case (cycles == nil input) and assert the emitted
// json carries cycles:[] not null. RED proof: dropping the Cycles init makes
// the clean case emit "cycles":null.
func TestGraphCheckCyclesEmptyIsNonNullArray_8wyu(t *testing.T) {
	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	out := captureGraphStdout(t, func() {
		// A clean graph has no cycles → the loop never appends to Cycles.
		if err := renderGraphCheck(nil); err != nil {
			t.Fatalf("renderGraphCheck(nil) returned error: %v", err)
		}
	})

	if out == "" {
		t.Fatal("renderGraphCheck emitted no json on stdout")
	}
	if strings.Contains(out, `"cycles":null`) || strings.Contains(out, `"cycles": null`) {
		t.Errorf("clean graph emitted cycles:null — must be [] for a stable machine contract (beads-8wyu)\njson:\n%s", out)
	}
	if !strings.Contains(out, `"cycles": []`) && !strings.Contains(out, `"cycles":[]`) {
		t.Errorf("expected cycles:[] on a clean graph, got:\n%s", out)
	}
	// The clean flag must still be true.
	if !strings.Contains(out, `"clean": true`) && !strings.Contains(out, `"clean":true`) {
		t.Errorf("expected clean:true on a clean graph, got:\n%s", out)
	}
}

// captureGraphStdout runs fn with os.Stdout redirected to a pipe and returns
// what it wrote. Mirrors captureADOStdout.
func captureGraphStdout(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	fn()
	_ = w.Close()
	os.Stdout = old
	<-done
	_ = r.Close()

	return buf.String()
}
