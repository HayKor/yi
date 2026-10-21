package config

import (
	"fmt"
	"strings"

	"kanban-sync/internal/branch"
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

// validate checks cfg (already defaulted) against raw (the pre-default
// values straight from YAML, needed to tell "the user set this to an
// invalid value" apart from "this was left to default"). It collects
// every problem instead of stopping at the first.
func validate(cfg *Config, raw rawConfig) error {
	var problems []string

	if cfg.Repository == "" {
		problems = append(problems, "repository is required")
	}

	if len(cfg.Statuses) == 0 {
		problems = append(problems, "statuses: at least one status is required")
	} else {
		seenHeadings := map[string]bool{}
		seenLabels := map[string]bool{}
		closeCount := 0
		for _, s := range cfg.Statuses {
			if seenHeadings[s.Heading] {
				problems = append(problems, fmt.Sprintf("duplicate heading %q", s.Heading))
			}
			seenHeadings[s.Heading] = true

			if seenLabels[s.Label] {
				problems = append(problems, fmt.Sprintf("duplicate label %q", s.Label))
			}
			seenLabels[s.Label] = true

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
			problems = append(problems, fmt.Sprintf("defaultStatus: unknown status %q", raw.DefaultStatus))
		}
	}
	if raw.StartStatus != "" {
		if _, ok := cfg.Status(cfg.StartStatus); !ok {
			problems = append(problems, fmt.Sprintf("startStatus: unknown status %q", raw.StartStatus))
		}
	}
	if raw.DoneStatus != "" {
		if st, ok := cfg.Status(cfg.DoneStatus); !ok {
			problems = append(problems, fmt.Sprintf("doneStatus: unknown status %q", raw.DoneStatus))
		} else if !st.CloseIssue {
			problems = append(problems, fmt.Sprintf("doneStatus %q must have closeIssue: true", raw.DoneStatus))
		}
	}

	if cfg.Notes != NotesAlways && cfg.Notes != NotesOnDemand {
		problems = append(problems, "notes.create: must be always or on-demand")
	}

	if raw.Git != nil {
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
