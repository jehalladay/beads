package main

import (
	"github.com/spf13/cobra"
)

// readonlyGate implements the CENTRAL, default-deny --readonly sandbox gate
// (beads-tjlq, durable follow-up to beads-q634).
//
// Before tjlq, --readonly was enforced by ~100 per-verb CheckReadonly() calls
// scattered across command RunEs. That is leak-prone by construction: any new
// write verb (or a write verb whose author forgets the call) silently re-opens
// the sandbox hole. beads-q634 was exactly that — config set/unset/set-many,
// import and vc commit shipped without the per-verb guard. Auditing this file
// also turned up two more never-guarded write paths: `bd branch <name>` (creates
// a branch) and `bd doctor --fix` (mutates the database).
//
// The durable fix is to invert the default: under --readonly, DENY every
// command unless it is on an explicit, curated READ allowlist. A new command is
// blocked by default, so forgetting to classify it fails safe (it is refused,
// not silently permitted). The per-verb CheckReadonly calls are intentionally
// LEFT IN PLACE as belt-and-suspenders — with the central gate they are
// redundant for unconditional writes, but they still carry the precise
// per-flag semantics for CONDITIONALLY-writing reads (e.g. `bd ready --claim`,
// `bd cook --persist`, `bd dep --blocks`, `bd duplicates --auto-merge`), which
// live on the allowlist as reads and must only be blocked when the write flag
// is actually set. Because the gate can only ever add blocking (never remove
// it), a mis-classification here can never open a hole that a per-verb guard
// would have caught.
//
// The gate keys on cobra CommandPath() (e.g. "bd config get"), which is stable
// and unambiguous across the whole tree. readonlyReadAllowlist is validated
// exhaustively by TestReadonlyGatePartition: every registered leaf command must
// be either on the allowlist (read) or off it (write), and the test fails if a
// newly-added command is unclassified — so the allowlist cannot silently drift.

