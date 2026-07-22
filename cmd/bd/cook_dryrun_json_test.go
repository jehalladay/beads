//go:build cgo

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCookDryRunJSON is the beads-cook-dryrun-json teeth (8lqh --json-contract
// family, the formula-compile sibling of the beads-51w8c mol pour/bond/squash/
// distill dry-run fix).
//
// `bd cook <formula> --dry-run` printed a plaintext preview via outputCookDryRun
// and returned nil BEFORE reaching the verb's `if jsonOutput { outputJSON(...) }`
// success block, so `cook --dry-run --json` silently emitted human text with
// rc=0. `bd cook f.toml --dry-run --json | jq` therefore got a parse error, not
// the intended machine result — and --dry-run is exactly the SAFE preview path
// scripts/agents use before a real `cook --persist`.
//
// The fix gates the dry-run branch: under --json emit a parseable preview
// envelope (dry_run:true + the same fields as the plaintext preview); otherwise
// keep the plaintext unchanged.
//
// These teeth drive the real embedded `bd cook` subprocess and assert:
// (a) `--dry-run --json` stdout is a single parseable JSON object carrying
// "dry_run":true and the formula/steps; (b) a positive control that WITHOUT
// --json the plaintext preview still prints (proving the dry-run branch is
// genuinely reached, so (a) can't false-green on a never-executed branch);
// (c) runtime mode (`--mode runtime` + --var) also emits JSON, not plaintext.
//
// Mutation-verify: drop the `if jsonOutput { return outputCookDryRunJSON(...) }`
// guard (restore the bare outputCookDryRun + return nil) and the
// _dryrun_json_parses subtest goes RED (stdout is plaintext -> json.Unmarshal
// fails).
func TestCookDryRunJSON(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// A minimal compile-time formula with one declared variable so the preview
	// has steps + variables to serialize.
	writeFormula := func(t *testing.T, dir, name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name+".formula.toml")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write formula %s: %v", name, err)
		}
		return path
	}

	compileBody := `formula = "cdj-compile"
description = "beads-cook-dryrun-json compile-time teeth"
version = 1
type = "workflow"

[vars.component]
description = "Component name"
required = true

[[steps]]
id = "build"
title = "Build {{component}}"
description = "one step so the proto is valid"
`

	t.Run("compile_dryrun_json_parses", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "cdj")
		path := writeFormula(t, dir, "cdj-compile", compileBody)

		out := bdRunOK(t, bd, dir, "cook", path, "--dry-run", "--json")
		trimmed := strings.TrimSpace(out)
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
			t.Fatalf("beads-cook-dryrun-json: `cook --dry-run --json` stdout must be a single "+
				"parseable JSON object (the dry-run branch leaked plaintext and broke `| jq`); "+
				"parse error %v; got:\n%s", err, out)
		}
		if dr, _ := obj["dry_run"].(bool); !dr {
			t.Errorf("beads-cook-dryrun-json: dry-run json envelope must carry \"dry_run\":true; got:\n%s", out)
		}
		if f, _ := obj["formula"].(string); f != "cdj-compile" {
			t.Errorf("beads-cook-dryrun-json: envelope formula = %q, want \"cdj-compile\"; got:\n%s", f, out)
		}
		steps, ok := obj["steps"].([]interface{})
		if !ok || len(steps) != 1 {
			t.Errorf("beads-cook-dryrun-json: envelope must carry the 1 preview step; got steps=%v\n%s", obj["steps"], out)
		}
	})

	t.Run("compile_dryrun_plain_keeps_preview", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "cdp")
		path := writeFormula(t, dir, "cdp-compile", strings.ReplaceAll(compileBody, "cdj-compile", "cdp-compile"))

		out := bdRunOK(t, bd, dir, "cook", path, "--dry-run")
		if !strings.Contains(out, "Dry run: would cook formula") {
			t.Errorf("beads-cook-dryrun-json: plain `cook --dry-run` must still print the plaintext "+
				"preview (fix gates only under --json); got:\n%s", out)
		}
	})

	t.Run("runtime_dryrun_json_parses", func(t *testing.T) {
		t.Parallel()
		dir, _, _ := bdInit(t, bd, "--prefix", "cdr")
		path := writeFormula(t, dir, "cdr-compile", strings.ReplaceAll(compileBody, "cdj-compile", "cdr-compile"))

		out := bdRunOK(t, bd, dir, "cook", path, "--mode", "runtime", "--var", "component=api", "--dry-run", "--json")
		trimmed := strings.TrimSpace(out)
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
			t.Fatalf("beads-cook-dryrun-json: runtime-mode `cook --dry-run --json` stdout must parse "+
				"as JSON; parse error %v; got:\n%s", err, out)
		}
		if m, _ := obj["mode"].(string); m != "runtime" {
			t.Errorf("beads-cook-dryrun-json: runtime envelope mode = %q, want \"runtime\"; got:\n%s", m, out)
		}
		// Substituted step title must be reflected (runtime mode substitutes vars).
		steps, _ := obj["steps"].([]interface{})
		if len(steps) != 1 {
			t.Fatalf("beads-cook-dryrun-json: runtime envelope must carry the 1 step; got:\n%s", out)
		}
		s0, _ := steps[0].(map[string]interface{})
		if title, _ := s0["title"].(string); title != "Build api" {
			t.Errorf("beads-cook-dryrun-json: runtime step title = %q, want \"Build api\" (var substituted); got:\n%s", title, out)
		}
	})
}
