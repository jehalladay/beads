package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/rpc"
)

// walkWithDepth walks a directory tree with depth limiting
func walkWithDepth(root string, currentDepth, maxDepth int, fn func(path string, info os.FileInfo) error) error {
	if currentDepth > maxDepth {
		return nil
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		// Skip directories we can't read
		return nil
	}

	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}

		// Skip common directories that won't have beads databases
		if info.IsDir() {
			name := entry.Name()
			if strings.HasPrefix(name, ".") && name != ".beads" {
				continue // Skip hidden dirs except .beads
			}
			if name == "node_modules" || name == "vendor" || name == ".git" {
				continue
			}
			// Recurse into subdirectory
			if err := walkWithDepth(path, currentDepth+1, maxDepth, fn); err != nil {
				return err
			}
		} else {
			// Process file
			if err := fn(path, info); err != nil {
				return err
			}
		}
	}

	return nil
}

// DaemonInfo represents metadata about a discovered daemon
type DaemonInfo struct {
	WorkspacePath       string
	DatabasePath        string
	SocketPath          string
	PID                 int
	Version             string
	UptimeSeconds       float64
	LastActivityTime    string
	ExclusiveLockActive bool
	ExclusiveLockHolder string
	Alive               bool
	Error               string
}

// DiscoverDaemons scans the filesystem for running bd daemons
// It searches common locations and uses the Status RPC endpoint to gather metadata
func DiscoverDaemons(searchRoots []string) ([]DaemonInfo, error) {
	var daemons []DaemonInfo
	seen := make(map[string]bool)

	// If no search roots provided, use common locations
	if len(searchRoots) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		searchRoots = []string{
			home,
			"/tmp",
		}
		// Also add current directory if in a git repo
		if cwd, err := os.Getwd(); err == nil {
			searchRoots = append(searchRoots, cwd)
		}
	}

	// Search for .beads/bd.sock files (limit depth to avoid traversing entire filesystem)
	for _, root := range searchRoots {
		maxDepth := 10 // Limit recursion depth
		if err := walkWithDepth(root, 0, maxDepth, func(path string, info os.FileInfo) error {
			// Skip if not a socket file
			if info.Name() != "bd.sock" {
				return nil
			}

			// Skip if already seen this socket
			if seen[path] {
				return nil
			}
			seen[path] = true

			// Try to connect and get status
			daemon := discoverDaemon(path)
			daemons = append(daemons, daemon)

			return nil
		}); err != nil {
			// Continue searching other roots even if one fails
			continue
		}
	}

	return daemons, nil
}

// discoverDaemon attempts to connect to a daemon socket and retrieve its status
func discoverDaemon(socketPath string) DaemonInfo {
	daemon := DaemonInfo{
		SocketPath: socketPath,
		Alive:      false,
	}

	// Try to connect with short timeout
	client, err := rpc.TryConnectWithTimeout(socketPath, 500*time.Millisecond)
	if err != nil {
		daemon.Error = fmt.Sprintf("failed to connect: %v", err)
		return daemon
	}
	if client == nil {
		daemon.Error = "daemon not responding or unhealthy"
		return daemon
	}
	defer client.Close()

	// Get status
	status, err := client.Status()
	if err != nil {
		daemon.Error = fmt.Sprintf("failed to get status: %v", err)
		return daemon
	}

	// Populate daemon info from status
	daemon.Alive = true
	daemon.WorkspacePath = status.WorkspacePath
	daemon.DatabasePath = status.DatabasePath
	daemon.PID = status.PID
	daemon.Version = status.Version
	daemon.UptimeSeconds = status.UptimeSeconds
	daemon.LastActivityTime = status.LastActivityTime
	daemon.ExclusiveLockActive = status.ExclusiveLockActive
	daemon.ExclusiveLockHolder = status.ExclusiveLockHolder

	return daemon
}

// FindDaemonByWorkspace finds a daemon serving a specific workspace
func FindDaemonByWorkspace(workspacePath string) (*DaemonInfo, error) {
	// First try the socket in the workspace itself
	socketPath := filepath.Join(workspacePath, ".beads", "bd.sock")
	if _, err := os.Stat(socketPath); err == nil {
		daemon := discoverDaemon(socketPath)
		if daemon.Alive {
			return &daemon, nil
		}
	}

	// Fall back to discovering all daemons
	daemons, err := DiscoverDaemons([]string{workspacePath})
	if err != nil {
		return nil, err
	}

	for _, daemon := range daemons {
		if daemon.WorkspacePath == workspacePath && daemon.Alive {
			return &daemon, nil
		}
	}

	return nil, fmt.Errorf("no daemon found for workspace: %s", workspacePath)
}

// CleanupStaleSockets removes socket files for dead daemons
func CleanupStaleSockets(daemons []DaemonInfo) (int, error) {
	cleaned := 0
	for _, daemon := range daemons {
		if !daemon.Alive && daemon.SocketPath != "" {
			if err := os.Remove(daemon.SocketPath); err != nil {
				if !os.IsNotExist(err) {
					return cleaned, fmt.Errorf("failed to remove stale socket %s: %w", daemon.SocketPath, err)
				}
			} else {
				cleaned++
			}
		}
	}
	return cleaned, nil
}
