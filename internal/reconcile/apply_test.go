package reconcile

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kanban-sync/internal/config"
	"kanban-sync/internal/domain"
	"kanban-sync/internal/kanban"
	"kanban-sync/internal/notes"
	"kanban-sync/internal/provider/fake"
)

// --- test fixtures --------------------------------------------------

// applyTestConfig builds a *config.Config like testConfig (same four
// statuses), but rooted at dir on disk, so Applier can actually read
// and write Kanban.md and task notes there.
func applyTestConfig(t *testing.T, dir, notesMode string) *config.Config {
	t.Helper()
	yamlSrc := `
provider: github
repository: my-org/repo

statuses:
  backlog:
    heading: Backlog
    label: status:backlog
  todo:
    heading: Todo
    label: status:todo
  in-progress:
    heading: In Progress
    label: status:in-progress
  completed:
    heading: Completed
    label: status:completed
    closeIssue: true

defaultStatus: backlog
notes:
  create: ` + notesMode + "\n"

	cfg, err := config.Parse([]byte(yamlSrc), filepath.Join(dir, ".issue-sync.yml"))
	if err != nil {
		t.Fatalf("parsing test config: %v", err)
	}
	return cfg
}

// writeKanban writes src as cfg's Kanban.md.
func writeKanban(t *testing.T, path, src string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// newApplier reads Kanban.md from cfg.KanbanPath, parses it, loads the
// notes index, and returns an *Applier wired to p with its own log
// buffer, plus the parsed board for convenience.
func newApplier(t *testing.T, cfg *config.Config, p *fake.Provider) (*Applier, *kanban.Board, *bytes.Buffer) {
	t.Helper()

	data, hash, err := kanban.ReadFile(cfg.KanbanPath)
	if err != nil {
		t.Fatalf("reading Kanban.md: %v", err)
	}
	board, err := kanban.Parse(data, cfg.Statuses)
	if err != nil {
		t.Fatalf("parsing board: %v", err)
	}
	store := notes.NewStore(cfg.Dir, cfg.TasksDir)
	idx, err := store.LoadIndex()
	if err != nil {
		t.Fatalf("loading notes index: %v", err)
	}

	var logBuf bytes.Buffer
	a := &Applier{
		Cfg:       cfg,
		Board:     board,
		BoardHash: hash,
		Provider:  p,
		Notes:     store,
		Index:     idx,
		Log:       &logBuf,
	}
	return a, board, &logBuf
}

// mutatingCalls filters out the "list"/"get ..." calls FetchRemote
// always makes, leaving only the create/update calls a test cares
// about.
func mutatingCalls(calls []string) []string {
	var out []string
	for _, c := range calls {
		if c == "list" || strings.HasPrefix(c, "get ") {
			continue
		}
		out = append(out, c)
	}
	return out
}

// hasNoteFn returns the Input.HasNote predicate for a, backed by its
// own Notes/Index (never the filesystem directly).
func hasNoteFn(a *Applier) func(*kanban.Card) bool {
	return func(c *kanban.Card) bool {
		_, ok := a.Notes.Resolve(a.Index, c.IssueID, c.Link)
		return ok
	}
}

// --- pull end to end --------------------------------------------------

func TestApplyPullEndToEnd(t *testing.T) {
	dir := t.TempDir()
	cfg := applyTestConfig(t, dir, "always")
	writeKanban(t, cfg.KanbanPath, "## Backlog\n\n## Todo\n\n## In Progress\n\n- [ ] Some task `#57`\n\n## Completed\n")

	p := fake.New()
	p.Seed(
		domain.Issue{ID: 57, Title: "Some task", State: domain.IssueClosed},
		domain.Issue{ID: 80, Title: "New thing", State: domain.IssueOpen, Labels: []string{"status:todo"}},
	)

	a, board, _ := newApplier(t, cfg, p)
	ctx := context.Background()
	remote, missing, err := FetchRemote(ctx, p, board)
	if err != nil {
		t.Fatalf("FetchRemote() error = %v", err)
	}
	plan := PlanPull(Input{Cfg: cfg, Board: board, Remote: remote, Missing: missing, HasNote: hasNoteFn(a)})

	if err := a.Apply(ctx, plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(cfg.TasksDir, "80 New thing.md")); err != nil {
		t.Errorf("note for #80 not created: %v", err)
	}

	data, err := os.ReadFile(cfg.KanbanPath)
	if err != nil {
		t.Fatalf("reading Kanban.md: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "- [x] [[Tasks/57 Some task]] `#57`") {
		t.Errorf("Kanban.md = %q, want #57 checked and linked to its note", got)
	}
	if !strings.Contains(got, "[[Tasks/80 New thing]] `#80`") {
		t.Errorf("Kanban.md = %q, want #80 added with its note link", got)
	}
	if _, err := os.Stat(filepath.Join(cfg.TasksDir, "57 Some task.md")); err != nil {
		t.Errorf("note for #57 not created: %v", err)
	}

	reparsed, err := kanban.Parse(data, cfg.Statuses)
	if err != nil {
		t.Fatalf("reparsing board: %v", err)
	}
	if c := reparsed.Find(57); c == nil || c.Status != "completed" {
		t.Errorf("card #57 = %+v, want status completed", c)
	}
	if c := reparsed.Find(80); c == nil || c.Status != "todo" {
		t.Errorf("card #80 = %+v, want status todo", c)
	}
}

func TestPullTwiceIsNoop(t *testing.T) {
	dir := t.TempDir()
	cfg := applyTestConfig(t, dir, "always")
	writeKanban(t, cfg.KanbanPath, "## Backlog\n\n## Todo\n\n## In Progress\n\n- [ ] Some task `#57`\n\n## Completed\n")

	p := fake.New()
	p.Seed(
		domain.Issue{ID: 57, Title: "Some task", State: domain.IssueClosed},
		domain.Issue{ID: 80, Title: "New thing", State: domain.IssueOpen, Labels: []string{"status:todo"}},
	)

	a, board, _ := newApplier(t, cfg, p)
	ctx := context.Background()
	remote, missing, err := FetchRemote(ctx, p, board)
	if err != nil {
		t.Fatalf("FetchRemote() error = %v", err)
	}
	plan := PlanPull(Input{Cfg: cfg, Board: board, Remote: remote, Missing: missing, HasNote: hasNoteFn(a)})
	if plan.Empty() {
		t.Fatalf("first PlanPull = empty, want MoveCard + AddCard")
	}

	if err := a.Apply(ctx, plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	// Re-read everything from disk, exactly as a second `yi pull` would.
	a2, board2, _ := newApplier(t, cfg, p)
	remote2, missing2, err := FetchRemote(ctx, p, board2)
	if err != nil {
		t.Fatalf("FetchRemote() (2nd) error = %v", err)
	}
	plan2 := PlanPull(Input{Cfg: cfg, Board: board2, Remote: remote2, Missing: missing2, HasNote: hasNoteFn(a2)})

	if !plan2.Empty() {
		t.Errorf("second PlanPull = %#v, want none", plan2.Changes)
	}
}

// TestPullAliasPreservedAfterRename exercises the alias-drift ruling
// end to end: RenameLocal on an aliased, linked card must rename the
// note and update the link target without disturbing the alias, and a
// fresh re-plan afterward must see no drift.
func TestPullAliasPreservedAfterRename(t *testing.T) {
	dir := t.TempDir()
	cfg := applyTestConfig(t, dir, "on-demand")

	if err := os.MkdirAll(cfg.TasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	notePath := filepath.Join(cfg.TasksDir, "42 Yandex OAuth.md")
	if err := os.WriteFile(notePath, []byte("---\nissue: 42\n---\n\n# Yandex OAuth\n\n## Context\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeKanban(t, cfg.KanbanPath, "## Backlog\n\n- [ ] [[Tasks/42 Yandex OAuth|OAuth]] `#42`\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	p := fake.New()
	p.Seed(domain.Issue{ID: 42, Title: "Yandex OAuth 2.0", State: domain.IssueOpen})

	a, board, _ := newApplier(t, cfg, p)
	ctx := context.Background()
	remote, missing, err := FetchRemote(ctx, p, board)
	if err != nil {
		t.Fatalf("FetchRemote() error = %v", err)
	}
	plan := PlanPull(Input{Cfg: cfg, Board: board, Remote: remote, Missing: missing, HasNote: hasNoteFn(a)})

	if len(plan.Changes) != 1 {
		t.Fatalf("Changes = %#v, want exactly one RenameLocal", plan.Changes)
	}
	if _, ok := plan.Changes[0].(RenameLocal); !ok {
		t.Fatalf("Changes[0] = %#v, want RenameLocal", plan.Changes[0])
	}

	if err := a.Apply(ctx, plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	data, err := os.ReadFile(cfg.KanbanPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[[Tasks/42 Yandex OAuth 2.0|OAuth]] `#42`") {
		t.Errorf("Kanban.md = %q, want the alias \"OAuth\" preserved", data)
	}
	if _, err := os.Stat(filepath.Join(cfg.TasksDir, "42 Yandex OAuth 2.0.md")); err != nil {
		t.Errorf("renamed note not found: %v", err)
	}

	a2, board2, _ := newApplier(t, cfg, p)
	remote2, missing2, err := FetchRemote(ctx, p, board2)
	if err != nil {
		t.Fatalf("FetchRemote() (2nd) error = %v", err)
	}
	plan2 := PlanPull(Input{Cfg: cfg, Board: board2, Remote: remote2, Missing: missing2, HasNote: hasNoteFn(a2)})
	if !plan2.Empty() {
		t.Errorf("second PlanPull = %#v, want none (alias must not drift)", plan2.Changes)
	}
}

// --- push end to end --------------------------------------------------

func TestPushTwiceIsNoop(t *testing.T) {
	dir := t.TempDir()
	cfg := applyTestConfig(t, dir, "on-demand")
	writeKanban(t, cfg.KanbanPath, "## Backlog\n\n## Todo\n\n## In Progress\n\n- [ ] Some title `#57`\n\n## Completed\n")

	p := fake.New()
	p.Seed(domain.Issue{ID: 57, Title: "Some title", State: domain.IssueOpen, Labels: []string{"bug", "status:todo"}})

	a, board, _ := newApplier(t, cfg, p)
	ctx := context.Background()
	remote, missing, err := FetchRemote(ctx, p, board)
	if err != nil {
		t.Fatalf("FetchRemote() error = %v", err)
	}
	plan := PlanPush(Input{Cfg: cfg, Board: board, Remote: remote, Missing: missing, HasNote: hasNoteFn(a)})
	if plan.Empty() {
		t.Fatalf("first PlanPush = empty, want a SetStatus")
	}

	if err := a.Apply(ctx, plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	a2, board2, _ := newApplier(t, cfg, p)
	remote2, missing2, err := FetchRemote(ctx, p, board2)
	if err != nil {
		t.Fatalf("FetchRemote() (2nd) error = %v", err)
	}
	plan2 := PlanPush(Input{Cfg: cfg, Board: board2, Remote: remote2, Missing: missing2, HasNote: hasNoteFn(a2)})

	if !plan2.Empty() {
		t.Errorf("second PlanPush = %#v, want none", plan2.Changes)
	}
}

func TestApplyCreateIssueWritesID(t *testing.T) {
	dir := t.TempDir()
	cfg := applyTestConfig(t, dir, "always")
	writeKanban(t, cfg.KanbanPath, "## Backlog\n\n## Todo\n\n- [ ] New thing\n\n## In Progress\n\n## Completed\n")

	p := fake.New()
	a, board, _ := newApplier(t, cfg, p)
	ctx := context.Background()
	remote, missing, err := FetchRemote(ctx, p, board)
	if err != nil {
		t.Fatalf("FetchRemote() error = %v", err)
	}
	plan := PlanPush(Input{Cfg: cfg, Board: board, Remote: remote, Missing: missing, HasNote: hasNoteFn(a)})

	if err := a.Apply(ctx, plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	calls := mutatingCalls(p.Calls())
	if len(calls) != 1 || calls[0] != "create New thing" {
		t.Fatalf("mutating Calls() = %v, want exactly [\"create New thing\"]", calls)
	}
	issue := p.Issue(1)
	if len(issue.Labels) != 1 || issue.Labels[0] != "status:todo" {
		t.Errorf("issue.Labels = %v, want [status:todo]", issue.Labels)
	}

	data, err := os.ReadFile(cfg.KanbanPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "- [ ] [[Tasks/1 New thing]] `#1`") {
		t.Errorf("Kanban.md = %q, want the new card linked to its note with #1", data)
	}
	if _, err := os.Stat(filepath.Join(cfg.TasksDir, "1 New thing.md")); err != nil {
		t.Errorf("note for #1 not created: %v", err)
	}
}

// TestApplyCreateIssueNoteFailureKeepsID guards against losing a
// created issue's id when the local follow-up step (creating its
// note) fails: a squatter file already occupies the exact path the
// new note would take, but belongs to a different issue, so
// notes.Store.Create refuses to touch it. The remote issue still
// exists, so the id must land on the card regardless.
func TestApplyCreateIssueNoteFailureKeepsID(t *testing.T) {
	dir := t.TempDir()
	cfg := applyTestConfig(t, dir, "always")
	writeKanban(t, cfg.KanbanPath, "## Backlog\n\n## Todo\n\n- [ ] New thing\n\n## In Progress\n\n## Completed\n")

	if err := os.MkdirAll(cfg.TasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The first issue fake.Provider creates will get id 1, so this
	// squats on the exact path notes.Store.Create would use for it.
	squatter := filepath.Join(cfg.TasksDir, "1 New thing.md")
	if err := os.WriteFile(squatter, []byte("---\nissue: 99\n---\n\n# Someone else's note\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := fake.New()
	a, board, _ := newApplier(t, cfg, p)
	ctx := context.Background()
	remote, missing, err := FetchRemote(ctx, p, board)
	if err != nil {
		t.Fatalf("FetchRemote() error = %v", err)
	}
	plan := PlanPush(Input{Cfg: cfg, Board: board, Remote: remote, Missing: missing, HasNote: hasNoteFn(a)})

	err = a.Apply(ctx, plan)
	if err == nil || !strings.Contains(err.Error(), "applied 0 of 1") {
		t.Fatalf("Apply() error = %v, want containing \"applied 0 of 1\"", err)
	}

	// The remote issue was created regardless of the local failure.
	if issue := p.Issue(1); issue.Title != "New thing" {
		t.Errorf("remote issue #1 = %+v, want it created regardless", issue)
	}

	data, err := os.ReadFile(cfg.KanbanPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "New thing `#1`") {
		t.Errorf("Kanban.md = %q, want #1 recorded on the card despite the note failure", data)
	}

	// A re-plan must not try to create a duplicate issue for it.
	a2, board2, _ := newApplier(t, cfg, p)
	remote2, missing2, err := FetchRemote(ctx, p, board2)
	if err != nil {
		t.Fatalf("FetchRemote() (2nd) error = %v", err)
	}
	plan2 := PlanPush(Input{Cfg: cfg, Board: board2, Remote: remote2, Missing: missing2, HasNote: hasNoteFn(a2)})
	for _, c := range plan2.Changes {
		if _, ok := c.(CreateIssue); ok {
			t.Errorf("second PlanPush = %#v, want no CreateIssue (would duplicate #1)", plan2.Changes)
		}
	}
}

func TestApplyCreateInCompletedCloses(t *testing.T) {
	dir := t.TempDir()
	cfg := applyTestConfig(t, dir, "on-demand")
	writeKanban(t, cfg.KanbanPath, "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n\n- [ ] Already done\n")

	p := fake.New()
	a, board, _ := newApplier(t, cfg, p)
	ctx := context.Background()
	remote, missing, err := FetchRemote(ctx, p, board)
	if err != nil {
		t.Fatalf("FetchRemote() error = %v", err)
	}
	plan := PlanPush(Input{Cfg: cfg, Board: board, Remote: remote, Missing: missing, HasNote: hasNoteFn(a)})

	if err := a.Apply(ctx, plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	issue := p.Issue(1)
	if issue.State != domain.IssueClosed {
		t.Errorf("issue.State = %v, want closed", issue.State)
	}
	if len(issue.Labels) != 1 || issue.Labels[0] != "status:completed" {
		t.Errorf("issue.Labels = %v, want [status:completed]", issue.Labels)
	}

	wantCalls := []string{"create Already done", "update 1"}
	if calls := mutatingCalls(p.Calls()); len(calls) != len(wantCalls) || calls[0] != wantCalls[0] || calls[1] != wantCalls[1] {
		t.Errorf("mutating Calls() = %v, want %v", calls, wantCalls)
	}

	data, err := os.ReadFile(cfg.KanbanPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Already done `#1`") {
		t.Errorf("Kanban.md = %q, want #1 recorded on the card", data)
	}
}

func TestPushPartialFailureKeepsIDs(t *testing.T) {
	dir := t.TempDir()
	cfg := applyTestConfig(t, dir, "on-demand")
	writeKanban(t, cfg.KanbanPath, "## Backlog\n\n## Todo\n\n- [ ] First\n- [ ] Second\n- [ ] Third\n\n## In Progress\n\n## Completed\n")

	p := fake.New()
	p.FailAfter(2, errors.New("boom"))

	a, board, _ := newApplier(t, cfg, p)
	ctx := context.Background()
	remote, missing, err := FetchRemote(ctx, p, board)
	if err != nil {
		t.Fatalf("FetchRemote() error = %v", err)
	}
	plan := PlanPush(Input{Cfg: cfg, Board: board, Remote: remote, Missing: missing, HasNote: hasNoteFn(a)})
	if len(plan.Changes) != 3 {
		t.Fatalf("Changes = %#v, want 3 CreateIssue", plan.Changes)
	}

	err = a.Apply(ctx, plan)
	if err == nil || !strings.Contains(err.Error(), "applied 2 of 3") {
		t.Fatalf("Apply() error = %v, want containing \"applied 2 of 3\"", err)
	}

	data, err := os.ReadFile(cfg.KanbanPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "First `#1`") || !strings.Contains(got, "Second `#2`") {
		t.Errorf("Kanban.md = %q, want #1 and #2 recorded", got)
	}
	if strings.Contains(got, "Third `#") {
		t.Errorf("Kanban.md = %q, want \"Third\" still id-less", got)
	}

	// A second push, against a working provider pre-seeded with what
	// the first one already created, must create exactly the one
	// remaining issue and no duplicates.
	p2 := fake.New()
	p2.Seed(p.Issue(1), p.Issue(2))

	a2, board2, _ := newApplier(t, cfg, p2)
	remote2, missing2, err := FetchRemote(ctx, p2, board2)
	if err != nil {
		t.Fatalf("FetchRemote() (2nd) error = %v", err)
	}
	plan2 := PlanPush(Input{Cfg: cfg, Board: board2, Remote: remote2, Missing: missing2, HasNote: hasNoteFn(a2)})
	if len(plan2.Changes) != 1 {
		t.Fatalf("second Changes = %#v, want exactly one CreateIssue", plan2.Changes)
	}

	if err := a2.Apply(ctx, plan2); err != nil {
		t.Fatalf("second Apply() error = %v", err)
	}
	if calls := mutatingCalls(p2.Calls()); len(calls) != 1 {
		t.Errorf("second mutating Calls() = %v, want exactly one create", calls)
	}
	open, _ := p2.ListOpenIssues(ctx)
	if len(open) != 3 {
		t.Errorf("open issues = %v, want 3 total", open)
	}
}

// TestApplyCreateLinkedCardPreservesLink is the applier-level half of
// the "linked id-less card" ruling: pushing it must only append the
// `#id` token (via SetIssueID), leaving its link, alias, and any
// other text on the line untouched, and must not create a second note
// for an issue that already has one linked.
func TestApplyCreateLinkedCardPreservesLink(t *testing.T) {
	dir := t.TempDir()
	cfg := applyTestConfig(t, dir, "always")
	writeKanban(t, cfg.KanbanPath, "## Backlog\n\n## Todo\n\n- [ ] [[Tasks/Draft idea|Idea]] @{2026-11-01}\n\n## In Progress\n\n## Completed\n")

	p := fake.New()
	a, board, _ := newApplier(t, cfg, p)
	ctx := context.Background()
	remote, missing, err := FetchRemote(ctx, p, board)
	if err != nil {
		t.Fatalf("FetchRemote() error = %v", err)
	}
	plan := PlanPush(Input{Cfg: cfg, Board: board, Remote: remote, Missing: missing, HasNote: hasNoteFn(a)})

	if err := a.Apply(ctx, plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	data, err := os.ReadFile(cfg.KanbanPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "- [ ] [[Tasks/Draft idea|Idea]] @{2026-11-01} `#1`") {
		t.Errorf("Kanban.md = %q, want the link/alias/date preserved with #1 appended", data)
	}

	if _, err := os.Stat(filepath.Join(cfg.TasksDir, "1 Draft idea.md")); !os.IsNotExist(err) {
		t.Errorf("a note file was created for a card that already had its own link")
	}
}

// --- concurrent edits --------------------------------------------------

func TestApplyAbortsOnConcurrentEdit(t *testing.T) {
	dir := t.TempDir()
	cfg := applyTestConfig(t, dir, "on-demand")
	writeKanban(t, cfg.KanbanPath, "## Backlog\n\n## Todo\n\n- [ ] Some title `#57`\n\n## In Progress\n\n## Completed\n")

	p := fake.New()
	p.Seed(domain.Issue{ID: 57, Title: "Some title", State: domain.IssueOpen, Labels: []string{"status:in-progress"}})

	a, board, logBuf := newApplier(t, cfg, p)
	ctx := context.Background()
	remote, missing, err := FetchRemote(ctx, p, board)
	if err != nil {
		t.Fatalf("FetchRemote() error = %v", err)
	}
	plan := PlanPull(Input{Cfg: cfg, Board: board, Remote: remote, Missing: missing, HasNote: hasNoteFn(a)})
	if plan.Empty() {
		t.Fatalf("PlanPull = empty, want a MoveCard to in-progress")
	}

	// Someone else (Obsidian, the user) edits Kanban.md after we read
	// it but before we apply.
	externalEdit := "## Backlog\n\n- [ ] Someone else's card\n\n## Todo\n\n- [ ] Some title `#57`\n\n## In Progress\n\n## Completed\n"
	writeKanban(t, cfg.KanbanPath, externalEdit)

	err = a.Apply(ctx, plan)
	if !errors.Is(err, kanban.ErrModified) {
		t.Fatalf("Apply() error = %v, want errors.Is(..., kanban.ErrModified)", err)
	}

	data, err := os.ReadFile(cfg.KanbanPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != externalEdit {
		t.Errorf("Kanban.md = %q, want the external edit left untouched", data)
	}

	// The "✓ move" line was printed before the save failed: the log
	// must say that it never reached Kanban.md (residual review R4).
	if got := logBuf.String(); !strings.Contains(got, "✓ #57") || !strings.Contains(got, notSavedLine) {
		t.Errorf("Log = %q, want the ✓ line followed by %q", got, notSavedLine)
	}
}

// notSavedLine is what Apply logs when the save hits kanban.ErrModified.
const notSavedLine = "warning: Kanban.md was changed by someone else during sync; nothing was saved to it, so the local board changes above were NOT applied\n"

func TestPushConcurrentEditPrintsCreatedIDs(t *testing.T) {
	dir := t.TempDir()
	cfg := applyTestConfig(t, dir, "on-demand")
	writeKanban(t, cfg.KanbanPath, "## Backlog\n\n## Todo\n\n- [ ] New thing\n\n## In Progress\n\n## Completed\n")

	p := fake.New()
	a, board, logBuf := newApplier(t, cfg, p)
	ctx := context.Background()
	remote, missing, err := FetchRemote(ctx, p, board)
	if err != nil {
		t.Fatalf("FetchRemote() error = %v", err)
	}
	plan := PlanPush(Input{Cfg: cfg, Board: board, Remote: remote, Missing: missing, HasNote: hasNoteFn(a)})

	externalEdit := "## Backlog\n\n- [ ] Someone else's card\n\n## Todo\n\n- [ ] New thing\n\n## In Progress\n\n## Completed\n"
	writeKanban(t, cfg.KanbanPath, externalEdit)

	err = a.Apply(ctx, plan)
	if !errors.Is(err, kanban.ErrModified) {
		t.Fatalf("Apply() error = %v, want errors.Is(..., kanban.ErrModified)", err)
	}
	if err.Error() != "Kanban.md was modified during sync; re-run" {
		t.Errorf("Apply() error message = %q, want the exact re-run message", err.Error())
	}

	if got := logBuf.String(); !strings.Contains(got, `add "#1" to card "New thing" manually`) {
		t.Errorf("Log = %q, want it to mention adding #1 to %q manually", got, "New thing")
	}
	if got := logBuf.String(); !strings.Contains(got, notSavedLine+`add "#1"`) {
		t.Errorf("Log = %q, want %q right before the manual-id lines", got, notSavedLine)
	}

	data, err := os.ReadFile(cfg.KanbanPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != externalEdit {
		t.Errorf("Kanban.md = %q, want the external edit left untouched", data)
	}

	if issue := p.Issue(1); issue.Title != "New thing" {
		t.Errorf("remote issue #1 = %+v, want it created regardless of the local write failing", issue)
	}
}

// --- unit-level change application --------------------------------------------------

func TestMoveCardCheckbox(t *testing.T) {
	cfg := testConfig(t, "on-demand")
	board := buildBoard(t, "## Backlog\n\n## Todo\n\n- [ ] Some title `#1`\n\n## In Progress\n\n## Completed\n")
	card := board.Find(1)

	if err := ApplyMoveCard(cfg, board, card, "completed"); err != nil {
		t.Fatalf("ApplyMoveCard() error = %v", err)
	}
	if !card.Checked || card.Status != "completed" {
		t.Errorf("card after move to completed = %+v, want checked in completed", card)
	}

	if err := ApplyMoveCard(cfg, board, card, "todo"); err != nil {
		t.Fatalf("ApplyMoveCard() error = %v", err)
	}
	if card.Checked || card.Status != "todo" {
		t.Errorf("card after move to todo = %+v, want unchecked in todo", card)
	}
}

func TestRenameLocalRenamesNote(t *testing.T) {
	dir := t.TempDir()
	cfg := applyTestConfig(t, dir, "on-demand")

	if err := os.MkdirAll(cfg.TasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldNotePath := filepath.Join(cfg.TasksDir, "42 Old Title.md")
	if err := os.WriteFile(oldNotePath, []byte("---\nissue: 42\n---\n\n# Old Title\n\n## Context\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeKanban(t, cfg.KanbanPath, "## Backlog\n\n- [ ] [[Tasks/42 Old Title]] `#42`\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	p := fake.New()
	a, board, logBuf := newApplier(t, cfg, p)
	card := board.Find(42)
	if card == nil {
		t.Fatalf("card #42 not found on board")
	}

	change := RenameLocal{Card: card, ID: 42, From: "Old Title", To: "New Title"}
	if err := a.Apply(context.Background(), Plan{Changes: []Change{change}}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	newNotePath := filepath.Join(cfg.TasksDir, "42 New Title.md")
	if _, err := os.Stat(newNotePath); err != nil {
		t.Errorf("note not renamed to %s: %v", newNotePath, err)
	}
	if _, err := os.Stat(oldNotePath); !os.IsNotExist(err) {
		t.Errorf("old note still exists at %s", oldNotePath)
	}
	newContent, err := os.ReadFile(newNotePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(newContent), "# New Title") {
		t.Errorf("note content = %q, want updated H1", newContent)
	}

	data, err := os.ReadFile(cfg.KanbanPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[[Tasks/42 New Title]] `#42`") {
		t.Errorf("Kanban.md = %q, want the link target updated", data)
	}

	if got := logBuf.String(); !strings.Contains(got, `warning: other links to "Old Title" were not updated`) {
		t.Errorf("Log = %q, want the \"other links\" warning", got)
	}
}

// TestRenameLocalMissingNoteLeavesCardUnchanged covers a RenameLocal
// whose card has a link, but nothing resolves it (the note was
// deleted, moved, or never existed): the card must be left exactly as
// it was — never silently rewritten into a plain, link-less card —
// and a warning must explain why nothing happened.
func TestRenameLocalMissingNoteLeavesCardUnchanged(t *testing.T) {
	dir := t.TempDir()
	cfg := applyTestConfig(t, dir, "on-demand")
	writeKanban(t, cfg.KanbanPath, "## Backlog\n\n- [ ] [[Tasks/42 Missing]] `#42`\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	p := fake.New()
	a, board, logBuf := newApplier(t, cfg, p)
	card := board.Find(42)
	if card == nil {
		t.Fatalf("card #42 not found on board")
	}

	change := RenameLocal{Card: card, ID: 42, From: "Missing", To: "New Title"}
	if err := a.Apply(context.Background(), Plan{Changes: []Change{change}}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	data, err := os.ReadFile(cfg.KanbanPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[[Tasks/42 Missing]] `#42`") {
		t.Errorf("Kanban.md = %q, want the card left exactly as it was", data)
	}

	if got := logBuf.String(); !strings.Contains(got, `warning: #42: note "Tasks/42 Missing" not found; card not renamed`) {
		t.Errorf("Log = %q, want the \"note not found\" warning", got)
	}
}

// TestPullTwiceIsNoopLossyTitle is TestPullTwiceIsNoop's required
// lossy-title variant (spec §6.4): the remote title "Fix: crash" is
// not byte-equal to the card's link-derived local title "Fix crash",
// but sanitize() makes them equal, so neither a pull nor a push round
// trip should ever produce a rename.
func TestPullTwiceIsNoopLossyTitle(t *testing.T) {
	dir := t.TempDir()
	cfg := applyTestConfig(t, dir, "on-demand")

	if err := os.MkdirAll(cfg.TasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	notePath := filepath.Join(cfg.TasksDir, "31 Fix crash.md")
	if err := os.WriteFile(notePath, []byte("---\nissue: 31\n---\n\n# Fix crash\n\n## Context\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeKanban(t, cfg.KanbanPath, "## Backlog\n\n- [ ] [[Tasks/31 Fix crash]] `#31`\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	p := fake.New()
	// Already labeled to match the card's column, so a push from this
	// same state has nothing to fix up either: this test is purely
	// about the lossy-title comparison, not status drift.
	p.Seed(domain.Issue{ID: 31, Title: "Fix: crash", State: domain.IssueOpen, Labels: []string{"status:backlog"}})

	a, board, _ := newApplier(t, cfg, p)
	ctx := context.Background()
	remote, missing, err := FetchRemote(ctx, p, board)
	if err != nil {
		t.Fatalf("FetchRemote() error = %v", err)
	}
	plan := PlanPull(Input{Cfg: cfg, Board: board, Remote: remote, Missing: missing, HasNote: hasNoteFn(a)})
	if !plan.Empty() {
		t.Fatalf("first PlanPull = %#v, want none (sanitize(\"Fix: crash\") == sanitize(\"Fix crash\"))", plan.Changes)
	}

	if err := a.Apply(ctx, plan); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	a2, board2, _ := newApplier(t, cfg, p)
	remote2, missing2, err := FetchRemote(ctx, p, board2)
	if err != nil {
		t.Fatalf("FetchRemote() (2nd) error = %v", err)
	}
	plan2 := PlanPull(Input{Cfg: cfg, Board: board2, Remote: remote2, Missing: missing2, HasNote: hasNoteFn(a2)})
	if !plan2.Empty() {
		t.Errorf("second PlanPull = %#v, want none", plan2.Changes)
	}

	plan3 := PlanPush(Input{Cfg: cfg, Board: board2, Remote: remote2, Missing: missing2, HasNote: hasNoteFn(a2)})
	if !plan3.Empty() {
		t.Errorf("PlanPush from the same state = %#v, want none", plan3.Changes)
	}
}
