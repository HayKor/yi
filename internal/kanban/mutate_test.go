package kanban

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kanban-sync/internal/domain"
)

var update = flag.Bool("update", false, "update golden files in testdata/")

// goldenCompare compares got against testdata/name, byte for byte. With
// -update it (re)writes the golden file instead of comparing — inspect
// every regenerated golden file by eye before committing; it is the
// spec of the output.
func goldenCompare(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("writing golden file %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden file %s: %v (run with -update to create it)", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s mismatch:\n--- want (%d bytes) ---\n%s\n--- got (%d bytes) ---\n%s", name, len(want), want, len(got), got)
	}
}

func TestMoveToEmptyColumn(t *testing.T) {
	data := readTestdata(t, "plugin.md")
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	card := b.Find(73)
	if card == nil {
		t.Fatal("Find(73) = nil, want card")
	}
	if err := b.Move(card, "todo"); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if card.Status != "todo" {
		t.Errorf("card.Status = %q, want %q", card.Status, "todo")
	}

	goldenCompare(t, "move_empty.golden.md", b.Bytes())
}

func TestMoveAppendsAfterLastCard(t *testing.T) {
	data := readTestdata(t, "plugin.md")
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	cards := b.Cards()
	plain := cards[0]
	if plain.Title != "Plain task without issue" {
		t.Fatalf("cards[0] = %+v, want the plain task", *plain)
	}

	if err := b.Move(plain, "completed"); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if !plain.Checked {
		t.Errorf("Checked = false after moving into a closeIssue status, want true")
	}

	goldenCompare(t, "move_append.golden.md", b.Bytes())
}

func TestMovePreservesExtras(t *testing.T) {
	data := readTestdata(t, "plugin.md")
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	card := b.Find(42)
	if card == nil {
		t.Fatal("Find(42) = nil, want card")
	}
	original := card.lines[0]

	if err := b.Move(card, "todo"); err != nil {
		t.Fatalf("Move: %v", err)
	}

	if card.lines[0] != original {
		t.Errorf("first line changed:\nbefore: %q\nafter:  %q", original, card.lines[0])
	}
	if !strings.Contains(card.lines[0], "|OAuth") {
		t.Errorf("first line lost its alias: %q", card.lines[0])
	}
	if !strings.Contains(card.lines[0], "@{2026-10-25}") {
		t.Errorf("first line lost its date tag: %q", card.lines[0])
	}
}

func TestMoveUnknownStatus(t *testing.T) {
	data := readTestdata(t, "plugin.md")
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	card := b.Find(73)

	err = b.Move(card, "nope")
	if err == nil {
		t.Fatal("Move: want error for unknown status, got nil")
	}
	wantMsg := `unknown status "nope"`
	if err.Error() != wantMsg {
		t.Errorf("Move error = %q, want %q", err.Error(), wantMsg)
	}
}

func reviewStatuses() []domain.Status {
	return []domain.Status{
		{Key: "backlog", Heading: "Backlog"},
		{Key: "todo", Heading: "Todo"},
		{Key: "in-progress", Heading: "In Progress"},
		{Key: "review", Heading: "Review"},
		{Key: "completed", Heading: "Completed", CloseIssue: true},
	}
}

