// Package kanban parses and round-trips an Obsidian Kanban board
// (Kanban.md) without disturbing anything it doesn't understand. It is
// not a general Markdown parser: the format is a strict, line-oriented
// subset, and anything outside that subset (frontmatter, unconfigured
// columns, the plugin's archive/settings footer) is kept opaque and
// reproduced byte-for-byte. See spec §6.2.
//
// kanban depends only on domain.
package kanban

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/HayKor/yi/internal/domain"
)

// item is one element of a section's body: either a card (first line plus
// any continuation lines) or a run of raw lines the parser didn't
// recognize as a card (blank lines, stray text).
type item struct {
	card *Card
	raw  []string
}

// section is a "## <heading>" line and everything up to the next
// section heading or the trailer. status is "" if the heading didn't
// match any configured domain.Status ("opaque" section): its cards, if
// any, are not parsed and are kept as raw content.
type section struct {
	heading string // the full heading line, with its original EOL
	status  domain.StatusKey
	items   []item
}

// appendRaw appends line to the section's trailing raw item, starting a
// new one if the section's last item is a card (or there is none yet).
func (s *section) appendRaw(line string) {
	if n := len(s.items); n > 0 && s.items[n-1].card == nil {
		s.items[n-1].raw = append(s.items[n-1].raw, line)
		return
	}
	s.items = append(s.items, item{raw: []string{line}})
}

// Board is a parsed Kanban.md. It keeps every original line (preamble,
// section bodies, trailer) so that Bytes() reproduces the input
// byte-for-byte when nothing has changed.
type Board struct {
	preamble []string // lines before the first "## " heading (e.g. frontmatter)
	sections []*section
	trailer  []string // lines from the plugin's "***" / "%% kanban:settings" marker to EOF
	eol      string
	statuses []domain.Status // the project's configured columns, in config order (Task 5: mutations need headings/order/CloseIssue)
}

// DuplicateError reports that the same issue id appears on more than one
// card. Parse returns it before any card is otherwise usable.
type DuplicateError struct {
	ID    int
	Lines []int // 1-based source line numbers of each card sharing ID, in document order
}

func (e *DuplicateError) Error() string {
	parts := make([]string, len(e.Lines))
	for i, l := range e.Lines {
		parts[i] = strconv.Itoa(l)
	}
	return fmt.Sprintf("duplicate card for #%d at lines %s", e.ID, strings.Join(parts, ", "))
}

// Parse reads a Kanban.md file. statuses is the project's configured
// columns; a "## " heading matches a status when the two are equal
// under domain.NormalizeHeading (trimmed, case-insensitive, ignoring a
// WIP-limit suffix such as "In Progress (3)"). Headings that match nothing
// are opaque sections: their content, including any card-shaped lines,
// is preserved but not parsed.
//
// Parse returns a *DuplicateError if the same non-zero issue id appears
// on more than one card in a configured section.
func Parse(data []byte, statuses []domain.Status) (*Board, error) {
	lines := splitLines(data)
	b := &Board{eol: detectEOL(lines), statuses: statuses}

	i := 0
	for i < len(lines) {
		content := stripEOL(lines[i])
		if isHeadingLine(content) {
			break
		}
		b.preamble = append(b.preamble, lines[i])
		i++
	}

	var cur *section
	var curCard *Card
	trailerStarted := false

	for i < len(lines) {
		line := lines[i]
		content := stripEOL(line)

		if trailerStarted {
			b.trailer = append(b.trailer, line)
			i++
			continue
		}

		if isTrailerStart(content) {
			trailerStarted = true
			curCard = nil
			b.trailer = append(b.trailer, line)
			i++
			continue
		}

		if isHeadingLine(content) {
			cur = &section{heading: line, status: matchStatus(content, statuses)}
			b.sections = append(b.sections, cur)
			curCard = nil
			i++
			continue
		}

		if curCard != nil {
			if content == "" {
				curCard = nil
				cur.appendRaw(line)
				i++
				continue
			}
			if strings.HasPrefix(content, " ") || strings.HasPrefix(content, "\t") {
				curCard.lines = append(curCard.lines, line)
				i++
				continue
			}
			// Not a continuation: the card ends here, without
			// consuming this line. Re-evaluate it fresh below.
			curCard = nil
		}

		if cur.status != "" && isCardStart(content) {
			card := &Card{Status: cur.status, Line: i + 1, lines: []string{line}}
			parseCardFirstLine(content, card)
			cur.items = append(cur.items, item{card: card})
			curCard = card
			i++
			continue
		}

		cur.appendRaw(line)
		i++
	}

	if err := checkDuplicates(b); err != nil {
		return nil, err
	}
	return b, nil
}

