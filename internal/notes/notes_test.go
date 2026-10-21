package notes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"Fix: crash":       "Fix crash",
		"a/b":              "ab",
		"x [y] #z":         "x y z",
		"Fix:  crash  ":    "Fix crash",
		"Добавить preview": "Добавить preview",
	}
	for in, want := range cases {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}

	long := strings.Repeat("x", 150)
	got := Sanitize(long)
	if n := len([]rune(got)); n != 100 {
		t.Errorf("Sanitize(150 x's): len = %d, want 100", n)
	}
	if got != strings.Repeat("x", 100) {
		t.Errorf("Sanitize(150 x's) = %q, want 100 x's", got)
	}
}

func TestCreateUsesTemplate(t *testing.T) {
	dir := t.TempDir()
	tasksDir := filepath.Join(dir, "Tasks")
	s := NewStore(dir, tasksDir)
	idx := &Index{byID: map[int]string{}}

	path, err := s.Create(idx, 73, "Creative Preview")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	wantPath := filepath.Join(tasksDir, "73 Creative Preview.md")
	if path != wantPath {
		t.Errorf("Create path = %q, want %q", path, wantPath)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading created note: %v", err)
	}
	want := "---\nissue: 73\n---\n\n# Creative Preview\n\n## Context\n\n## Implementation plan\n\n## Notes\n"
	if string(data) != want {
		t.Errorf("content = %q, want %q", string(data), want)
	}
}

func TestCreateDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	tasksDir := filepath.Join(dir, "Tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	s := NewStore(dir, tasksDir)

	existingPath := filepath.Join(tasksDir, "73 Old Name.md")
	existingContent := "---\nissue: 73\n---\n\n# Old Name\n\n## Context\n\n## Implementation plan\n\n## Notes\n"
	if err := os.WriteFile(existingPath, []byte(existingContent), 0o644); err != nil {
		t.Fatalf("seeding existing note: %v", err)
	}

	idx, err := s.LoadIndex()
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}

	path, err := s.Create(idx, 73, "New Name")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if path != existingPath {
		t.Errorf("Create path = %q, want existing path %q", path, existingPath)
	}

	data, err := os.ReadFile(existingPath)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if string(data) != existingContent {
		t.Errorf("existing note content changed: got %q, want %q", string(data), existingContent)
	}

	// The new-name file must not have been created either.
	if _, err := os.Stat(filepath.Join(tasksDir, "73 New Name.md")); !os.IsNotExist(err) {
		t.Errorf("expected no file for the new name, stat err = %v", err)
	}
}

func TestResolveOrder(t *testing.T) {
	dir := t.TempDir()
	tasksDir := filepath.Join(dir, "Tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	s := NewStore(dir, tasksDir)

	// frontmatter note for issue 42
	frontmatterPath := filepath.Join(tasksDir, "frontmatter-42.md")
	if err := os.WriteFile(frontmatterPath, []byte("---\nissue: 42\n---\n\n# Frontmatter\n"), 0o644); err != nil {
		t.Fatalf("seeding frontmatter note: %v", err)
	}

	// filename-prefix note, also claiming id 42 (lower priority than frontmatter)
	prefixPath := filepath.Join(tasksDir, "42 Prefix Only.md")
	if err := os.WriteFile(prefixPath, []byte("# Prefix Only\n"), 0o644); err != nil {
		t.Fatalf("seeding prefix note: %v", err)
	}

	idx, err := s.LoadIndex()
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}

	// With no link, frontmatter wins over filename-prefix.
	path, ok := s.Resolve(idx, 42, "")
	if !ok {
		t.Fatalf("Resolve: want ok=true")
	}
	if path != frontmatterPath {
		t.Errorf("Resolve path = %q, want frontmatter path %q", path, frontmatterPath)
	}

	// A link to an existing file wins over the index entirely.
	rel, err := filepath.Rel(dir, prefixPath)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}
	link := strings.TrimSuffix(filepath.ToSlash(rel), ".md")
	path, ok = s.Resolve(idx, 42, link)
	if !ok {
		t.Fatalf("Resolve with link: want ok=true")
	}
	if path != prefixPath {
		t.Errorf("Resolve with link path = %q, want %q", path, prefixPath)
	}

	// A link to a non-existent file falls back to the index.
	path, ok = s.Resolve(idx, 42, "Tasks/does-not-exist")
	if !ok {
		t.Fatalf("Resolve with missing link: want ok=true (fallback)")
	}
	if path != frontmatterPath {
		t.Errorf("Resolve with missing link path = %q, want fallback to frontmatter path %q", path, frontmatterPath)
	}

	// No entry at all.
	if _, ok := s.Resolve(idx, 999, ""); ok {
		t.Errorf("Resolve(999): want ok=false")
	}
}

