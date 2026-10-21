// Package branch builds git branch names for issues from a configurable
// template. It depends only on domain.
package branch

import (
	"fmt"
	"regexp"
)

// DefaultTemplate is the branch name template used when a project's git
// section doesn't set one.
const DefaultTemplate = "{type}/{issue}-{slug}"

// DefaultType is the branch type used when a project's git section
// doesn't set one.
const DefaultType = "feat"

// placeholderPattern matches any {...} placeholder in a branch template.
var placeholderPattern = regexp.MustCompile(`\{[^{}]*\}`)

// knownPlaceholders are the only placeholders a branch template may use.
var knownPlaceholders = map[string]bool{
	"{type}":  true,
	"{issue}": true,
	"{slug}":  true,
}

// ValidateTemplate reports an error if t contains any placeholder other
// than {type}, {issue}, or {slug}.
func ValidateTemplate(t string) error {
	for _, placeholder := range placeholderPattern.FindAllString(t, -1) {
		if !knownPlaceholders[placeholder] {
			return fmt.Errorf("unknown placeholder %s", placeholder)
		}
	}
	return nil
}
