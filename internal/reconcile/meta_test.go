package reconcile

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/HayKor/yi/internal/config"
	"github.com/HayKor/yi/internal/domain"
	"github.com/HayKor/yi/internal/provider/fake"
)

// Final review C1: Obsidian Kanban metadata on a card (dates, times,
// tags) must survive every rewrite yi makes, and never leak into an
// issue title or a note filename.

// syncRound runs one pull or push (plan + apply) against the project
// in cfg, re-reading Kanban.md and the notes index from disk exactly as
// a fresh `yi pull` / `yi push` would, and returns the applied plan.
func syncRound(t *testing.T, cfg *config.Config, p *fake.Provider, push bool) Plan {
	t.Helper()
	a, board, _ := newApplier(t, cfg, p)
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
	return plan
}

func readKanban(t *testing.T, cfg *config.Config) string {
	t.Helper()
	data, err := os.ReadFile(cfg.KanbanPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// assertSteady checks that a further pull and push both plan nothing.
func assertSteady(t *testing.T, cfg *config.Config, p *fake.Provider) {
	t.Helper()
	if plan := syncRound(t, cfg, p, false); !plan.Empty() {
		t.Errorf("follow-up pull = %#v, want none", plan.Changes)
	}
	if plan := syncRound(t, cfg, p, true); !plan.Empty() {
		t.Errorf("follow-up push = %#v, want none", plan.Changes)
	}
}

func TestPullLinkNoteKeepsDate(t *testing.T) {
	dir := t.TempDir()
	cfg := applyTestConfig(t, dir, "always")
	writeKanban(t, cfg.KanbanPath, "## Backlog\n\n- [ ] Write docs `#5` @{2026-11-01}\n\n## Todo\n\n## In Progress\n\n## Completed\n")
	p := fake.New()
	p.Seed(domain.Issue{ID: 5, Title: "Write docs", State: domain.IssueOpen, Labels: []string{"status:backlog"}})

	syncRound(t, cfg, p, false)

	if got, want := readKanban(t, cfg), "- [ ] [[Tasks/5 Write docs|Write docs]] @{2026-11-01} `#5`\n"; !strings.Contains(got, want) {
		t.Errorf("Kanban.md = %q, want it to contain %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(cfg.TasksDir, "5 Write docs.md")); err != nil {
		t.Errorf("note not created: %v", err)
	}
	assertSteady(t, cfg, p)
}

func TestPushCreateKeepsMetadataOutOfTitle(t *testing.T) {
	dir := t.TempDir()
	cfg := applyTestConfig(t, dir, "always")
	writeKanban(t, cfg.KanbanPath, "## Backlog\n\n- [ ] Fix login @{2026-10-30} #urgent\n\n## Todo\n\n## In Progress\n\n## Completed\n")
	p := fake.New()

	syncRound(t, cfg, p, true)

	if got := p.Issue(1).Title; got != "Fix login" {
		t.Errorf("GitHub title = %q, want %q", got, "Fix login")
	}
	entries, err := os.ReadDir(cfg.TasksDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !reflect.DeepEqual(names, []string{"1 Fix login.md"}) {
		t.Errorf("notes = %q, want [\"1 Fix login.md\"]", names)
	}
	if got, want := readKanban(t, cfg), "- [ ] [[Tasks/1 Fix login|Fix login]] @{2026-10-30} #urgent `#1`\n"; !strings.Contains(got, want) {
		t.Errorf("Kanban.md = %q, want it to contain %q", got, want)
	}
	assertSteady(t, cfg, p)
}

func TestDatedCardIsNotRenamed(t *testing.T) {
	cfg := testConfig(t, "on-demand")
	board := buildBoard(t, "## Backlog\n\n- [ ] Fix login @{2026-10-30} @@{10:00} #urgent `#6`\n\n## Todo\n\n## In Progress\n\n## Completed\n")
	in := Input{
		Cfg:     cfg,
		Board:   board,
		Remote:  map[int]domain.Issue{6: {ID: 6, Title: "Fix login", State: domain.IssueOpen, Labels: []string{"status:backlog"}}},
		Missing: map[int]bool{},
		HasNote: noNote,
	}
	if plan := PlanPush(in); !plan.Empty() {
		t.Errorf("PlanPush = %#v, want none (no RenameRemote carrying the date)", plan.Changes)
	}
	if plan := PlanPull(in); !plan.Empty() {
		t.Errorf("PlanPull = %#v, want none", plan.Changes)
	}
}

// TestRemoteTitleWithTagIsSteady: a remote title that itself contains
// a tag-like word ("Release #v2") must not drift against a plain card
// whose title (by the card rule) excludes it.
func TestRemoteTitleWithTagIsSteady(t *testing.T) {
	cfg := testConfig(t, "on-demand")
	board := buildBoard(t, "## Backlog\n\n- [ ] Release #v2 `#5`\n\n## Todo\n\n## In Progress\n\n## Completed\n")
	in := Input{
		Cfg:     cfg,
		Board:   board,
		Remote:  map[int]domain.Issue{5: {ID: 5, Title: "Release #v2", State: domain.IssueOpen, Labels: []string{"status:backlog"}}},
		Missing: map[int]bool{},
		HasNote: noNote,
	}
	if plan := PlanPull(in); !plan.Empty() {
		t.Errorf("PlanPull = %#v, want none", plan.Changes)
	}
	if plan := PlanPush(in); !plan.Empty() {
		t.Errorf("PlanPush = %#v, want none (must not strip the remote title)", plan.Changes)
	}
}

func TestPullRenamePlainCardKeepsMetadata(t *testing.T) {
	dir := t.TempDir()
	cfg := applyTestConfig(t, dir, "on-demand")
	writeKanban(t, cfg.KanbanPath, "## Backlog\n\n- [ ] Old title @{2026-10-30} #urgent `#6`\n\t- [ ] sub-item\n\n## Todo\n\n## In Progress\n\n## Completed\n")
	p := fake.New()
	p.Seed(domain.Issue{ID: 6, Title: "New title", State: domain.IssueOpen, Labels: []string{"status:backlog"}})

	syncRound(t, cfg, p, false)

	if got, want := readKanban(t, cfg), "- [ ] New title @{2026-10-30} #urgent `#6`\n\t- [ ] sub-item\n"; !strings.Contains(got, want) {
		t.Errorf("Kanban.md = %q, want it to contain %q", got, want)
	}
	assertSteady(t, cfg, p)
}

// TestShortestPathLinkedCardNeverRelinked: a card linked with an
// Obsidian shortest-path link that doesn't resolve under <project>/
// (and has no indexed note) is the user's own link. Pull must not
// replace it with a new yi note (nor drop its alias or date). The link
// isn't yi-managed, so its title is the alias (residual review R3); the
// remote title matches it, so nothing else is planned either.
func TestShortestPathLinkedCardNeverRelinked(t *testing.T) {
	dir := t.TempDir()
	cfg := applyTestConfig(t, dir, "always")
	src := "## Backlog\n\n- [ ] [[Design doc|Login work]] @{2026-11-01} `#7`\n\n## Todo\n\n## In Progress\n\n## Completed\n"
	writeKanban(t, cfg.KanbanPath, src)
	p := fake.New()
	p.Seed(domain.Issue{ID: 7, Title: "Login work", State: domain.IssueOpen, Labels: []string{"status:backlog"}})

	if plan := syncRound(t, cfg, p, false); !plan.Empty() {
		t.Errorf("pull = %#v, want none (no LinkNote for a card that already has a link)", plan.Changes)
	}
	if plan := syncRound(t, cfg, p, true); !plan.Empty() {
		t.Errorf("push = %#v, want none", plan.Changes)
	}
	if got := readKanban(t, cfg); got != src {
		t.Errorf("Kanban.md = %q, want it unchanged", got)
	}
	if entries, _ := os.ReadDir(cfg.TasksDir); len(entries) != 0 {
		t.Errorf("Tasks/ has %d entries, want no note created", len(entries))
	}
}

func TestPlanPullNoLinkNoteForLinkedCard(t *testing.T) {
	cfg := testConfig(t, "always")
	board := buildBoard(t, "## Backlog\n\n- [ ] [[Elsewhere/Spec]] `#7`\n\n## Todo\n\n## In Progress\n\n## Completed\n")
	in := Input{
		Cfg:     cfg,
		Board:   board,
		Remote:  map[int]domain.Issue{7: {ID: 7, Title: "Spec", State: domain.IssueOpen, Labels: []string{"status:backlog"}}},
		Missing: map[int]bool{},
		HasNote: noNote,
	}
	for _, c := range PlanPull(in).Changes {
		if _, ok := c.(LinkNote); ok {
			t.Errorf("PlanPull planned %#v for a card that already has a link", c)
		}
	}
}

// TestAddCardNoteNameExcludesMetadata: an issue whose remote title
// carries a date token (e.g. created by an older yi) gets a clean note
// filename.
func TestAddCardNoteNameExcludesMetadata(t *testing.T) {
	dir := t.TempDir()
	cfg := applyTestConfig(t, dir, "always")
	writeKanban(t, cfg.KanbanPath, "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n")
	p := fake.New()
	p.Seed(domain.Issue{ID: 9, Title: "Fix login @{2026-10-30} #urgent", State: domain.IssueOpen, Labels: []string{"status:backlog"}})

	syncRound(t, cfg, p, false)

	if _, err := os.Stat(filepath.Join(cfg.TasksDir, "9 Fix login.md")); err != nil {
		t.Errorf("note 9 Fix login.md not created: %v", err)
	}
	if got, want := readKanban(t, cfg), "- [ ] [[Tasks/9 Fix login|Fix login]] `#9`\n"; !strings.Contains(got, want) {
		t.Errorf("Kanban.md = %q, want it to contain %q", got, want)
	}
	if plan := syncRound(t, cfg, p, false); !plan.Empty() {
		t.Errorf("follow-up pull = %#v, want none", plan.Changes)
	}
}

// --- M2: case-insensitive labels ---------------------------------------

func TestRemoteUpdateForIgnoresLabelCase(t *testing.T) {
	cfg := testConfig(t, "on-demand")

	is := domain.Issue{ID: 1, State: domain.IssueOpen, Labels: []string{"Status:Todo"}}
	if upd, ok := RemoteUpdateFor(cfg, is, "todo"); ok {
		t.Errorf("RemoteUpdateFor(Status:Todo → todo) = %+v, want no update", upd)
	}

	is = domain.Issue{ID: 1, State: domain.IssueOpen, Labels: []string{"bug", "Status:Todo"}}
	upd, ok := RemoteUpdateFor(cfg, is, "in-progress")
	if !ok {
		t.Fatal("RemoteUpdateFor(Status:Todo → in-progress) = no update, want one")
	}
	if !reflect.DeepEqual(upd.AddLabels, []string{"status:in-progress"}) {
		t.Errorf("AddLabels = %q, want [status:in-progress]", upd.AddLabels)
	}
	if !reflect.DeepEqual(upd.RemoveLabels, []string{"Status:Todo"}) {
		t.Errorf("RemoveLabels = %q, want the issue's own spelling [Status:Todo]", upd.RemoveLabels)
	}
}

func TestLabelCaseSyncIsNoop(t *testing.T) {
	cfg := testConfig(t, "on-demand")
	board := buildBoard(t, "## Backlog\n\n## Todo\n\n- [ ] Task `#3`\n\n## In Progress\n\n## Completed\n")
	in := Input{
		Cfg:     cfg,
		Board:   board,
		Remote:  map[int]domain.Issue{3: {ID: 3, Title: "Task", State: domain.IssueOpen, Labels: []string{"Status:Todo"}}},
		Missing: map[int]bool{},
		HasNote: noNote,
	}
	if plan := PlanPush(in); !plan.Empty() {
		t.Errorf("PlanPush = %#v, want none", plan.Changes)
	}
	if plan := PlanPull(in); !plan.Empty() {
		t.Errorf("PlanPull = %#v, want none (Status:Todo maps to todo)", plan.Changes)
	}
}

// --- M6: a skipped RenameLocal is not reported as applied --------------

func TestSkippedRenameLocalNotReportedApplied(t *testing.T) {
	dir := t.TempDir()
	cfg := applyTestConfig(t, dir, "on-demand")
	writeKanban(t, cfg.KanbanPath, "## Backlog\n\n- [ ] [[Tasks/42 Missing]] `#42`\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	p := fake.New()
	a, board, logBuf := newApplier(t, cfg, p)
	card := board.Find(42)

	plan := Plan{Changes: []Change{
		RenameLocal{Card: card, ID: 42, From: "Missing", To: "New Title"},
		MoveCard{Card: card, From: "backlog", To: "todo"},
	}}
	if err := a.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	log := logBuf.String()
	if strings.Contains(log, "✓ #42 rename") {
		t.Errorf("Log = %q, want no ✓ line for the skipped rename", log)
	}
	if !strings.Contains(log, "✓ #42 move") {
		t.Errorf("Log = %q, want the move still reported", log)
	}
	if !strings.Contains(log, "not found; card not renamed") {
		t.Errorf("Log = %q, want the warning", log)
	}
}

func TestSkippedRenameLocalNotCounted(t *testing.T) {
	dir := t.TempDir()
	cfg := applyTestConfig(t, dir, "on-demand")
	writeKanban(t, cfg.KanbanPath, "## Backlog\n\n- [ ] [[Tasks/42 Missing]] `#42`\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	p := fake.New()
	// #42 doesn't exist remotely, so the RenameRemote after the skip fails.
	a, board, _ := newApplier(t, cfg, p)
	card := board.Find(42)

	plan := Plan{Changes: []Change{
		RenameLocal{Card: card, ID: 42, From: "Missing", To: "New Title"},
		RenameRemote{ID: 42, From: "Missing", To: "X"},
	}}
	err := a.Apply(context.Background(), plan)
	if err == nil || !strings.Contains(err.Error(), "applied 0 of 2 changes") {
		t.Errorf("Apply() error = %v, want \"applied 0 of 2 changes\"", err)
	}
}

// TestPullRenamePlainCardWithUnlinkedNote: a plain card whose note is
// found only through the index (not linked from the card) is renamed
// like any plain card, and its note is renamed with it. (Before, the
// rename tried to update a link the card doesn't have and failed every
// pull.)
func TestPullRenamePlainCardWithUnlinkedNote(t *testing.T) {
	dir := t.TempDir()
	cfg := applyTestConfig(t, dir, "on-demand")
	if err := os.MkdirAll(cfg.TasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.TasksDir, "6 Old title.md"), []byte("---\nissue: 6\n---\n\n# Old title\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeKanban(t, cfg.KanbanPath, "## Backlog\n\n- [ ] Old title #urgent `#6`\n\n## Todo\n\n## In Progress\n\n## Completed\n")
	p := fake.New()
	p.Seed(domain.Issue{ID: 6, Title: "New title", State: domain.IssueOpen, Labels: []string{"status:backlog"}})

	syncRound(t, cfg, p, false)

	if got, want := readKanban(t, cfg), "- [ ] New title #urgent `#6`\n"; !strings.Contains(got, want) {
		t.Errorf("Kanban.md = %q, want it to contain %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(cfg.TasksDir, "6 New title.md")); err != nil {
		t.Errorf("note not renamed: %v", err)
	}
	assertSteady(t, cfg, p)
}
