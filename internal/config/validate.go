package config

import (
	"fmt"
	"strings"

	"github.com/HayKor/yi/internal/branch"
	"github.com/HayKor/yi/internal/domain"
)

// ValidationError reports every problem found while validating a
// config, rather than just the first.
type ValidationError struct {
	Path     string
	Problems []string
}

func (e *ValidationError) Error() string {
	lines := make([]string, 0, len(e.Problems)+1)
	lines = append(lines, fmt.Sprintf("invalid config %s:", e.Path))
	for _, p := range e.Problems {
		lines = append(lines, "  - "+p)
	}
	return strings.Join(lines, "\n")
}

// initPlaceholderRepository is the repository `yi init` writes into a
// new .issue-sync.yml for the user to replace.
const initPlaceholderRepository = "owner/repo"

// validate checks cfg (already defaulted) against raw (the pre-default
// values straight from YAML, needed to tell "the user set this to an
// invalid value" apart from "this was left to default"). It collects
// every problem instead of stopping at the first.
func validate(cfg *Config, raw rawConfig) error {
	var problems []string

	switch cfg.Repository {
	case "":
		problems = append(problems, "repository is required")
	case initPlaceholderRepository:
		problems = append(problems, `repository is still the "owner/repo" placeholder; set it to your repository`)
	}

	if len(cfg.Statuses) == 0 {
		problems = append(problems, "statuses: at least one status is required")
	} else {
		seenHeadings := map[string]bool{}
		seenLabels := map[string]bool{}
		closeCount := 0
		for _, s := range cfg.Statuses {
			if s.Heading == "" {
				problems = append(problems, fmt.Sprintf("statuses.%s: heading is required", s.Key))
			} else if h := domain.NormalizeHeading(s.Heading); seenHeadings[h] {
				// Same rule the board uses to match lanes: two
				// headings that normalise alike would map to one lane.
				problems = append(problems, fmt.Sprintf("duplicate heading %q", s.Heading))
			} else {
				seenHeadings[h] = true
			}

			if s.Label == "" {
				problems = append(problems, fmt.Sprintf("statuses.%s: label is required", s.Key))
			} else if l := strings.ToLower(s.Label); seenLabels[l] {
				// Label names are case-insensitive (domain.Issue.HasLabel).
				problems = append(problems, fmt.Sprintf("duplicate label %q", s.Label))
			} else {
				seenLabels[l] = true
			}

			if s.CloseIssue {
				closeCount++
			}
		}
		if closeCount != 1 {
			problems = append(problems, "exactly one status must have closeIssue: true")
		}
	}

	if raw.DefaultStatus != "" {
		if _, ok := cfg.Status(cfg.DefaultStatus); !ok {
			problems = append(problems, unknownStatus("defaultStatus", raw.DefaultStatus, cfg.Statuses))
		}
	}
	if raw.StartStatus != "" {
		if _, ok := cfg.Status(cfg.StartStatus); !ok {
			problems = append(problems, unknownStatus("startStatus", raw.StartStatus, cfg.Statuses))
		}
	}
	if raw.DoneStatus != "" {
		if st, ok := cfg.Status(cfg.DoneStatus); !ok {
			problems = append(problems, unknownStatus("doneStatus", raw.DoneStatus, cfg.Statuses))
		} else if !st.CloseIssue {
			problems = append(problems, fmt.Sprintf("doneStatus %q must have closeIssue: true", raw.DoneStatus))
		}
	}

	if cfg.Notes != NotesAlways && cfg.Notes != NotesOnDemand {
		problems = append(problems, "notes.create: must be always or on-demand")
	}

	if raw.Git != nil {
		if raw.Git.Repository == "" {
			problems = append(problems, "git.repository is required")
		}
		tmpl := raw.Git.Branches.Template
		if tmpl == "" {
			tmpl = branch.DefaultTemplate
		}
		if err := branch.ValidateTemplate(tmpl); err != nil {
			problems = append(problems, fmt.Sprintf("git.branches.template: %s", err))
		}
	}

	if len(problems) == 0 {
		return nil
	}
	return &ValidationError{Path: cfg.Path, Problems: problems}
}

// unknownStatus describes a status reference (defaultStatus & co.)
// that names no configured status key. Those fields take a key, so
// when value is a status's label or heading instead, it says which key
// was meant; otherwise it lists the valid keys.
func unknownStatus(field, value string, statuses []domain.Status) string {
	msg := fmt.Sprintf("%s: unknown status %q", field, value)
	var keys []string
	for _, s := range statuses {
		if strings.EqualFold(value, s.Label) || domain.NormalizeHeading(value) == domain.NormalizeHeading(s.Heading) {
			return fmt.Sprintf("%s (use the status key %q, not its label or heading)", msg, s.Key)
		}
		keys = append(keys, string(s.Key))
	}
	if len(keys) == 0 {
		return msg
	}
	return fmt.Sprintf("%s (one of: %s)", msg, strings.Join(keys, ", "))
}
