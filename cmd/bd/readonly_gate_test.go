package main

import (
	"sort"
	"testing"

	"github.com/spf13/cobra"
)

// knownWriteCommands is the exhaustive complement of readonlyReadAllowlist:
// every registered leaf command that is a WRITE (or a conditional-write whose
// write branch is guarded per-verb and whose default is otherwise a write).
// Together with readonlyReadAllowlist it must partition the ENTIRE user command
// surface — TestReadonlyGatePartition fails if any registered command is in
// neither set (an unclassified command) or in both (a contradiction). This is
// the mechanism that prevents the beads-q634 leak-by-omission: a newly-added
// command that nobody classified breaks the build until it is explicitly put on
// one side, and the central gate refuses it under --readonly until then.
var knownWriteCommands = map[string]bool{
	"bd admin cleanup":          true,
	"bd admin compact":          true,
	"bd admin reset":            true,
	"bd ado pull":               true,
	"bd ado push":               true,
	"bd ado sync":               true,
	"bd assign":                 true,
	"bd audit label":            true,
	"bd audit record":           true,
	"bd backup init":            true,
	"bd backup remove":          true,
	"bd backup restore":         true,
	"bd backup sync":            true,
	"bd batch":                  true,
	"bd bootstrap":              true,
	"bd branch":                 true,
	"bd close":                  true,
	"bd codex-hook":             true,
	"bd comment":                true,
	"bd comments add":           true,
	"bd compact":                true,
	"bd config apply":           true,
	"bd config set":             true,
	"bd config set-many":        true,
	"bd config unset":           true,
	"bd cook":                   true,
	"bd create":                 true,
	"bd create-form":            true,
	"bd defer":                  true,
	"bd delete":                 true,
	"bd dep add":                true,
	"bd dep relate":             true,
	"bd dep remove":             true,
	"bd dep unrelate":           true,
	"bd doctor":                 true,
	"bd dolt clean-databases":   true,
	"bd dolt commit":            true,
	"bd dolt killall":           true,
	"bd dolt pull":              true,
	"bd dolt push":              true,
	"bd dolt remote add":        true,
	"bd dolt remote remove":     true,
	"bd dolt set":               true,
	"bd dolt start":             true,
	"bd dolt stop":              true,
	"bd duplicate":              true,
	"bd duplicates":             true,
	"bd edit":                   true,
	"bd epic close-eligible":    true,
	"bd federation add-peer":    true,
	"bd federation remove-peer": true,
	"bd federation sync":        true,
	"bd flatten":                true,
	"bd forget":                 true,
	"bd formula convert":        true,
	"bd gate add-waiter":        true,
	"bd gate check":             true,
	"bd gate create":            true,
	"bd gate discover":          true,
	"bd gate resolve":           true,
	"bd gc":                     true,
	"bd github pull":            true,
	"bd github push":            true,
	"bd github sync":            true,
	"bd gitlab pull":            true,
	"bd gitlab push":            true,
	"bd gitlab sync":            true,
	"bd hooks install":          true,
	"bd hooks run":              true,
	"bd hooks uninstall":        true,
	"bd human dismiss":          true,
	"bd human respond":          true,
	"bd import":                 true,
	"bd init":                   true,
	"bd jira pull":              true,
	"bd jira push":              true,
	"bd jira sync":              true,
	"bd kv clear":               true,
	"bd kv set":                 true,
	"bd label add":              true,
	"bd label propagate":        true,
	"bd label remove":           true,
	"bd linear pull":            true,
	"bd linear push":            true,
	"bd linear sync":            true,
	"bd link":                   true,
	"bd mail":                   true,
	"bd merge-slot acquire":     true,
	"bd merge-slot create":      true,
	"bd merge-slot release":     true,
	"bd metrics off":            true,
	"bd metrics on":             true,
	"bd migrate":                true,
	"bd migrate hooks":          true,
	"bd migrate issues":         true,
	"bd migrate schema":         true,
	"bd migrate sync":           true,
	"bd migrate-issues":         true,
	"bd mol bond":               true,
	"bd mol burn":               true,
	"bd mol distill":            true,
	"bd mol pour":               true,
	"bd mol seed":               true,
	"bd mol squash":             true,
	"bd mol wisp create":        true,
	"bd mol wisp gc":            true,
	"bd note":                   true,
	"bd notion connect":         true,
	"bd notion init":            true,
	"bd notion pull":            true,
	"bd notion push":            true,
	"bd notion sync":            true,
	"bd priority":               true,
	"bd promote":                true,
	"bd prune":                  true,
	"bd purge":                  true,
	"bd q":                      true,
	"bd recompute-blocked":      true,
	"bd remember":               true,
	"bd rename":                 true,
	"bd rename-prefix":          true,
	"bd reopen":                 true,
	"bd repo add":               true,
	"bd repo remove":            true,
	"bd repo sync":              true,
	"bd restore":                true,
	"bd rules compact":          true,
	"bd set-state":              true,
	"bd setup":                  true,
	"bd ship":                   true,
	"bd sql":                    true,
	"bd supersede":              true,
	"bd swarm create":           true,
	"bd tag":                    true,
	"bd todo add":               true,
	"bd todo done":              true,
	"bd undefer":                true,
	"bd update":                 true,
	"bd upgrade ack":            true,
	"bd vc commit":              true,
	"bd vc merge":               true,
	"bd worktree create":        true,
	"bd worktree remove":        true,
}

