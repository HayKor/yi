package kanban

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"kanban-sync/internal/domain"
)

func testStatuses() []domain.Status {
	return []domain.Status{
		{Key: "backlog", Heading: "Backlog"},
		{Key: "todo", Heading: "Todo"},
		{Key: "in-progress", Heading: "In Progress"},
		{Key: "completed", Heading: "Completed", CloseIssue: true},
	}
}

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading testdata/%s: %v", name, err)
	}
	return data
}

func TestRoundTrip(t *testing.T) {
	idea := readTestdata(t, "idea.md")
	plugin := readTestdata(t, "plugin.md")
	pluginCRLF := []byte(strings.ReplaceAll(string(plugin), "\n", "\r\n"))
	pluginNoFinalNL := bytes.TrimSuffix(plugin, []byte("\n"))

	cases := map[string][]byte{
		"idea.md":                      idea,
		"plugin.md":                    plugin,
		"plugin.md (CRLF)":             pluginCRLF,
		"plugin.md (no final newline)": pluginNoFinalNL,
	}

	for name, data := range cases {
		data := data
		t.Run(name, func(t *testing.T) {
			b, err := Parse(data, testStatuses())
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got := b.Bytes()
			if !bytes.Equal(got, data) {
				t.Errorf("round trip mismatch for %s:\n--- want (%d bytes) ---\n%s\n--- got (%d bytes) ---\n%s", name, len(data), data, len(got), got)
			}
		})
	}
}

func TestCardsParsedFromPluginBoard(t *testing.T) {
	data := readTestdata(t, "plugin.md")
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	type want struct {
		status  domain.StatusKey
		issueID int
		title   string
		link    string
		checked bool
	}
	wants := []want{
		{"backlog", 0, "Plain task without issue", "", false},
		{"backlog", 73, "Creative Preview", "Tasks/73 Creative Preview", false},
		{"in-progress", 42, "OAuth", "Tasks/42 Yandex OAuth", false},
		{"completed", 31, "Fix: crash", "", true},
	}

	cards := b.Cards()
	if len(cards) != len(wants) {
		t.Fatalf("Cards() returned %d cards, want %d: %+v", len(cards), len(wants), cards)
	}
	for i, w := range wants {
		c := cards[i]
		if c.Status != w.status || c.IssueID != w.issueID || c.Title != w.title || c.Link != w.link || c.Checked != w.checked {
			t.Errorf("cards[%d] = %+v, want %+v", i, *c, w)
		}
	}

	if got := b.Find(99); got != nil {
		t.Errorf("Find(99) = %+v, want nil (Ideas is unconfigured)", *got)
	}
	if got := b.Find(5); got != nil {
		t.Errorf("Find(5) = %+v, want nil (Archive is past the trailer marker)", *got)
	}
}

// TestCardOwnsContinuationLines checks that card #73's own line storage
// (not just its parsed fields) includes its two tab-indented
// continuation lines, in order — required for Task 5's Move, which
// relocates a card by moving its lines as a unit.
func TestCardOwnsContinuationLines(t *testing.T) {
	data := readTestdata(t, "plugin.md")
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	card := b.Find(73)
	if card == nil {
		t.Fatal("Find(73) = nil, want card")
	}
	if len(card.lines) != 3 {
		t.Fatalf("card #73 has %d lines, want 3 (first line + 2 continuation lines): %q", len(card.lines), card.lines)
	}
	if want := "- [ ] [[Tasks/73 Creative Preview]] `#73`\n"; card.lines[0] != want {
		t.Errorf("lines[0] = %q, want %q", card.lines[0], want)
	}
	if want := "\tsecond line of card\n"; card.lines[1] != want {
		t.Errorf("lines[1] = %q, want %q", card.lines[1], want)
	}
	if want := "\t- [ ] nested checklist item\n"; card.lines[2] != want {
		t.Errorf("lines[2] = %q, want %q", card.lines[2], want)
	}
}

// TestIndentedTripleStarInContinuationDoesNotStartTrailer is a
// regression test: an indented "***" that is a card's continuation
// line must not be mistaken for the plugin's archive separator, which
// only ever appears at column 0. Before the fix, this swallowed every
// later card and section into the trailer.
func TestIndentedTripleStarInContinuationDoesNotStartTrailer(t *testing.T) {
	data := []byte("## Backlog\n" +
		"\n" +
		"- [ ] A `#1`\n" +
		"\t***\n" +
		"- [ ] B `#2`\n" +
		"\n" +
		"## Todo\n" +
		"\n" +
		"- [ ] C `#3`\n")

	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	cards := b.Cards()
	if len(cards) != 3 {
		t.Fatalf("Cards() = %d cards, want 3: %+v", len(cards), cards)
	}
	if b.Find(1) == nil {
		t.Error("Find(1) = nil, want card A")
	}
	if b.Find(2) == nil {
		t.Error("Find(2) = nil, want card B")
	}
	if b.Find(3) == nil {
		t.Error("Find(3) = nil, want card C")
	}

	if got := b.Bytes(); !bytes.Equal(got, data) {
		t.Errorf("round trip mismatch:\n--- want ---\n%s\n--- got ---\n%s", data, got)
	}
}

