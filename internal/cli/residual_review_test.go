package cli

import (
	"strings"
	"testing"

	"kanban-sync/internal/domain"
)

// End-to-end regression tests for the residual review findings, driven
// through Main with the fake provider.

// --- R1: issue references are not tags ------------------------------------

func TestE2EPushKeepsIssueReferenceInTitle(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n- [ ] Regression (see #12)\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	env.mustRun("push", "--yes")
	if got := env.prov.Issue(1).Title; got != "Regression (see #12)" {
		t.Errorf("GitHub title = %q, want %q", got, "Regression (see #12)")
	}
	if got, want := env.kanban(), "- [ ] [[Tasks/1 Regression (see 12)]] `#1`\n"; !strings.Contains(got, want) {
		t.Errorf("Kanban.md = %q, want it to contain %q", got, want)
	}

	before := env.kanban()
	env.mustRun("push", "--dry-run")
	if !strings.Contains(env.stdout.String(), "No changes.") {
		t.Errorf("re-plan push: stdout = %q, want No changes.", env.stdout.String())
	}
	env.mustRun("pull")
	if !strings.Contains(env.stdout.String(), "No changes.") || env.kanban() != before {
		t.Errorf("pull after push: stdout = %q, Kanban.md = %q; want no changes", env.stdout.String(), env.kanban())
	}
}

func TestE2EPullKeepsIssueReferenceInNoteName(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n")
	env.prov.Seed(domain.Issue{ID: 7, Title: "Fix crash (regression from #123)", State: domain.IssueOpen, Labels: []string{"status:backlog"}})

	env.mustRun("pull")
	if names := env.noteNames(); len(names) != 1 || names[0] != "7 Fix crash (regression from 123).md" {
		t.Errorf("notes = %q, want [7 Fix crash (regression from 123).md]", names)
	}
}
