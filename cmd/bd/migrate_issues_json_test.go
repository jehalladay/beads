//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrateIssuesJSONSingleDocument is the beads-nqbv regression teeth.
//
// `bd migrate issues --from ... --to ... --json --yes` used to emit TWO JSON
// documents on stdout for a non-dry-run success: Step 6 emitted {dry_run, plan}
// via displayMigrationPlan and Step 7 emitted {success, message, plan}. Two
// concatenated top-level docs break `bd migrate issues --json | jq`. The fix
// gates the Step-6 emission behind `!jsonOutput || dryRun`, so a JSON non-dry-run
// run emits exactly one document (the Step-7 result, which embeds the plan).
//
// Mutation proof: revert the guard in migrate_issues.go (Step 6 emits
// unconditionally) and the non_dry_run subtest sees 2 documents → RED.
func TestMigrateIssuesJSONSingleDocument(t *testing.T) {
	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "nq")

	// Seed migrate-able issues with a concrete source_repo by importing a
	// sibling repo's issues.jsonl via `bd repo add` + `bd repo sync`
	// (repo-sync stamps source_repo = the added repo path). A plain
	// `bd create` leaves source_repo empty, which migrate cannot select
	// (--from "" is rejected as required), so this is the seeding lever.
	srcRepo := t.TempDir()
	srcBeads := filepath.Join(srcRepo, ".beads")
	if err := os.MkdirAll(srcBeads, 0o755); err != nil {
		t.Fatalf("mkdir src .beads: %v", err)
	}
	jsonl := `{"id":"src-001","title":"src issue one","status":"open","priority":2,"issue_type":"bug","source_repo":"` + srcRepo + `","created_at":"2026-07-19T00:00:00Z","updated_at":"2026-07-19T00:00:00Z"}
`
	if err := os.WriteFile(filepath.Join(srcBeads, "issues.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatalf("write src issues.jsonl: %v", err)
	}

	runBD := func(t *testing.T, args ...string) (string, string) {
		t.Helper()
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		stdout, stderr, err := runCommandBuffers(t, cmd)
		if err != nil {
			t.Fatalf("bd %v failed: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
		}
		return stdout.String(), stderr.String()
	}

	runBD(t, "repo", "add", srcRepo)
	runBD(t, "repo", "sync")

	// countJSONDocs returns how many independent top-level JSON documents the
	// output contains. A --json contract requires exactly one.
	countJSONDocs := func(t *testing.T, out string) int {
		t.Helper()
		dec := json.NewDecoder(strings.NewReader(out))
		n := 0
		for {
			var v json.RawMessage
			if err := dec.Decode(&v); err != nil {
				break
			}
			n++
		}
		return n
	}

	t.Run("non_dry_run_success_emits_single_json_doc", func(t *testing.T) {
		dest := t.TempDir()
		stdout, _ := runBD(t, "migrate", "issues", "--from", srcRepo, "--to", dest, "--json", "--yes")
		if got := countJSONDocs(t, stdout); got != 1 {
			t.Fatalf("expected exactly 1 JSON document on stdout, got %d\nstdout:\n%s", got, stdout)
		}
		// The single doc must be the Step-7 result (success + message).
		var result map[string]interface{}
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatalf("stdout is not a single valid JSON object: %v\nstdout:\n%s", err, stdout)
		}
		if result["success"] != true {
			t.Fatalf("expected success=true in migration result, got: %v", result)
		}
		if _, ok := result["plan"]; !ok {
			t.Fatalf("expected plan embedded in migration result, got: %v", result)
		}
	})

	t.Run("dry_run_still_emits_plan_doc", func(t *testing.T) {
		// Re-seed (the previous subtest migrated src-001 away).
		jsonl2 := `{"id":"src-002","title":"src issue two","status":"open","priority":2,"issue_type":"bug","source_repo":"` + srcRepo + `","created_at":"2026-07-19T00:00:00Z","updated_at":"2026-07-19T00:00:00Z"}
`
		if err := os.WriteFile(filepath.Join(srcBeads, "issues.jsonl"), []byte(jsonl2), 0o644); err != nil {
			t.Fatalf("re-write src issues.jsonl: %v", err)
		}
		runBD(t, "repo", "sync")

		dest := t.TempDir()
		stdout, _ := runBD(t, "migrate", "issues", "--from", srcRepo, "--to", dest, "--json", "--dry-run")
		if got := countJSONDocs(t, stdout); got != 1 {
			t.Fatalf("dry-run expected exactly 1 JSON document, got %d\nstdout:\n%s", got, stdout)
		}
		var doc map[string]interface{}
		if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
			t.Fatalf("dry-run stdout not valid JSON: %v\nstdout:\n%s", err, stdout)
		}
		if _, ok := doc["dry_run"]; !ok {
			t.Fatalf("dry-run doc missing dry_run key: %v", doc)
		}
		if _, ok := doc["plan"]; !ok {
			t.Fatalf("dry-run doc missing plan key: %v", doc)
		}
	})
}
