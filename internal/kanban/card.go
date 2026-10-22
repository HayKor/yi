package kanban

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"kanban-sync/internal/domain"
)

// Card is a single kanban task, parsed from one "- [ ] ..." line (plus any
// indented continuation lines belonging to it).
type Card struct {
	Status  domain.StatusKey // the section the card currently lives in
	IssueID int              // 0 if the card has no `#id` token yet
	Title   string
	Link    string // note target ("Tasks/42 Yandex OAuth"), "" if the card has no [[link]]
	Alias   string // the link's alias ("[[target|alias]]"), "" if it has none
	Checked bool

	// Meta holds the card's Obsidian Kanban metadata tokens — dates
	// ("@{2026-11-01}", "@[[2026-11-01]]"), times ("@@{10:00}") and
	// tags ("#urgent") — in the order they appear on the first line.
	// They are never part of Title; SetTitle keeps them.
	Meta []string

	// Line is the 1-based line number of the card's first line in the
	// file Parse read it from, for use in error messages. Task 5's
	// Move and SetTitle relocate or rewrite the card without
	// updating it, so it can go stale; it is 0 for a card created by
	// Add, which was never parsed from a line.
	Line int

	lines []string // first line + continuation lines, each with its original EOL
}

// cardStartRe matches the start of a card's first line: a checkbox list
// item in column 0.
var cardStartRe = regexp.MustCompile(`^- \[( |x|X)\] `)

// idRe matches a backtick-wrapped issue id, e.g. "`#42`". A bare "#42"
// (no backticks) is an Obsidian tag, not an issue reference.
var idRe = regexp.MustCompile("`#([0-9]+)`")

// linkRe matches an Obsidian wikilink, with an optional alias:
// "[[target]]" or "[[target|alias]]". One preceded by "@" is a
// date linked to a daily note, not the card's link (see findLink).
var linkRe = regexp.MustCompile(`\[\[([^\]|]+)(?:\|([^\]]+))?\]\]`)

// codeRe matches an inline code span ("`...`"); nothing inside one is
// a metadata token.
var codeRe = regexp.MustCompile("`[^`]*`")

// dateRe matches the Kanban plugin's date and time tokens: "@{date}",
// "@@{time}", and a date linked to a daily note, "@[[date]]".
var dateRe = regexp.MustCompile(`@@?\{[^}]*\}|@\[\[[^\]]*\]\]`)

// tagRe matches a candidate Obsidian tag: "#" followed by a run of
// letters, digits, "_", "/" and "-" (any other character, punctuation
// included, ends it: "#urgent." is the tag "#urgent"). metaSpans
// additionally requires it to start the line or follow whitespace and
// not to be all digits, as Obsidian does: "#12)", "#45," and "#123."
// are issue references in plain text, not tags.
var tagRe = regexp.MustCompile(`#[\p{L}\p{N}_/-]+`)

// leadingNumberRe matches a leading "<digits> " prefix, as used by note
// filenames ("42 Yandex OAuth.md").
var leadingNumberRe = regexp.MustCompile(`^[0-9]+ `)

// isCardStart reports whether content (a line with its EOL already
// stripped) starts a card.
func isCardStart(content string) bool {
	return cardStartRe.MatchString(content)
}

// span is a [start, end) byte range within a string.
type span struct{ start, end int }

func (s span) overlaps(o span) bool { return s.start < o.end && o.start < s.end }

func overlapsAny(s span, list []span) bool {
	for _, o := range list {
		if s.overlaps(o) {
			return true
		}
	}
	return false
}

// findLink returns the submatch indices (as linkRe's) of the card's
// link in s: the first wikilink that isn't a "@[[date]]" token, or nil.
func findLink(s string) []int {
	for _, m := range linkRe.FindAllStringSubmatchIndex(s, -1) {
		if m[0] > 0 && s[m[0]-1] == '@' {
			continue
		}
		return m
	}
	return nil
}

