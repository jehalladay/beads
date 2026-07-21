package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestTokenizeBatchLine(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr bool
	}{
		{
			name: "simple",
			in:   "close bd-1 done",
			want: []string{"close", "bd-1", "done"},
		},
		{
			name: "tabs and spaces",
			in:   "close\tbd-1  done",
			want: []string{"close", "bd-1", "done"},
		},
		{
			name: "quoted with spaces",
			in:   `update bd-2 title="hello world"`,
			want: []string{"update", "bd-2", "title=hello world"},
		},
		{
			name: "escaped quote",
			in:   `create bug 1 "say \"hi\""`,
			want: []string{"create", "bug", "1", `say "hi"`},
		},
		{
			name: "escaped backslash",
			in:   `create bug 1 "back\\slash"`,
			want: []string{"create", "bug", "1", `back\slash`},
		},
		{
			name:    "unterminated quote",
			in:      `close bd-1 "oops`,
			wantErr: true,
		},
		{
			name: "empty string",
			in:   "",
			want: nil,
		},
		{
			name: "empty quoted",
			in:   `update bd-1 title=""`,
			want: []string{"update", "bd-1", "title="},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tokenizeBatchLine(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("tokenizeBatchLine(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("tokenizeBatchLine(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseBatchScript(t *testing.T) {
	script := `# leading comment
# another
close bd-1 stale

update bd-2 status=in_progress priority=1
create task 2 "triage the backlog"
dep add bd-3 bd-4
dep add bd-3 bd-5 related
dep remove bd-6 bd-7
dep rm bd-8 bd-9
`
	ops, err := parseBatchScript(strings.NewReader(script))
	if err != nil {
		t.Fatalf("parseBatchScript: %v", err)
	}
	wantCmds := []string{
		"close",
		"update",
		"create",
		"dep.add",
		"dep.add",
		"dep.remove",
		"dep.remove",
	}
	if len(ops) != len(wantCmds) {
		t.Fatalf("got %d ops, want %d: %+v", len(ops), len(wantCmds), ops)
	}
	for i, op := range ops {
		if op.cmd != wantCmds[i] {
			t.Errorf("op %d: cmd = %q, want %q", i, op.cmd, wantCmds[i])
		}
		if op.line == 0 {
			t.Errorf("op %d: line number not set", i)
		}
	}

	// Spot check: create title is one token via quoting
	create := ops[2]
	if create.cmd != "create" {
		t.Fatalf("expected create, got %q", create.cmd)
	}
	if len(create.args) != 3 {
		t.Fatalf("create args = %v, want 3 tokens", create.args)
	}
	if create.args[2] != "triage the backlog" {
		t.Errorf("create title = %q, want %q", create.args[2], "triage the backlog")
	}

	// dep.add with type
	depWithType := ops[4]
	if depWithType.cmd != "dep.add" || len(depWithType.args) != 3 || depWithType.args[2] != "related" {
		t.Errorf("dep.add with type mismatch: %+v", depWithType)
	}
}

func TestParseBatchScript_Empty(t *testing.T) {
	ops, err := parseBatchScript(strings.NewReader(""))
	if err != nil {
		t.Fatalf("parseBatchScript empty: %v", err)
	}
	if len(ops) != 0 {
		t.Errorf("expected 0 ops, got %d", len(ops))
	}
}

func TestParseBatchScript_OnlyCommentsAndBlank(t *testing.T) {
	ops, err := parseBatchScript(strings.NewReader("# comment\n\n  \n# another\n"))
	if err != nil {
		t.Fatalf("parseBatchScript: %v", err)
	}
	if len(ops) != 0 {
		t.Errorf("expected 0 ops, got %d", len(ops))
	}
}

func TestParseBatchScript_UnsupportedCommand(t *testing.T) {
	_, err := parseBatchScript(strings.NewReader("show bd-1\n"))
	if err == nil {
		t.Fatal("expected error for unsupported command")
	}
	if !strings.Contains(err.Error(), "unsupported batch command") {
		t.Errorf("error should mention unsupported command, got: %v", err)
	}
	if !strings.Contains(err.Error(), "line 1") {
		t.Errorf("error should include line number, got: %v", err)
	}
}

func TestParseBatchScript_UnsupportedCommandOnLaterLine(t *testing.T) {
	script := "close bd-1\nshow bd-2\n"
	_, err := parseBatchScript(strings.NewReader(script))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error should include line 2, got: %v", err)
	}
}

func TestParseBatchScript_DepRequiresSubcommand(t *testing.T) {
	_, err := parseBatchScript(strings.NewReader("dep\n"))
	if err == nil {
		t.Fatal("expected error for bare 'dep'")
	}
	if !strings.Contains(err.Error(), "subcommand") {
		t.Errorf("error should mention subcommand, got: %v", err)
	}
}

func TestParseBatchScript_DepUnknownSubcommand(t *testing.T) {
	_, err := parseBatchScript(strings.NewReader("dep list bd-1\n"))
	if err == nil {
		t.Fatal("expected error for unknown dep subcommand")
	}
	if !strings.Contains(err.Error(), "dep subcommand") {
		t.Errorf("error should mention dep subcommand, got: %v", err)
	}
}

func TestParseBatchScript_UnterminatedQuote(t *testing.T) {
	_, err := parseBatchScript(strings.NewReader(`create task 1 "oops`))
	if err == nil {
		t.Fatal("expected error for unterminated quote")
	}
}

func TestParseUpdateKVs(t *testing.T) {
	tests := []struct {
		name    string
		in      []string
		want    map[string]interface{}
		wantErr bool
	}{
		{
			name: "status and priority",
			in:   []string{"status=in_progress", "priority=1"},
			want: map[string]interface{}{"status": "in_progress", "priority": 1},
		},
		{
			name: "title",
			in:   []string{"title=new title"},
			want: map[string]interface{}{"title": "new title"},
		},
		{
			// beads-fo5l1: batch update title must be trimmed before storage,
			// mirroring `bd update --title` (update.go:119) and `bd create`
			// (create.go:119). A raw padded title is unsearchable/mis-sorted —
			// same class as the assignee sibling (beads-5k1y6) in this switch.
			name: "title padded value trimmed",
			in:   []string{"title=  hello world  "},
			want: map[string]interface{}{"title": "hello world"},
		},
		{
			name: "assignee blank allowed (unassign)",
			in:   []string{"assignee="},
			want: map[string]interface{}{"assignee": ""},
		},
		{
			// beads-5k1y6: batch update assignee must be trimmed + fold "none"→""
			// through normalizeAssignee, mirroring `bd update --assignee`
			// (beads-llzt). A raw padded value is unmatchable by
			// `bd ready/list --assignee alice`, orphaning the work.
			name: "assignee padded value trimmed",
			in:   []string{"assignee=  alice  "},
			want: map[string]interface{}{"assignee": "alice"},
		},
		{
			// beads-5k1y6: "none" (any case) folds to the empty unassigned form.
			name: "assignee none folds to empty",
			in:   []string{"assignee=None"},
			want: map[string]interface{}{"assignee": ""},
		},
		{
			name:    "unsupported key",
			in:      []string{"description=foo"},
			wantErr: true,
		},
		{
			name:    "missing equals",
			in:      []string{"status"},
			wantErr: true,
		},
		{
			name:    "invalid priority",
			in:      []string{"priority=high"},
			wantErr: true,
		},
		{
			// beads-r06.11: out-of-range priority must be rejected at the CLI
			// boundary rather than silently written to the DB.
			name:    "priority above range",
			in:      []string{"priority=999"},
			wantErr: true,
		},
		{
			name:    "priority below range",
			in:      []string{"priority=-1"},
			wantErr: true,
		},
		{
			name:    "priority 5 out of range",
			in:      []string{"priority=5"},
			wantErr: true,
		},
		{
			// P-prefix format is accepted and normalized to the numeric value,
			// matching the `bd update --priority` flag path.
			name: "priority P-format normalized",
			in:   []string{"priority=P3"},
			want: map[string]interface{}{"priority": 3},
		},
		{
			name:    "empty status",
			in:      []string{"status="},
			wantErr: true,
		},
		{
			name:    "empty title",
			in:      []string{"title="},
			wantErr: true,
		},
		{
			// beads-fo5l1: whitespace-only title rejected after trimming,
			// matching the single-command paths.
			name:    "whitespace-only title rejected",
			in:      []string{"title=   "},
			wantErr: true,
		},
		{
			// beads-gqvu: batch update status= must case-fold built-in statuses
			// (write sibling of beads-7wrj), matching `bd update --status` and
			// the read/filter path. Before the fix "OPEN" stored raw and would
			// fail the storage-layer status validation.
			name: "status uppercase case-folded",
			in:   []string{"status=OPEN"},
			want: map[string]interface{}{"status": "open"},
		},
		{
			name: "status mixed-case case-folded",
			in:   []string{"status=In_Progress"},
			want: map[string]interface{}{"status": "in_progress"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseUpdateKVs(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseUpdateKVs err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseUpdateKVs = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBatchCmd_Registered(t *testing.T) {
	// Ensure 'bd batch' is wired into rootCmd with the correct group.
	cmd, _, err := rootCmd.Find([]string{"batch"})
	if err != nil {
		t.Fatalf("rootCmd.Find batch: %v", err)
	}
	if cmd == nil || cmd.Name() != "batch" {
		t.Fatalf("expected batch command, got %+v", cmd)
	}
	if cmd.GroupID != "maint" {
		t.Errorf("batch GroupID = %q, want %q", cmd.GroupID, "maint")
	}
	if cmd.Flags().Lookup("file") == nil {
		t.Error("batch missing --file flag")
	}
	if cmd.Flags().Lookup("dry-run") == nil {
		t.Error("batch missing --dry-run flag")
	}
	if cmd.Flags().Lookup("message") == nil {
		t.Error("batch missing --message flag")
	}
}
