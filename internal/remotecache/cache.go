package remotecache

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/debug"
	"github.com/steveyegge/beads/internal/lockfile"
	"github.com/steveyegge/beads/internal/storage"
)

// staleLockAge is the maximum age of a lock file before it's considered stale.
const staleLockAge = 5 * time.Minute

// StoreOpener is a function that opens a DoltStorage from a beads directory.
// This is injected by the cmd layer to abstract over build-tag-specific
// store construction (embedded vs server).
type StoreOpener func(ctx context.Context, beadsDir string) (storage.DoltStorage, error)

// Cache manages local clones of remote Dolt databases.
// Each remote URL maps to a directory under Dir named by CacheKey(url).
type Cache struct {
	Dir      string        // e.g., ~/.cache/beads/remotes
	FreshFor time.Duration // skip pull if last pull was within this duration; 0 means always pull
}

// CacheMeta stores metadata about a cached remote clone.
type CacheMeta struct {
	RemoteURL string `json:"remote_url"`
	LastPull  int64  `json:"last_pull_ns"`
	LastPush  int64  `json:"last_push_ns"`
}

// defaultFreshFor is the default TTL for cached clones. Ensure() skips
// pulling when the last pull was within this duration.
const defaultFreshFor = 30 * time.Second

// DefaultCache returns a Cache using the XDG-conventional cache directory.
func DefaultCache() (*Cache, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("failed to determine cache directory: %w", err)
	}
	dir := filepath.Join(cacheDir, "beads", "remotes")
	return &Cache{Dir: dir, FreshFor: defaultFreshFor}, nil
}

// entryDir returns the cache entry directory for a remote URL.
func (c *Cache) entryDir(remoteURL string) string {
	return filepath.Join(c.Dir, CacheKey(remoteURL))
}

// cloneTarget returns the dolt database directory within a cache entry.
// dolt clone creates <target>/.dolt/ directly, so the target is named
// after the database (default "beads") to match the embedded driver layout.
func (c *Cache) cloneTarget(remoteURL string) string {
	return filepath.Join(c.entryDir(remoteURL), configfile.DefaultDoltDatabase)
}

// metaPath returns the path to the metadata file for a cache entry.
func (c *Cache) metaPath(remoteURL string) string {
	return filepath.Join(c.entryDir(remoteURL), ".meta.json")
}

// lockPath returns the path to the lock file for a cache entry.
func (c *Cache) lockPath(remoteURL string) string {
	return filepath.Join(c.entryDir(remoteURL), ".lock")
}

