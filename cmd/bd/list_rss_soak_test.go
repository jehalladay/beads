//go:build cgo

package main

// list_rss_soak_test.go — the RSS soak / acceptance gate for the 134GB OOM
// class (beads-r06.5, audit hq-uo7a1; sibling of the landed beads-kbw list.go
// fix and beads-r06.13 engine sync fix).
//
// WHAT THIS GUARDS
// ----------------
// `bd list` used to load EVERY dependency record for EVERY issue in the rig
// (GetAllDependencyRecords), regardless of how many issues the command actually
// displayed. On a large rig that materialized every dep row — including its
// JSON metadata — and grew one process to ~128-134GB RSS, OOM-crashing the host
// (RCA hq-lcu9o). beads-kbw fixed it: the display paths now load deps for ONLY
// the displayed page via GetDependencyRecordsForIssues (helper displayedIssueDeps
// in list.go). This test is the guardrail that pins that property so it cannot
// silently regress.
//
// THE PROPERTY UNDER TEST: peak live heap for the list dependency-load must be
// bounded by the DISPLAYED PAGE, not by the total DB size. That is exactly the
// property whose absence caused the 134GB blowup.
//
// HOW IT ASSERTS THAT (a differential, fail-first by construction)
// ----------------------------------------------------------------
// Seeding a literal 200k-1M-issue rig (the audit's prod scale) through the real
// Dolt store is not CI-feasible (seeding is commit-bound at ~100 issues/sec and
// dependency inserts are slower still). Instead we seed a prod-shaped rig
// (~soakIssueCount issues, each with a dependency carrying JSON metadata — the
// row shape that drove the leak) and directly contrast the two load strategies
// under sustained load with a high-frequency heap sampler:
//
//   - BOUNDED (current / post-kbw): the displayedIssueDeps path —
//     GetDependencyRecordsForIssues(displayed page of soakDisplayPage IDs).
//     Peak heap is a function of the PAGE, so it stays flat as the DB grows.
//   - UNBOUNDED (pre-kbw witness): GetAllDependencyRecords — loads every dep in
//     the rig. Peak heap is a function of DB SIZE; on a prod rig this is the
//     134GB path.
//
// The test asserts the bounded path's peak stays under a page-sized ceiling AND
// well below the unbounded path's peak (which scales with the whole rig). On
// pre-kbw code the display path WAS the unbounded load, so this same assertion
// would have FAILED — that is the fail-first property. It passes on current code
// because the display path is bounded.
//
// The high-frequency sampler is essential: the leak is a TRANSIENT allocation
// spike during the load (the whole result set materialized at once), which the
// GC reclaims between iterations. Sampling HeapAlloc only between calls misses
// it; a background sampler at sub-millisecond cadence catches the peak the OOM
// killer actually reacts to.
//
// TIER: long/heavy. Skipped under `go test -short`; run in the -race/long tier:
//   go test -tags cgo -run TestListRSSSoak ./cmd/bd/...
// It uses the package's shared Dolt container + a per-test isolated branch.

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage/dolt"
	"github.com/steveyegge/beads/internal/types"
)

// soakIssueCount is the seeded rig size. The bead spec calls for a prod-shaped
// rig of ~1.5K+ issues with dependencies; this exceeds that comfortably (which
// widens the whole-rig-vs-page differential) while still seeding in about a
// minute on the shared container.
const soakIssueCount = 4000

// soakDisplayPage is how many issues a single `bd list` invocation displays —
// the bound the fixed code loads deps for. A realistic page/limit.
const soakDisplayPage = 50

// soakIterations drives the load repeatedly to model a long-lived server under
// sustained query load and to give the sampler many chances at the peak.
const soakIterations = 50

// soakDepMetaBytes is the size of each dependency's JSON metadata. Metadata was
// a major contributor to the per-row weight that made GetAllDependencyRecords
// balloon; giving each edge a realistic metadata blob makes the bounded-vs-
// unbounded contrast representative rather than trivially small.
const soakDepMetaBytes = 2048

// hfHeapSampler polls runtime HeapAlloc at high frequency and records the max,
// catching the transient allocation spike during a load (the driver of RSS/OOM)
// rather than only the heap retained between calls.
type hfHeapSampler struct {
	stop chan struct{}
	done chan struct{}
	peak uint64
}

func startHeapSampler() *hfHeapSampler {
	s := &hfHeapSampler{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		var m runtime.MemStats
		t := time.NewTicker(200 * time.Microsecond)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
				runtime.ReadMemStats(&m)
				for {
					old := atomic.LoadUint64(&s.peak)
					if m.HeapAlloc <= old || atomic.CompareAndSwapUint64(&s.peak, old, m.HeapAlloc) {
						break
					}
				}
			}
		}
	}()
	return s
}