func TestAddCreatesMissingSection(t *testing.T) {
	data := readTestdata(t, "plugin.md")
	b, err := Parse(data, reviewStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	card, err := b.Add("review", "- [ ] X `#8`")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if card.Status != "review" || card.IssueID != 8 || card.Title != "X" {
		t.Errorf("card = %+v, want status review, id 8, title X", *card)
	}

	goldenCompare(t, "add_missing_section.golden.md", b.Bytes())
}

func TestAddUsesCRLF(t *testing.T) {
	plugin := readTestdata(t, "plugin.md")
	data := []byte(strings.ReplaceAll(string(plugin), "\n", "\r\n"))
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	card, err := b.Add("todo", "- [ ] New task `#90`")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !strings.HasSuffix(card.lines[0], "\r\n") {
		t.Errorf("added card's line = %q, want it to end with CRLF", card.lines[0])
	}
	if strings.Contains(card.lines[0], "\r\r\n") {
		t.Errorf("added card's line has doubled CR: %q", card.lines[0])
	}
}

func TestAddAtEOFWithoutNewline(t *testing.T) {
	data := []byte("## Todo\n\n- [ ] a `#1`")
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if _, err := b.Add("todo", "- [ ] b `#2`"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	want := []byte("## Todo\n\n- [ ] a `#1`\n- [ ] b `#2`\n")
	if got := b.Bytes(); !bytes.Equal(got, want) {
		t.Errorf("Bytes() =\n%q\nwant\n%q", got, want)
	}
}

func TestAddUnknownStatus(t *testing.T) {
	data := readTestdata(t, "plugin.md")
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	_, err = b.Add("nope", "- [ ] X")
	if err == nil {
		t.Fatal("Add: want error for unknown status, got nil")
	}
	if want := `unknown status "nope"`; err.Error() != want {
		t.Errorf("Add error = %q, want %q", err.Error(), want)
	}
}

func TestAddRejectsNonCardLine(t *testing.T) {
	data := readTestdata(t, "plugin.md")
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	_, err = b.Add("todo", "not a card")
	if err == nil {
		t.Fatal("Add: want error for a non-card line, got nil")
	}
	if want := `not a card line: "not a card"`; err.Error() != want {
		t.Errorf("Add error = %q, want %q", err.Error(), want)
	}
}

func TestAddDuplicateID(t *testing.T) {
	data := readTestdata(t, "plugin.md")
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	_, err = b.Add("todo", "- [ ] Second OAuth `#42`")
	if err == nil {
		t.Fatal("Add: want *DuplicateError for a repeated #42, got nil")
	}
	var dupErr *DuplicateError
	if !errors.As(err, &dupErr) {
		t.Fatalf("Add error = %v (%T), want *DuplicateError", err, err)
	}
	if dupErr.ID != 42 {
		t.Errorf("DuplicateError.ID = %d, want 42", dupErr.ID)
	}
}

func TestSetChecked(t *testing.T) {
	data := []byte("## Backlog\n\n- [ ] Task `#1` @{2026-10-25}\n")
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	card := b.Find(1)

	b.SetChecked(card, true)
	if !card.Checked {
		t.Error("Checked = false, want true")
	}
	want := "- [x] Task `#1` @{2026-10-25}\n"
	if card.lines[0] != want {
		t.Errorf("lines[0] = %q, want %q", card.lines[0], want)
	}

	b.SetChecked(card, false)
	if card.Checked {
		t.Error("Checked = true, want false")
	}
	want = "- [ ] Task `#1` @{2026-10-25}\n"
	if card.lines[0] != want {
		t.Errorf("lines[0] = %q, want %q", card.lines[0], want)
	}
}

func TestSetIssueIDAppends(t *testing.T) {
	data := readTestdata(t, "plugin.md")
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	card := b.Cards()[0]
	if card.Title != "Plain task without issue" {
		t.Fatalf("cards[0] = %+v, want the plain task", *card)
	}

	if err := b.SetIssueID(card, 80); err != nil {
		t.Fatalf("SetIssueID: %v", err)
	}
	want := "- [ ] Plain task without issue `#80`\n"
	if card.lines[0] != want {
		t.Errorf("lines[0] = %q, want %q", card.lines[0], want)
	}
	if card.IssueID != 80 {
		t.Errorf("IssueID = %d, want 80", card.IssueID)
	}
	if got := b.Find(80); got != card {
		t.Errorf("Find(80) = %v, want the same card", got)
	}
}

func TestSetIssueIDAlreadySetErrors(t *testing.T) {
	data := readTestdata(t, "plugin.md")
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	card := b.Find(42)
	if err := b.SetIssueID(card, 99); err == nil {
		t.Fatal("SetIssueID: want error for a card that already has an id, got nil")
	}
}

func TestSetLinkKeepsAlias(t *testing.T) {
	data := readTestdata(t, "plugin.md")
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	card := b.Find(42)
	if err := b.SetLink(card, "Tasks/42 New"); err != nil {
		t.Fatalf("SetLink: %v", err)
	}
	want := "- [ ] [[Tasks/42 New|OAuth]] `#42` @{2026-10-25}\n"
	if card.lines[0] != want {
		t.Errorf("lines[0] = %q, want %q", card.lines[0], want)
	}
	if card.Link != "Tasks/42 New" {
		t.Errorf("Link = %q, want %q", card.Link, "Tasks/42 New")
	}
	if card.Title != "OAuth" {
		t.Errorf("Title = %q, want %q (alias preserved)", card.Title, "OAuth")
	}
}

func TestSetLinkWithoutLinkErrors(t *testing.T) {
	data := readTestdata(t, "plugin.md")
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	card := b.Cards()[0] // the plain task, no link
	if err := b.SetLink(card, "Tasks/1 New"); err == nil {
		t.Fatal("SetLink: want error for a card without a link, got nil")
	}
}

func TestRewriteFirstLineRejectsNonCardLine(t *testing.T) {
	data := readTestdata(t, "plugin.md")
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	card := b.Find(42)
	if err := b.RewriteFirstLine(card, "not a card"); err == nil {
		t.Fatal("RewriteFirstLine: want error for a non-card line, got nil")
	}
}

func TestRewriteFirstLineReparsesFields(t *testing.T) {
	data := readTestdata(t, "plugin.md")
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	card := b.Cards()[0]
	if err := b.RewriteFirstLine(card, FormatCard(false, "Renamed", "", 0)); err != nil {
		t.Fatalf("RewriteFirstLine: %v", err)
	}
	if card.Title != "Renamed" {
		t.Errorf("Title = %q, want %q", card.Title, "Renamed")
	}
	if card.IssueID != 0 {
		t.Errorf("IssueID = %d, want 0", card.IssueID)
	}
}

func TestFormatCard(t *testing.T) {
	cases := []struct {
		name    string
		checked bool
		title   string
		link    string
		id      int
		want    string
	}{
		{"with note and id", false, "Yandex OAuth", "Tasks/42 Yandex OAuth", 42, "- [ ] [[Tasks/42 Yandex OAuth]] `#42`"},
		{"checked, no note", true, "Fix crash", "", 31, "- [x] Fix crash `#31`"},
		{"no id, no note", false, "Title", "", 0, "- [ ] Title"},
		{"no id, with note", false, "Yandex OAuth", "Tasks/42 Yandex OAuth", 0, "- [ ] [[Tasks/42 Yandex OAuth]]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := FormatCard(c.checked, c.title, c.link, c.id)
			if got != c.want {
				t.Errorf("FormatCard(%v, %q, %q, %d) = %q, want %q", c.checked, c.title, c.link, c.id, got, c.want)
			}
		})
	}
}

// --- Fix round 1 (controller review) ---

// TestAddRejectsMultilineLine checks that Add rejects a line smuggling
// extra lines (and so, potentially, extra cards) via '\n' or '\r'
// instead of taking it as the single card line it's documented to be.
func TestAddRejectsMultilineLine(t *testing.T) {
	data := readTestdata(t, "plugin.md")
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	before := b.Bytes()
	_, err = b.Add("todo", "- [ ] x\n- [ ] y `#7`")
	if err == nil {
		t.Fatal("Add: want error for a line containing '\\n', got nil")
	}
	wantMsg := "card line must be a single line: \"- [ ] x\\n- [ ] y `#7`\""
	if err.Error() != wantMsg {
		t.Errorf("Add error = %q, want %q", err.Error(), wantMsg)
	}
	if got := b.Bytes(); !bytes.Equal(got, before) {
		t.Errorf("board changed despite the rejected Add:\n--- before ---\n%s\n--- after ---\n%s", before, got)
	}

	if _, err := b.Add("todo", "- [ ] x\r\n- [ ] y `#7`"); err == nil {
		t.Fatal("Add: want error for a line containing '\\r\\n', got nil")
	}
}

// TestRewriteFirstLineRejectsMultilineLine is the same check for
// RewriteFirstLine.
func TestRewriteFirstLineRejectsMultilineLine(t *testing.T) {
	data := readTestdata(t, "plugin.md")
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	card := b.Find(42)
	original := card.lines[0]

	err = b.RewriteFirstLine(card, "- [ ] x\n- [ ] y `#7`")
	if err == nil {
		t.Fatal("RewriteFirstLine: want error for a line containing '\\n', got nil")
	}
	wantMsg := "card line must be a single line: \"- [ ] x\\n- [ ] y `#7`\""
	if err.Error() != wantMsg {
		t.Errorf("RewriteFirstLine error = %q, want %q", err.Error(), wantMsg)
	}
	if card.lines[0] != original {
		t.Errorf("card line changed despite the rejected rewrite: %q, want %q", card.lines[0], original)
	}
}

func TestSetIssueIDRejectsNonPositive(t *testing.T) {
	data := readTestdata(t, "plugin.md")
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	card := b.Cards()[0] // the plain task, no issue id yet
	for _, id := range []int{0, -1, -42} {
		if err := b.SetIssueID(card, id); err == nil {
			t.Errorf("SetIssueID(card, %d): want error, got nil", id)
		}
	}
	if card.IssueID != 0 {
		t.Errorf("IssueID = %d after rejected SetIssueID calls, want 0", card.IssueID)
	}
}

func TestSetIssueIDRejectsDuplicate(t *testing.T) {
	data := readTestdata(t, "plugin.md")
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	card := b.Cards()[0] // the plain task, no issue id yet
	err = b.SetIssueID(card, 42)
	if err == nil {
		t.Fatal("SetIssueID: want *DuplicateError for an id already on the board (#42), got nil")
	}
	var dupErr *DuplicateError
	if !errors.As(err, &dupErr) {
		t.Fatalf("SetIssueID error = %v (%T), want *DuplicateError", err, err)
	}
	if dupErr.ID != 42 {
		t.Errorf("DuplicateError.ID = %d, want 42", dupErr.ID)
	}
	if card.IssueID != 0 {
		t.Errorf("IssueID = %d after rejected SetIssueID, want 0", card.IssueID)
	}
}

// TestMoveToSameStatusIsNoOp checks the controller ruling that moving
// a card to the status it's already in doesn't reorder it within its
// section: before this fix, Move(c, c.Status) on a non-last card
// (A, B) unconditionally cut it out and re-appended it after the
// section's last card, turning (A, B) into (B, A).
func TestMoveToSameStatusIsNoOp(t *testing.T) {
	data := []byte("## Backlog\n\n- [ ] A `#1`\n- [ ] B `#2`\n\n## Todo\n\n## In Progress\n\n## Completed\n")
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	card := b.Find(1)
	if card == nil {
		t.Fatal("Find(1) = nil, want card A")
	}

	if err := b.Move(card, "backlog"); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if got := b.Bytes(); !bytes.Equal(got, data) {
		t.Errorf("Move(c, c.Status) changed the board:\n--- want (unchanged) ---\n%s\n--- got ---\n%s", data, got)
	}
	if card.Status != "backlog" {
		t.Errorf("Status = %q, want %q", card.Status, "backlog")
	}
}
