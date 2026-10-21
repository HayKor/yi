// Package notes locates, creates, and renames task note files: the
// per-issue Markdown files linked from Kanban cards. It has no
// dependency on any other package in this module.
package notes

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Store resolves and manages task notes for one vault.
//
// ProjectDir is the vault root (used to resolve wikilink targets and
// to compute LinkTarget). TasksDir is the directory notes are created
// in and the one LoadIndex scans; it need not be inside ProjectDir,
// though in practice it is.
type Store struct {
	ProjectDir string
	TasksDir   string
}

// NewStore returns a Store for the given project and tasks directories.
func NewStore(projectDir, tasksDir string) *Store {
	return &Store{ProjectDir: projectDir, TasksDir: tasksDir}
}

// Index maps issue IDs to the absolute path of their note, built by
// LoadIndex.
type Index struct {
	byID map[int]string
}

// sanitizeRemove is the set of characters Sanitize strips before
// collapsing whitespace. It mirrors filename characters that are
// unsafe or awkward across common filesystems, plus Markdown/wikilink
// syntax characters.
var sanitizeRemove = map[rune]bool{
	'\\': true, '/': true, ':': true, '*': true, '?': true, '"': true,
	'<': true, '>': true, '|': true, '#': true, '^': true, '[': true, ']': true,
}

// Sanitize turns a title into a filesystem- and wikilink-safe string:
// it removes `\ / : * ? " < > | # ^ [ ]`, collapses runs of
// whitespace to a single space, trims leading/trailing whitespace,
// and truncates to at most 100 runes (trimming any trailing space
// truncation left behind).
func Sanitize(title string) string {
	var b strings.Builder
	for _, r := range title {
		if sanitizeRemove[r] {
			continue
		}
		b.WriteRune(r)
	}

	collapsed := strings.Join(strings.Fields(b.String()), " ")

	runes := []rune(collapsed)
	if len(runes) > 100 {
		runes = runes[:100]
	}
	return strings.TrimRight(string(runes), " ")
}

// filenameFor returns the note filename for an issue: "<id>
// <Sanitize(title)>.md", or "<id>.md" if the sanitized title is
// empty.
func filenameFor(id int, title string) string {
	s := Sanitize(title)
	if s == "" {
		return fmt.Sprintf("%d.md", id)
	}
	return fmt.Sprintf("%d %s.md", id, s)
}

// parseFrontmatterIssue looks for an `issue: <int>` key in data's
// YAML frontmatter. It only considers data a candidate at all if its
// very first line is exactly "---"; it then scans lines up to the
// next "---" line for the issue key. No general YAML parsing is
// performed.
func parseFrontmatterIssue(data []byte) (int, bool) {
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		return 0, false
	}

	id, ok := 0, false
	for _, line := range lines[1:] {
		trimmed := strings.TrimRight(line, "\r")
		if trimmed == "---" {
			break
		}
		if rest, found := strings.CutPrefix(trimmed, "issue:"); found {
			if n, err := strconv.Atoi(strings.TrimSpace(rest)); err == nil {
				id, ok = n, true
			}
		}
	}
	return id, ok
}

// idFromFilename extracts the leading "<id>" from a note's base
// filename ("73 Title.md" or "73.md"), without requiring frontmatter.
func idFromFilename(name string) (int, bool) {
	name = strings.TrimSuffix(name, ".md")
	prefix := name
	if i := strings.IndexByte(name, ' '); i >= 0 {
		prefix = name[:i]
	}
	n, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, false
	}
	return n, true
}

// noteOwnerID determines which issue a note file belongs to:
// frontmatter takes priority over the filename prefix.
func noteOwnerID(data []byte, filename string) (int, bool) {
	if id, ok := parseFrontmatterIssue(data); ok {
		return id, true
	}
	return idFromFilename(filename)
}

// LoadIndex scans TasksDir recursively for "*.md" files and builds an
// Index from issue ID to note path. A file's frontmatter `issue:` key
// takes priority over its filename prefix; if two files claim the
// same ID with equal priority, the lexicographically smallest path
// wins. A missing TasksDir yields an empty Index and no error.
func (s *Store) LoadIndex() (*Index, error) {
	idx := &Index{byID: map[int]string{}}
	priority := map[int]int{}

	consider := func(id int, path string, pri int) {
		cur, exists := priority[id]
		switch {
		case !exists || pri > cur:
			idx.byID[id] = path
			priority[id] = pri
		case pri == cur && path < idx.byID[id]:
			idx.byID[id] = path
		}
	}

	err := filepath.WalkDir(s.TasksDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if id, ok := parseFrontmatterIssue(data); ok {
			consider(id, path, 2)
			return nil
		}
		if id, ok := idFromFilename(d.Name()); ok {
			consider(id, path, 1)
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return &Index{byID: map[int]string{}}, nil
		}
		return nil, err
	}
	return idx, nil
}

