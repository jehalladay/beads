package main

import (
	"reflect"
	"testing"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/storage/domain"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/internal/validation"
)

func TestParseDepSpecs(t *testing.T) {
	tests := []struct {
		name    string
		in      []string
		want    []domain.DependencySpec
		wantErr bool
	}{
		{
			name: "empty input",
			in:   nil,
			want: nil,
		},
		{
			name: "whitespace and empties skipped",
			in:   []string{"", "  "},
			want: nil,
		},
		{
			name: "bare id becomes blocks edge",
			in:   []string{"bd-1"},
			want: []domain.DependencySpec{
				{Type: types.DepBlocks, TargetID: "bd-1"},
			},
		},
		{
			name: "depends-on alias maps to blocks",
			in:   []string{"depends-on:bd-1"},
			want: []domain.DependencySpec{
				{Type: types.DepBlocks, TargetID: "bd-1"},
			},
		},
		{
			name: "blocked-by alias maps to blocks",
			in:   []string{"blocked-by:bd-2"},
			want: []domain.DependencySpec{
				{Type: types.DepBlocks, TargetID: "bd-2"},
			},
		},
		{
			name: "explicit blocks swaps direction",
			in:   []string{"blocks:bd-3"},
			want: []domain.DependencySpec{
				{Type: types.DepBlocks, TargetID: "bd-3", SwapDirection: true},
			},
		},
		{
			name: "discovered-from preserved as typed edge",
			in:   []string{"discovered-from:bd-4"},
			want: []domain.DependencySpec{
				{Type: types.DepDiscoveredFrom, TargetID: "bd-4"},
			},
		},
		{
			name: "parent-child typed edge",
			in:   []string{"parent-child:bd-5"},
			want: []domain.DependencySpec{
				{Type: types.DepParentChild, TargetID: "bd-5"},
			},
		},
		{
			name: "multiple entries with whitespace trimmed",
			in:   []string{"  bd-1  ", "blocks: bd-2 ", "discovered-from:bd-3"},
			want: []domain.DependencySpec{
				{Type: types.DepBlocks, TargetID: "bd-1"},
				{Type: types.DepBlocks, TargetID: "bd-2", SwapDirection: true},
				{Type: types.DepDiscoveredFrom, TargetID: "bd-3"},
			},
		},
		{
			name:    "unknown type rejected",
			in:      []string{"nonsense:bd-1"},
			wantErr: true,
		},
		{
			name:    "empty type rejected",
			in:      []string{":bd-1"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDepSpecs(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseDepSpecs(%v) = %v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDepSpecs(%v) error: %v", tt.in, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseDepSpecs(%v) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestBuildWaitsFor(t *testing.T) {
	t.Run("empty spawner returns nil", func(t *testing.T) {
		got, err := buildWaitsFor("", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Fatalf("expected nil, got %+v", got)
		}
	})
	t.Run("empty gate defaults to all-children", func(t *testing.T) {
		got, err := buildWaitsFor("bd-1", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := &domain.WaitsForSpec{SpawnerID: "bd-1", Gate: types.WaitsForAllChildren}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})
	t.Run("any-children gate accepted", func(t *testing.T) {
		got, err := buildWaitsFor("bd-1", types.WaitsForAnyChildren)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Gate != types.WaitsForAnyChildren {
			t.Errorf("got gate %q, want %q", got.Gate, types.WaitsForAnyChildren)
		}
	})
	t.Run("invalid gate rejected", func(t *testing.T) {
		_, err := buildWaitsFor("bd-1", "bogus")
		if err == nil {
			t.Fatal("expected error for invalid gate")
		}
	})
	t.Run("whitespace spawner treated as empty", func(t *testing.T) {
		got, err := buildWaitsFor("   ", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Fatalf("expected nil for whitespace spawner, got %+v", got)
		}
	})
}

func TestDiscoveredFromParent(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"none", []string{"bd-1", "blocks:bd-2"}, ""},
		{"present", []string{"bd-1", "discovered-from:bd-99", "blocks:bd-2"}, "bd-99"},
		{"first wins", []string{"discovered-from:bd-7", "discovered-from:bd-8"}, "bd-7"},
		{"empty target ignored", []string{"discovered-from:", "discovered-from:bd-9"}, "bd-9"},
		{"whitespace trimmed", []string{"  discovered-from: bd-5 "}, "bd-5"},
		{"empty input", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := discoveredFromParent(tt.in)
			if got != tt.want {
				t.Errorf("discoveredFromParent(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestOverlayYAMLPrefix(t *testing.T) {
	t.Run("yaml wins when set", func(t *testing.T) {
		config.ResetForTesting()
		_ = config.Initialize()
		config.Set("issue-prefix", "yml")
		t.Cleanup(config.ResetForTesting)

		if got := overlayYAMLPrefix("dbp"); got != "yml" {
			t.Errorf("got %q, want %q", got, "yml")
		}
	})
	t.Run("db wins when yaml empty", func(t *testing.T) {
		config.ResetForTesting()
		_ = config.Initialize()
		config.Set("issue-prefix", "")
		t.Cleanup(config.ResetForTesting)

		if got := overlayYAMLPrefix("dbp"); got != "dbp" {
			t.Errorf("got %q, want %q", got, "dbp")
		}
	})
	t.Run("empty db ok when yaml empty", func(t *testing.T) {
		config.ResetForTesting()
		_ = config.Initialize()
		config.Set("issue-prefix", "")
		t.Cleanup(config.ResetForTesting)

		if got := overlayYAMLPrefix(""); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

// TestResolvePrefixValidation covers the union-accept resolution for --id
// validation (beads-xevo): the live DB prefix is always authoritative and
// accepted; a YAML issue-prefix that DISAGREES with the DB prefix is folded
// into the allowed-list (so both work) rather than REPLACING the DB prefix.
// The pre-fix overlayYAMLPrefix let a stale config.yaml prefix shadow the DB
// prefix, so `bd create --id <db-prefix>-x` was rejected on the DB's own prefix.
func TestResolvePrefixValidation(t *testing.T) {
	t.Run("db prefix authoritative; disagreeing yaml folded into allowed", func(t *testing.T) {
		config.ResetForTesting()
		_ = config.Initialize()
		config.Set("issue-prefix", "bd")
		t.Cleanup(config.ResetForTesting)

		gotDB, gotAllowed := resolvePrefixValidation("beads", "")
		if gotDB != "beads" {
			t.Errorf("dbPrefix = %q, want %q (live DB prefix stays authoritative)", gotDB, "beads")
		}
		// The disagreeing YAML prefix must be accepted too (union), so an id
		// carrying either 'beads-' or 'bd-' passes.
		if err := validationValidate(t, "beads-x", gotDB, gotAllowed); err != nil {
			t.Errorf("id with DB prefix should be accepted: %v", err)
		}
		if err := validationValidate(t, "bd-x", gotDB, gotAllowed); err != nil {
			t.Errorf("id with YAML prefix should be accepted (folded into allowed): %v", err)
		}
	})

	// Child-create regression (beads-xevo severity upgrade, dogfooder repro):
	// `bd create --parent <db-prefixed-parent>` needs NO explicit --id — the
	// child id `.N` is auto-generated from the DB counter (so it carries the DB
	// prefix, e.g. "beads-ttrc.1") and then flows through the SAME
	// resolvePrefixValidation + ValidateIDPrefixAllowed path as an explicit id
	// (cmd/bd/create.go: parentID!="" sets explicitID=childID before validation).
	// Under the split-brain (DB prefix "beads", stale YAML "bd"), the pre-fix
	// overlayYAMLPrefix returned "bd" so the DB's OWN child id was rejected with
	// "database uses 'bd-' but ID 'beads-ttrc.1' doesn't match" — every child
	// create failed without --force. Union-accept keeps the DB prefix
	// authoritative, so the generated child id is accepted with force=false.
	t.Run("generated child .N id with db prefix accepted under disagreeing yaml (no --force)", func(t *testing.T) {
		config.ResetForTesting()
		_ = config.Initialize()
		config.Set("issue-prefix", "bd") // stale YAML disagrees with the live DB counter
		t.Cleanup(config.ResetForTesting)

		gotDB, gotAllowed := resolvePrefixValidation("beads", "")
		// The dogfooder's exact failing id: a first- and deeper-level child id
		// auto-generated from the "beads" counter. Both must pass without force.
		for _, childID := range []string{"beads-ttrc.1", "beads-ttrc.2", "beads-ttrc.1.1"} {
			if err := validationValidate(t, childID, gotDB, gotAllowed); err != nil {
				t.Errorf("generated child id %q (DB prefix) should be accepted without --force: %v", childID, err)
			}
		}
	})

	t.Run("yaml equal to db is a no-op (not duplicated)", func(t *testing.T) {
		config.ResetForTesting()
		_ = config.Initialize()
		config.Set("issue-prefix", "beads")
		t.Cleanup(config.ResetForTesting)

		gotDB, gotAllowed := resolvePrefixValidation("beads", "")
		if gotDB != "beads" {
			t.Errorf("dbPrefix = %q, want %q", gotDB, "beads")
		}
		if gotAllowed != "" {
			t.Errorf("allowed = %q, want empty (yaml == db, nothing to add)", gotAllowed)
		}
	})

	t.Run("existing allowed-list preserved and augmented", func(t *testing.T) {
		config.ResetForTesting()
		_ = config.Initialize()
		config.Set("issue-prefix", "bd")
		t.Cleanup(config.ResetForTesting)

		gotDB, gotAllowed := resolvePrefixValidation("beads", "legacy,old")
		if gotDB != "beads" {
			t.Errorf("dbPrefix = %q, want %q", gotDB, "beads")
		}
		// All three of DB, existing-allowed, and YAML prefixes must pass.
		for _, id := range []string{"beads-x", "legacy-x", "old-x", "bd-x"} {
			if err := validationValidate(t, id, gotDB, gotAllowed); err != nil {
				t.Errorf("id %q should be accepted: %v", id, err)
			}
		}
	})

	t.Run("empty db prefix falls back to yaml (un-inited store, old behavior)", func(t *testing.T) {
		config.ResetForTesting()
		_ = config.Initialize()
		config.Set("issue-prefix", "yml")
		t.Cleanup(config.ResetForTesting)

		gotDB, gotAllowed := resolvePrefixValidation("", "")
		if gotDB != "yml" {
			t.Errorf("dbPrefix = %q, want %q (fall back to yaml when db empty)", gotDB, "yml")
		}
		if gotAllowed != "" {
			t.Errorf("allowed = %q, want empty", gotAllowed)
		}
	})

	t.Run("both empty stays empty", func(t *testing.T) {
		config.ResetForTesting()
		_ = config.Initialize()
		config.Set("issue-prefix", "")
		t.Cleanup(config.ResetForTesting)

		gotDB, gotAllowed := resolvePrefixValidation("", "")
		if gotDB != "" || gotAllowed != "" {
			t.Errorf("got (%q,%q), want empty pair", gotDB, gotAllowed)
		}
	})
}

// validationValidate is a tiny local wrapper so the resolvePrefixValidation
// test asserts end-to-end through the real ValidateIDPrefixAllowed contract.
func validationValidate(t *testing.T, id, dbPrefix, allowed string) error {
	t.Helper()
	return validation.ValidateIDPrefixAllowed(id, dbPrefix, allowed, false)
}
