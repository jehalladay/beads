package debug

import (
	"os"
	"path/filepath"
	"strings"
)

// beads-uwdua: test-only ceiling for the upward .beads/workspace-discovery walks.
//
// The workspace-resolution walks (internal/beads FindBeadsDir/findLocalBeadsDir/
// FindBeadsDirFrom, internal/config config.yaml discovery, cmd/bd findBeadsRepoRoot)
// climb from cwd toward the filesystem root looking for a `.beads` directory. When
// a "no-workspace / expect-error" test runs from a temp dir that happens to be a
// DESCENDANT of a real populated `.beads` (e.g. the refinery worktree, where
// TMPDIR=/fsx/ubuntu/tmp sits under /fsx/ubuntu/.beads), the walk discovers that
// ancestor workspace and the test false-FAILs. Real CI (a clean checkout with no
// `.beads` ancestor above the temp base) is unaffected, so this is purely a
// test-environment hazard.
//
// GT_TEST_WORKSPACE_CEILING names a directory the walks must NOT climb at or
// above. The hermetic test wrapper (scripts/ci/lib/test-env.sh) sets it to $TMPDIR
// so no walk started from a t.TempDir() can escape the temp base into the host's
// real workspace. It is unset in production, so WalkCeilingReached always returns
// false there and normal discovery is unchanged.
//
// It deliberately does NOT use a BEADS_/BD_ prefix: several config-package tests
// call envSnapshot(t), which unsets every BEADS_*/BD_* var for the duration of the
// test (to isolate config resolution). A ceiling under those prefixes would be
// stripped exactly in the tests that need it. The GT_ prefix survives envSnapshot
// while staying clearly a test/orchestration-only knob.
const WalkCeilingEnvVar = "GT_TEST_WORKSPACE_CEILING"

// walkCeiling reads and canonicalizes the ceiling env var on EVERY call rather
// than caching. Caching (e.g. sync.Once) is wrong here: tests in a single package
// run set/unset the env with t.Setenv between subtests, and a cached value from
// the first walk would leak into every later test. The env read + canonicalize is
// cheap and these walks are not hot loops, so no cache is warranted. In production
// the var is unset and this returns "" immediately.
func walkCeiling() string {
	raw := os.Getenv(WalkCeilingEnvVar)
	if raw == "" {
		return ""
	}
	return canonicalizeForCompare(raw)
}

// WalkCeilingReached reports whether an upward .beads-discovery walk must stop at
// dir. Returns false whenever the ceiling env var is unset (always the case in
// production), so callers can guard a walk with a cheap
// `if debug.WalkCeilingReached(dir) { break }` at the top of each iteration.
//
// When a ceiling IS set, a walk stops the moment it reaches the ceiling directory
// itself OR any STRICT ANCESTOR of it — i.e. the walk may inspect the ceiling's
// descendants but never steps out of the ceiling subtree toward the filesystem
// root. A walk that starts inside $TMPDIR (every t.TempDir() under the hermetic
// wrapper) climbs within $TMPDIR and halts at $TMPDIR before it can discover the
// host workspace's real .beads/ one level up. A walk that lives in a DIFFERENT
// subtree (not under the ceiling) is unaffected — it never equals the ceiling nor
// a strict ancestor of it until it reaches the shared filesystem root, where the
// caller loops already terminate — so unrelated tests keep their normal boundary.
func WalkCeilingReached(dir string) bool {
	ceiling := walkCeiling()
	if ceiling == "" {
		return false
	}
	d := canonicalizeForCompare(dir)
	if d == ceiling {
		return true // at the ceiling itself — off-limits
	}
	// Stop when dir is a strict ANCESTOR of the ceiling (would step above it).
	// Paths that are descendants of the ceiling, or that lie in an unrelated
	// subtree, are NOT ancestors of the ceiling and keep walking.
	return isStrictDescendant(ceiling, d)
}

// isStrictDescendant reports whether child is a proper descendant of parent
// (child != parent and child is under parent), using path-segment comparison so
// "/a/bc" is NOT treated as under "/a/b".
func isStrictDescendant(child, parent string) bool {
	if child == parent {
		return false
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// canonicalizeForCompare resolves symlinks and cleans a path for robust equality
// comparison, falling back to the abs/clean form when resolution fails (e.g. the
// path does not exist). Stdlib-only to keep internal/debug a dependency-free leaf.
func canonicalizeForCompare(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(abs)
}