// Resolve finds the note for an issue, in priority order: (1) link,
// if non-empty and "<ProjectDir>/<link>.md" exists as a regular file;
// (2) idx, which already prefers a frontmatter `issue:` match over a
// filename-prefix match. ok is false if nothing matches.
func (s *Store) Resolve(idx *Index, id int, link string) (path string, ok bool) {
	if link != "" {
		candidate := filepath.Join(s.ProjectDir, filepath.FromSlash(link)+".md")
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, true
		}
	}
	if idx != nil {
		if p, found := idx.byID[id]; found {
			return p, true
		}
	}
	return "", false
}

// Create returns the note for an issue, creating it from the standard
// template if it doesn't exist yet. If Resolve already finds a note
// for id, Create returns its path unchanged and does not write
// anything. It creates TasksDir if necessary. If the computed target
// filename exists but belongs to a different issue, Create returns an
// error rather than overwriting it. On success (whether the note was
// just created or already existed), idx is updated.
func (s *Store) Create(idx *Index, id int, title string) (string, error) {
	if err := os.MkdirAll(s.TasksDir, 0o755); err != nil {
		return "", err
	}

	if p, ok := s.Resolve(idx, id, ""); ok {
		return p, nil
	}

	path := filepath.Join(s.TasksDir, filenameFor(id, title))

	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		owner, ok := noteOwnerID(data, filepath.Base(path))
		if !ok || owner != id {
			return "", fmt.Errorf("notes: %s already exists and belongs to a different issue", path)
		}
		// Same issue, just missing from idx: treat as found.
	case os.IsNotExist(err):
		content := fmt.Sprintf(
			"---\nissue: %d\n---\n\n# %s\n\n## Context\n\n## Implementation plan\n\n## Notes\n",
			id, title,
		)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return "", err
		}
	default:
		return "", err
	}

	if idx != nil {
		idx.byID[id] = path
	}
	return path, nil
}

// replaceH1 replaces the first top-level Markdown heading ("# ...")
// found after any frontmatter block, but only if its text equals
// oldTitle exactly. All other content, including line endings, is
// left untouched.
func replaceH1(content, oldTitle, newTitle string) string {
	lines := strings.Split(content, "\n")

	start := 0
	if len(lines) > 0 && lines[0] == "---" {
		for i := 1; i < len(lines); i++ {
			if lines[i] == "---" {
				start = i + 1
				break
			}
		}
	}

	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "# ") {
			if lines[i] == "# "+oldTitle {
				lines[i] = "# " + newTitle
			}
			break
		}
	}

	return strings.Join(lines, "\n")
}

// Rename moves a note to match a new title, updating its H1 heading
// if (and only if) that heading currently equals oldTitle exactly.
// All other content and the file's mode are preserved byte-for-byte.
//
// If the sanitized newTitle produces the same filename the note
// already has, the file is not moved (but its H1 may still be
// updated). If the computed new path already exists, Rename returns
// an error and leaves everything unchanged. On success, idx is
// updated to the (possibly unchanged) new path.
func (s *Store) Rename(idx *Index, path string, id int, oldTitle, newTitle string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}

	newPath := filepath.Join(filepath.Dir(path), filenameFor(id, newTitle))
	sameFile := newPath == path

	if !sameFile {
		if _, err := os.Stat(newPath); err == nil {
			return "", fmt.Errorf("notes: %s already exists", newPath)
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}

	newData := replaceH1(string(data), oldTitle, newTitle)

	if sameFile {
		if newData != string(data) {
			if err := os.WriteFile(path, []byte(newData), info.Mode().Perm()); err != nil {
				return "", err
			}
		}
		if idx != nil {
			idx.byID[id] = path
		}
		return path, nil
	}

	if err := os.WriteFile(newPath, []byte(newData), info.Mode().Perm()); err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	if idx != nil {
		idx.byID[id] = newPath
	}
	return newPath, nil
}

// LinkTarget returns path as a wikilink target relative to
// ProjectDir, with forward slashes and no ".md" extension (e.g.
// "Tasks/42 Title").
func (s *Store) LinkTarget(path string) string {
	rel, err := filepath.Rel(s.ProjectDir, path)
	if err != nil {
		rel = path
	}
	rel = strings.TrimSuffix(rel, ".md")
	return filepath.ToSlash(rel)
}