func TestRenameUpdatesH1(t *testing.T) {
	dir := t.TempDir()
	tasksDir := filepath.Join(dir, "Tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	s := NewStore(dir, tasksDir)

	oldPath := filepath.Join(tasksDir, "42 Old.md")
	content := "---\nissue: 42\n---\n\n# Old\n\n## Context\n\nsome notes\n\n## Implementation plan\n\n## Notes\n"
	if err := os.WriteFile(oldPath, []byte(content), 0o644); err != nil {
		t.Fatalf("seeding note: %v", err)
	}

	idx := &Index{byID: map[int]string{42: oldPath}}
	newPath, err := s.Rename(idx, oldPath, 42, "Old", "New")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}

	wantPath := filepath.Join(tasksDir, "42 New.md")
	if newPath != wantPath {
		t.Errorf("Rename path = %q, want %q", newPath, wantPath)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Errorf("old file still exists, stat err = %v", err)
	}

	data, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("reading renamed note: %v", err)
	}
	want := "---\nissue: 42\n---\n\n# New\n\n## Context\n\nsome notes\n\n## Implementation plan\n\n## Notes\n"
	if string(data) != want {
		t.Errorf("content = %q, want %q", string(data), want)
	}

	if idx.byID[42] != newPath {
		t.Errorf("idx[42] = %q, want %q", idx.byID[42], newPath)
	}
}

func TestRenameKeepsCustomH1(t *testing.T) {
	dir := t.TempDir()
	tasksDir := filepath.Join(dir, "Tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	s := NewStore(dir, tasksDir)

	oldPath := filepath.Join(tasksDir, "42 Old.md")
	content := "---\nissue: 42\n---\n\n# My heading\n\n## Context\n\n## Implementation plan\n\n## Notes\n"
	if err := os.WriteFile(oldPath, []byte(content), 0o644); err != nil {
		t.Fatalf("seeding note: %v", err)
	}

	idx := &Index{byID: map[int]string{42: oldPath}}
	newPath, err := s.Rename(idx, oldPath, 42, "Old", "New")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}

	data, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("reading renamed note: %v", err)
	}
	want := "---\nissue: 42\n---\n\n# My heading\n\n## Context\n\n## Implementation plan\n\n## Notes\n"
	if string(data) != want {
		t.Errorf("content = %q, want %q (H1 should be untouched)", string(data), want)
	}
}

func TestRenameSameFilenameNoRename(t *testing.T) {
	dir := t.TempDir()
	tasksDir := filepath.Join(dir, "Tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	s := NewStore(dir, tasksDir)

	// "Old: Title" and "Old Title" sanitize identically.
	oldPath := filepath.Join(tasksDir, "42 Old Title.md")
	content := "---\nissue: 42\n---\n\n# Old: Title\n\n## Context\n\n## Implementation plan\n\n## Notes\n"
	if err := os.WriteFile(oldPath, []byte(content), 0o644); err != nil {
		t.Fatalf("seeding note: %v", err)
	}

	idx := &Index{byID: map[int]string{42: oldPath}}
	newPath, err := s.Rename(idx, oldPath, 42, "Old: Title", "Old Title")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if newPath != oldPath {
		t.Errorf("Rename path = %q, want unchanged %q", newPath, oldPath)
	}

	data, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatalf("reading note: %v", err)
	}
	want := "---\nissue: 42\n---\n\n# Old Title\n\n## Context\n\n## Implementation plan\n\n## Notes\n"
	if string(data) != want {
		t.Errorf("content = %q, want %q", string(data), want)
	}
}

func TestRenameRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	tasksDir := filepath.Join(dir, "Tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	s := NewStore(dir, tasksDir)

	oldPath := filepath.Join(tasksDir, "42 Old.md")
	if err := os.WriteFile(oldPath, []byte("---\nissue: 42\n---\n\n# Old\n"), 0o644); err != nil {
		t.Fatalf("seeding note: %v", err)
	}
	collidePath := filepath.Join(tasksDir, "42 New.md")
	collideContent := "---\nissue: 99\n---\n\n# Something else\n"
	if err := os.WriteFile(collidePath, []byte(collideContent), 0o644); err != nil {
		t.Fatalf("seeding colliding note: %v", err)
	}

	idx := &Index{byID: map[int]string{42: oldPath, 99: collidePath}}
	if _, err := s.Rename(idx, oldPath, 42, "Old", "New"); err == nil {
		t.Fatal("Rename: want error when target already exists, got nil")
	}

	if _, err := os.Stat(oldPath); err != nil {
		t.Errorf("old file should still exist: %v", err)
	}
	data, err := os.ReadFile(collidePath)
	if err != nil {
		t.Fatalf("reading colliding file: %v", err)
	}
	if string(data) != collideContent {
		t.Errorf("colliding file content changed: got %q, want %q", string(data), collideContent)
	}
}

