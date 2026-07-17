//go:build !unix

package atomicfile

// syncDir is a no-op on non-Unix platforms. Windows does not support fsync on a
// directory handle (an attempt returns ERROR_ACCESS_DENIED / "Incorrect
// function"), and its rename durability model differs; wasm has no meaningful
// filesystem to sync. The temp-file fsync in Close still runs on all platforms;
// only the extra directory-entry durability step is Unix-specific.
func syncDir(_ string) error { return nil }
