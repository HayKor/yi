package kanban

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrModified indicates that the file WriteFileAtomic was about to
// write to had changed on disk since it was last read: someone else
// (the user, Obsidian, another yi process) edited Kanban.md in the
// meantime, and writing would have clobbered that edit.
var ErrModified = errors.New("file was modified")

// ReadFile reads path and returns its contents along with the SHA-256
// hash of those contents. Pass the hash to WriteFileAtomic later to
// detect a concurrent edit before overwriting the file.
func ReadFile(path string) ([]byte, [32]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, [32]byte{}, err
	}
	return data, sha256.Sum256(data), nil
}

// WriteFileAtomic writes data to path without ever leaving a partial
// or corrupted file in its place: it writes to a temp file in path's
// own directory, carries over path's file mode, and only renames the
// temp file over path once it's fully and durably written.
//
// It refuses to clobber a concurrent edit: expect must be the hash
// ReadFile returned when path was last read, and WriteFileAtomic
// re-hashes path immediately before renaming. If the hash has
// changed, it removes the temp file and returns an error wrapping
// ErrModified, leaving path untouched.
//
// path must already exist; yi never creates Kanban.md, so a missing
// path is an error. If path is a symlink, the file it points to is
// replaced and the link itself is left in place.
func WriteFileAtomic(path string, data []byte, expect [32]byte) error {
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".kanban-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Chmod(info.Mode()); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}

	current, err := os.ReadFile(path)
	if err != nil {
		os.Remove(tmpPath)
		return err
	}
	if sha256.Sum256(current) != expect {
		os.Remove(tmpPath)
		return fmt.Errorf("%s: %w", path, ErrModified)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}