func TestLinkTarget(t *testing.T) {
	dir := t.TempDir()
	tasksDir := filepath.Join(dir, "Tasks")
	s := NewStore(dir, tasksDir)

	path := filepath.Join(dir, "Tasks", "42 X.md")
	got := s.LinkTarget(path)
	want := "Tasks/42 X"
	if got != want {
		t.Errorf("LinkTarget(%q) = %q, want %q", path, got, want)
	}
}

func TestLoadIndexMissingTasksDir(t *testing.T) {
	dir := t.TempDir()
	tasksDir := filepath.Join(dir, "DoesNotExist")
	s := NewStore(dir, tasksDir)

	idx, err := s.LoadIndex()
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if len(idx.byID) != 0 {
		t.Errorf("idx.byID = %v, want empty", idx.byID)
	}
}

func TestLoadIndexRecursive(t *testing.T) {
	dir := t.TempDir()
	tasksDir := filepath.Join(dir, "Tasks")
	subDir := filepath.Join(tasksDir, "Sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	s := NewStore(dir, tasksDir)

	nested := filepath.Join(subDir, "7 Nested.md")
	if err := os.WriteFile(nested, []byte("# Nested\n"), 0o644); err != nil {
		t.Fatalf("seeding nested note: %v", err)
	}
	// non-markdown file should be ignored
	if err := os.WriteFile(filepath.Join(tasksDir, "notes.txt"), []byte("ignore me"), 0o644); err != nil {
		t.Fatalf("seeding non-md file: %v", err)
	}

	idx, err := s.LoadIndex()
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if idx.byID[7] != nested {
		t.Errorf("idx.byID[7] = %q, want %q", idx.byID[7], nested)
	}
}

// TestReplaceH1SanitizedAndCRLF covers final review M5: the H1 is
// matched against the old title through Sanitize (the old title often
// comes from a lossy filename: "Fix crash" for "# Fix: crash"), and CRLF
// notes are handled, keeping their line endings.
func TestReplaceH1SanitizedAndCRLF(t *testing.T) {
	cases := []struct {
		name, content, old, new, want string
	}{
		{
			"lossy old title",
			"---\nissue: 31\n---\n\n# Fix: crash\n\nbody\n", "Fix crash", "Fix: crash on start",
			"---\nissue: 31\n---\n\n# Fix: crash on start\n\nbody\n",
		},
		{
			"CRLF note",
			"---\r\nissue: 42\r\n---\r\n\r\n# Old\r\n\r\nbody\r\n", "Old", "New",
			"---\r\nissue: 42\r\n---\r\n\r\n# New\r\n\r\nbody\r\n",
		},
		{
			"custom H1 kept",
			"# Something else\n", "Old", "New",
			"# Something else\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := replaceH1(c.content, c.old, c.new); got != c.want {
				t.Errorf("replaceH1() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestRenameCaseOnlyOnCaseInsensitiveFS covers final review I4. On a
// case-insensitive filesystem "42 foo.md" and "42 Foo.md" are the same
// file, so Stat(newPath) succeeds. That is simulated here with a hard
// link (os.SameFile reports true for both names), which must not be
// mistaken for a different note squatting the target path.
func TestRenameCaseOnlyOnCaseInsensitiveFS(t *testing.T) {
	dir := t.TempDir()
	tasksDir := filepath.Join(dir, "Tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewStore(dir, tasksDir)

	oldPath := filepath.Join(tasksDir, "42 foo.md")
	if err := os.WriteFile(oldPath, []byte("---\nissue: 42\n---\n\n# foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(tasksDir, "42 Foo.md")
	if err := os.Link(oldPath, newPath); err != nil {
		t.Skipf("hard links unsupported: %v", err)
	}

	idx := &Index{byID: map[int]string{42: oldPath}}
	got, err := s.Rename(idx, oldPath, 42, "foo", "Foo")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if got != newPath {
		t.Errorf("Rename path = %q, want %q", got, newPath)
	}
	data, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := "---\nissue: 42\n---\n\n# Foo\n"; string(data) != want {
		t.Errorf("content = %q, want %q", data, want)
	}
	if idx.byID[42] != newPath {
		t.Errorf("idx[42] = %q, want %q", idx.byID[42], newPath)
	}
}
