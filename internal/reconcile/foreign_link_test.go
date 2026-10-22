package reconcile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kanban-sync/internal/config"
	"kanban-sync/internal/domain"
	"kanban-sync/internal/provider/fake"
)

// Cards that link the user's own notes (residual review R3). A link is
// yi-managed iff its basename starts with "<id> "; any other link is
// the user's: its file is never renamed, the link never rewritten, and
// its title is the alias (else the basename).

const designDocBody = "# Design doc\n\nThe user's own note.\n"

// foreignLinkProject sets up a project whose Kanban.md is src and with
// the user's note Docs/Design doc.md, linked by shortest path.
func foreignLinkProject(t *testing.T, src string) (*config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := applyTestConfig(t, dir, "always")
	writeKanban(t, cfg.KanbanPath, src)
	doc := filepath.Join(dir, "Docs", "Design doc.md")
	if err := os.MkdirAll(filepath.Dir(doc), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(doc, []byte(designDocBody), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg, doc
}

// syncRoundLog is syncRound, also returning the plan's warnings and
// what Apply logged.
func syncRoundLog(t *testing.T, cfg *config.Config, p *fake.Provider, push bool) (Plan, string) {
	t.Helper()
	a, board, logBuf := newApplier(t, cfg, p)
	ctx := context.Background()
	remote, missing, err := FetchRemote(ctx, p, board)
	if err != nil {
		t.Fatalf("FetchRemote() error = %v", err)
	}
	in := Input{Cfg: cfg, Board: board, Remote: remote, Missing: missing, HasNote: hasNoteFn(a)}
	plan := PlanPull(in)
	if push {
		plan = PlanPush(in)
	}
	if err := a.Apply(ctx, plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	return plan, logBuf.String()
}

func assertDocUntouched(t *testing.T, doc string) {
	t.Helper()
	data, err := os.ReadFile(doc)
	if err != nil {
		t.Fatalf("the user's note is gone: %v", err)
	}
	if string(data) != designDocBody {
		t.Errorf("the user's note changed: %q", data)
	}
	if entries, _ := os.ReadDir(filepath.Dir(doc)); len(entries) != 1 {
		t.Errorf("Docs/ has %d entries, want only Design doc.md", len(entries))
	}
}

func TestForeignLinkSteady(t *testing.T) {
	src := "## Backlog\n\n- [ ] [[Design doc|Login work]] @{2026-10-30} `#7`\n\n## Todo\n\n## In Progress\n\n## Completed\n"
	cfg, doc := foreignLinkProject(t, src)
	p := fake.New()
	p.Seed(domain.Issue{ID: 7, Title: "Login work", State: domain.IssueOpen, Labels: []string{"status:backlog"}})

	for _, push := range []bool{false, true} {
		plan, log := syncRoundLog(t, cfg, p, push)
		if !plan.Empty() || len(plan.Warnings) != 0 || log != "" {
			t.Errorf("push=%v: plan=%#v warnings=%q log=%q, want nothing", push, plan.Changes, plan.Warnings, log)
		}
	}
	if got := readKanban(t, cfg); got != src {
		t.Errorf("Kanban.md = %q, want unchanged", got)
	}
	if entries, _ := os.ReadDir(cfg.TasksDir); len(entries) != 0 {
		t.Errorf("Tasks/ has %d entries, want no note created", len(entries))
	}
	assertDocUntouched(t, doc)
}

func TestForeignLinkPullRenameSetsAlias(t *testing.T) {
	cases := []struct{ name, card, want string }{
		{"replace alias", "- [ ] [[Design doc|Login work]] @{2026-10-30} `#7`", "- [ ] [[Design doc|Login v2]] @{2026-10-30} `#7`\n"},
		{"add alias", "- [ ] [[Design doc]] @{2026-10-30} `#7`", "- [ ] [[Design doc|Login v2]] @{2026-10-30} `#7`\n"},
		{"unresolved link", "- [ ] [[Elsewhere/Spec|Login work]] `#7`", "- [ ] [[Elsewhere/Spec|Login v2]] `#7`\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, doc := foreignLinkProject(t, "## Backlog\n\n"+c.card+"\n\n## Todo\n\n## In Progress\n\n## Completed\n")
			p := fake.New()
			p.Seed(domain.Issue{ID: 7, Title: "Login v2", State: domain.IssueOpen, Labels: []string{"status:backlog"}})

			plan, log := syncRoundLog(t, cfg, p, false)
			if len(plan.Changes) != 1 {
				t.Fatalf("pull = %#v, want one RenameLocal", plan.Changes)
			}
			if strings.Contains(log, "warning") {
				t.Errorf("log = %q, want no warning (no file was renamed)", log)
			}
			if got := readKanban(t, cfg); !strings.Contains(got, c.want) {
				t.Errorf("Kanban.md = %q, want it to contain %q", got, c.want)
			}
			assertDocUntouched(t, doc)
			if entries, _ := os.ReadDir(cfg.TasksDir); len(entries) != 0 {
				t.Errorf("Tasks/ has %d entries, want no note created", len(entries))
			}
			assertSteady(t, cfg, p)
		})
	}
}

func TestForeignLinkPullRenameDropsBrackets(t *testing.T) {
	cfg, doc := foreignLinkProject(t, "## Backlog\n\n- [ ] [[Design doc|Login work]] `#7`\n\n## Todo\n\n## In Progress\n\n## Completed\n")
	p := fake.New()
	p.Seed(domain.Issue{ID: 7, Title: "Fix [x] bug", State: domain.IssueOpen, Labels: []string{"status:backlog"}})

	syncRound(t, cfg, p, false)
	if got, want := readKanban(t, cfg), "- [ ] [[Design doc|Fix x bug]] `#7`\n"; !strings.Contains(got, want) {
		t.Errorf("Kanban.md = %q, want it to contain %q", got, want)
	}
	assertDocUntouched(t, doc)
	assertSteady(t, cfg, p)
}

func TestForeignLinkPushRenamesRemoteToAlias(t *testing.T) {
	cfg, doc := foreignLinkProject(t, "## Backlog\n\n- [ ] [[Design doc|Login work]] `#7`\n\n## Todo\n\n## In Progress\n\n## Completed\n")
	p := fake.New()
	p.Seed(domain.Issue{ID: 7, Title: "Old name", State: domain.IssueOpen, Labels: []string{"status:backlog"}})

	syncRound(t, cfg, p, true)
	if got := p.Issue(7).Title; got != "Login work" {
		t.Errorf("GitHub title = %q, want the alias %q", got, "Login work")
	}
	assertDocUntouched(t, doc)
	assertSteady(t, cfg, p)
}

func TestLocalTitleManagedVsForeign(t *testing.T) {
	cases := map[string]string{
		"- [ ] [[Tasks/42 Yandex OAuth]] `#42`":       "Yandex OAuth",
		"- [ ] [[Tasks/42 Yandex OAuth|OAuth]] `#42`": "Yandex OAuth", // managed: alias is display-only
		"- [ ] [[42 Yandex OAuth|OAuth]] `#42`":       "Yandex OAuth",
		"- [ ] [[Design doc|Login work]] `#42`":       "Login work",
		"- [ ] [[Docs/Design doc]] `#42`":             "Design doc",
		"- [ ] [[Tasks/41 Other]] `#42`":              "41 Other", // another issue's id: not managed
		"- [ ] [[Tasks/Draft idea|Idea]]":             "Idea",
		"- [ ] Plain title `#42`":                     "Plain title",
	}
	for line, want := range cases {
		board := buildBoard(t, "## Backlog\n\n"+line+"\n")
		if got := localTitle(board.Cards()[0]); got != want {
			t.Errorf("%q: localTitle = %q, want %q", line, got, want)
		}
	}
}

// An id-less card linking the user's note creates its issue under the
// card's visible title (the alias): once it has an id, its link stays
// foreign, so the same title is what later pushes compare against.
func TestPushCreateForeignLinkUsesAliasAndIsSteady(t *testing.T) {
	cfg, doc := foreignLinkProject(t, "## Backlog\n\n- [ ] [[Design doc|Login work]]\n\n## Todo\n\n## In Progress\n\n## Completed\n")
	p := fake.New()

	syncRound(t, cfg, p, true)
	if got := p.Issue(1).Title; got != "Login work" {
		t.Errorf("GitHub title = %q, want %q", got, "Login work")
	}
	if got, want := readKanban(t, cfg), "- [ ] [[Design doc|Login work]] `#1`\n"; !strings.Contains(got, want) {
		t.Errorf("Kanban.md = %q, want it to contain %q", got, want)
	}
	assertDocUntouched(t, doc)
	assertSteady(t, cfg, p)
}
