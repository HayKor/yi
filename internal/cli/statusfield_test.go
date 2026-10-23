package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/HayKor/yi/internal/domain"
	"github.com/HayKor/yi/internal/provider"
	"github.com/HayKor/yi/internal/provider/fake"
)

// statusFieldConfigYAML is testConfigYAML for a provider that keeps
// status in a field (a GitHub Project): no labels, and one column
// mapped to an option named differently from its heading.
const statusFieldConfigYAML = `
provider: fake
repository: my-org/repo

statuses:
  backlog:
    heading: Backlog
  todo:
    heading: Todo
  in-progress:
    heading: In Progress
    projectStatus: Doing
  completed:
    heading: Completed
    closeIssue: true

defaultStatus: backlog
notes:
  create: on-demand
`

const emptyBoard = "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n"

// newStatusFieldEnv is newTestEnv with statusFieldConfigYAML and a
// fake provider in status field mode.
func newStatusFieldEnv(t *testing.T, kanbanSrc string) *testEnv {
	t.Helper()
	env := newTestEnv(t, kanbanSrc)
	if err := os.WriteFile(filepath.Join(env.dir, ".issue-sync.yml"), []byte(statusFieldConfigYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	p := fake.NewWithStatusField()
	env.prov = p
	env.deps.NewProvider = func(context.Context, provider.Config) (provider.IssueProvider, error) {
		return p, nil
	}
	return env
}

// column returns the heading of the board column holding `#id`, or "".
func column(board string, id string) string {
	heading := ""
	for _, line := range strings.Split(board, "\n") {
		if h, ok := strings.CutPrefix(line, "## "); ok {
			heading = h
		}
		if strings.HasSuffix(line, "`#"+id+"`") {
			return heading
		}
	}
	return ""
}

func TestStatusFieldPull(t *testing.T) {
	env := newStatusFieldEnv(t, emptyBoard)
	env.prov.Seed(
		// Labels are ignored when status lives in a field.
		domain.Issue{ID: 1, Title: "On todo", State: domain.IssueOpen, Labels: []string{"status:completed"}, Status: "todo"},
		domain.Issue{ID: 2, Title: "Not on project", State: domain.IssueOpen},
		domain.Issue{ID: 3, Title: "Doing it", State: domain.IssueOpen, Status: "Doing"},
		domain.Issue{ID: 4, Title: "In review", State: domain.IssueOpen, Status: "Review"},
	)

	env.mustRun("pull")
	board := env.kanban()
	for id, want := range map[string]string{"1": "Todo", "2": "Backlog", "3": "In Progress", "4": "Backlog"} {
		if got := column(board, id); got != want {
			t.Errorf("#%s in %q, want %q:\n%s", id, got, want, board)
		}
	}
	if !strings.Contains(env.stdout.String()+env.stderr.String(), `#4 has project status "Review", which no column maps to`) {
		t.Errorf("no warning about the unmapped status:\nstdout=%s\nstderr=%s", env.stdout, env.stderr)
	}

	// A card whose status becomes unmapped stays where it is.
	review := "Review"
	if _, err := env.prov.UpdateIssue(t.Context(), 3, fakeStatus(review)); err != nil {
		t.Fatal(err)
	}
	env.mustRun("pull")
	if got := column(env.kanban(), "3"); got != "In Progress" {
		t.Errorf("#3 moved to %q after its status became unmapped, want it left in In Progress", got)
	}
}

func TestStatusFieldPush(t *testing.T) {
	env := newStatusFieldEnv(t, "## Backlog\n\n## Todo\n\n- [ ] New idea\n\n## In Progress\n\n## Completed\n\n- [x] Finished `#1`\n")
	env.prov.Seed(domain.Issue{ID: 1, Title: "Finished", State: domain.IssueOpen, Labels: []string{"bug", "status:todo"}, Status: "Todo"})

	env.mustRun("push", "--yes")

	one := env.prov.Issue(1)
	if one.State != domain.IssueClosed || one.Status != "Completed" {
		t.Errorf("#1 = state %v, Status %q; want closed, Completed", one.State, one.Status)
	}
	if !slices.Equal(one.Labels, []string{"bug", "status:todo"}) {
		t.Errorf("#1 labels = %v, want them untouched", one.Labels)
	}

	created := env.prov.Issue(2)
	if created.Title != "New idea" || created.Status != "Todo" || len(created.Labels) != 0 {
		t.Errorf("created issue = %+v, want \"New idea\" with Status Todo and no labels", created)
	}
	if !strings.Contains(env.kanban(), "- [ ] New idea `#2`") {
		t.Errorf("card didn't get its id:\n%s", env.kanban())
	}

	env.mustRun("push", "--dry-run")
	if !strings.Contains(env.stdout.String(), "No changes.") {
		t.Errorf("second push not steady:\n%s", env.stdout)
	}
}

func TestStatusFieldNewAndDone(t *testing.T) {
	env := newStatusFieldEnv(t, emptyBoard)

	env.mustRun("new", "Add OAuth", "--status", "in-progress")
	iss := env.prov.Issue(1)
	if iss.Status != "Doing" || len(iss.Labels) != 0 {
		t.Errorf("yi new issue = %+v, want Status Doing and no labels", iss)
	}

	env.mustRun("done", "1")
	iss = env.prov.Issue(1)
	if iss.State != domain.IssueClosed || iss.Status != "Completed" {
		t.Errorf("after yi done = state %v, Status %q; want closed, Completed", iss.State, iss.Status)
	}
	if got := column(env.kanban(), "1"); got != "Completed" {
		t.Errorf("#1 in %q after done, want Completed", got)
	}
}

func fakeStatus(s string) provider.IssueUpdate { return provider.IssueUpdate{Status: &s} }