// Ensure clones the remote if not cached (cold start), or pulls if already
// cached (warm start). Returns the cache entry directory path.
//
// Auth credentials are inherited from environment variables:
// DOLT_REMOTE_USER, DOLT_REMOTE_PASSWORD, or DoltHub credentials
// configured via `dolt creds`.
func (c *Cache) Ensure(ctx context.Context, remoteURL string) (string, error) {
	if err := ValidateRemoteURL(remoteURL); err != nil {
		return "", fmt.Errorf("invalid remote URL: %w", err)
	}
	if _, err := exec.LookPath("dolt"); err != nil {
		return "", fmt.Errorf("dolt CLI not found (required for remote cache): %w", err)
	}

	entry := c.entryDir(remoteURL)
	if err := os.MkdirAll(entry, 0o750); err != nil {
		return "", fmt.Errorf("failed to create cache entry dir: %w", err)
	}

	// Acquire exclusive lock for clone/pull
	lock, err := c.acquireLock(ctx, remoteURL)
	if err != nil {
		return "", fmt.Errorf("failed to acquire cache lock: %w", err)
	}
	defer c.releaseLock(lock)

	target := c.cloneTarget(remoteURL)
	if c.doltExists(target) {
		// Warm start: skip pull if the cache is still fresh
		if c.FreshFor > 0 {
			meta := c.readMeta(remoteURL)
			age := time.Since(time.Unix(0, meta.LastPull))
			if age < c.FreshFor {
				debug.Logf("remotecache: skipping pull for %s (%.1fs old, fresh for %.0fs)\n",
					remoteURL, age.Seconds(), c.FreshFor.Seconds())
				return entry, nil
			}
		}
		if err := c.doltPull(ctx, target); err != nil {
			// Belt-and-suspenders: a clone corrupted by other means (e.g. a
			// partial dir from an older bd, or external corruption) makes the
			// warm path fail forever with "not a valid dolt repository". Detect
			// that and self-heal by evicting the corrupt entry and re-cloning
			// cold, instead of returning a permanent error.
			if isInvalidRepoErr(err) {
				debug.Logf("remotecache: corrupt cached clone for %s (%v); re-cloning\n", remoteURL, err)
				// Remove only the corrupt clone dir, not the whole entry: the
				// entry also holds the .lock file we currently flock, and
				// deleting it out from under our fd would break the lock's
				// mutual exclusion for a concurrent process.
				if rmErr := os.RemoveAll(target); rmErr != nil {
					return "", fmt.Errorf("dolt pull failed for %s and removing corrupt clone failed: %v (original: %w)", remoteURL, rmErr, err)
				}
				if clErr := c.doltClone(ctx, remoteURL, target); clErr != nil {
					return "", fmt.Errorf("dolt re-clone failed for %s after corrupt cache: %w", remoteURL, clErr)
				}
			} else {
				return "", fmt.Errorf("dolt pull failed for %s: %w", remoteURL, err)
			}
		}
	} else {
		// Cold start: clone
		if err := c.doltClone(ctx, remoteURL, target); err != nil {
			return "", fmt.Errorf("dolt clone failed for %s: %w", remoteURL, err)
		}
	}

	// Write metadata
	meta := CacheMeta{
		RemoteURL: remoteURL,
		LastPull:  time.Now().UnixNano(),
	}
	c.writeMeta(remoteURL, &meta)

	return entry, nil
}

// Push pushes local commits in the cached clone back to the remote.
func (c *Cache) Push(ctx context.Context, remoteURL string) error {
	target := c.cloneTarget(remoteURL)
	if !c.doltExists(target) {
		return fmt.Errorf("no cached clone for %s", remoteURL)
	}

	lock, err := c.acquireLock(ctx, remoteURL)
	if err != nil {
		return fmt.Errorf("failed to acquire cache lock: %w", err)
	}
	defer c.releaseLock(lock)

	cmd := exec.CommandContext(ctx, "dolt", "push", "origin", "main")
	cmd.Dir = target
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("dolt push failed: %w\nOutput: %s", err, output)
	}

	// Update push timestamp
	meta := c.readMeta(remoteURL)
	meta.LastPush = time.Now().UnixNano()
	c.writeMeta(remoteURL, meta)

	return nil
}

// OpenStore opens a DoltStorage from the cached clone using the provided
// StoreOpener. The cache entry directory is used as the beads directory.
// The caller is responsible for calling Close() on the returned store.
//
// Note: OpenStore does not acquire a cache lock. The caller must ensure
// no concurrent Ensure() or Push() is running against the same remoteURL,
// as those modify the underlying dolt database. This is safe for single-
// process CLI use but not for concurrent multi-process access.
func (c *Cache) OpenStore(ctx context.Context, remoteURL string, opener StoreOpener) (storage.DoltStorage, error) {
	entry := c.entryDir(remoteURL)
	if !c.doltExists(c.cloneTarget(remoteURL)) {
		return nil, fmt.Errorf("no cached clone for %s — run Ensure first", remoteURL)
	}
	return opener(ctx, entry)
}

// Evict removes a cached remote clone entirely.
func (c *Cache) Evict(remoteURL string) error {
	entry := c.entryDir(remoteURL)
	return os.RemoveAll(entry)
}

