package cli

import (
	"os"
	"path/filepath"
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

// --- R2: yi new in a closeIssue status whose card can't be added ----------

// squatNote makes the note path for issue #1 titled title belong to
// another issue, so adding #1's card (which creates its note) fails.
func (e *testEnv) squatNote(title string) {
	e.t.Helper()
	p := filepath.Join(e.dir, "Tasks", "1 "+title+".md")
	if err := os.WriteFile(p, []byte("---\nissue: 99\n---\n\n# Someone else\n"), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func TestE2ENewClosedIssueCardFailureGivesCardLine(t *testing.T) {
	src := "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n"
	env := newTestEnv(t, src)
	env.squatNote("Already done")

	if code := env.run("new", "Already done", "--status", "completed"); code != 1 {
		t.Fatalf("Main(new) = %d, want 1; stderr=%s", code, env.stderr.String())
	}
	if st := env.prov.Issue(1).State; st != domain.IssueClosed {
		t.Fatalf("issue #1 state = %v, want closed", st)
	}
	stderr := env.stderr.String()
	if want := "add this line under \"## Completed\": - [x] Already done `#1`"; !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr, want)
	}
	if strings.Contains(stderr, "yi pull") {
		t.Errorf("stderr = %q: pull never adds a closed issue, so it must not be suggested", stderr)
	}
	if got := env.kanban(); got != src {
		t.Errorf("Kanban.md = %q, want unchanged", got)
	}
}

func TestE2ENewOpenIssueCardFailureSuggestsPull(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n")
	env.squatNote("Open task")

	if code := env.run("new", "Open task"); code != 1 {
		t.Fatalf("Main(new) = %d, want 1; stderr=%s", code, env.stderr.String())
	}
	if want := "run yi pull to add it"; !strings.Contains(env.stderr.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", env.stderr.String(), want)
	}
}

// --- R5: push confirmation with no Stdin ----------------------------------

func TestE2EPushConfirmNilStdinAborts(t *testing.T) {
	src := "## Backlog\n\n- [ ] Unpushed\n\n## Todo\n\n## In Progress\n\n## Completed\n"
	env := newTestEnv(t, src)
	env.deps.IsTerminal = true
	env.deps.Stdin = nil

	env.mustRun("push")
	if !strings.Contains(env.stdout.String(), "Aborted.") {
		t.Errorf("stdout = %q, want Aborted.", env.stdout.String())
	}
	if m := env.mutations(); len(m) != 0 {
		t.Errorf("push made %v, want nothing", m)
	}
	if got := env.kanban(); got != src {
		t.Errorf("Kanban.md = %q, want unchanged", got)
	}
}
