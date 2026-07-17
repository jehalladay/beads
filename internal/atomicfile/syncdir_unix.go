//go:build unix

package atomicfile

import "os"

// syncDir fsyncs the directory dir so that a rename (or create/remove) of an
// entry within it is durable across a crash/power-loss. On Unix a successful
// os.Rename only guarantees the new name is visible to concurrent readers; the
// directory entry itself is not persisted until the directory's own fsync
// completes. Without this, a crash immediately after rename can leave the
// target missing (neither old nor new file), violating atomicfile's
// crash-durability contract (ext4/xfs).
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return err
	}
	return d.Close()
}
