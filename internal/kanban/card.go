package kanban

import (
	"regexp"
	"strconv"
	"strings"

	"kanban-sync/internal/domain"
)

// Card is a single kanban task, parsed from one "- [ ] ..." line (plus any
// indented continuation lines belonging to it).
type Card struct {
	Status  domain.StatusKey // the section the card currently lives in
	IssueID int              // 0 if the card has no `#id` token yet
	Title   string
	Link    string // note target ("Tasks/42 Yandex OAuth"), "" if the card has no [[link]]
	Checked bool

	// Line is the 1-based line number of the card's first line in the
	// file Parse read it from, for use in error messages. Task 5's
	// Move and RewriteFirstLine relocate or rewrite the card without
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
// "[[target]]" or "[[target|alias]]".
var linkRe = regexp.MustCompile(`\[\[([^\]|]+)(?:\|([^\]]+))?\]\]`)

// leadingNumberRe matches a leading "<digits> " prefix, as used by note
// filenames ("42 Yandex OAuth.md").
var leadingNumberRe = regexp.MustCompile(`^[0-9]+ `)

// isCardStart reports whether content (a line with its EOL already
// stripped) starts a card.
func isCardStart(content string) bool {
	return cardStartRe.MatchString(content)
}

// parseCardFirstLine fills in card's Checked, IssueID, Link, and Title
// fields by parsing content, the card's first line (EOL already
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

	if linkMatch := linkRe.FindStringSubmatchIndex(rest); linkMatch != nil {
		target := rest[linkMatch[2]:linkMatch[3]]
		var alias string
		if linkMatch[4] != -1 {
			alias = rest[linkMatch[4]:linkMatch[5]]
		}
		card.Link = target
		if alias != "" {
			card.Title = alias
		} else {
			card.Title = leadingNumberRe.ReplaceAllString(basename(target), "")
		}
		return
	}

	text := rest
	if len(idMatches) > 0 {
		last := idMatches[len(idMatches)-1]
		text = rest[:last[0]] + rest[last[1]:]
	}
	card.Title = strings.TrimSpace(text)
}

// basename returns the last "/"-separated segment of s.
func basename(s string) string {
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		return s[idx+1:]
	}
	return s
}
