package atomicfile

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// tempPrefix is the marker atomicfile.Create prepends to every temp file
// (os.CreateTemp(dir, ".~"+base+".")). SweepStale matches on it so it never
// touches a file this package did not create.
const tempPrefix = ".~"

// SweepStale removes orphaned atomicfile temp files from dir. A temp is
// orphaned when a process crashes (SIGKILL/OOM/panic-without-recover) between
// Create and Close/Abort: the ordinary error paths already remove the temp, so
// only a crash leaves a ".~<base>.<rand>" file behind (beads-qoda). Nothing else
// sweeps them, so they accumulate in the target directory indefinitely.
//
// Only regular files whose name starts with tempPrefix AND whose mtime is older
// than olderThan are removed. The age gate is load-bearing: a temp from a
// CONCURRENT live write has a recent mtime, so removing it would corrupt an
// in-flight atomic write — SweepStale must never touch a fresh temp. Callers
// should pass a threshold comfortably larger than the longest expected write
// (e.g. an hour) so a slow-but-live write is never mistaken for an orphan.
//
// Directories are skipped (a subdir matching the prefix is left untouched), as
// are non-temp files. A missing dir is a benign no-op. Individual remove errors
// (e.g. a racing writer removed the temp first) are ignored so one unremovable
// entry does not abort the sweep; SweepStale returns the count actually removed.
func SweepStale(dir string, olderThan time.Duration) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	cutoff := time.Now().Add(-olderThan)
	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasPrefix(e.Name(), tempPrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			// The entry vanished (a racing writer/sweep) — nothing to do.
			continue
		}
		if info.ModTime().After(cutoff) {
			// Fresh: possibly an in-flight write. Leave it.
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err == nil {
			removed++
		}
	}
	return removed, nil
}
