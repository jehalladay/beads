package main

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/steveyegge/beads/internal/metrics"
)

// mailCmd delegates to an external mail provider.
// This enables agents to use 'bd mail' consistently, while the actual
// mail implementation is provided by the orchestrator.
var mailCmd = &cobra.Command{
	Use:   "mail [subcommand] [args...]",
	Short: "Delegate to mail provider (e.g., gt mail)",
	Long: `Delegates mail operations to an external mail provider.

Agents often type 'bd mail' when working with beads, but mail functionality
is typically provided by the orchestrator. This command bridges that gap
by delegating to the configured mail provider.

Configuration (checked in order):
  1. BEADS_MAIL_DELEGATE or BD_MAIL_DELEGATE environment variable
  2. 'mail.delegate' config setting (bd config set mail.delegate "gt mail")

Examples:
  # Configure delegation (one-time setup)
  export BEADS_MAIL_DELEGATE="gt mail"
  # or
  bd config set mail.delegate "gt mail"

  # Then use bd mail as if it were gt mail
  bd mail inbox                    # Lists inbox
  bd mail send mayor/ -s "Hi"      # Sends mail
  bd mail read msg-123             # Reads a message`,
	DisableFlagParsing: true,
	SilenceUsage:       true,
	SilenceErrors:      true,
	RunE: func(cmd *cobra.Command, args []string) error {
		evt := metrics.NewCommandEvent("mail")
		defer func() {
			if c := metrics.Global(); c != nil {
				c.CloseEventAndAdd(evt)
			}
		}()

		for _, arg := range args {
			if arg == "--help" || arg == "-h" {
				_ = cmd.Help()
				return nil
			}
		}

		delegate := findMailDelegate()
		if delegate == "" {
			return HandleErrorWithHint(
				"no mail delegate configured",
				"Set BEADS_MAIL_DELEGATE=\"gt mail\" or run: bd config set mail.delegate \"gt mail\"")
		}

		parts := strings.Fields(delegate)
		if len(parts) == 0 {
			return HandleError("invalid mail delegate: %q", delegate)
		}

		cmdName := parts[0]
		cmdArgs := append(parts[1:], args...)

		// #nosec G204 - cmdName comes from user configuration (mail_delegate setting)
		execCmd := exec.Command(cmdName, cmdArgs...)
		execCmd.Stdin = os.Stdin
		execCmd.Stdout = os.Stdout
		execCmd.Stderr = os.Stderr

		if err := execCmd.Run(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				return &exitError{Code: exitErr.ExitCode()}
			}
			return HandleError("running %s: %v", delegate, err)
		}
		return nil
	},
}

// findMailDelegate checks for mail delegation configuration
// Priority: env vars > bd config
func findMailDelegate() string {
	// Check environment variables first
	if delegate := os.Getenv("BEADS_MAIL_DELEGATE"); delegate != "" {
		return delegate
	}
	if delegate := os.Getenv("BD_MAIL_DELEGATE"); delegate != "" {
		return delegate
	}

	// Check bd config. In direct mode this reads the global store; for
	// hub-connected (proxiedServerMode) crew the global store is nil (main.go
	// wires uowProvider and returns before store init), so route through a
	// read UOW instead — otherwise a hub crew's `bd config set mail.delegate`
	// (which persists fine via runConfigSetProxiedServer) would be silently
	// ignored here and `bd mail` would fail "no mail delegate configured"
	// while direct crew with identical config work (beads-ufnyb). Fail-soft:
	// any error yields "" (unchanged contract for callers).
	if store != nil {
		if delegate, err := store.GetConfig(rootCtx, "mail.delegate"); err == nil && delegate != "" {
			return delegate
		}
	} else if usesProxiedServer() && uowProvider != nil {
		if delegate := mailDelegateFromProxiedConfig(rootCtx); delegate != "" {
			return delegate
		}
	}

	return ""
}

// mailDelegateFromProxiedConfig reads the mail.delegate config for a
// hub-connected (proxiedServerMode) crew via a read UOW. It is deliberately
// fail-soft — findMailDelegate treats an empty return as "no delegate
// configured" — so any provider/UOW error yields "" rather than aborting the
// process (unlike openConfigProxiedUOW, which is Fatal). beads-ufnyb.
func mailDelegateFromProxiedConfig(ctx context.Context) string {
	if uowProvider == nil {
		return ""
	}
	uw, err := uowProvider.NewUOW(ctx)
	if err != nil {
		return ""
	}
	defer uw.Close(ctx)

	delegate, err := uw.ConfigUseCase().GetConfig(ctx, "mail.delegate")
	if err != nil {
		return ""
	}
	return delegate
}

func init() {
	rootCmd.AddCommand(mailCmd)
}
