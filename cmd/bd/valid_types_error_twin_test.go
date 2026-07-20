//go:build cgo

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestValidTypesErrorMessage_t3crf is the twin-invariant teeth for beads-t3crf.
//
// beads-71j1 replaced the stale hardcoded 6-type valid-types string
// ("bug, feature, task, epic, chore, decision") with the canonical
// types.ValidWorkTypesString() (the full 9-type list, which adds
// spike/story/milestone) on count.go/ready.go/search.go/lint.go — but TWO
// runtime sites were missed and still carried the stale literal:
//   - cmd/bd/list_filter.go (buildListFilter)      -> bd list --type <typo>
//   - cmd/bd/migrate_issues.go (validateMigrateIssuesFilters) -> bd migrate --type <typo>
//
// So their "invalid issue type" error LIED, omitting spike/story/milestone.
// These are the twins; this test proves both error strings now enumerate the
// canonical full list. End-to-end through the real CLI error path (subprocess),
// so a regression at either print site is caught — not a re-call of
// ValidWorkTypesString().
func TestValidTypesErrorMessage_t3crf(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}

	bd := buildEmbeddedBD(t)
	dir, _, _ := bdInit(t, bd, "--prefix", "vt")

	// The canonical list must contain the types the stale 6-literal omitted;
	// guards the assertion itself against a future list change.
	canonical := types.ValidWorkTypesString()
	for _, missing := range []string{"spike", "story", "milestone"} {
		if !strings.Contains(canonical, missing) {
			t.Fatalf("precondition: ValidWorkTypesString()=%q must contain %q", canonical, missing)
		}
	}

	run := func(t *testing.T, args ...string) string {
		t.Helper()
		cmd := exec.Command(bd, args...)
		cmd.Dir = dir
		cmd.Env = bdEnv(dir)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected `bd %s` to fail on a bogus --type, but it succeeded:\n%s",
				strings.Join(args, " "), out)
		}
		return string(out)
	}

	cases := []struct {
		name string
		args []string
	}{
		{"list", []string{"list", "--type", "bogusxyz"}},
		// `bd migrate issues` needs --from/--to; the --type validation
		// (validateMigrateIssuesFilters) fires after the repo warning, before any
		// migration, regardless of whether the repos hold issues.
		{"migrate", []string{"migrate", "issues", "--from", ".", "--to", "/tmp/vt-t3crf-dst", "--type", "bogusxyz"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := run(t, tc.args...)
			if !strings.Contains(out, "invalid issue type") {
				t.Fatalf("bd %s: expected an 'invalid issue type' error, got:\n%s",
					strings.Join(tc.args, " "), out)
			}
			// The lie: the stale literal omitted spike/story/milestone. Assert
			// the full canonical list (spike is the sentinel) is now enumerated.
			for _, want := range []string{"spike", "story", "milestone"} {
				if !strings.Contains(out, want) {
					t.Errorf("bd %s error message omits %q (stale 6-type literal not replaced by ValidWorkTypesString): \n%s",
						strings.Join(tc.args, " "), want, out)
				}
			}
		})
	}
}
