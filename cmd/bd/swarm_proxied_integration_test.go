//go:build cgo

package main

import (
	"strings"
	"testing"
)

// TestProxiedServerSwarm proves the `bd swarm` subcommand family
// (validate/status/create/list) is proxied-server-aware (beads-2n2f, aocj/qppc/
// 92ld class). Before the fix every swarm subcommand touched the global `store`
// (GetIssue/GetDependents/GetDependencyRecords/SearchIssues + create's
// CreateIssue/AddDependency), which is nil in proxiedServerMode ('swarm' is not
// in main.go noDbCommands) → "no database connection" / "storage is nil" /
// nil-pointer panic on hub-connected crew. The fix routes each subcommand
// through a UOW-backed SwarmStorage adapter (swarm_proxied_server.go).
//
// The pre-fix signature is the nil-store failure; the stronger teeth prove the
// proxied read path resolves the reverse-edge dependents (validate/status see
// the epic's children) and the proxied write path actually creates + links a
// swarm molecule (create → list surfaces it).
func TestProxiedServerSwarm(t *testing.T) {
	requireProxiedServerEnv(t)
	bd := buildEmbeddedBD(t)

	// nilStore reports whether combined output shows the pre-fix failure
	// signature (nil global store dereference / "storage is nil" / no-DB).
	nilStore := func(combined string) string {
		switch {
		case strings.Contains(combined, "storage is nil"):
			return "hit 'storage is nil'"
		case strings.Contains(combined, "no database connection"):
			return "hit 'no database connection'"
		case strings.Contains(combined, "nil pointer dereference"), strings.Contains(combined, "panic:"):
			return "PANICKED (nil store deref)"
		default:
			return ""
		}
	}

	t.Run("validate_does_not_nil_fail_and_sees_children", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "swv")
		epic := bdProxiedCreate(t, bd, p.dir, "Swarm epic", "--type", "epic")
		// Two independent (unblocked) children → a swarmable epic.
		bdProxiedCreate(t, bd, p.dir, "Child A", "--type", "task", "--parent", epic.ID)
		bdProxiedCreate(t, bd, p.dir, "Child B", "--type", "task", "--parent", epic.ID)

		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "swarm", "validate", epic.ID, "--json")
		combined := stdout + stderr
		if sig := nilStore(combined); sig != "" {
			t.Fatalf("bd swarm validate %s in proxied mode (not proxied-server-aware):\nerr:%v\nstdout:\n%s\nstderr:\n%s", sig, err, stdout, stderr)
		}
		// The reverse-edge dependents read must resolve the two children.
		if !strings.Contains(stdout, "\"total_issues\": 2") {
			t.Fatalf("proxied swarm validate did not count 2 children (reverse-edge read broken):\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
		if !strings.Contains(stdout, "\"swarmable\": true") {
			t.Fatalf("epic with 2 unblocked children should be swarmable:\nstdout:\n%s", stdout)
		}
	})

	t.Run("create_then_status_and_list_via_proxy", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "swc")
		epic := bdProxiedCreate(t, bd, p.dir, "Create-swarm epic", "--type", "epic")
		bdProxiedCreate(t, bd, p.dir, "Task 1", "--type", "task", "--parent", epic.ID)
		bdProxiedCreate(t, bd, p.dir, "Task 2", "--type", "task", "--parent", epic.ID)

		// Proxied write: create the swarm molecule + relates-to link + commit.
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "swarm", "create", epic.ID)
		combined := stdout + stderr
		if sig := nilStore(combined); sig != "" {
			t.Fatalf("bd swarm create %s in proxied mode:\nerr:%v\nstdout:\n%s\nstderr:\n%s", sig, err, stdout, stderr)
		}
		if err != nil {
			t.Fatalf("bd swarm create errored in proxied mode: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		}
		if !strings.Contains(stdout, "Created swarm molecule") {
			t.Fatalf("proxied swarm create did not report a created molecule:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}

		// Proxied read: status on the epic must resolve the swarm + children.
		stdout, stderr, err = bdProxiedRunBuffers(t, bd, p.dir, "swarm", "status", epic.ID, "--json")
		combined = stdout + stderr
		if sig := nilStore(combined); sig != "" {
			t.Fatalf("bd swarm status %s in proxied mode:\nerr:%v\nstdout:\n%s\nstderr:\n%s", sig, err, stdout, stderr)
		}
		if !strings.Contains(stdout, epic.ID) {
			t.Fatalf("proxied swarm status did not surface epic %s:\nstdout:\n%s\nstderr:\n%s", epic.ID, stdout, stderr)
		}

		// Proxied read: list must surface the created swarm molecule linked to
		// this epic (proves searchIssues + reverse-edge GetDependencyRecords).
		stdout, stderr, err = bdProxiedRunBuffers(t, bd, p.dir, "swarm", "list", "--json")
		combined = stdout + stderr
		if sig := nilStore(combined); sig != "" {
			t.Fatalf("bd swarm list %s in proxied mode:\nerr:%v\nstdout:\n%s\nstderr:\n%s", sig, err, stdout, stderr)
		}
		if !strings.Contains(stdout, epic.ID) {
			t.Fatalf("proxied swarm list did not surface the swarm linked to epic %s:\nstdout:\n%s\nstderr:\n%s", epic.ID, stdout, stderr)
		}
	})

	t.Run("list_empty_does_not_nil_fail", func(t *testing.T) {
		p := bdProxiedInit(t, bd, "swe")
		stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "swarm", "list", "--json")
		combined := stdout + stderr
		if sig := nilStore(combined); sig != "" {
			t.Fatalf("bd swarm list (empty) %s in proxied mode:\nerr:%v\nstdout:\n%s\nstderr:\n%s", sig, err, stdout, stderr)
		}
		// Empty list must marshal the swarms array, not error.
		if !strings.Contains(stdout, "\"swarms\"") {
			t.Fatalf("proxied empty swarm list --json missing swarms key:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
		}
	})
}
