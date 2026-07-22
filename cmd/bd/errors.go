package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/metrics"
)

type exitError struct {
	Code int
}

func (e *exitError) Error() string {
	return fmt.Sprintf("exit code %d", e.Code)
}

func exitCodeFromError(err error) (int, bool) {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.Code, true
	}
	return 0, false
}

// isArgCountError reports whether err is a cobra positional-arg-count
// validation error (from cobra.ExactArgs/MinimumNArgs/MaximumNArgs/RangeArgs).
// These fire inside rootCmd.ExecuteC() BEFORE a command's RunE, so they never
// reach a --json-aware handler; the main.go error branch uses this to honor
// the --json contract for arg-count failures (beads-71br). The messages are
// cobra's stable literals (args.go): "accepts %d arg(s), received %d",
// "requires at least %d arg(s), only received %d", "accepts at most %d
// arg(s)...", "accepts between %d and %d arg(s)...".
func isArgCountError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "arg(s), received ") ||
		strings.Contains(msg, "arg(s), only received ") ||
		strings.Contains(msg, "accepts between ")
}

// isCobraExecuteCValidationError reports whether err is one of the cobra/pflag
// validation errors that fire inside rootCmd.ExecuteC() BEFORE a command's RunE
// (beads-3tgu, sibling of beads-71br which handled only the arg-count leg). Like
// arg-count errors, these never reach a per-command --json-aware handler, so the
// main.go error branch uses this to honor the --json contract centrally instead
// of leaking plaintext to stderr with an empty stdout.
//
// The covered shapes, matched via cobra/pflag's stable literals:
//   - required-flag        (cobra command.go): `required flag(s) "X" not set`
//   - unknown-flag         (pflag errors.go):  `unknown flag: --X`
//                                              `unknown shorthand flag: "c" in -X`
//   - flag-value-required  (pflag errors.go):  `flag needs an argument: ...`
//   - flag-parse           (pflag errors.go):  `invalid argument "V" for "--F" flag: ...`
//   - bad-flag-syntax      (pflag errors.go):  `bad flag syntax: X`
//   - unknown-command      (cobra args.go):    `unknown command "X" for "bd..."`
//     (both the top-level `bd frobnicate` typo and the leaf-command stray-arg
//     `bd stale bogus` case, which cobra treats as an attempted subcommand
//     dispatch — DISTINCT from the pure-parent-group unknown-subcommand case,
//     which is already JSON-correct via the dthi subcmd_guard.)
//
// Arg-count errors are intentionally NOT folded in here: they retain their own
// isArgCountError matcher + test (beads-71br) and are checked separately in
// main.go. This matcher covers the remaining pre-RunE validation classes.
func isCobraExecuteCValidationError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "required flag(s) ") ||
		strings.Contains(msg, "unknown flag: ") ||
		strings.Contains(msg, "unknown shorthand flag: ") ||
		strings.Contains(msg, "flag needs an argument: ") ||
		strings.Contains(msg, "invalid argument ") ||
		strings.Contains(msg, "bad flag syntax: ") ||
		strings.Contains(msg, "unknown command ")
}

// wantsJSONOutput reports whether the user asked for --json output, robust to
// the parse-abort legs of beads-3tgu. For required-flag and unknown-command
// (arg-position) errors, cobra finishes flag parsing before failing, so the
// bound jsonOutput / executedCmd's "json" flag is reliable. But for
// unknown-flag / flag-parse / bad-syntax errors, pflag aborts at the bad token
// BEFORE recording a `--json` that appears alongside or after it, so neither
// jsonOutput nor the flag lookup is set. The leak is position-independent, so
// detection must be too: fall back to scanning the raw argv for a --json or
// --format json intent token. This mirrors how the user expressed the request,
// not how far cobra got parsing it.
func wantsJSONOutput(executedCmd *cobra.Command, args []string) bool {
	if jsonOutput {
		return true
	}
	if executedCmd != nil {
		if jf := executedCmd.Flags().Lookup("json"); jf != nil && jf.Value.String() == "true" {
			return true
		}
	}
	for i, a := range args {
		switch {
		case a == "--json", a == "--json=true":
			return true
		case strings.EqualFold(a, "--format=json"):
			return true
		case a == "--format":
			// `--format json` (space form).
			if i+1 < len(args) && strings.EqualFold(args[i+1], "json") {
				return true
			}
		}
	}
	return false
}