// metaSpans returns the byte ranges of s's metadata tokens (see
// Card.Meta), in order. Tokens inside an inline code span or inside the
// card's own [[link]] (e.g. "[[Note#Heading]]") don't count.
func metaSpans(s string) []span {
	var protected []span
	for _, m := range codeRe.FindAllStringIndex(s, -1) {
		protected = append(protected, span{m[0], m[1]})
	}
	for _, m := range linkRe.FindAllStringIndex(s, -1) {
		if m[0] > 0 && s[m[0]-1] == '@' {
			continue
		}
		protected = append(protected, span{m[0], m[1]})
	}

	var out []span
	for _, m := range dateRe.FindAllStringIndex(s, -1) {
		if sp := (span{m[0], m[1]}); !overlapsAny(sp, protected) {
			out = append(out, sp)
		}
	}
	for _, m := range tagRe.FindAllStringIndex(s, -1) {
		sp := span{m[0], m[1]}
		if prev, _ := utf8.DecodeLastRuneInString(s[:m[0]]); m[0] > 0 && !unicode.IsSpace(prev) {
			continue
		}
		if isAllDigits(s[m[0]+1 : m[1]]) {
			continue
		}
		if overlapsAny(sp, protected) || overlapsAny(sp, out) {
			continue
		}
		out = append(out, sp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// cutSpans returns s with every span in cut removed, its whitespace
// collapsed to single spaces and trimmed.
func cutSpans(s string, cut []span) string {
	sort.Slice(cut, func(i, j int) bool { return cut[i].start < cut[j].start })
	var b strings.Builder
	pos := 0
	for _, c := range cut {
		if c.start < pos {
			continue
		}
		b.WriteString(s[pos:c.start])
		b.WriteByte(' ')
		pos = c.end
	}
	b.WriteString(s[pos:])
	return strings.Join(strings.Fields(b.String()), " ")
}

// StripMeta returns s without its Obsidian Kanban metadata tokens
// (dates, times, tags — see Card.Meta), whitespace collapsed. It's the
// title rule for link-less cards applied to arbitrary text, e.g. a
// remote issue title.
func StripMeta(s string) string {
	return cutSpans(s, metaSpans(s))
}

// parseCardFirstLine fills in card's Checked, IssueID, Link, Alias,
// Title and Meta fields by parsing content, the card's first line (EOL already
// stripped). See spec §6.2 for the exact rules.
func parseCardFirstLine(content string, card *Card) {
	m := cardStartRe.FindStringSubmatchIndex(content)
	marker := content[m[2]:m[3]]
	card.Checked = marker == "x" || marker == "X"
	rest := content[m[1]:]

	idMatches := idRe.FindAllStringSubmatchIndex(rest, -1)
	if len(idMatches) > 0 {
		last := idMatches[len(idMatches)-1]
		card.IssueID, _ = strconv.Atoi(rest[last[2]:last[3]])
	}

	meta := metaSpans(rest)
	card.Meta = nil
	for _, sp := range meta {
		card.Meta = append(card.Meta, rest[sp.start:sp.end])
	}

	if linkMatch := findLink(rest); linkMatch != nil {
		target := rest[linkMatch[2]:linkMatch[3]]
		var alias string
		if linkMatch[4] != -1 {
			alias = rest[linkMatch[4]:linkMatch[5]]
		}
		card.Link = target
		card.Alias = alias
		if alias != "" {
			card.Title = alias
		} else {
			card.Title = leadingNumberRe.ReplaceAllString(basename(target), "")
		}
		return
	}

	cut := meta
	if len(idMatches) > 0 {
		last := idMatches[len(idMatches)-1]
		cut = append(append([]span(nil), meta...), span{last[0], last[1]})
	}
	card.Title = cutSpans(rest, cut)
}

// basename returns the last "/"-separated segment of s.
func basename(s string) string {
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		return s[idx+1:]
	}
	return s
}
