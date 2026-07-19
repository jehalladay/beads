package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestAttachUnknownSubcommandGuards pins the beads-e71t fix: a pure parent
// group ("bd label", "bd dolt") must reject an unknown/typo'd subcommand with a
// non-zero exit and a clear error, rather than cobra's default of silently
// printing help and returning nil (exit 0). Bare groups still show help, and
// valid children (including nested ones) still dispatch normally.
func TestAttachUnknownSubcommandGuards(t *testing.T) {
	// build a small tree mirroring bd's shape: a leaf-bearing group (label), a
	// nested group-of-groups (dolt > remote), and a plain leaf command (create).
	mk := func() *cobra.Command {
		root := &cobra.Command{Use: "bd"}

		label := &cobra.Command{Use: "label", Short: "Manage labels"}
		label.AddCommand(&cobra.Command{Use: "add", RunE: func(c *cobra.Command, a []string) error { return nil }})

		dolt := &cobra.Command{Use: "dolt", Short: "Dolt ops"}
		remote := &cobra.Command{Use: "remote", Short: "Remotes"}
		remote.AddCommand(&cobra.Command{Use: "add", RunE: func(c *cobra.Command, a []string) error { return nil }})
		dolt.AddCommand(remote)

		create := &cobra.Command{Use: "create", RunE: func(c *cobra.Command, a []string) error { return nil }}

		root.AddCommand(label, dolt, create)
		attachUnknownSubcommandGuards(root)
		return root
	}

	tests := []struct {
		name       string
		args       []string
		wantErr    bool
		wantErrHas string
	}{
		{name: "unknown subcommand of a group errors", args: []string{"label", "bogus"}, wantErr: true, wantErrHas: `unknown label subcommand "bogus"`},
		{name: "unknown subcommand of a nested group errors", args: []string{"dolt", "remote", "bogus"}, wantErr: true, wantErrHas: `unknown remote subcommand "bogus"`},
		{name: "valid child of a group runs", args: []string{"label", "add"}, wantErr: false},
		{name: "valid nested child runs", args: []string{"dolt", "remote", "add"}, wantErr: false},
		{name: "bare group shows help (no error)", args: []string{"label"}, wantErr: false},
		{name: "bare group-of-groups shows help (no error)", args: []string{"dolt"}, wantErr: false},
		{name: "leaf command is untouched", args: []string{"create"}, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// beads-dthi: the guard now routes its error through
			// HandleErrorRespectJSON, which WRITES the message to stderr (plain
			// mode) and returns the opaque &exitError{1} sentinel — so the
			// message is no longer carried on err.Error(). Pin plain mode and
			// capture stderr to assert the message; assert err is the non-nil
			// exit sentinel.
			t.Setenv("BEADS_JSON", "")
			savedJSON := jsonOutput
			jsonOutput = false
			t.Cleanup(func() { jsonOutput = savedJSON })

			var execErr error
			stderr := captureStderr(t, func() {
				root := mk()
				root.SetArgs(tt.args)
				root.SetOut(&bytes.Buffer{})
				root.SetErr(&bytes.Buffer{})
				execErr = root.Execute()
			})
			if tt.wantErr {
				if execErr == nil {
					t.Fatalf("args=%v: expected an error (non-zero exit), got nil", tt.args)
				}
				if tt.wantErrHas != "" && !strings.Contains(stderr, tt.wantErrHas) {
					t.Errorf("args=%v: stderr = %q, want it to contain %q", tt.args, stderr, tt.wantErrHas)
				}
			} else if execErr != nil {
				t.Fatalf("args=%v: expected no error, got %v", tt.args, execErr)
			}
		})
	}
}

// TestUnknownSubcommandErrorNamesTheParent verifies the error text points the
// user at the right --help, which is what makes a typo actionable.
func TestUnknownSubcommandErrorNamesTheParent(t *testing.T) {
	root := &cobra.Command{Use: "bd"}
	cfg := &cobra.Command{Use: "config", Short: "config"}
	cfg.AddCommand(&cobra.Command{Use: "get", RunE: func(c *cobra.Command, a []string) error { return nil }})
	root.AddCommand(cfg)
	attachUnknownSubcommandGuards(root)

	// beads-dthi: message is on stderr (HandleErrorRespectJSON), not err.Error().
	t.Setenv("BEADS_JSON", "")
	savedJSON := jsonOutput
	jsonOutput = false
	t.Cleanup(func() { jsonOutput = savedJSON })

	var execErr error
	stderr := captureStderr(t, func() {
		root.SetArgs([]string{"config", "gett"})
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		execErr = root.Execute()
	})
	if execErr == nil {
		t.Fatal("config gett: expected error, got nil")
	}
	if !strings.Contains(stderr, "bd config --help") {
		t.Errorf("error should point at 'bd config --help'; got stderr: %q", stderr)
	}
}
