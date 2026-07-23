//go:build cgo

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCreateUpdateStdinDoubleDrain_sz8ae pins the beads-sz8ae fix: `bd create`
// and `bd update` may take at most ONE flag from stdin ("-"). The description
// side (--stdin / --body-file - / --description-file - / --description=- ...) and
// the design side (--design-file -) both call readBodyFile("-"), which reads
// os.Stdin. getDescriptionFlag runs first and drains the whole stream, so
// getDesignFlag's read returned an empty EOF → design silently "", RC=0, no
// diagnostic (silent-data-loss / input-source class; a DIFFERENT mechanism from
// 7ymzp/dz1t8's two-sources-into-one-field — this is two-fields-one-stream).
//
// The registerCommonIssueFlags mutexes cover stdin<->description-sources and
// design<->design-file, but NOT description-stdin-sources<->design-file, hence
// the gap. The fix is a RUNTIME check (checkStdinConsumerConflict), not a blanket
// cobra mutex, so the legit combos survive: --stdin + --design-file <realpath>
// (only description reads stdin) and --design-file - + inline --description.
//
// Mutation check: remove the `if err := checkStdinConsumerConflict(cmd); err !=
// nil` guard at the top of getDescriptionFlag (flags.go) and the *_rejected
// subtests go RED (the command succeeds rc0 with design silently empty).
func TestCreateUpdateStdinDoubleDrain_sz8ae(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "sz")

	designText := "DESIGN FROM REAL FILE"
	dpath := filepath.Join(dir, "design.txt")
	if err := os.WriteFile(dpath, []byte(designText), 0o644); err != nil {
		t.Fatalf("write design file: %v", err)
	}

	// run executes `bd <args...>` with the given stdin, returning combined output
	// and whether it exited non-zero (a rejection).
	run := func(t *testing.T, stdin string, args ...string) (string, bool) {
		t.Helper()
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		if stdin != "" {
			cmd.Stdin = strings.NewReader(stdin)
		}
		out, err := cmd.CombinedOutput()
		return string(out), err != nil
	}

	// Each description-side stdin source, combined with --design-file -, must be
	// rejected (both would read stdin — the second silently loses).
	descStdinVariants := []struct {
		name string
		args []string
	}{
		{"stdin", []string{"--stdin"}},
		{"body_file_dash", []string{"--body-file", "-"}},
		{"description_file_dash", []string{"--description-file", "-"}},
		{"description_dash", []string{"--description", "-"}},
	}
	for _, v := range descStdinVariants {
		v := v
		t.Run("create_"+v.name+"_plus_design_file_dash_rejected", func(t *testing.T) {
			args := append([]string{"create", "t-" + v.name}, v.args...)
			args = append(args, "--design-file", "-", "--json")
			out, failed := run(t, "DESC\nDESIGN\n", args...)
			if !failed {
				t.Fatalf("bd create %s --design-file - must be rejected (two stdin consumers), got success:\n%s", strings.Join(v.args, " "), out)
			}
			if !strings.Contains(out, "cannot read both description and design from stdin") {
				t.Errorf("expected a 'cannot read both description and design from stdin' error, got:\n%s", out)
			}
		})
	}

	// update path shares registerCommonIssueFlags → same gap, same guard.
	t.Run("update_stdin_plus_design_file_dash_rejected", func(t *testing.T) {
		target := bdCreate(t, bd, dir, "update target", "--type", "task")
		out, failed := run(t, "DESC\nDESIGN\n", "update", target.ID, "--stdin", "--design-file", "-")
		if !failed {
			t.Fatalf("bd update --stdin --design-file - must be rejected (two stdin consumers), got success:\n%s", out)
		}
		if !strings.Contains(out, "cannot read both description and design from stdin") {
			t.Errorf("expected a 'cannot read both description and design from stdin' error, got:\n%s", out)
		}
	})

	// Regression: --stdin description + --design-file <realpath> is LEGIT (only
	// description reads stdin; design comes from the file) and both must persist.
	t.Run("stdin_desc_plus_design_realfile_ok", func(t *testing.T) {
		cmd := exec.Command(bd, "create", "--json", "legit-combo", "--stdin", "--design-file", dpath)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		cmd.Stdin = strings.NewReader("REAL DESC FROM STDIN\n")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bd create --stdin --design-file <realpath> must succeed (only one stdin consumer), got failure:\n%s", out)
		}
		got := parseIssueJSON(t, out)
		if !strings.Contains(got.Description, "REAL DESC FROM STDIN") {
			t.Errorf("description from stdin was dropped: %q\n%s", got.Description, out)
		}
		if !strings.Contains(got.Design, designText) {
			t.Errorf("design from real file was dropped: %q\n%s", got.Design, out)
		}
	})

	// Regression: --design-file - alone (no description stdin source) still reads
	// design from stdin.
	t.Run("design_file_dash_alone_ok", func(t *testing.T) {
		out, failed := run(t, designText+"\n",
			"create", "design-only-stdin", "--design-file", "-", "--json")
		if failed {
			t.Fatalf("bd create --design-file - (alone) must succeed, got failure:\n%s", out)
		}
	})

	// Regression: --stdin alone (no design stdin source) still reads description.
	t.Run("stdin_alone_ok", func(t *testing.T) {
		out, failed := run(t, "DESC ALONE\n",
			"create", "desc-only-stdin", "--stdin", "--json")
		if failed {
			t.Fatalf("bd create --stdin (alone) must succeed, got failure:\n%s", out)
		}
	})
}
