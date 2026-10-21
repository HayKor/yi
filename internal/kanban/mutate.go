package kanban

import (
	"fmt"
	"strconv"
	"strings"

	"kanban-sync/internal/domain"
)

// FormatCard returns the canonical Markdown for a card's first line
// (§6.2), without a trailing EOL. A zero id omits the "`#id`" token
// entirely (controller ruling R2): callers that add a card before an
// issue exists format it with id 0, then call SetIssueID once the
// issue is created.
func FormatCard(checked bool, title, link string, id int) string {
	box := " "
	if checked {
		box = "x"
	}

	var sb strings.Builder
	sb.WriteString("- [")
	sb.WriteString(box)
	sb.WriteString("] ")
	if link != "" {
		sb.WriteString("[[")
		sb.WriteString(link)
		sb.WriteString("]]")
	} else {
		sb.WriteString(title)
	}
	if id != 0 {
		sb.WriteString(" `#")
		sb.WriteString(strconv.Itoa(id))
		sb.WriteString("`")
	}
	return sb.String()
}

// refreshFields re-derives a card's exported fields other than Status
// and Line (Checked, IssueID, Title, Link, Meta) from lines[0], using Task
// 4's first-line parser. Every mutator that edits lines[0] calls this
// afterward so the fields never drift from the text they're
// supposedly describing.
func refreshFields(c *Card) {
	c.Checked = false
	c.IssueID = 0
	c.Title = ""
	c.Link = ""
	c.Meta = nil
	parseCardFirstLine(stripEOL(c.lines[0]), c)
}

// splitEOL splits line into its content and its trailing EOL (which
// may be "" if the line is the file's last and has none).
func splitEOL(line string) (content, eol string) {
	content = stripEOL(line)
	return content, line[len(content):]
}

// hasEOL reports whether line ends in "\n" (covers both "\n" and
// "\r\n").
func hasEOL(line string) bool {
	return strings.HasSuffix(line, "\n")
}

// isSingleLine reports whether s contains no line break of its own.
// Add and SetTitle take a bare line (no EOL) to place verbatim as (part
// of) a card's first line; without this check, a caller-supplied "\n"
// or "\r" would smuggle extra lines (even extra cards) into the board
// instead of the one line the caller asked for.
func isSingleLine(s string) bool {
	return !strings.ContainsAny(s, "\r\n")
}

// SetChecked sets the checkbox character on c's first line, leaving
// everything else about the line untouched (dates, tags, continuation
// lines).
func (b *Board) SetChecked(c *Card, v bool) {
	setCheckboxChar(c, v)
	refreshFields(c)
}

// setCheckboxChar edits the checkbox character on c's first line in
// place, without touching anything else on the line or re-deriving
// the card's fields; SetChecked does both of those around it.
func setCheckboxChar(c *Card, checked bool) {
	content, eol := splitEOL(c.lines[0])
	ch := byte(' ')
	if checked {
		ch = 'x'
	}
	loc := cardStartRe.FindStringSubmatchIndex(content)
	c.lines[0] = content[:loc[2]] + string(ch) + content[loc[3]:] + eol
}

// SetIssueID appends a "`#id`" token to the end of c's first line. It
// errors if id isn't a valid (positive) issue id, if the card already
// has an issue id — SetIssueID is only for a plain card that just got
// one, never for replacing an existing id — or if id already belongs
// to another card on the board (a *DuplicateError, as Add returns for
// the same situation).
func (b *Board) SetIssueID(c *Card, id int) error {
	if id <= 0 {
		return fmt.Errorf("invalid issue id: %d", id)
	}
	if c.IssueID != 0 {
		return fmt.Errorf("card already has issue id #%d: %q", c.IssueID, stripEOL(c.lines[0]))
	}
	if existing := b.Find(id); existing != nil {
		return &DuplicateError{ID: id, Lines: []int{existing.Line}}
	}
	content, eol := splitEOL(c.lines[0])
	c.lines[0] = content + " `#" + strconv.Itoa(id) + "`" + eol
	refreshFields(c)
	return nil
}

