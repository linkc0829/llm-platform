// Package lockfile provides a small cross-process exclusive lock based on a
// lock file. It deliberately does not guess whether an existing lock is stale.
package lockfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// ErrLocked indicates that another process already owns the lock file.
var ErrLocked = errors.New("lock file already exists")

// Lock is an acquired exclusive lock file.
type Lock struct {
	path string
	file *os.File
	once sync.Once
	err  error
}

// Path returns the lock path used for a data file.
func Path(dataPath string) string {
	return dataPath + ".lock"
}

// Acquire creates path exclusively. Existing locks are never removed
// automatically because the holder may still be writing the data file.
func Acquire(path string) (*Lock, error) {
	if path == "" {
		return nil, errors.New("lock file path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create lock file directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("%w: %s; if no process is using it, remove this stale lock file manually", ErrLocked, path)
		}
		return nil, fmt.Errorf("create lock file: %w", err)
	}
	if _, err := file.WriteString(strconv.Itoa(os.Getpid()) + "\n"); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("write lock file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("sync lock file: %w", err)
	}
	return &Lock{path: path, file: file}, nil
}

// Release closes and removes the lock. It is safe to call more than once.
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		closeErr := l.file.Close()
		removeErr := os.Remove(l.path)
		l.err = errors.Join(closeErr, removeErr)
	})
	return l.err
}
