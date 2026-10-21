// Package domain holds the core types shared across yi: issues and the
// kanban statuses they map to. It has no dependencies on any other
// package in this module.
package domain

import (
	"fmt"
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
}

// HasLabel reports whether the issue carries the given label.
func (i Issue) HasLabel(label string) bool {
	for _, l := range i.Labels {
		if l == label {
			return true
		}
	}
	return false
}

// StatusKey identifies a kanban column/status in the config.
type StatusKey string

// Status maps a kanban column to an issue label.
type Status struct {
	Key        StatusKey
	Heading    string
	Label      string
	CloseIssue bool
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
