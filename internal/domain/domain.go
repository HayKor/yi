// Package domain holds the core types shared across yi: issues and the
// kanban statuses they map to. It has no dependencies on any other
// package in this module.
package domain

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// IssueState is the open/closed state of an Issue.
type IssueState int

const (
	IssueOpen IssueState = iota
	IssueClosed
)

// String returns "open" or "closed".
func (s IssueState) String() string {
	if s == IssueClosed {
		return "closed"
	}
	return "open"
}

// Issue is a tracked issue, identified by its provider-assigned number
// (GitHub issue number, GitLab iid, ...). yi never mints its own IDs.
type Issue struct {
	ID        int
	Title     string
	State     IssueState
	Labels    []string
	URL       string
	UpdatedAt time.Time

	// HasStatusField is set by a provider that keeps issue status in a
	// field of the tracker (a GitHub Project's Status) instead of in
	// labels. Status is then that field's value: "" if the issue isn't
	// on the project or the field is empty.
	HasStatusField bool
	Status         string
}

// HasLabel reports whether the issue carries the given label,
// compared case-insensitively (GitHub label names are).
func (i Issue) HasLabel(label string) bool {
	_, ok := i.FindLabel(label)
	return ok
}

// FindLabel returns the issue's own spelling of label (matched
// case-insensitively), so a caller removing it can name it exactly as
// the tracker has it.
func (i Issue) FindLabel(label string) (string, bool) {
	for _, l := range i.Labels {
		if strings.EqualFold(l, label) {
			return l, true
		}
	}
	return "", false
}

// StatusKey identifies a kanban column/status in the config.
type StatusKey string

// Status maps a kanban column to an issue label, or to a value of the
// tracker's status field (ProjectStatus) when the provider has one.
type Status struct {
	Key           StatusKey
	Heading       string
	Label         string
	ProjectStatus string
	CloseIssue    bool
}

// SameStatusName reports whether two status-field values name the same
// option: compared case-insensitively, ignoring surrounding spaces.
func SameStatusName(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// wipLimitRe matches the Kanban plugin's max-items suffix on a lane
// heading, e.g. the " (3)" in "In Progress (3)".
var wipLimitRe = regexp.MustCompile(`\s*\(\d+\)$`)

// NormalizeHeading reduces a kanban column heading to the form used to
// match a board section against a configured Status.Heading: trimmed,
// without a trailing WIP-limit suffix ("In Progress (3)"), and
// lower-cased. Two headings name the same column iff their normalized
// forms are equal; config validation uses the same rule to reject
// headings that would collide on the board.
func NormalizeHeading(s string) string {
	s = strings.TrimSpace(s)
	s = wipLimitRe.ReplaceAllString(s, "")
	return strings.ToLower(strings.TrimSpace(s))
}

// ParseIssueID parses an issue ID from user input, accepting a bare
// number ("42"), a hash-prefixed number ("#42"), and surrounding
// whitespace (" #7 "). It rejects zero, negative numbers, and anything
// that isn't a plain positive integer.
func ParseIssueID(s string) (int, error) {
	trimmed := strings.TrimSpace(s)
	digits := strings.TrimPrefix(trimmed, "#")
	if digits == "" {
		return 0, fmt.Errorf("invalid issue id: %q", s)
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return 0, fmt.Errorf("invalid issue id: %q", s)
	}
	if n <= 0 {
		return 0, fmt.Errorf("invalid issue id: %q", s)
	}
	return n, nil
}
