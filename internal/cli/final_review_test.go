package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HayKor/yi/internal/domain"
)

// End-to-end regression tests for the final review findings, driven
// through Main with the fake provider.

// mutations returns the create/update calls the fake provider saw.
func (e *testEnv) mutations() []string {
	var out []string
	for _, c := range e.prov.Calls() {
		if strings.HasPrefix(c, "create ") || strings.HasPrefix(c, "update ") {
			out = append(out, c)
		}
	}
	return out
}

func (e *testEnv) mustRun(args ...string) {
	e.t.Helper()
	if code := e.run(args...); code != 0 {
		e.t.Fatalf("Main(%v) = %d, want 0; stdout=%s stderr=%s", args, code, e.stdout.String(), e.stderr.String())
	}
}

func (e *testEnv) noteNames() []string {
	e.t.Helper()
	entries, err := os.ReadDir(filepath.Join(e.dir, "Tasks"))
	if err != nil {
		e.t.Fatal(err)
	}
	var names []string
	for _, en := range entries {
		names = append(names, en.Name())
	}
	return names
}

// --- C1: card metadata ---------------------------------------------------

func TestE2EPullKeepsCardDate(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n- [ ] Write docs `#5` @{2026-11-01}\n\n## Todo\n\n## In Progress\n\n## Completed\n")
	env.prov.Seed(domain.Issue{ID: 5, Title: "Write docs", State: domain.IssueOpen, Labels: []string{"status:backlog"}})

	env.mustRun("pull")
	if got, want := env.kanban(), "- [ ] [[Tasks/5 Write docs]] @{2026-11-01} `#5`\n"; !strings.Contains(got, want) {
		t.Errorf("Kanban.md = %q, want it to contain %q", got, want)
	}
	if names := env.noteNames(); len(names) != 1 || names[0] != "5 Write docs.md" {
		t.Errorf("notes = %q, want [5 Write docs.md]", names)
	}

	before := env.kanban()
	env.mustRun("pull")
	if !strings.Contains(env.stdout.String(), "No changes.") || env.kanban() != before {
		t.Errorf("second pull: stdout=%q, Kanban.md changed=%v; want no changes", env.stdout.String(), env.kanban() != before)
	}
	env.mustRun("push", "--yes")
	if m := env.mutations(); len(m) != 0 {
		t.Errorf("push after pull made %v, want no remote changes", m)
	}
}

func TestE2EPushKeepsMetadataOutOfTitle(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n- [ ] Fix login @{2026-10-30} #urgent\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	env.mustRun("push", "--yes")
	if got := env.prov.Issue(1).Title; got != "Fix login" {
		t.Errorf("GitHub title = %q, want %q", got, "Fix login")
	}
	if names := env.noteNames(); len(names) != 1 || names[0] != "1 Fix login.md" {
		t.Errorf("notes = %q, want [1 Fix login.md]", names)
	}
	if got, want := env.kanban(), "- [ ] [[Tasks/1 Fix login]] @{2026-10-30} #urgent `#1`\n"; !strings.Contains(got, want) {
		t.Errorf("Kanban.md = %q, want it to contain %q", got, want)
	}

	before := env.kanban()
	env.mustRun("push", "--yes")
	env.mustRun("pull")
	if m := env.mutations(); len(m) != 1 {
		t.Errorf("mutations = %v, want only the first create", m)
	}
	if env.kanban() != before {
		t.Errorf("Kanban.md changed on re-sync: %q", env.kanban())
	}
}

func TestE2EPushDatedCardNoRename(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n- [ ] Fix login @{2026-10-30} `#6`\n\n## Todo\n\n## In Progress\n\n## Completed\n")
	env.prov.Seed(domain.Issue{ID: 6, Title: "Fix login", State: domain.IssueOpen, Labels: []string{"status:backlog"}})

	env.mustRun("push", "--yes")
	if m := env.mutations(); len(m) != 0 {
		t.Errorf("push made %v, want none (no RenameRemote with the date)", m)
	}
	if got := env.prov.Issue(6).Title; got != "Fix login" {
		t.Errorf("GitHub title = %q, want unchanged", got)
	}
}

func TestE2EShortestPathLinkNotRelinked(t *testing.T) {
	src := "## Backlog\n\n- [ ] [[Design doc|Login work]] @{2026-11-01} `#7`\n\n## Todo\n\n## In Progress\n\n## Completed\n"
	env := newTestEnv(t, src)
	env.prov.Seed(domain.Issue{ID: 7, Title: "Login work", State: domain.IssueOpen, Labels: []string{"status:backlog"}})

	env.mustRun("pull")
	if got := env.kanban(); got != src {
		t.Errorf("Kanban.md = %q, want it unchanged", got)
	}
	if names := env.noteNames(); len(names) != 0 {
		t.Errorf("notes = %q, want none created", names)
	}
}

// --- C2: failed `yi new` -------------------------------------------------

