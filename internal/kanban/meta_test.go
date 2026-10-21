package kanban

import (
	"reflect"
	"strings"
	"testing"
)

// Obsidian Kanban metadata tokens (dates, times, tags) on a card's
// first line belong to the card, not to its title (final review C1).

func TestCardTitleExcludesMetadata(t *testing.T) {
	cases := []struct {
		line  string
		title string
		meta  []string
		id    int
		link  string
	}{
		{"- [ ] Write docs `#5` @{2026-11-01}", "Write docs", []string{"@{2026-11-01}"}, 5, ""},
		{"- [ ] Fix login @{2026-10-30} #urgent", "Fix login", []string{"@{2026-10-30}", "#urgent"}, 0, ""},
		{"- [ ] Call @{2026-10-30} @@{10:00} bob", "Call bob", []string{"@{2026-10-30}", "@@{10:00}"}, 0, ""},
		{"- [ ] #ops Rotate  keys `#9`", "Rotate keys", []string{"#ops"}, 9, ""},
		{"- [ ] Tagged #42", "Tagged #42", nil, 0, ""},
		{"- [ ] Use `#hash` in code", "Use `#hash` in code", nil, 0, ""},
		{"- [ ] C# rocks", "C# rocks", nil, 0, ""},
		{"- [ ] Ship @[[2026-11-02]] it", "Ship it", []string{"@[[2026-11-02]]"}, 0, ""},
		{"- [ ] [[Tasks/5 Write docs]] @{2026-11-01} `#5`", "Write docs", []string{"@{2026-11-01}"}, 5, "Tasks/5 Write docs"},
		{"- [ ] [[Design doc#Login|Login work]] #auth `#7`", "Login work", []string{"#auth"}, 7, "Design doc#Login"},
		{"- [ ] @[[2026-11-02]] [[Tasks/8 Real]] `#8`", "Real", []string{"@[[2026-11-02]]"}, 8, "Tasks/8 Real"},
	}
	for _, c := range cases {
		t.Run(c.line, func(t *testing.T) {
			card := parseSingleCard(t, c.line)
			if card.Title != c.title {
				t.Errorf("Title = %q, want %q", card.Title, c.title)
			}
			if !reflect.DeepEqual(card.Meta, c.meta) {
				t.Errorf("Meta = %q, want %q", card.Meta, c.meta)
			}
			if card.IssueID != c.id {
				t.Errorf("IssueID = %d, want %d", card.IssueID, c.id)
			}
			if card.Link != c.link {
				t.Errorf("Link = %q, want %q", card.Link, c.link)
			}
		})
	}
}

func TestStripMeta(t *testing.T) {
	cases := map[string]string{
		"Fix login @{2026-10-30} #urgent": "Fix login",
		"Plain title":                     "Plain title",
		"Issue #42 regression":            "Issue #42 regression",
		"  spaced   out  ":                "spaced out",
	}
	for in, want := range cases {
		if got := StripMeta(in); got != want {
			t.Errorf("StripMeta(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSetTitleKeepsMetadataAndID(t *testing.T) {
	cases := []struct {
		name string
		line string
		text string
		want string
	}{
		{"link a dated card", "- [ ] Write docs `#5` @{2026-11-01}", "[[Tasks/5 Write docs]]", "- [ ] [[Tasks/5 Write docs]] @{2026-11-01} `#5`"},
		{"rename keeps date, time, tag", "- [X] Old @{2026-10-30} @@{10:00} #urgent `#6`", "New title", "- [X] New title @{2026-10-30} @@{10:00} #urgent `#6`"},
		{"no metadata, no id", "- [ ] Draft", "Renamed", "- [ ] Renamed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := "## Backlog\n\n" + c.line + "\n\tcontinuation\n"
			b, err := Parse([]byte(src), testStatuses())
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			card := b.Cards()[0]
			if err := b.SetTitle(card, c.text); err != nil {
				t.Fatalf("SetTitle: %v", err)
			}
			want := "## Backlog\n\n" + c.want + "\n\tcontinuation\n"
			if got := string(b.Bytes()); got != want {
				t.Errorf("Bytes() =\n%q\nwant\n%q", got, want)
			}
			reparsed := parseSingleCard(t, c.want)
			if card.Title != reparsed.Title || card.IssueID != reparsed.IssueID || card.Link != reparsed.Link {
				t.Errorf("fields not refreshed: got %+v, want %+v", card, reparsed)
			}
		})
	}
}

func TestSetTitleRejectsMultilineText(t *testing.T) {
	b, err := Parse([]byte("## Backlog\n\n- [ ] Draft\n"), testStatuses())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	before := string(b.Bytes())
	for _, text := range []string{"a\nb", "a\r\n- [ ] b", "a\rb"} {
		if err := b.SetTitle(b.Cards()[0], text); err == nil {
			t.Errorf("SetTitle(%q): want error, got nil", text)
		}
	}
	if got := string(b.Bytes()); got != before {
		t.Errorf("board changed after rejected SetTitle: %q", got)
	}
	if strings.Count(string(b.Bytes()), "- [ ]") != 1 {
		t.Errorf("extra cards smuggled in: %q", b.Bytes())
	}
}
