//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestLinearTeamsJSONErrorContract is the teeth for beads-ru7wr: `bd linear
// teams --json` on the auth-not-configured path used a bare HandleError, so it
// left stdout EMPTY and leaked the error as plaintext on stderr — while the
// sibling commands in the same group (`linear sync --json`, `linear status
// --json`) honor the --json error contract for the SAME condition (an 8lqh
// intra-group asymmetry). The fix routes both runLinearTeams error returns
// through HandleErrorRespectJSON, so a --json consumer gets a parseable
// {error,schema_version} object on stdout with a non-zero exit.
func TestLinearTeamsJSONErrorContract(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "lt")

	// Strip any LINEAR_* / OAuth auth from the environment so buildLinearClient
	// hits the "authentication not configured" path deterministically (HOME is
	// the fresh temp dir, so no config file supplies linear.api_key either).
	env := make([]string, 0, len(bdEnv(dir)))
	for _, e := range bdEnv(dir) {
		if strings.HasPrefix(e, "LINEAR_") {
			continue
		}
		env = append(env, e)
	}

	cmd := exec.Command(bd, "linear", "teams", "--json")
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	// Must fail (auth not configured) with a non-zero exit.
	if err == nil {
		t.Fatalf("expected linear teams --json to fail without auth, got rc=0\nstdout:%s\nstderr:%s", stdout.String(), stderr.String())
	}

	// The error must be a JSON object on STDOUT (jsonStdoutError), not plaintext
	// on stderr — the --json error contract the sibling commands honor.
	s := strings.TrimSpace(stdout.String())
	start := strings.Index(s, "{")
	if start < 0 {
		t.Fatalf("linear teams --json emitted no JSON object on stdout (beads-ru7wr: plaintext-stderr leak)\nstdout:%q\nstderr:%q", stdout.String(), stderr.String())
	}
	var obj map[string]interface{}
	if e := json.Unmarshal([]byte(s[start:]), &obj); e != nil {
		t.Fatalf("linear teams --json stdout is not valid JSON: %v\n%s", e, s)
	}
	ev, ok := obj["error"]
	if !ok {
		t.Errorf("expected an \"error\" key in the JSON error object, got: %v", obj)
	}
	if es, _ := ev.(string); !strings.Contains(es, "not configured") {
		t.Errorf("expected the error to mention 'not configured', got: %v", ev)
	}
}