// SetLink replaces the target of c's existing "[[target]]" or
// "[[target|alias]]" link with target, keeping any alias and the rest
// of the line (dates, tags, ...) untouched. It errors if the card has
// no link to update.
func (b *Board) SetLink(c *Card, target string) error {
	content, eol := splitEOL(c.lines[0])
	loc := findLink(content)
	if c.Link == "" || loc == nil {
		return fmt.Errorf("card has no link to update: %q", content)
	}

	var alias string
	if loc[4] != -1 {
		alias = content[loc[4]:loc[5]]
	}
	repl := "[[" + target + "]]"
	if alias != "" {
		repl = "[[" + target + "|" + alias + "]]"
	}

	c.lines[0] = content[:loc[0]] + repl + content[loc[1]:] + eol
	refreshFields(c)
	return nil
}

// SetTitle rebuilds c's first line as "- [<box>] <text>", followed by
// the card's metadata tokens (dates, times, tags — Card.Meta) in their
// original order and then its "`#id`" token, if it has one. The
// checkbox character and continuation lines are kept as they were.
// text is the card's new visible text: a plain title or a
// "[[target]]" link. Unlike SetChecked/SetIssueID/SetLink (surgical
// edits), SetTitle replaces whatever else was on the line, so it is
// meant for link-less cards, whose first line is exactly title +
// metadata + id: a plain card gaining a note, or a plain card being
// renamed. text must be a single line (no '\r' or '\n').
func (b *Board) SetTitle(c *Card, text string) error {
	if !isSingleLine(text) {
		return fmt.Errorf("card title must be a single line: %q", text)
	}
	content, eol := splitEOL(c.lines[0])
	loc := cardStartRe.FindStringSubmatchIndex(content)

	var sb strings.Builder
	sb.WriteString(content[:loc[1]]) // "- [<box>] "
	sb.WriteString(text)
	for _, m := range c.Meta {
		sb.WriteString(" ")
		sb.WriteString(m)
	}
	if c.IssueID > 0 {
		sb.WriteString(" `#")
		sb.WriteString(strconv.Itoa(c.IssueID))
		sb.WriteString("`")
	}
	c.lines[0] = sb.String() + eol
	refreshFields(c)
	return nil
}

// RewriteFirstLine replaces c's first line wholesale and re-parses its
// fields. Deprecated: it drops the card's metadata (dates, tags); use
// SetTitle. Kept only until reconcile stops calling it.
func (b *Board) RewriteFirstLine(c *Card, line string) error {
	if !isSingleLine(line) {
		return fmt.Errorf("card line must be a single line: %q", line)
	}
	if !isCardStart(line) {
		return fmt.Errorf("not a card line: %q", line)
	}
	_, eol := splitEOL(c.lines[0])
	c.lines[0] = line + eol
	refreshFields(c)
	return nil
}

// statusByKey returns the configured status with the given key.
func statusByKey(statuses []domain.Status, key domain.StatusKey) (domain.Status, bool) {
	for _, s := range statuses {
		if s.Key == key {
			return s, true
		}
	}
	return domain.Status{}, false
}

// statusOrder returns key's index in statuses (config order), or -1 if
// it isn't configured.
func statusOrder(statuses []domain.Status, key domain.StatusKey) int {
	for i, s := range statuses {
		if s.Key == key {
			return i
		}
	}
	return -1
}

// findSection returns the board's existing section for key, or nil if
// there isn't one yet.
func (b *Board) findSection(key domain.StatusKey) *section {
	for _, s := range b.sections {
		if s.status == key {
			return s
		}
	}
	return nil
}

// locateCard returns the section and item index holding c, found by
// pointer identity (a *Card is a stable handle into the board).
func (b *Board) locateCard(c *Card) (sec *section, idx int, found bool) {
	for _, s := range b.sections {
		for i, it := range s.items {
			if it.card == c {
				return s, i, true
			}
		}
	}
	return nil, 0, false
}