// doltExists checks if a dolt database exists at the given path.
func (c *Cache) doltExists(dbPath string) bool {
	doltDir := filepath.Join(dbPath, ".dolt")
	info, err := os.Stat(doltDir)
	return err == nil && info.IsDir()
}

// doltClone clones a remote into the target directory.
//
// On failure it removes any partially-created target directory. Without this,
// an interrupted cold clone (cancelled ctx, disk-full, network drop after
// target/.dolt was created) would leave a partial .dolt behind; doltExists()
// trusts that dir forever, so every subsequent Ensure takes the warm (pull)
// path against the corrupt clone and fails permanently — wedging the cache
// until a manual Evict. Cleaning up on error means the next Ensure re-clones
// cold, matching dolt's own behavior for a clone that fails before writing.
func (c *Cache) doltClone(ctx context.Context, remoteURL, target string) (err error) {
	defer func() {
		if err != nil {
			if rmErr := os.RemoveAll(target); rmErr != nil {
				debug.Logf("remotecache: failed to clean up partial clone target %s: %v\n", target, rmErr)
			}
		}
	}()
	cmd := exec.CommandContext(ctx, "dolt", "clone", remoteURL, target)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w\nOutput: %s", err, output)
	}
	return nil
}

// doltPull pulls from origin in the given database directory.
func (c *Cache) doltPull(ctx context.Context, dbDir string) error {
	cmd := exec.CommandContext(ctx, "dolt", "pull", "origin", "main")
	cmd.Dir = dbDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w\nOutput: %s", err, output)
	}
	return nil
}

// isInvalidRepoErr reports whether a dolt error indicates the target directory
// is not a valid dolt repository — the signature of a corrupt/partial clone
// (e.g. an interrupted cold start that left a stub .dolt behind). doltPull's
// error wraps the CLI's combined output, which contains this phrase.
func isInvalidRepoErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "not a valid dolt repository")
}

// acquireLock acquires an exclusive file lock for a cache entry.
func (c *Cache) acquireLock(ctx context.Context, remoteURL string) (*os.File, error) {
	lp := c.lockPath(remoteURL)

	// Clean up stale locks
	if info, err := os.Stat(lp); err == nil {
		if time.Since(info.ModTime()) > staleLockAge {
			_ = os.Remove(lp)
		}
	}

	// #nosec G304 - controlled path
	f, err := os.OpenFile(lp, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}

	// Poll with timeout
	deadline := time.Now().Add(2 * time.Minute)
	for {
		err := lockfile.FlockExclusiveNonBlocking(f)
		if err == nil {
			return f, nil
		}
		if !lockfile.IsLocked(err) {
			_ = f.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("timeout waiting for cache lock on %s", remoteURL)
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, fmt.Errorf("interrupted waiting for cache lock on %s: %w", remoteURL, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// releaseLock releases a cache entry file lock.
// The lock file is intentionally NOT removed: deleting it after unlock creates
// a TOCTOU race where another process's newly-acquired lock gets deleted.
// Stale lock files are cleaned up by acquireLock's age check instead.
func (c *Cache) releaseLock(f *os.File) {
	if f != nil {
		_ = lockfile.FlockUnlock(f)
		_ = f.Close()
	}
}

// readMeta reads the cache metadata for a remote URL.
func (c *Cache) readMeta(remoteURL string) *CacheMeta {
	data, err := os.ReadFile(c.metaPath(remoteURL))
	if err != nil {
		return &CacheMeta{RemoteURL: remoteURL}
	}
	var meta CacheMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return &CacheMeta{RemoteURL: remoteURL}
	}
	return &meta
}

// writeMeta writes cache metadata for a remote URL.
func (c *Cache) writeMeta(remoteURL string, meta *CacheMeta) {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		debug.Logf("remotecache: failed to marshal meta for %s: %v\n", remoteURL, err)
		return
	}
	if err := os.WriteFile(c.metaPath(remoteURL), data, 0o600); err != nil {
		debug.Logf("remotecache: failed to write meta for %s: %v\n", remoteURL, err)
	}
}
