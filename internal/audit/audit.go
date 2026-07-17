package audit

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/steveyegge/beads/internal/beads"
	"github.com/steveyegge/beads/internal/lockfile"
)

const (
	// FileName is the audit log file name stored under .beads/.
	FileName = "interactions.jsonl"
	idPrefix = "int-"
)

var ensureFileBeforeCreateHook func(string)

// lockHeldHook, when set (tests only), is invoked inside Append after the
// advisory lock attempt and before the write, with the audit file path and
// whether the lock was acquired. It lets a test verify the lock is genuinely
// held around the write (beads-sf6p) — the deterministic guarantee on a local
// fs, where the Lustre cross-client tear the flock guards against cannot be
// reproduced.
var lockHeldHook func(path string, locked bool)

// randRead is the entropy source for newID, injectable in tests to exercise the
// (practically unreachable) read-failure branch.
var randRead = rand.Read

// Entry is a generic append-only audit event. It is intentionally flexible:
// use Kind + typed fields for common cases, and Extra for everything else.
type Entry struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"created_at"`

	// Common metadata
	Actor   string `json:"actor,omitempty"`
	IssueID string `json:"issue_id,omitempty"`

	// LLM call
	Model    string `json:"model,omitempty"`
	Prompt   string `json:"prompt,omitempty"`
	Response string `json:"response,omitempty"`
	Error    string `json:"error,omitempty"`

	// Tool call
	ToolName string `json:"tool_name,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`

	// Labeling (append-only)
	ParentID string `json:"parent_id,omitempty"`
	Label    string `json:"label,omitempty"`  // "good" | "bad" | etc
	Reason   string `json:"reason,omitempty"` // human / pipeline explanation

	Extra map[string]any `json:"extra,omitempty"`
}

func Path() (string, error) {
	beadsDir := beads.FindBeadsDir()
	if beadsDir == "" {
		return "", fmt.Errorf("no .beads directory found")
	}
	return filepath.Join(beadsDir, FileName), nil
}

// EnsureFile creates .beads/interactions.jsonl if it does not exist.
func EnsureFile() (string, error) {
	p, err := Path()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return "", fmt.Errorf("failed to create .beads directory: %w", err)
	}
	if ensureFileBeforeCreateHook != nil {
		ensureFileBeforeCreateHook(p)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644) // nolint:gosec // JSONL is intended to be shared via git across clones/tools.
	if err == nil {
		if closeErr := f.Close(); closeErr != nil {
			return "", fmt.Errorf("failed to close interactions log: %w", closeErr)
		}
		return p, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("failed to create interactions log: %w", err)
	}
	return p, nil
}

// Append appends an event to .beads/interactions.jsonl as a single JSON line.
// This is intentionally append-only: callers must not mutate existing lines.
func Append(e *Entry) (string, error) {
	if e == nil {
		return "", fmt.Errorf("nil entry")
	}
	if e.Kind == "" {
		return "", fmt.Errorf("kind is required")
	}

	p, err := EnsureFile()
	if err != nil {
		return "", err
	}

	if e.ID == "" {
		e.ID, err = newID()
		if err != nil {
			return "", err
		}
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	} else {
		e.CreatedAt = e.CreatedAt.UTC()
	}

	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644) // nolint:gosec // intended permissions
	if err != nil {
		return "", fmt.Errorf("failed to open interactions log: %w", err)
	}
	defer func() { _ = f.Close() }() // Best effort: file close in defer after flush

	// Serialize concurrent appends with an exclusive advisory lock. A single
	// f.Write under O_APPEND is atomic on a LOCAL filesystem (the kernel holds
	// the inode lock regardless of size), but the audit log lives in .beads/
	// which for crew workspaces is on Lustre (/fsx) — and via .beads/redirect
	// it is a SHARED file written by many concurrent bd processes. Lustre does
	// NOT guarantee O_APPEND write atomicity for concurrent CROSS-CLIENT writers
	// of large payloads (an LLM prompt/response, or a large description/notes
	// field-change) spanning stripe boundaries, so a torn interleave could
	// corrupt the JSONL — and this log is the GC-flatten-SURVIVING history of
	// record, so a torn line is unrecoverable history loss, not a rebuildable
	// cache (beads-sf6p). The flock makes the marshal-once + single-write
	// critical section mutually exclusive across processes; the /fsx mount is
	// `flock` (cluster-coherent), not `localflock`, so it holds cross-client.
	// Best-effort: if the lock can't be taken we still write (audit logging
	// must never block or fail an operation).
	locked := lockfile.FlockExclusiveBlocking(f) == nil
	if locked {
		defer func() { _ = lockfile.FlockUnlock(f) }()
	}
	// Test seam: fires while the exclusive lock is held (or after a failed
	// acquire), before the write, so a test can assert the lock is actually
	// engaged around the write on a local fs — the structural guarantee that
	// stands in for the un-reproducible-locally Lustre cross-client tear.
	if lockHeldHook != nil {
		lockHeldHook(p, locked)
	}

	// Marshal to a single byte slice and write atomically.
	// Using bufio.NewWriter could split into multiple write() syscalls,
	// which interleave under concurrent O_APPEND and corrupt lines.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(e); err != nil {
		return "", fmt.Errorf("failed to marshal interactions log entry: %w", err)
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		return "", fmt.Errorf("failed to write interactions log entry: %w", err)
	}

	return e.ID, nil
}

// LogFieldChange logs a field change (status, assignee, priority, etc.) to the
// audit log. This survives Dolt GC flatten, which destroys commit history.
// Best-effort: errors are silently ignored so audit logging never blocks operations.
// Optional reason is included when non-empty (e.g., close reason, cleanup rule).
func LogFieldChange(issueID, field, oldValue, newValue, actor, reason string) {
	if oldValue == newValue {
		return
	}
	extra := map[string]any{
		"field":     field,
		"old_value": oldValue,
		"new_value": newValue,
	}
	if reason != "" {
		extra["reason"] = reason
	}
	_, _ = Append(&Entry{
		Kind:    "field_change",
		IssueID: issueID,
		Actor:   actor,
		Extra:   extra,
	})
}

func newID() (string, error) {
	// 16 bytes (128-bit) of entropy — birthday probability for 8000 IDs is ~9e-32.
	var b [16]byte
	if _, err := randRead(b[:]); err != nil {
		return "", fmt.Errorf("failed to generate id: %w", err)
	}
	return idPrefix + hex.EncodeToString(b[:]), nil
}
