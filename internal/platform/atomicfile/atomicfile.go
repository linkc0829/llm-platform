// Package atomicfile writes files through a same-directory temporary file and
// rename, so readers see either the old complete file or the new complete file.
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var renameBackoff = []time.Duration{
	20 * time.Millisecond,
	50 * time.Millisecond,
	100 * time.Millisecond,
	250 * time.Millisecond,
}

// Replace writes data to path with perm. The temporary file is synced before
// it is renamed into place, so readers see the new complete file after a
// successful return. It does not fsync the parent directory; crash durability
// of the rename therefore depends on the platform and filesystem. Callers can
// safely publish in-memory state only after Replace returns nil.
func Replace(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create atomic file directory: %w", err)
	}

	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create atomic file temporary file: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()

	if err := temp.Chmod(perm); err != nil {
		_ = temp.Close()
		return fmt.Errorf("set atomic file permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write atomic file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync atomic file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close atomic file: %w", err)
	}
	if err := RenameRetry(tempPath, path); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}
	return nil
}

// RenameRetry retries a rename briefly for transient Windows file-handle
// contention (for example, Defender scanning a freshly written file).
func RenameRetry(oldPath, newPath string) error {
	err := os.Rename(oldPath, newPath)
	for _, delay := range renameBackoff {
		if err == nil {
			return nil
		}
		time.Sleep(delay)
		err = os.Rename(oldPath, newPath)
	}
	return err
}
