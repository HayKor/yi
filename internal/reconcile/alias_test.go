package reconcile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HayKor/yi/internal/domain"
	"github.com/HayKor/yi/internal/provider/fake"
)

// A card linked before yi wrote aliases ("[[Tasks/1 X]]") gains one on
// pull, so the board shows the title instead of the note's path. A
// foreign link, or a card that already has an alias, is left alone.
func TestPullAliasesLegacyManagedLink(t *testing.T) {
	cfg := testConfig(t, "on-demand")
	board := buildBoard(t, "## Backlog\n\n"+
		"- [ ] [[Tasks/1 Removing a Car]] `#1`\n"+
		"- [ ] [[Design doc]] `#2`\n"+
		"- [ ] [[Tasks/3 Garage|Mine]] `#3`\n"+
		"\n## Todo\n\n## In Progress\n\n## Completed\n")

	plan := PlanPull(Input{
		Cfg:   cfg,
		Board: board,
		Remote: map[int]domain.Issue{
			1: {ID: 1, Title: "Removing a Car", State: domain.IssueOpen},
			2: {ID: 2, Title: "Design doc", State: domain.IssueOpen},
			3: {ID: 3, Title: "Garage", State: domain.IssueOpen},
		},
		Missing: map[int]bool{},
		HasNote: allNotes,
	})

	if len(plan.Changes) != 1 {
		t.Fatalf("Changes = %#v, want only an AliasLink for #1", plan.Changes)
	}
	al, ok := plan.Changes[0].(AliasLink)
	if !ok || al.ID != 1 || al.Alias != "Removing a Car" {
		t.Fatalf("Changes[0] = %#v, want AliasLink #1 \"Removing a Car\"", plan.Changes[0])
	}
}

// renameNote sets up Tasks/42 Old Title.md and a board with cardLine,
// applies a RenameLocal of #42 to "New Title", and returns Kanban.md.
func renameNote(t *testing.T, cardLine string) string {
	t.Helper()
	dir := t.TempDir()
	cfg := applyTestConfig(t, dir, "on-demand")
	if err := os.MkdirAll(cfg.TasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.TasksDir, "42 Old Title.md"), []byte("---\nissue: 42\n---\n\n# Old Title\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeKanban(t, cfg.KanbanPath, "## Backlog\n\n"+cardLine+"\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	a, board, _ := newApplier(t, cfg, fake.New())
	change := RenameLocal{Card: board.Find(42), ID: 42, From: "Old Title", To: "New Title"}
	if err := a.Apply(context.Background(), Plan{Changes: []Change{change}}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	return readKanban(t, cfg)
}

func TestRenameLocalUpdatesYiAlias(t *testing.T) {
	got := renameNote(t, "- [ ] [[Tasks/42 Old Title|Old Title]] `#42`")
	if want := "- [ ] [[Tasks/42 New Title|New Title]] `#42`"; !strings.Contains(got, want) {
		t.Errorf("Kanban.md = %q, want it to contain %q", got, want)
	}
}

func TestRenameLocalKeepsUserAlias(t *testing.T) {
	got := renameNote(t, "- [ ] [[Tasks/42 Old Title|Short]] `#42`")
	if want := "- [ ] [[Tasks/42 New Title|Short]] `#42`"; !strings.Contains(got, want) {
		t.Errorf("Kanban.md = %q, want it to contain %q", got, want)
	}
}