// readonlyReadAllowlist is the set of command paths that are pure reads (or
// no-op/diagnostic/help commands) and are therefore permitted under --readonly.
// Everything NOT in this set is treated as a write and refused. Keep entries in
// sync with TestReadonlyGatePartition (which enforces total coverage).
var readonlyReadAllowlist = map[string]bool{
	// Core issue reads
	"bd blocked":         true,
	"bd children":        true,
	"bd count":           true,
	"bd diff":            true,
	"bd history":         true,
	"bd list":            true,
	"bd query":           true,
	"bd ready":           true, // --claim is a conditional write, still guarded per-verb
	"bd search":          true,
	"bd show":            true,
	"bd stale":           true,
	"bd state":           true,
	"bd state list":      true,
	"bd status":          true,
	"bd statuses":        true,
	"bd types":           true,
	"bd comments":        true, // parent (help); comments add is a write
	"bd comments list":   true,
	"bd find-duplicates": true, // analysis only; does not persist
	"bd lint":            true,
	"bd orphans":         true,
	"bd preflight":       true,

	// Dependency / structure reads
	"bd dep":            true, // parent (help); --blocks is a conditional write, guarded per-verb
	"bd dep cycles":     true,
	"bd dep list":       true,
	"bd dep tree":       true,
	"bd graph":          true,
	"bd graph check":    true,
	"bd epic status":    true,
	"bd swarm list":     true,
	"bd swarm status":   true,
	"bd swarm validate": true, // analysis only; does not persist

	// TODO reads (bare `bd todo` lists; `todo add`/`todo done` are conditional
	// writes still guarded per-verb).
	"bd todo":      true,
	"bd todo list": true,

	// Config / kv / memory reads ("bd config" is a non-runnable parent: cobra
	// prints help without invoking PersistentPreRunE, so it needs no entry).
	"bd config get":      true,
	"bd config list":     true,
	"bd config show":     true,
	"bd config drift":    true, // read-only diagnostic
	"bd config validate": true,
	"bd kv get":          true,
	"bd kv list":         true,
	"bd memories":        true,
	"bd recall":          true,

	// Introspection / help / setup-info (no DB writes)
	"bd context":     true,
	"bd info":        true,
	"bd init-safety": true,
	"bd onboard":     true,
	"bd ping":        true,
	"bd prime":       true,
	"bd quickstart":  true,
	"bd version":     true,
	"bd where":       true,
	"bd human":       true, // parent: help/essential-commands
	"bd human list":  true,
	"bd human stats": true,

	// Export is a read (emits JSONL; does not mutate the DB)
	"bd export": true,

	// Provider "status"/"projects"/"teams"/"repos" reads (pull/push/sync/connect/init are writes)
	"bd ado projects":    true,
	"bd ado status":      true,
	"bd github repos":    true,
	"bd github status":   true,
	"bd gitlab projects": true,
	"bd gitlab status":   true,
	"bd jira status":     true,
	"bd linear status":   true,
	"bd linear teams":    true,
	"bd notion status":   true,

	// Gate / merge-slot reads (check/list/show/discover mutate → NOT here except pure reads)
	"bd gate list":        true,
	"bd gate show":        true,
	"bd merge-slot check": true,

	// Backup status is a read (init/sync/restore/remove are writes)
	"bd backup status": true,

	// Dolt diagnostic/read subcommands (push/pull/commit/set/remote-add/... are writes)
	"bd dolt show":        true,
	"bd dolt status":      true,
	"bd dolt test":        true,
	"bd dolt remote list": true, // lists configured remotes; no mutation

	// Federation / repo reads
	"bd federation list-peers": true,
	"bd federation status":     true,
	"bd repo list":             true,

	// Formula reads (convert writes a .toml file → NOT here)
	"bd formula list": true,
	"bd formula show": true,

	// Hooks reads (install/uninstall/run mutate → NOT here)
	"bd hooks list": true,

	// Label reads
	"bd label list":     true,
	"bd label list-all": true,

	// Molecule reads (bond/burn/squash/pour/seed/distill mutate → NOT here)
	"bd mol current":       true,
	"bd mol last-activity": true,
	"bd mol progress":      true,
	"bd mol ready":         true,
	"bd mol show":          true,
	"bd mol stale":         true,
	"bd mol wisp":          true, // parent (help)
	"bd mol wisp list":     true,

	// Metrics config/help. `bd metrics` (bare) shows the current setting and
	// `metrics example` just prints a sample payload — both pure reads. `metrics
	// on`/`off` write user-global config and stay off the allowlist (writes).
	"bd metrics":         true, // parent: shows current setting
	"bd metrics example": true, // prints a sample metrics payload; no write

	// Upgrade reads (ack mutates → NOT here)
	"bd upgrade review": true,
	"bd upgrade status": true,

	// vc / restore reads
	"bd vc status": true,

	// Worktree reads (create/remove mutate → NOT here)
	"bd worktree info": true,
	"bd worktree list": true,

	// Rules audit is a read (rules compact mutates → NOT here)
	"bd rules audit": true,

	// NOTE: `bd audit record`/`bd audit label` are WRITES (append-only JSONL),
	// `bd set-state` writes an event, and provider pull/push/sync/connect/init
	// all persist — none appear here (default-deny refuses them).
}

// isReadonlyPermitted reports whether cmd may run under --readonly. A command is
// permitted iff its CommandPath is on readonlyReadAllowlist. Internal/no-op
// commands that never touch a DB (completion scripts, the metrics flusher, the
// db-proxy child) are permitted so shells and internal plumbing keep working.
func isReadonlyPermitted(cmd *cobra.Command) bool {
	if cmd == nil {
		return true
	}
	// Cobra internal + help commands: always allowed (no user data write).
	switch cmd.Name() {
	case "help", "completion", "__complete", "__completeNoDesc",
		"bash", "zsh", "fish", "powershell",
		"send-metrics", "db-proxy-child":
		return true
	}
	// The bare root ("bd" with no subcommand) just prints help.
	if cmd.CommandPath() == "bd" {
		return true
	}
	return readonlyReadAllowlist[cmd.CommandPath()]
}

// enforceReadonlyGate blocks cmd when running under --readonly and cmd is not on
// the read allowlist. It reuses CheckReadonly so the error message/exit path is
// identical to the per-verb guards (beads-tjlq).
func enforceReadonlyGate(cmd *cobra.Command) {
	if !readonlyMode {
		return
	}
	if isReadonlyPermitted(cmd) {
		return
	}
	CheckReadonly(cmd.CommandPath()) // os.Exit(1) with the read-only-mode error
}
