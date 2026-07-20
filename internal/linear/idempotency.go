package linear

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const idempotencyPrefix = "<!-- bd-idempotency: "
const idempotencySuffix = " -->"

// GenerateIdempotencyMarker produces a deterministic HTML comment marker for
// embedding in Linear issue descriptions. The marker enables dedup queries
// after sync interruptions: if the marker already exists in Linear, we skip
// creation and return the existing issue.
//
// Hash inputs are intentionally limited to immutable fields (beadID,
// creatorEmail, createdAtNano). Title is excluded because it can change
// after creation, which would break dedup on rename.
func GenerateIdempotencyMarker(beadID, creatorEmail string, createdAtNano int64) string {
	h := sha256.New()
	h.Write([]byte(beadID))
	h.Write([]byte(creatorEmail))
	h.Write([]byte(fmt.Sprintf("%d", createdAtNano)))
	hash := hex.EncodeToString(h.Sum(nil))[:12]
	return idempotencyPrefix + hash + idempotencySuffix
}

// AppendIdempotencyMarker appends a marker to a description, separated by a
// newline. If the description is empty, the marker becomes the entire body.
func AppendIdempotencyMarker(description, marker string) string {
	if description == "" {
		return marker
	}
	return description + "\n" + marker
}

// StripIdempotencyMarker removes the bd-idempotency HTML-comment marker (and the
// single newline separator that AppendIdempotencyMarker inserts before it when
// the body is non-empty) from a description, returning the original body. It is
// the exact inverse of AppendIdempotencyMarker. A description with no marker is
// returned unchanged.
//
// This must be applied on import (so the marker never leaks into the beads
// description) and in the push change-detection comparators (so a
// beads-originated issue whose remote description carries the marker still
// compares equal to the unchanged local issue and is not re-pushed every sync).
// beads-5ahf.
func StripIdempotencyMarker(description string) string {
	idx := strings.Index(description, idempotencyPrefix)
	if idx < 0 {
		return description
	}
	end := strings.Index(description[idx:], idempotencySuffix)
	if end < 0 {
		return description
	}
	markerEnd := idx + end + len(idempotencySuffix)
	before := description[:idx]
	after := description[markerEnd:]
	// Drop the single newline separator AppendIdempotencyMarker inserts before
	// the marker (marker appended to a non-empty body), so the body round-trips
	// exactly. Guarded because the marker can also be the whole body.
	if strings.HasSuffix(before, "\n") {
		before = before[:len(before)-1]
	}
	return before + after
}