func (s *hfHeapSampler) finish() uint64 {
	close(s.stop)
	<-s.done
	return atomic.LoadUint64(&s.peak)
}

// TestListRSSSoak is the RSS soak / acceptance gate for the bd-list OOM class.
//
// It asserts the fixed (bounded, per-displayed-page) dependency load keeps peak
// heap bounded by the page, and demonstrates fail-first by contrast with the
// pre-kbw unbounded (whole-rig) load whose peak scales with DB size — the 134GB
// path.
func TestListRSSSoak(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping RSS soak test in short mode (long/-race tier only)")
	}
	if testSharedDB == "" {
		t.Skip("shared test Dolt database not initialized, skipping soak")
	}

	ctx := context.Background()
	store := newTestStoreSharedBranch(t, t.TempDir(), "bd")

	t0 := time.Now()
	pageIDs := seedListSoakCorpus(t, ctx, store, soakIssueCount)
	t.Logf("soak: seeded %d issues (each with a %s-metadata dependency) in %s",
		soakIssueCount, humanIBytes(soakDepMetaBytes), time.Since(t0))

	// --- BOUNDED load: the current/post-kbw list display path. ---
	// displayedIssueDeps loads deps for ONLY the displayed page.
	page := make([]*types.Issue, len(pageIDs))
	for i, id := range pageIDs {
		page[i] = &types.Issue{ID: id}
	}
	boundedPeak, boundedBase := measureLoadPeak(t, func() error {
		_ = displayedIssueDeps(ctx, store, page)
		return nil
	})
	t.Logf("soak: BOUNDED (displayedIssueDeps over %d shown) peak live heap = %s (growth %s) over %d iters",
		soakDisplayPage, humanIBytes(boundedPeak), humanIBytesSigned(int64(boundedPeak)-int64(boundedBase)), soakIterations)

	// --- UNBOUNDED witness: the pre-kbw whole-rig load. ---
	// This is the load that grew to 134GB on a prod rig; its peak scales with
	// the entire DB, not the displayed page.
	unboundedPeak, unboundedBase := measureLoadPeak(t, func() error {
		_, err := store.GetAllDependencyRecords(ctx)
		return err
	})
	t.Logf("soak: UNBOUNDED witness (GetAllDependencyRecords over whole rig) peak live heap = %s (growth %s) over %d iters",
		humanIBytes(unboundedPeak), humanIBytesSigned(int64(unboundedPeak)-int64(unboundedBase)), soakIterations)

	boundedGrowth := int64(boundedPeak) - int64(boundedBase)
	unboundedGrowth := int64(unboundedPeak) - int64(unboundedBase)

	// The core assertion is a SCALING DIFFERENTIAL, not an absolute byte ceiling.
	// An absolute ceiling is fragile: the bounded path still allocates real map/
	// slice/GC churn (a few MiB) that has nothing to do with the leak. What
	// actually distinguishes "bounded by page" from "bounded by rig" — the
	// property whose absence caused 134GB — is that the whole-rig load's heap
	// grows with DB SIZE while the page load's does not. We prove that by
	// contrast on the same seeded rig.

	// Sanity floor: the whole-rig load must actually exercise the rig, else the
	// contrast is meaningless (a fixture problem, not a pass). Each of the
	// soakIssueCount dependency rows is a Dependency struct + string fields;
	// require the whole-rig load to grow heap by at least a conservative
	// per-row floor times the row count.
	const minPerDepRowBytes = 256 // very conservative lower bound on a loaded Dependency row's heap cost
	rigLoadFloor := int64(soakIssueCount) * minPerDepRowBytes
	if unboundedGrowth < rigLoadFloor {
		t.Fatalf("RSS soak inconclusive: whole-rig dep-load grew heap by only %s (< %s expected for a "+
			"%d-dependency rig). The seeded corpus is not exercising the load path — fix the fixture, "+
			"not the assertion.",
			humanIBytesSigned(unboundedGrowth), humanIBytesSigned(rigLoadFloor), soakIssueCount)
	}

	// Guardrail assertion: the page-bounded load must cost materially less heap
	// than the whole-rig load. If the display path regressed to loading the
	// whole rig (the pre-kbw / 134GB behavior), the two would be ~equal and this
	// trips. The required margin is conservative (3x) so normal allocator noise
	// never flakes it, yet any reversion to whole-rig loading — where the ratio
	// collapses to ~1x — fails loudly. The margin only WIDENS on a larger rig,
	// so this is strictly a floor on the real prod-scale headroom.
	const minRatio = 3
	if boundedGrowth*minRatio > unboundedGrowth {
		t.Fatalf("RSS soak FAILED (guardrail not biting): page-bounded list dep-load heap growth %s is "+
			"not materially below whole-rig heap growth %s (ratio %.1fx < required %dx) on a %d-issue "+
			"rig. The `bd list` display path appears to load dependency records for the whole rig "+
			"rather than only the displayed page — this is the unbounded-load regression that grew a "+
			"process to 134GB RSS and OOM-crashed the host (beads-kbw / RCA hq-lcu9o). The display "+
			"path must load deps for the displayed page only (displayedIssueDeps → "+
			"GetDependencyRecordsForIssues).",
			humanIBytesSigned(boundedGrowth), humanIBytesSigned(unboundedGrowth),
			float64(unboundedGrowth)/float64(boundedGrowth+1), minRatio, soakIssueCount)
	}

	t.Logf("soak: PASS — page-bounded dep-load growth %s vs whole-rig growth %s (%.1fx headroom, "+
		"required ≥%dx); `bd list` dep-load is bounded by the displayed page, not rig size.",
		humanIBytesSigned(boundedGrowth), humanIBytesSigned(unboundedGrowth),
		float64(unboundedGrowth)/float64(boundedGrowth+1), minRatio)
}

