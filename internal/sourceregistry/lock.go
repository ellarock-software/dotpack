package sourceregistry

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var errLocked = errors.New("lock held")

// Lock represents an exclusive file lock held by a dotpack process.
type Lock struct {
	file *os.File
	path string
}

// AcquireLock attempts to acquire a non-blocking exclusive lock in the DotpackHome directory.
// If another process is holding the lock, it returns an error indicating concurrency.
func AcquireLock(dotpackHome string) (*Lock, error) {
	if dotpackHome == "" {
		return nil, fmt.Errorf("dotpack home directory is unavailable")
	}
	if err := os.MkdirAll(dotpackHome, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir dotpack home %s: %w", dotpackHome, err)
	}
	lockPath := filepath.Join(dotpackHome, ".lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lockfile %s: %w", lockPath, err)
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		if errors.Is(err, errLocked) {
			return nil, fmt.Errorf("another dotpack process is currently modifying state (lock held at %s)", lockPath)
		}
		return nil, fmt.Errorf("acquire lock on %s: %w", lockPath, err)
	}
	return &Lock{file: f, path: lockPath}, nil
}

// Release releases the lock and closes the lockfile.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	_ = unlockFile(l.file)
	err := l.file.Close()
	l.file = nil
	return err
}