// isInternalAlwaysAllowed mirrors the name-based short-circuit in
// isReadonlyPermitted: cobra-internal / help / completion / plumbing commands
// that never touch user data are always allowed and are neither reads nor
// writes for partition purposes. Cobra adds some of these lazily (e.g.
// "__complete", "completion bash") once completion is exercised by any earlier
// test in the package, so match on the leaf Name(), not the full path.
func isInternalAlwaysAllowed(path, name string) bool {
	switch name {
	case "help", "completion", "__complete", "__completeNoDesc",
		"bash", "zsh", "fish", "powershell",
		"send-metrics", "db-proxy-child":
		return true
	}
	return path == "bd"
}

// leafCommand is a registered runnable command's full path plus its leaf name
// (needed to match cobra-internal commands added lazily under varying paths).
type leafCommand struct {
	path string
	name string
}

func allLeafCommands(t *testing.T) []leafCommand {
	t.Helper()
	var leaves []leafCommand
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Runnable() {
			leaves = append(leaves, leafCommand{path: c.CommandPath(), name: c.Name()})
		}
		for _, k := range c.Commands() {
			walk(k)
		}
	}
	for _, k := range rootCmd.Commands() {
		walk(k)
	}
	sort.Slice(leaves, func(i, j int) bool { return leaves[i].path < leaves[j].path })
	return leaves
}

// TestReadonlyGatePartition is the anti-drift guard for beads-tjlq: it asserts
// that EVERY registered leaf command is classified exactly once as a read
// (readonlyReadAllowlist), a write (knownWriteCommands), or an internal no-op.
// A new command that nobody classified fails here — which is the whole point:
// under --readonly the central gate denies unclassified commands by default, so
// the failing test is the loud, build-time signal to classify it (and it fails
// SAFE meanwhile). It also asserts read and write sets are disjoint.
func TestReadonlyGatePartition(t *testing.T) {
	for _, leaf := range allLeafCommands(t) {
		path := leaf.path
		read := readonlyReadAllowlist[path]
		write := knownWriteCommands[path]
		internal := isInternalAlwaysAllowed(path, leaf.name)

		classified := 0
		if read {
			classified++
		}
		if write {
			classified++
		}
		if internal {
			classified++
		}

		switch {
		case classified == 0:
			t.Errorf("command %q is UNCLASSIFIED for --readonly: add it to "+
				"readonlyReadAllowlist (if it is a pure read) or knownWriteCommands "+
				"(if it can persist state). Leaving it unclassified is the beads-q634 "+
				"leak-by-omission class; the central gate denies it under --readonly "+
				"until it is classified.", path)
		case classified > 1:
			t.Errorf("command %q is classified more than once (read=%v write=%v internal=%v); "+
				"it must be exactly one.", path, read, write, internal)
		}
	}

	// Every allowlist entry must correspond to a real registered command
	// (catches typos and stale entries after a command is removed/renamed).
	registered := make(map[string]bool)
	for _, leaf := range allLeafCommands(t) {
		registered[leaf.path] = true
	}
	// Parent commands (e.g. "bd config", "bd dep") are Runnable in this tree
	// too, so they appear in allLeafCommands; any allowlist key that is not a
	// registered runnable path is stale.
	for path := range readonlyReadAllowlist {
		if !registered[path] {
			t.Errorf("readonlyReadAllowlist has stale/unknown entry %q (no such registered command)", path)
		}
	}
	for path := range knownWriteCommands {
		if !registered[path] {
			t.Errorf("knownWriteCommands has stale/unknown entry %q (no such registered command)", path)
		}
	}
}

// TestReadonlyGateBlocksWrites is a unit-level check that the gate helper
// refuses a representative write command and permits a representative read
// WITHOUT needing an embedded DB. It flips readonlyMode directly and asserts
// isReadonlyPermitted's verdict (the enforceReadonlyGate os.Exit path is
// exercised end-to-end by the cgo integration test).
func TestReadonlyGateBlocksWrites(t *testing.T) {
	find := func(path string) *cobra.Command {
		var found *cobra.Command
		var walk func(c *cobra.Command)
		walk = func(c *cobra.Command) {
			if c.CommandPath() == path {
				found = c
				return
			}
			for _, k := range c.Commands() {
				walk(k)
			}
		}
		walk(rootCmd)
		return found
	}

	// Representative writes that historically lacked or bypassed a per-verb
	// guard (the beads-tjlq motivating cases) must be refused by the gate.
	for _, path := range []string{"bd branch", "bd doctor", "bd config apply", "bd import", "bd update"} {
		c := find(path)
		if c == nil {
			t.Fatalf("could not locate command %q in the tree", path)
		}
		if isReadonlyPermitted(c) {
			t.Errorf("write command %q must NOT be permitted under --readonly (beads-tjlq)", path)
		}
	}
	// Representative reads must stay permitted.
	for _, path := range []string{"bd list", "bd show", "bd query", "bd count", "bd config get", "bd ready"} {
		c := find(path)
		if c == nil {
			t.Fatalf("could not locate command %q in the tree", path)
		}
		if !isReadonlyPermitted(c) {
			t.Errorf("read command %q must remain permitted under --readonly", path)
		}
	}
}