// Move relocates c to the to section, inserting it per §6.2's rule
// (after the section's last card, or after the first blank line
// following the heading if it has none) and creating the section if
// it doesn't exist yet. Entering a closeIssue status checks the card;
// leaving one unchecks it; otherwise the checkbox is untouched. Moving
// a card to the status it's already in is a no-op: Move never
// reorders cards within their own section.
func (b *Board) Move(c *Card, to domain.StatusKey) error {
	target, ok := statusByKey(b.statuses, to)
	if !ok {
		return fmt.Errorf("unknown status %q", to)
	}
	if to == c.Status {
		return nil
	}
	from, _ := statusByKey(b.statuses, c.Status) // zero value if c.Status is somehow unconfigured; CloseIssue false either way

	sec, idx, found := b.locateCard(c)
	if !found {
		return fmt.Errorf("card not found on board: %q", stripEOL(c.lines[0]))
	}

	sec.items = append(sec.items[:idx:idx], sec.items[idx+1:]...)

	b.placeCard(to, target, c)

	if target.CloseIssue {
		b.SetChecked(c, true)
	} else if from.CloseIssue {
		b.SetChecked(c, false)
	}
	c.Status = to
	return nil
}

// Add parses line as a new card and inserts it into the to section
// (creating the section if needed), same as Move. line must be a
// single line (no '\r' or '\n') that parses as a card. If line
// carries an issue id already present elsewhere on the board, Add
// returns a *DuplicateError instead of creating a second card for it.
func (b *Board) Add(to domain.StatusKey, line string) (*Card, error) {
	target, ok := statusByKey(b.statuses, to)
	if !ok {
		return nil, fmt.Errorf("unknown status %q", to)
	}
	if !isSingleLine(line) {
		return nil, fmt.Errorf("card line must be a single line: %q", line)
	}
	if !isCardStart(line) {
		return nil, fmt.Errorf("not a card line: %q", line)
	}

	card := &Card{Status: to, lines: []string{line + b.eol}}
	parseCardFirstLine(line, card)

	if card.IssueID != 0 {
		if existing := b.Find(card.IssueID); existing != nil {
			// The new card was never parsed from a line (it has none
			// yet), so only the existing one is reported.
			return nil, &DuplicateError{ID: card.IssueID, Lines: []int{existing.Line}}
		}
	}

	b.placeCard(to, target, card)
	return card, nil
}

// placeCard inserts card into the board's section for key (creating it
// per §6.2 if it doesn't exist), ensuring every line it touches ends
// with an EOL before anything follows it.
func (b *Board) placeCard(key domain.StatusKey, target domain.Status, card *Card) {
	ensureCardEOL(card, b.eol)

	if sec := b.findSection(key); sec != nil {
		insertCardInSection(sec, card, b.eol)
		return
	}

	idx := b.insertSectionIndex(key)
	b.ensurePrecedingEOL(idx)

	newSec := &section{
		heading: "## " + target.Heading + b.eol,
		status:  key,
		items:   []item{{raw: []string{b.eol}}, {card: card}, {raw: []string{b.eol}}},
	}
	b.insertSectionAt(idx, newSec)
}

// ensureCardEOL makes sure card's last line ends in an EOL, appending
// b's if it doesn't. A stored line can lack one only if it was the
// last line of a file with no final newline; once the card is
// (re)placed, something always follows it, or it becomes the file's
// new last line and normalizing it is fine either way.
func ensureCardEOL(card *Card, eol string) {
	n := len(card.lines) - 1
	if !hasEOL(card.lines[n]) {
		card.lines[n] += eol
	}
}

// ensureItemEOL makes sure it's last line ends in an EOL.
func ensureItemEOL(it item, eol string) {
	if it.card != nil {
		ensureCardEOL(it.card, eol)
		return
	}
	if n := len(it.raw) - 1; n >= 0 && !hasEOL(it.raw[n]) {
		it.raw[n] += eol
	}
}