func TestE2ENewCreateFailureWritesNothing(t *testing.T) {
	src := "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n"
	env := newTestEnv(t, src)
	env.prov.FailAfter(0, errors.New("tracker down"))

	if code := env.run("new", "Flaky task"); code != 1 {
		t.Fatalf("Main(new) = %d, want 1; stderr=%s", code, env.stderr.String())
	}
	if !strings.Contains(env.stderr.String(), "tracker down") {
		t.Errorf("stderr = %q, want the create error", env.stderr.String())
	}
	if got := env.kanban(); got != src {
		t.Errorf("Kanban.md = %q, want byte-identical %q", got, src)
	}
	if names := env.noteNames(); len(names) != 0 {
		t.Errorf("notes = %q, want none", names)
	}

	env.prov.FailAfter(-1, nil)
	env.mustRun("new", "Flaky task")
	env.mustRun("push", "--yes")

	if m := env.mutations(); len(m) != 2 || m[0] != "create Flaky task" || m[1] != "create Flaky task" {
		// m[0] is the failed attempt (recorded, but it changed nothing).
		t.Errorf("mutations = %v, want [failed create, one successful create] and nothing from push", m)
	}
	if env.prov.Issue(1).Title != "Flaky task" || env.prov.Issue(2).ID != 0 {
		t.Errorf("issues: #1=%+v #2=%+v, want exactly one issue", env.prov.Issue(1), env.prov.Issue(2))
	}
	if strings.Count(env.kanban(), "`#") != 1 {
		t.Errorf("Kanban.md = %q, want exactly one card with an id", env.kanban())
	}
}

func TestE2ENewCloseFailureStillAddsCard(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n")
	env.prov.FailAfter(1, errors.New("close failed")) // create succeeds, close fails

	if code := env.run("new", "Already done", "--status", "completed"); code != 1 {
		t.Fatalf("Main(new) = %d, want 1; stderr=%s", code, env.stderr.String())
	}
	if !strings.Contains(env.stderr.String(), "close failed") {
		t.Errorf("stderr = %q, want the close error", env.stderr.String())
	}
	if got, want := env.kanban(), "- [x] [[Tasks/1 Already done]] `#1`"; !strings.Contains(got, want) {
		t.Errorf("Kanban.md = %q, want it to contain %q", got, want)
	}

	env.prov.FailAfter(-1, nil)
	env.mustRun("push", "--yes")
	if st := env.prov.Issue(1).State; st != domain.IssueClosed {
		t.Errorf("issue #1 state = %v after push, want closed", st)
	}
	if env.prov.Issue(2).ID != 0 {
		t.Errorf("push created a second issue: %+v", env.prov.Issue(2))
	}
}

func TestE2ENewInCloseStatusClosesIssue(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	env.mustRun("new", "Already done", "--status", "completed")
	if st := env.prov.Issue(1).State; st != domain.IssueClosed {
		t.Errorf("issue #1 state = %v, want closed", st)
	}
	if !strings.Contains(env.stdout.String(), "✓ Created #1 https://fake/issues/1") {
		t.Errorf("stdout = %q, want the created line", env.stdout.String())
	}
	if calls := env.prov.Calls(); len(calls) != 2 || calls[0] != "create Already done" || calls[1] != "update 1" {
		t.Errorf("Calls() = %v, want [create, update] (no extra GetIssue)", calls)
	}
}

// --- I1: lanes marked complete --------------------------------------------

func TestE2EDoneIntoEmptyCompleteLane(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n- [ ] Yandex OAuth `#42`\n\n## Completed\n\n**Complete**\n\n\n")
	env.prov.Seed(domain.Issue{ID: 42, Title: "Yandex OAuth", State: domain.IssueOpen, Labels: []string{"status:in-progress"}})

	env.mustRun("done", "42")
	want := "## Completed\n\n**Complete**\n- [x] Yandex OAuth `#42`\n\n\n"
	if got := env.kanban(); !strings.HasSuffix(got, want) {
		t.Errorf("Kanban.md = %q, want it to end with %q (card directly below **Complete**)", got, want)
	}
}

// --- I3: headings that only differ in case ---------------------------------

func TestE2ECaseDuplicateHeadingsRejected(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n")
	cfg := strings.Replace(testConfigYAML, "heading: Backlog", "heading: todo", 1)
	if err := os.WriteFile(filepath.Join(env.dir, ".issue-sync.yml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := env.run("pull"); code != 1 {
		t.Fatalf("Main(pull) = %d, want 1", code)
	}
	if !strings.Contains(env.stderr.String(), `duplicate heading "Todo"`) {
		t.Errorf("stderr = %q, want the duplicate heading problem", env.stderr.String())
	}
}

// --- M2: label case ---------------------------------------------------------

func TestE2ELabelCaseIsNoop(t *testing.T) {
	src := "## Backlog\n\n## Todo\n\n- [ ] Task `#3`\n\n## In Progress\n\n## Completed\n"
	env := newTestEnv(t, src)
	env.prov.Seed(domain.Issue{ID: 3, Title: "Task", State: domain.IssueOpen, Labels: []string{"Status:Todo"}})
	// on-demand notes, so a pull has nothing else to do.
	cfg := strings.Replace(testConfigYAML, "create: always", "create: on-demand", 1)
	if err := os.WriteFile(filepath.Join(env.dir, ".issue-sync.yml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	env.mustRun("push", "--yes")
	if m := env.mutations(); len(m) != 0 {
		t.Errorf("push made %v, want none", m)
	}
	env.mustRun("pull")
	if got := env.kanban(); got != src {
		t.Errorf("Kanban.md = %q, want unchanged (Status:Todo maps to todo)", got)
	}
}

// --- M3: done on an unchecked card already in the done lane -------------------

func TestE2EDoneChecksCardAlreadyInDoneLane(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n\n- [ ] Yandex OAuth `#42`\n")
	env.prov.Seed(domain.Issue{ID: 42, Title: "Yandex OAuth", State: domain.IssueClosed, Labels: []string{"status:completed"}})

	env.mustRun("done", "42")
	if got := env.kanban(); !strings.Contains(got, "- [x] Yandex OAuth `#42`") {
		t.Errorf("Kanban.md = %q, want the card checked", got)
	}
}