func activeWorkspaceNotFoundError() string {
	return "no active beads workspace found"
}

func activeWorkspaceNotFoundMessage() string {
	return "No active beads workspace found."
}

func diagHint() string {
	return workspaceDiagHint(true)
}

func whereDiagHint() string {
	return workspaceDiagHint(false)
}

func workspaceDiagHint(includeWhere bool) string {
	if includeWhere {
		if !usesSQLServer() {
			return "run 'bd where' to inspect the resolved workspace, or 'bd init' to create a new database"
		}
		return "run 'bd where' to inspect the resolved workspace, run 'bd doctor' to diagnose, or 'bd init' to create a new database"
	}
	if !usesSQLServer() {
		return "check BEADS_DIR/worktree setup, or run 'bd init' to create a new database"
	}
	return "check BEADS_DIR/worktree setup, run 'bd doctor' to diagnose, or run 'bd init' to create a new database"
}

func buildJSONError(message, hint string) interface{} {
	inner := map[string]interface{}{
		"error": message,
	}
	if hint != "" {
		inner["hint"] = hint
	}
	if jsonEnvelopeEnabled() {
		return map[string]interface{}{
			"schema_version": JSONSchemaVersion,
			"data":           inner,
		}
	}
	inner["schema_version"] = JSONSchemaVersion
	return inner
}

func jsonStderrError(message, hint string) {
	encoder := json.NewEncoder(os.Stderr)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(buildJSONError(message, hint))
}

func jsonStdoutError(message, hint string) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(buildJSONError(message, hint))
}

func HandleError(format string, args ...interface{}) error {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
	return &exitError{Code: 1}
}

func HandleErrorRespectJSON(format string, args ...interface{}) error {
	if jsonOutput {
		jsonStdoutError(fmt.Sprintf(format, args...), "")
		return &exitError{Code: 1}
	}
	return HandleError(format, args...)
}

// argValidationError is the --json-aware error for a custom cobra Args:
// validator (beads-540er, generalizing the beads-t1lx maintNoArgs pattern).
// Args-validators run inside rootCmd.ExecuteC() AFTER flag-parse but BEFORE a
// command's RunE and before the global jsonOutput is bound, so a bare
// fmt.Errorf here never reaches a --json-aware handler: the central main.go
// rescue only recognizes arg-count (isArgCountError) and cobra/pflag literals
// (isCobraExecuteCValidationError), so a custom flag-combo / stray-positional
// message falls through to the SilenceErrors plaintext-stderr branch with an
// empty stdout — breaking the --json contract. Read --json off the command
// directly; under --json emit a parseable JSON error object on stdout and
// return an *exitError (main consumes that via exitCodeFromError before its
// plaintext print). Otherwise preserve the plaintext error unchanged.
func argValidationError(cmd *cobra.Command, format string, args ...interface{}) error {
	msg := fmt.Sprintf(format, args...)
	// A nil cmd carries no flags (some unit tests invoke a validator directly as
	// fn(nil, args) to assert the rejection message); cmd.Flags() would panic, so
	// treat nil as the non-json plaintext path. At real runtime cobra always
	// passes the executed *Command, so the --json leg is reached as intended.
	if cmd != nil {
		if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
			jsonStdoutError(msg, "")
			return &exitError{Code: 1}
		}
	}
	return fmt.Errorf("%s", msg)
}

func HandleErrorWithHint(message, hint string) error {
	if jsonOutput {
		jsonStderrError(message, hint)
	} else {
		fmt.Fprintf(os.Stderr, "Error: %s\n", message)
		fmt.Fprintf(os.Stderr, "Hint: %s\n", hint)
	}
	return &exitError{Code: 1}
}

func HandleErrorWithHintRespectJSON(message, hint string) error {
	if jsonOutput {
		jsonStdoutError(message, hint)
	} else {
		fmt.Fprintf(os.Stderr, "Error: %s\n", message)
		fmt.Fprintf(os.Stderr, "Hint: %s\n", hint)
	}
	return &exitError{Code: 1}
}