// insertCardInSection inserts card into an existing section, per
// §6.2's insertion point: after the section's last card if it has one;
// otherwise directly below the last non-blank line of the section's
// leading text (the plugin's "**Complete**" marker), or, if that text
// is all blank, after the first blank line following the heading
// (adding one right after the heading if there is none).
func insertCardInSection(sec *section, card *Card, eol string) {
	lastCardIdx := -1
	for i, it := range sec.items {
		if it.card != nil {
			lastCardIdx = i
		}
	}
	if lastCardIdx >= 0 {
		ensureItemEOL(sec.items[lastCardIdx], eol)
		sec.items = insertItemAt(sec.items, lastCardIdx+1, item{card: card})
		return
	}

	// No cards yet. If the section's leading raw block has text in it
	// — the plugin's "**Complete**" marker on a lane it marks complete
	// — the card goes directly below its last non-blank line, as the
	// plugin writes items under the marker. Otherwise it goes right
	// after the first blank line following the heading, if there is one.
	if len(sec.items) > 0 && sec.items[0].card == nil {
		lines := sec.items[0].raw
		at := -1
		for i, l := range lines {
			if stripEOL(l) != "" {
				at = i
			}
		}
		if at < 0 {
			for i, l := range lines {
				if stripEOL(l) == "" {
					at = i
					break
				}
			}
		}
		if at >= 0 {
			before := append([]string{}, lines[:at+1]...)
			if n := len(before); n > 0 && !hasEOL(before[n-1]) {
				before[n-1] += eol
			}
			after := lines[at+1:]

			newItems := make([]item, 0, len(sec.items)+2)
			newItems = append(newItems, item{raw: before})
			newItems = append(newItems, item{card: card})
			if len(after) > 0 {
				newItems = append(newItems, item{raw: append([]string{}, after...)})
			}
			newItems = append(newItems, sec.items[1:]...)
			sec.items = newItems
			return
		}
	}

	// No blank line right after the heading: add one, then the card.
	if !hasEOL(sec.heading) {
		sec.heading += eol
	}
	newItems := make([]item, 0, len(sec.items)+2)
	newItems = append(newItems, item{raw: []string{eol}}, item{card: card})
	newItems = append(newItems, sec.items...)
	sec.items = newItems
}

// insertItemAt returns items with it inserted before index idx.
func insertItemAt(items []item, idx int, it item) []item {
	out := make([]item, 0, len(items)+1)
	out = append(out, items[:idx]...)
	out = append(out, it)
	out = append(out, items[idx:]...)
	return out
}

// insertSectionIndex returns the index in b.sections at which a new
// section for key belongs (§6.2): right after the nearest preceding
// section (in status order) that already exists on the board; failing
// that, right before the first existing configured section; failing
// that, at the end of the sections (before the trailer).
func (b *Board) insertSectionIndex(key domain.StatusKey) int {
	toOrder := statusOrder(b.statuses, key)

	bestIdx, bestOrder := -1, -1
	firstConfigured := -1
	for i, s := range b.sections {
		if s.status == "" {
			continue
		}
		if firstConfigured == -1 {
			firstConfigured = i
		}
		order := statusOrder(b.statuses, s.status)
		if order >= 0 && order < toOrder && order > bestOrder {
			bestOrder = order
			bestIdx = i
		}
	}

	if bestIdx >= 0 {
		return bestIdx + 1
	}
	if firstConfigured >= 0 {
		return firstConfigured
	}
	return len(b.sections)
}

// insertSectionAt inserts s into b.sections before index idx.
func (b *Board) insertSectionAt(idx int, s *section) {
	sections := make([]*section, 0, len(b.sections)+1)
	sections = append(sections, b.sections[:idx]...)
	sections = append(sections, s)
	sections = append(sections, b.sections[idx:]...)
	b.sections = sections
}

// ensurePrecedingEOL makes sure whatever will immediately precede a
// new section inserted at b.sections[idx] (the previous section, or
// the preamble if idx is 0) ends with an EOL.
func (b *Board) ensurePrecedingEOL(idx int) {
	if idx > 0 {
		ensureSectionTailEOL(b.sections[idx-1], b.eol)
		return
	}
	if n := len(b.preamble); n > 0 && !hasEOL(b.preamble[n-1]) {
		b.preamble[n-1] += b.eol
	}
}

// ensureSectionTailEOL makes sure s's very last line ends with an EOL.
func ensureSectionTailEOL(s *section, eol string) {
	if len(s.items) == 0 {
		if !hasEOL(s.heading) {
			s.heading += eol
		}
		return
	}
	ensureItemEOL(s.items[len(s.items)-1], eol)
}