// checkDuplicates returns a *DuplicateError for the first (in document
// order) non-zero issue id that appears on more than one card.
func checkDuplicates(b *Board) error {
	linesByID := map[int][]int{}
	var order []int
	for _, c := range b.Cards() {
		if c.IssueID == 0 {
			continue
		}
		if _, ok := linesByID[c.IssueID]; !ok {
			order = append(order, c.IssueID)
		}
		linesByID[c.IssueID] = append(linesByID[c.IssueID], c.Line)
	}
	for _, id := range order {
		if lines := linesByID[id]; len(lines) > 1 {
			return &DuplicateError{ID: id, Lines: lines}
		}
	}
	return nil
}

// matchStatus returns the StatusKey of the configured status whose
// heading matches headingLineContent (a "## ..." line with its EOL
// stripped) under domain.NormalizeHeading — trimmed, case-insensitive,
// ignoring a WIP-limit suffix like " (3)" — or "" if none matches.
func matchStatus(headingLineContent string, statuses []domain.Status) domain.StatusKey {
	h := domain.NormalizeHeading(strings.TrimPrefix(headingLineContent, "## "))
	for _, st := range statuses {
		if h == domain.NormalizeHeading(st.Heading) {
			return st.Key
		}
	}
	return ""
}

// isHeadingLine reports whether content (EOL already stripped) is a
// section heading: a line starting with "## " at column 0.
func isHeadingLine(content string) bool {
	return strings.HasPrefix(content, "## ")
}

// isTrailerStart reports whether content (EOL already stripped) is the
// first line of the trailer: the Kanban plugin's archive separator
// ("***") or the start of its settings block. Both must start at column
// 0 — an indented "***" (e.g. a card's continuation line, or stray text
// in an opaque section) is ordinary content, not a trailer marker.
// Trailing whitespace after "***" is still allowed.
func isTrailerStart(content string) bool {
	if strings.TrimRight(content, " \t") == "***" {
		return true
	}
	return strings.HasPrefix(content, "%% kanban:settings")
}

// Bytes reassembles the board into its Markdown source. If the board has
// not been modified since Parse, it is identical, byte-for-byte, to the
// data Parse was given.
func (b *Board) Bytes() []byte {
	var buf bytes.Buffer
	for _, l := range b.preamble {
		buf.WriteString(l)
	}
	for _, s := range b.sections {
		buf.WriteString(s.heading)
		for _, it := range s.items {
			if it.card != nil {
				for _, l := range it.card.lines {
					buf.WriteString(l)
				}
				continue
			}
			for _, l := range it.raw {
				buf.WriteString(l)
			}
		}
	}
	for _, l := range b.trailer {
		buf.WriteString(l)
	}
	return buf.Bytes()
}

// Cards returns every card from configured sections, in document order.
// Cards in opaque (unconfigured) sections are not included.
func (b *Board) Cards() []*Card {
	var out []*Card
	for _, s := range b.sections {
		if s.status == "" {
			continue
		}
		for _, it := range s.items {
			if it.card != nil {
				out = append(out, it.card)
			}
		}
	}
	return out
}

// Find returns the card with the given issue id, or nil if there is
// none (including when id only appears in an opaque section). id <= 0
// never matches: 0 means "no issue yet", a state shared by every
// plain card, not an id to look up.
func (b *Board) Find(id int) *Card {
	if id <= 0 {
		return nil
	}
	for _, c := range b.Cards() {
		if c.IssueID == id {
			return c
		}
	}
	return nil
}

// EOL returns the line ending used by the source file: "\r\n" if the
// first line ending in the file was CRLF, otherwise "\n" (also "\n" for
// a file with no line endings at all).
func (b *Board) EOL() string {
	return b.eol
}

// splitLines splits data into lines, each retaining its original EOL
// ("\n" or "\r\n"); the final line has no EOL if data doesn't end with
// one.
func splitLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			lines = append(lines, string(data[start:i+1]))
			start = i + 1
		}
	}
	if start < len(data) {
		lines = append(lines, string(data[start:]))
	}
	return lines
}

// stripEOL returns line with its trailing EOL ("\r\n" or "\n"), if any,
// removed.
func stripEOL(line string) string {
	if strings.HasSuffix(line, "\r\n") {
		return line[:len(line)-2]
	}
	return strings.TrimSuffix(line, "\n")
}

// detectEOL returns the line ending of the first line that has one, or
// "\n" if none do.
func detectEOL(lines []string) string {
	for _, l := range lines {
		if strings.HasSuffix(l, "\r\n") {
			return "\r\n"
		}
		if strings.HasSuffix(l, "\n") {
			return "\n"
		}
	}
	return "\n"
}