// reportItemError reports a per-item failure inside a batch command that loops
// over multiple IDs and `continue`s past individual failures (e.g. bd show, bd
// update). Such commands cannot funnel a single error through RunE, so they
// historically printed a bare plain-text "Error ..." line to stderr even under
// --json, which breaks parsers (beads-fg6).
//
// Under --json it emits a structured JSON error object to STDERR — stdout is
// reserved for the parseable success payload (partial success must keep stdout
// a pure JSON array/object). Otherwise it prints plain text to stderr, matching
// the pre-existing behavior. It does not return an error or change the exit
// code; the caller decides the terminal outcome (see the all-failed paths that
// emit a stdout JSON error via HandleErrorRespectJSON / SilentExit).
func reportItemError(format string, args ...interface{}) {
	if jsonOutput {
		jsonStderrError(fmt.Sprintf(format, args...), "")
		return
	}
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func SilentExit() error {
	return &exitError{Code: 1}
}

// FatalError writes an error message to stderr (structured JSON when --json is
// set) and exits with code 1.
//
// It is retained ONLY for the proxied-server code paths, which run outside
// cobra's RunE error-return convention; every RunE-converted command uses
// HandleError and friends instead. Because FatalError calls os.Exit it bypasses
// the per-command deferred metrics CloseEventAndAdd and main()'s
// metrics.Global().Close()/MaybeSpawnFlusher, so a command that exits through a
// proxied-server FatalError* path records no usage event. Proxied-server mode
// is now enterable (beads-iu9f un-gated "bd init --proxied-server", replacing
// the old TestInitProxiedServerRejectedKeepsMetricsGapLatent with
// TestInitProxiedServerSucceedsAndRecordsProxiedMode), so usesProxiedServer()
// CAN be true and these paths DO run — the telemetry gap is live, not latent.
// The live TODO is to convert these helpers to return errors up through RunE —
// like HandleError — so the deferred metrics close/flush is preserved. NOTE:
// for the --json contract itself, prefer FatalErrorRespectJSON (structured
// {error} on STDOUT) over bare FatalError (STDERR) at any reachable proxied
// validation leg (beads-ag3ru/broz/9fww).
func FatalError(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if jsonOutput {
		jsonStderrError(msg, "")
	} else {
		fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
	}
	os.Exit(1)
}

// FatalErrorRespectJSON writes an error message and exits with code 1. If
// --json is set, outputs structured JSON to stdout; otherwise plain text to
// stderr.
func FatalErrorRespectJSON(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if jsonOutput {
		jsonStdoutError(msg, "")
	} else {
		fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
	}
	os.Exit(1)
}

// FatalErrorWithHintRespectJSON writes an error message with a hint and exits.
// If --json is set, emits structured JSON to stdout so callers can parse it.
func FatalErrorWithHintRespectJSON(message, hint string) {
	if jsonOutput {
		jsonStdoutError(message, hint)
	} else {
		fmt.Fprintf(os.Stderr, "Error: %s\n", message)
		fmt.Fprintf(os.Stderr, "Hint: %s\n", hint)
	}
	os.Exit(1)
}

// FatalErrorWithHint writes an error message with a hint to stderr and exits.
func FatalErrorWithHint(message, hint string) {
	if jsonOutput {
		jsonStderrError(message, hint)
	} else {
		fmt.Fprintf(os.Stderr, "Error: %s\n", message)
		fmt.Fprintf(os.Stderr, "Hint: %s\n", hint)
	}
	os.Exit(1)
}

func WarnError(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "Warning: "+format+"\n", args...)
}

// CheckReadonly aborts the command when bd is running in read-only mode (the
// worker-sandbox posture, see readonlyMode). Like the proxied-server FatalError*
// family above, it exits via os.Exit and so cannot run the per-command deferred
// CloseEventAndAdd — a command blocked here records no cli_command event of its
// own (it never actually ran). It does flush metrics first, so events already
// queued earlier in this run are still written and scheduled for upload rather
// than stranded until the next clean exit.
func CheckReadonly(operation string) {
	if readonlyMode {
		// beads-rus7u: honor --json. Under --json emit the structured {error}
		// object to STDOUT (mirroring FatalErrorRespectJSON / broz / 9fww), so a
		// scripted --json caller in a read-only sandbox gets a parseable payload
		// on stdout rather than bare plaintext on stderr. Non-json behavior is
		// byte-for-byte unchanged (plaintext to stderr). One chokepoint → all
		// ~105 write commands. metrics.CloseAndFlush() + os.Exit(1) preserved.
		msg := fmt.Sprintf("operation '%s' is not allowed in read-only mode", operation)
		if jsonOutput {
			jsonStdoutError(msg, "")
		} else {
			fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
		}
		metrics.CloseAndFlush()
		os.Exit(1)
	}
}
