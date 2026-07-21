//go:build cgo

package main

import (
	"os"
	"strings"
	"testing"
)

// TestEmbeddedImportPreservesDependencyMetadata covers beads-gnopw: the batch
// create / import path (PersistDependencies in internal/storage/issueops/create.go)
// historically INSERTed dependency edges WITHOUT the metadata or thread_id
// columns, so both defaulted to empty at the DB level. An export->import
// round-trip (JSONL sync via refs/dolt/data, or a backup restore) therefore
// silently dropped edge metadata.
//
// The sharpest observable symptom is the waits-for fanout gate: a source issue
// created with `--waits-for-gate any-children` carries edge metadata
// {"gate":"any-children"}. On the interactive create path that metadata is
// preserved and export emits it. But re-importing into a fresh database ran the
// batch INSERT, dropping the metadata to {} — and ParseWaitsForGateMetadata
// (internal/types/types.go) treats {} as the all-children default, a SEMANTIC
// FLIP from "proceed when the first child completes" to "wait for all
// children." This test proves the metadata survives the round-trip.
//
// Note: the SOURCE create uses the interactive AddDependencyInTx path (which is
// correct), so the loss only shows up after the IMPORT into the fresh target db
// re-exports the edge. Mutation-verify: with the metadata/thread_id columns
// removed from the create.go INSERT, the re-exported edge is {} -> RED.
func TestEmbeddedImportPreservesDependencyMetadata(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt integration tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	// Source db: spawner + waiter with an any-children waits-for gate.
	srcDir, _, _ := bdInit(t, bd, "--prefix", "gnsrc")
	sp := bdCreate(t, bd, srcDir, "spawner", "--type", "task")
	bdCreate(t, bd, srcDir, "waiter", "--type", "task",
		"--waits-for", sp.ID, "--waits-for-gate", "any-children")

	// Sanity: the interactive create path preserved the gate in the source
	// export (this is the ✓ leg of the repro — proves the loss is on import).
	srcExport := bdExport(t, bd, srcDir)
	if !strings.Contains(srcExport, "any-children") {
		t.Fatalf("precondition: source export should carry the any-children gate metadata:\n%s", srcExport)
	}
	expPath := srcDir + "/exp.jsonl"
	if err := os.WriteFile(expPath, []byte(srcExport), 0644); err != nil {
		t.Fatalf("write source export: %v", err)
	}

	// Fresh target db: import the source JSONL (batch PersistDependencies path),
	// then re-export. The gate metadata must survive the import round-trip.
	tgtDir, _, _ := bdInit(t, bd, "--prefix", "gntgt")
	bdImport(t, bd, tgtDir, expPath)

	reExport := bdExport(t, bd, tgtDir)
	if !strings.Contains(reExport, "any-children") {
		t.Errorf("beads-gnopw: bd import dropped the waits-for edge metadata — "+
			"the any-children gate should survive an export->import->export "+
			"round-trip but the re-export lacks it (flips to all-children):\n%s", reExport)
	}
	// The empty-metadata flip is the specific failure: assert the re-export did
	// not collapse the gate to the {} default that ParseWaitsForGateMetadata
	// reads as all-children.
	if strings.Contains(reExport, `"metadata":"{}"`) || strings.Contains(reExport, `"metadata": "{}"`) {
		t.Errorf("beads-gnopw: imported dependency edge metadata collapsed to {} "+
			"(any-children gate lost -> reads as all-children):\n%s", reExport)
	}
}
