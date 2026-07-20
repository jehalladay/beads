package pidfile

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/steveyegge/beads/internal/atomicfile"
)

// sweepAge is the age threshold for reaping crash-orphaned atomicfile temps
// from the pidfile directory. It is deliberately far longer than any real
// pidfile write so a concurrent in-flight write (a fresh temp) is never
// mistaken for an orphan — the same safety property as beads-qoda's SweepStale.
const sweepAge = 24 * time.Hour

type PidFile struct {
	Pid        int    `json:"pid"`
	Port       int    `json:"port"`
	UpstreamID string `json:"upstream_id,omitempty"`
}

func Path(rootDir, name string) string {
	return filepath.Join(rootDir, name)
}

func Read(rootDir, name string) (*PidFile, error) {
	data, err := os.ReadFile(Path(rootDir, name))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var pf PidFile
	if err := json.Unmarshal(data, &pf); err != nil {
		return nil, err
	}
	return &pf, nil
}

func Write(rootDir, name string, pf PidFile) error {
	data, err := json.Marshal(pf)
	if err != nil {
		return err
	}
	// Opportunistically reap crash-orphaned atomicfile temps that a prior proxy
	// left in rootDir when SIGKILLed/OOMed between Create and rename (beads-9o6s,
	// follow-on to beads-qoda). Best-effort and age-gated: a sweep failure must
	// never fail a pidfile write, and a fresh concurrent temp is never touched.
	_, _ = atomicfile.SweepStale(rootDir, sweepAge)

	return atomicfile.WriteFile(Path(rootDir, name), data, 0o644)
}

func Remove(rootDir, name string) error {
	err := os.Remove(Path(rootDir, name))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
