package main

import (
	"github.com/spf13/cobra"
)

// attachUnknownSubcommandGuards walks the command tree and makes every pure
// parent-group command (one that HAS subcommands but is not itself Runnable —
// e.g. "bd label", "bd dep", "bd dolt", "bd config") reject an unknown
// subcommand loudly instead of silently printing help and exiting 0.
//
// Cobra's default: a command with subcommands but no Run/RunE is not runnable,
// so "bd label bogus-typo" short-circuits to Help() and returns nil — exit 0.
// A top-level typo ("bd frobnicate") correctly errors, but a typo'd SUBcommand
// ("bd label st" for "set") silently no-ops. That is a real scripting hazard:
// a CI step or agent gate reads exit 0 as success while the intended write
// never happened (same false-success class as beads-ib1u maintNoArgs and the
// dep-list exit-code guard). This centralizes the fix so every current and
// future parent group is covered without editing each command definition.
//
// Behavior after attach:
//   - "bd label"            -> Help(), exit 0 (bare group: unchanged)
//   - "bd label add ..."    -> runs the child (unchanged)
//   - "bd label bogus"      -> Error, exit 1 (was: Help, exit 0)
//   - nested groups (e.g. "bd dolt remote bogus") are covered recursively.
func attachUnknownSubcommandGuards(cmd *cobra.Command) {
	for _, child := range cmd.Commands() {
		attachUnknownSubcommandGuards(child)
	}

	// Only pure parent groups: has subcommands, no Run/RunE of its own. Skip
	// the auto-generated help/completion commands (they are Runnable) and leaf
	// commands (no subcommands).
	if !cmd.HasSubCommands() || cmd.Runnable() {
		return
	}

	cmd.RunE = func(c *cobra.Command, args []string) error {
		// Bare invocation ("bd label"): preserve the historical help output.
		if len(args) == 0 {
			return c.Help()
		}
		// A leftover positional here is an unknown subcommand (a valid child
		// would have been dispatched by cobra before reaching this RunE).
		return rejectUnknownSubcommand(c, args[0])
	}
}

// rejectUnknownSubcommand emits the canonical "unknown <group> subcommand"
// error for a leftover positional that is not a valid child command. Cobra
// dispatches valid subcommands to the child BEFORE the parent's RunE runs, so a
// positional reaching a parent-group RunE is by definition an unknown
// subcommand (or a typo). Both the pure parent-group guard above and the
// Runnable-hybrid groups (dep/human/metrics/migrate — which have their own RunE
// so the tree walk deliberately skips them, beads-3l5q) route through here for
// one consistent shape.
//
// Silence the usage dump so the error line is the salient output, and return a
// non-nil error so the process exits non-zero. Route through
// HandleErrorRespectJSON so under --json the error is a structured
// {error,schema_version} object on stdout instead of plaintext on stderr with
// an empty stdout (beads-dthi). jsonOutput is set by the root PersistentPreRunE
// before any RunE fires.
//
// HandleErrorRespectJSON writes the message itself and returns the
// &exitError{1} sentinel, so SilenceErrors must be set too — otherwise cobra
// would render the sentinel's own Error() ("exit code 1") to stderr on top of
// our message.
func rejectUnknownSubcommand(c *cobra.Command, arg string) error {
	c.SilenceUsage = true
	c.SilenceErrors = true
	return HandleErrorRespectJSON("unknown %s subcommand %q; run '%s --help' to list available subcommands",
		c.Name(), arg, c.CommandPath())
}