// TestIndentedTripleStarInOpaqueSectionDoesNotStartTrailer covers the
// same bug in an unconfigured ("Ideas") section: a "***" with leading
// whitespace there is just opaque content, not a trailer marker either.
func TestIndentedTripleStarInOpaqueSectionDoesNotStartTrailer(t *testing.T) {
	data := []byte("## Backlog\n" +
		"\n" +
		"- [ ] A `#1`\n" +
		"\n" +
		"## Ideas\n" +
		"\n" +
		"  ***\n" +
		"- [ ] Unknown `#99`\n" +
		"\n" +
		"## Todo\n" +
		"\n" +
		"- [ ] C `#3`\n")

	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	cards := b.Cards()
	if len(cards) != 2 {
		t.Fatalf("Cards() = %d cards, want 2: %+v", len(cards), cards)
	}
	if b.Find(1) == nil {
		t.Error("Find(1) = nil, want card A")
	}
	if b.Find(3) == nil {
		t.Error("Find(3) = nil, want card C")
	}
	if b.Find(99) != nil {
		t.Error("Find(99) != nil, want nil (Ideas is unconfigured)")
	}

	if got := b.Bytes(); !bytes.Equal(got, data) {
		t.Errorf("round trip mismatch:\n--- want ---\n%s\n--- got ---\n%s", data, got)
	}
}

// TestFindRejectsNonPositiveID checks the controller ruling that
// Find(id) with id <= 0 always returns nil, rather than matching the
// first plain card (IssueID == 0).
func TestFindRejectsNonPositiveID(t *testing.T) {
	data := readTestdata(t, "plugin.md")
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	for _, id := range []int{0, -1, -42} {
		if got := b.Find(id); got != nil {
			t.Errorf("Find(%d) = %+v, want nil", id, *got)
		}
	}
}

func parseSingleCard(t *testing.T, firstLine string) *Card {
	t.Helper()
	data := []byte("## Backlog\n\n" + firstLine + "\n")
	b, err := Parse(data, testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cards := b.Cards()
	if len(cards) != 1 {
		t.Fatalf("got %d cards, want 1: %+v", len(cards), cards)
	}
	return cards[0]
}

func TestCardTitleRules(t *testing.T) {
	cases := []struct {
		line    string
		title   string
		issueID int
		link    string
		checked bool
	}{
		{"- [ ] [[Tasks/42 Yandex OAuth]] `#42`", "Yandex OAuth", 42, "Tasks/42 Yandex OAuth", false},
		{"- [ ] [[Tasks/Some note]] `#42`", "Some note", 42, "Tasks/Some note", false},
		{"- [X] Title `#1` and `#2`", "Title `#1` and", 2, "", true},
		{"- [ ] Tagged #42", "Tagged #42", 0, "", false},
	}
	for _, c := range cases {
		t.Run(c.line, func(t *testing.T) {
			card := parseSingleCard(t, c.line)
			if card.Title != c.title {
				t.Errorf("Title = %q, want %q", card.Title, c.title)
			}
			if card.IssueID != c.issueID {
				t.Errorf("IssueID = %d, want %d", card.IssueID, c.issueID)
			}
			if card.Link != c.link {
				t.Errorf("Link = %q, want %q", card.Link, c.link)
			}
			if card.Checked != c.checked {
				t.Errorf("Checked = %v, want %v", card.Checked, c.checked)
			}
		})
	}
}

func TestDuplicateIssueID(t *testing.T) {
	data := []byte("## Backlog\n" +
		"\n" +
		"## Todo\n" +
		"\n" +
		"- [ ] First `#42`\n" +
		"\n" +
		"## In Progress\n" +
		"\n" +
		"## Completed\n" +
		"\n" +
		"- [ ] Second `#42`\n")

	_, err := Parse(data, testStatuses())
	if err == nil {
		t.Fatal("Parse: want error for duplicate #42, got nil")
	}

	var dupErr *DuplicateError
	if !errors.As(err, &dupErr) {
		t.Fatalf("Parse error = %v (%T), want *DuplicateError", err, err)
	}
	if dupErr.ID != 42 {
		t.Errorf("DuplicateError.ID = %d, want 42", dupErr.ID)
	}
	wantLines := []int{5, 11}
	if !reflect.DeepEqual(dupErr.Lines, wantLines) {
		t.Errorf("DuplicateError.Lines = %v, want %v", dupErr.Lines, wantLines)
	}
	wantMsg := "duplicate card for #42 at lines 5, 11"
	if err.Error() != wantMsg {
		t.Errorf("Error() = %q, want %q", err.Error(), wantMsg)
	}
}

func TestEOLDetected(t *testing.T) {
	lf := []byte("## Backlog\n\n- [ ] Task\n")
	crlf := []byte("## Backlog\r\n\r\n- [ ] Task\r\n")
	noEOL := []byte("## Backlog")

	b, err := Parse(lf, testStatuses())
	if err != nil {
		t.Fatalf("Parse(lf): %v", err)
	}
	if got := b.EOL(); got != "\n" {
		t.Errorf("EOL() = %q, want %q", got, "\n")
	}

	b2, err := Parse(crlf, testStatuses())
	if err != nil {
		t.Fatalf("Parse(crlf): %v", err)
	}
	if got := b2.EOL(); got != "\r\n" {
		t.Errorf("EOL() = %q, want %q", got, "\r\n")
	}

	b3, err := Parse(noEOL, testStatuses())
	if err != nil {
		t.Fatalf("Parse(noEOL): %v", err)
	}
	if got := b3.EOL(); got != "\n" {
		t.Errorf("EOL() = %q, want %q", got, "\n")
	}
}
