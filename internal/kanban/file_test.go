package kanban

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Kanban.md")
	original := []byte("## Backlog\n\n- [ ] Task\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatalf("seeding file: %v", err)
	}

	_, hash, err := ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	updated := []byte("## Backlog\n\n- [x] Task\n")
	if err := WriteFileAtomic(path, updated, hash); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if string(got) != string(updated) {
		t.Errorf("content = %q, want %q", got, updated)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want %v", info.Mode().Perm(), os.FileMode(0o640))
	}

	// No leftover temp files in the directory.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "Kanban.md" {
			t.Errorf("leftover file in dir: %s", e.Name())
		}
	}
}

func TestWriteFileAtomicDetectsConcurrentModification(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Kanban.md")
	original := []byte("## Backlog\n\n- [ ] Task\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatalf("seeding file: %v", err)
	}

	_, hash, err := ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	othersEdit := []byte("## Backlog\n\n- [ ] Someone else's edit\n")
	if err := os.WriteFile(path, othersEdit, 0o640); err != nil {
		t.Fatalf("simulating concurrent edit: %v", err)
	}

	err = WriteFileAtomic(path, []byte("## Backlog\n\n- [x] Task\n"), hash)
	if err == nil {
		t.Fatal("WriteFileAtomic: want error for a concurrently modified file, got nil")
	}
	if !errors.Is(err, ErrModified) {
		t.Errorf("WriteFileAtomic error = %v, want it to wrap ErrModified", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if string(got) != string(othersEdit) {
		t.Errorf("content = %q, want the other edit %q untouched", got, othersEdit)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "Kanban.md" {
			t.Errorf("leftover temp file in dir: %s", e.Name())
		}
	}
}

func TestWriteFileAtomicMissingTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Kanban.md")

	err := WriteFileAtomic(path, []byte("## Backlog\n"), [32]byte{})
	if err == nil {
		t.Fatal("WriteFileAtomic: want error when the target file doesn't exist, got nil")
	}
}