// measureLoadPeak runs fn soakIterations times under a high-frequency heap
// sampler and returns (peak, baseline) live heap in bytes. baseline is captured
// after a GC just before the loop; peak is the max HeapAlloc observed during it.
func measureLoadPeak(t *testing.T, fn func() error) (peak, baseline uint64) {
	t.Helper()
	runtime.GC()
	time.Sleep(30 * time.Millisecond)
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	baseline = m.HeapAlloc

	s := startHeapSampler()
	for i := 0; i < soakIterations; i++ {
		if err := fn(); err != nil {
			s.finish()
			t.Fatalf("load iteration %d failed: %v", i, err)
		}
	}
	peak = s.finish()
	if peak < baseline {
		peak = baseline
	}
	return peak, baseline
}

// seedListSoakCorpus creates n issues, each carrying one dependency on a shared
// root issue with a soakDepMetaBytes-sized JSON metadata blob. It returns the
// IDs of the first soakDisplayPage issues, to stand in for a displayed page.
//
// A shared-root (star) dependency shape is used deliberately: it gives every
// issue a real dependency record with heavy metadata (the leak's row shape)
// while keeping seeding fast — a chain shape would make the per-edge cycle
// check O(n) and seeding O(n^2).
func seedListSoakCorpus(t *testing.T, ctx context.Context, store *dolt.DoltStore, n int) []string {
	t.Helper()

	meta := `{"note":"` + strings.Repeat("m", soakDepMetaBytes) + `"}`
	rootID := "bd-soak-root"
	now := time.Now().UTC()
	if err := store.CreateIssues(ctx, []*types.Issue{{
		ID: rootID, Title: "soak root", Status: types.StatusOpen,
		IssueType: types.TypeTask, Priority: 2, CreatedAt: now, UpdatedAt: now,
	}}, "fixture"); err != nil {
		t.Fatalf("seed root: %v", err)
	}

	pageIDs := make([]string, 0, soakDisplayPage)
	const batch = 500
	for start := 0; start < n; start += batch {
		end := start + batch
		if end > n {
			end = n
		}
		issues := make([]*types.Issue, 0, end-start)
		for i := start; i < end; i++ {
			id := fmt.Sprintf("bd-soak-%06d", i)
			if len(pageIDs) < soakDisplayPage {
				pageIDs = append(pageIDs, id)
			}
			ts := time.Now().UTC()
			issues = append(issues, &types.Issue{
				ID: id, Title: fmt.Sprintf("Soak issue %d", i), Status: types.StatusOpen,
				IssueType: types.TypeTask, Priority: (i % 4) + 1, CreatedAt: ts, UpdatedAt: ts,
				// A non-blocking "related" edge to the shared root: cheap to
				// validate (no cycle traversal) yet a real dep record with
				// heavy metadata.
				Dependencies: []*types.Dependency{{
					IssueID: id, DependsOnID: rootID, Type: types.DepRelated,
					CreatedAt: ts, CreatedBy: "fixture", Metadata: meta,
				}},
			})
		}
		if err := store.CreateIssues(ctx, issues, "fixture"); err != nil {
			t.Fatalf("seed CreateIssues [%d,%d): %v", start, end, err)
		}
	}
	return pageIDs
}

func humanIBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func humanIBytesSigned(b int64) string {
	if b < 0 {
		return "-" + humanIBytes(uint64(-b))
	}
	return humanIBytes(uint64(b))
}
