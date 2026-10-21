package reconcile

import (
	"strings"
	"testing"

	"kanban-sync/internal/config"
	"kanban-sync/internal/domain"
	"kanban-sync/internal/kanban"
	"kanban-sync/internal/provider"
)

// --- test fixtures --------------------------------------------------

// testConfig builds a *config.Config with the four statuses from spec
// §4, and notes.create set to notesMode ("always" or "on-demand").
func testConfig(t *testing.T, notesMode string) *config.Config {
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

	cfg, err := config.Parse([]byte(yamlSrc), "/project/.issue-sync.yml")
	if err != nil {
		t.Fatalf("parsing test config: %v", err)
	}
	return cfg
}

func testStatuses() []domain.Status {
	return []domain.Status{
		{Key: "backlog", Heading: "Backlog", Label: "status:backlog"},
		{Key: "todo", Heading: "Todo", Label: "status:todo"},
		{Key: "in-progress", Heading: "In Progress", Label: "status:in-progress"},
		{Key: "completed", Heading: "Completed", Label: "status:completed", CloseIssue: true},
	}
}

// buildBoard parses src (a plain, non-raw string so it can contain
// backtick-wrapped "`#id`" tokens) as a Kanban board.
func buildBoard(t *testing.T, src string) *kanban.Board {
	t.Helper()
	b, err := kanban.Parse([]byte(src), testStatuses())
	if err != nil {
		t.Fatalf("parsing board: %v", err)
	}
	return b
}

func noNote(*kanban.Card) bool { return false }

func allNotes(*kanban.Card) bool { return true }

// --- PlanPull ---------------------------------------------------------

func TestPlanPullMovesCard(t *testing.T) {
	cfg := testConfig(t, "on-demand")
	board := buildBoard(t, "## Backlog\n\n## Todo\n\n- [ ] Some title `#57`\n\n## In Progress\n\n## Completed\n")

	in := Input{
		Cfg:   cfg,
		Board: board,
		Remote: map[int]domain.Issue{
			57: {ID: 57, Title: "Some title", State: domain.IssueOpen, Labels: []string{"status:in-progress"}},
		},
		Missing: map[int]bool{},
		HasNote: allNotes,
	}

	plan := PlanPull(in)

	if len(plan.Changes) != 1 {
		t.Fatalf("Changes = %#v, want exactly one MoveCard", plan.Changes)
	}
	mc, ok := plan.Changes[0].(MoveCard)
	if !ok {
		t.Fatalf("Changes[0] = %#v, want MoveCard", plan.Changes[0])
	}
	if mc.Card.IssueID != 57 || mc.From != "todo" || mc.To != "in-progress" {
		t.Errorf("MoveCard = %+v, want {#57 todo->in-progress}", mc)
	}
	if len(plan.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", plan.Warnings)
	}
}

func TestPlanPullClosedToCompleted(t *testing.T) {
	cfg := testConfig(t, "on-demand")
	board := buildBoard(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n- [ ] Some title `#42`\n\n## Completed\n")

	in := Input{
		Cfg:   cfg,
		Board: board,
		Remote: map[int]domain.Issue{
			42: {ID: 42, Title: "Some title", State: domain.IssueClosed},
		},
		Missing: map[int]bool{},
		HasNote: allNotes,
	}

	plan := PlanPull(in)

	if len(plan.Changes) != 1 {
		t.Fatalf("Changes = %#v, want exactly one MoveCard", plan.Changes)
	}
	mc, ok := plan.Changes[0].(MoveCard)
	if !ok {
		t.Fatalf("Changes[0] = %#v, want MoveCard", plan.Changes[0])
	}
	if mc.From != "in-progress" || mc.To != "completed" {
		t.Errorf("MoveCard = %+v, want in-progress->completed", mc)
	}
}

func TestPlanPullAddsUnknownOpen(t *testing.T) {
	board := buildBoard(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n")
	remote := map[int]domain.Issue{
		80: {ID: 80, Title: "New thing", State: domain.IssueOpen},
	}

	t.Run("always", func(t *testing.T) {
		in := Input{Cfg: testConfig(t, "always"), Board: board, Remote: remote, Missing: map[int]bool{}, HasNote: noNote}
		plan := PlanPull(in)
		if len(plan.Changes) != 1 {
			t.Fatalf("Changes = %#v, want exactly one AddCard", plan.Changes)
		}
		ac, ok := plan.Changes[0].(AddCard)
		if !ok {
			t.Fatalf("Changes[0] = %#v, want AddCard", plan.Changes[0])
		}
		if ac.Issue.ID != 80 || ac.To != "backlog" || !ac.WithNote {
			t.Errorf("AddCard = %+v, want {#80 backlog WithNote:true}", ac)
		}
	})

	t.Run("on-demand", func(t *testing.T) {
		in := Input{Cfg: testConfig(t, "on-demand"), Board: board, Remote: remote, Missing: map[int]bool{}, HasNote: noNote}
		plan := PlanPull(in)
		ac, ok := plan.Changes[0].(AddCard)
		if !ok {
			t.Fatalf("Changes[0] = %#v, want AddCard", plan.Changes[0])
		}
		if ac.WithNote {
			t.Errorf("AddCard.WithNote = true, want false in on-demand mode")
		}
	})
}

func TestPlanPullIgnoresUnknownClosed(t *testing.T) {
	cfg := testConfig(t, "on-demand")
	board := buildBoard(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	// Issue #81 is closed and was never on the board, so FetchRemote
	// would never have fetched it: it simply isn't in Remote at all.
	in := Input{Cfg: cfg, Board: board, Remote: map[int]domain.Issue{}, Missing: map[int]bool{}, HasNote: noNote}

	plan := PlanPull(in)

	if !plan.Empty() {
		t.Errorf("Changes = %#v, want none", plan.Changes)
	}
}

func TestPlanPullRename(t *testing.T) {
	cfg := testConfig(t, "on-demand")
	board := buildBoard(t, "## Backlog\n\n- [ ] Yandex OAuth `#1`\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	in := Input{
		Cfg:   cfg,
		Board: board,
		Remote: map[int]domain.Issue{
			1: {ID: 1, Title: "Yandex OAuth 2.0", State: domain.IssueOpen},
		},
		Missing: map[int]bool{},
		HasNote: allNotes,
	}

	plan := PlanPull(in)

	if len(plan.Changes) != 1 {
		t.Fatalf("Changes = %#v, want exactly one RenameLocal", plan.Changes)
	}
	rl, ok := plan.Changes[0].(RenameLocal)
	if !ok {
		t.Fatalf("Changes[0] = %#v, want RenameLocal", plan.Changes[0])
	}
	if rl.From != "Yandex OAuth" || rl.To != "Yandex OAuth 2.0" || rl.ID != 1 {
		t.Errorf("RenameLocal = %+v, want {ID:1 From:\"Yandex OAuth\" To:\"Yandex OAuth 2.0\"}", rl)
	}
}

func TestPullLossyTitleNoDrift(t *testing.T) {
	cfg := testConfig(t, "on-demand")
	board := buildBoard(t, "## Backlog\n\n- [ ] [[Tasks/31 Fix crash]] `#31`\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	in := Input{
		Cfg:   cfg,
		Board: board,
		Remote: map[int]domain.Issue{
			31: {ID: 31, Title: "Fix: crash", State: domain.IssueOpen},
		},
		Missing: map[int]bool{},
		HasNote: allNotes,
	}

	plan := PlanPull(in)

	if !plan.Empty() {
		t.Errorf("Changes = %#v, want none (sanitize(\"Fix: crash\") == sanitize(\"Fix crash\"))", plan.Changes)
	}
}

func TestPlanPullLinkNote(t *testing.T) {
	board := buildBoard(t, "## Backlog\n\n- [ ] Some title `#5`\n\n## Todo\n\n## In Progress\n\n## Completed\n")
	remote := map[int]domain.Issue{
		5: {ID: 5, Title: "Some title", State: domain.IssueOpen},
	}

	t.Run("always", func(t *testing.T) {
		in := Input{Cfg: testConfig(t, "always"), Board: board, Remote: remote, Missing: map[int]bool{}, HasNote: noNote}
		plan := PlanPull(in)
		if len(plan.Changes) != 1 {
			t.Fatalf("Changes = %#v, want exactly one LinkNote", plan.Changes)
		}
		ln, ok := plan.Changes[0].(LinkNote)
		if !ok {
			t.Fatalf("Changes[0] = %#v, want LinkNote", plan.Changes[0])
		}
		if ln.ID != 5 || ln.Title != "Some title" {
			t.Errorf("LinkNote = %+v, want {ID:5 Title:\"Some title\"}", ln)
		}
	})

	t.Run("on-demand", func(t *testing.T) {
		in := Input{Cfg: testConfig(t, "on-demand"), Board: board, Remote: remote, Missing: map[int]bool{}, HasNote: noNote}
		plan := PlanPull(in)
		if !plan.Empty() {
			t.Errorf("Changes = %#v, want none in on-demand mode", plan.Changes)
		}
	})
}

func TestPlanPullMissingWarns(t *testing.T) {
	cfg := testConfig(t, "on-demand")
	board := buildBoard(t, "## Backlog\n\n- [ ] Ghost `#99`\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	in := Input{Cfg: cfg, Board: board, Remote: map[int]domain.Issue{}, Missing: map[int]bool{99: true}, HasNote: noNote}

	plan := PlanPull(in)

	if !plan.Empty() {
		t.Errorf("Changes = %#v, want none (card left unchanged)", plan.Changes)
	}
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "#99 not found") {
		t.Errorf("Warnings = %v, want one containing %q", plan.Warnings, "#99 not found")
	}
}

func TestPlanPullSkipsCardsWithoutID(t *testing.T) {
	cfg := testConfig(t, "always")
	board := buildBoard(t, "## Backlog\n\n- [ ] No issue yet\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	in := Input{Cfg: cfg, Board: board, Remote: map[int]domain.Issue{}, Missing: map[int]bool{}, HasNote: noNote}

	plan := PlanPull(in)

	if !plan.Empty() {
		t.Errorf("Changes = %#v, want none (card has no id)", plan.Changes)
	}
}

// --- PlanPush ---------------------------------------------------------

func TestPlanPushStatusLabels(t *testing.T) {
	cfg := testConfig(t, "on-demand")
	board := buildBoard(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n- [ ] Some title `#57`\n\n## Completed\n")

	in := Input{
		Cfg:   cfg,
		Board: board,
		Remote: map[int]domain.Issue{
			57: {ID: 57, Title: "Some title", State: domain.IssueOpen, Labels: []string{"bug", "status:todo"}},
		},
		Missing: map[int]bool{},
		HasNote: noNote,
	}

	plan := PlanPush(in)

	if len(plan.Changes) != 1 {
		t.Fatalf("Changes = %#v, want exactly one SetStatus", plan.Changes)
	}
	ss, ok := plan.Changes[0].(SetStatus)
	if !ok {
		t.Fatalf("Changes[0] = %#v, want SetStatus", plan.Changes[0])
	}
	if ss.ID != 57 || ss.From != "todo" || ss.To != "in-progress" {
		t.Errorf("SetStatus = %+v, want {57 todo->in-progress}", ss)
	}
	if ss.Update.State != nil {
		t.Errorf("Update.State = %v, want nil", ss.Update.State)
	}
	if got := ss.Update.AddLabels; len(got) != 1 || got[0] != "status:in-progress" {
		t.Errorf("Update.AddLabels = %v, want [status:in-progress]", got)
	}
	if got := ss.Update.RemoveLabels; len(got) != 1 || got[0] != "status:todo" {
		t.Errorf("Update.RemoveLabels = %v, want [status:todo]", got)
	}
}

func TestPlanPushClose(t *testing.T) {
	cfg := testConfig(t, "on-demand")
	board := buildBoard(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n\n- [ ] Some title `#1`\n")

	in := Input{
		Cfg:   cfg,
		Board: board,
		Remote: map[int]domain.Issue{
			1: {ID: 1, Title: "Some title", State: domain.IssueOpen, Labels: []string{"status:in-progress"}},
		},
		Missing: map[int]bool{},
		HasNote: noNote,
	}

	plan := PlanPush(in)

	ss, ok := plan.Changes[0].(SetStatus)
	if !ok {
		t.Fatalf("Changes[0] = %#v, want SetStatus", plan.Changes[0])
	}
	if ss.Update.State == nil || *ss.Update.State != domain.IssueClosed {
		t.Errorf("Update.State = %v, want &closed", ss.Update.State)
	}
	if got := ss.Update.AddLabels; len(got) != 1 || got[0] != "status:completed" {
		t.Errorf("Update.AddLabels = %v, want [status:completed]", got)
	}
	if got := ss.Update.RemoveLabels; len(got) != 1 || got[0] != "status:in-progress" {
		t.Errorf("Update.RemoveLabels = %v, want [status:in-progress]", got)
	}
}

func TestPlanPushReopen(t *testing.T) {
	cfg := testConfig(t, "on-demand")
	board := buildBoard(t, "## Backlog\n\n## Todo\n\n- [ ] Some title `#1`\n\n## In Progress\n\n## Completed\n")

	in := Input{
		Cfg:   cfg,
		Board: board,
		Remote: map[int]domain.Issue{
			1: {ID: 1, Title: "Some title", State: domain.IssueClosed, Labels: []string{"status:completed"}},
		},
		Missing: map[int]bool{},
		HasNote: noNote,
	}

	plan := PlanPush(in)

	ss, ok := plan.Changes[0].(SetStatus)
	if !ok {
		t.Fatalf("Changes[0] = %#v, want SetStatus", plan.Changes[0])
	}
	if ss.Update.State == nil || *ss.Update.State != domain.IssueOpen {
		t.Errorf("Update.State = %v, want &open", ss.Update.State)
	}
	if got := ss.Update.AddLabels; len(got) != 1 || got[0] != "status:todo" {
		t.Errorf("Update.AddLabels = %v, want [status:todo]", got)
	}
	if got := ss.Update.RemoveLabels; len(got) != 1 || got[0] != "status:completed" {
		t.Errorf("Update.RemoveLabels = %v, want [status:completed]", got)
	}
}

func TestPlanPushCreate(t *testing.T) {
	cfg := testConfig(t, "always")
	board := buildBoard(t, "## Backlog\n\n## Todo\n\n- [ ] New thing\n\n## In Progress\n\n## Completed\n")

	in := Input{Cfg: cfg, Board: board, Remote: map[int]domain.Issue{}, Missing: map[int]bool{}, HasNote: noNote}

	plan := PlanPush(in)

	if len(plan.Changes) != 1 {
		t.Fatalf("Changes = %#v, want exactly one CreateIssue", plan.Changes)
	}
	ci, ok := plan.Changes[0].(CreateIssue)
	if !ok {
		t.Fatalf("Changes[0] = %#v, want CreateIssue", plan.Changes[0])
	}
	if ci.Title != "New thing" || ci.Status != "todo" || !ci.WithNote {
		t.Errorf("CreateIssue = %+v, want {Title:\"New thing\" Status:todo WithNote:true}", ci)
	}
}

func TestPlanPushCreateSkipsEmptyTitle(t *testing.T) {
	cfg := testConfig(t, "on-demand")
	board := buildBoard(t, "## Backlog\n\n## Todo\n\n- [ ] \n\n## In Progress\n\n## Completed\n")

	in := Input{Cfg: cfg, Board: board, Remote: map[int]domain.Issue{}, Missing: map[int]bool{}, HasNote: noNote}

	plan := PlanPush(in)

	if !plan.Empty() {
		t.Errorf("Changes = %#v, want none (empty title skipped)", plan.Changes)
	}
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "has no title; skipped") {
		t.Errorf("Warnings = %v, want one containing %q", plan.Warnings, "has no title; skipped")
	}
}

func TestPlanPushRenameRemote(t *testing.T) {
	cfg := testConfig(t, "on-demand")
	board := buildBoard(t, "## Backlog\n\n- [ ] [[Tasks/42 OAuth v2]] `#42`\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	in := Input{
		Cfg:   cfg,
		Board: board,
		Remote: map[int]domain.Issue{
			42: {ID: 42, Title: "OAuth", State: domain.IssueOpen, Labels: []string{"status:backlog"}},
		},
		Missing: map[int]bool{},
		HasNote: noNote,
	}

	plan := PlanPush(in)

	if len(plan.Changes) != 1 {
		t.Fatalf("Changes = %#v, want exactly one RenameRemote", plan.Changes)
	}
	rr, ok := plan.Changes[0].(RenameRemote)
	if !ok {
		t.Fatalf("Changes[0] = %#v, want RenameRemote", plan.Changes[0])
	}
	if rr.ID != 42 || rr.From != "OAuth" || rr.To != "OAuth v2" {
		t.Errorf("RenameRemote = %+v, want {ID:42 From:\"OAuth\" To:\"OAuth v2\"}", rr)
	}
}

func TestPlanPushIgnoresRemoteOnly(t *testing.T) {
	cfg := testConfig(t, "on-demand")
	board := buildBoard(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	in := Input{
		Cfg:   cfg,
		Board: board,
		Remote: map[int]domain.Issue{
			7: {ID: 7, Title: "Not on board", State: domain.IssueOpen},
		},
		Missing: map[int]bool{},
		HasNote: noNote,
	}

	plan := PlanPush(in)

	if !plan.Empty() {
		t.Errorf("Changes = %#v, want none (remote-only issues untouched by push)", plan.Changes)
	}
}

// --- Status -------------------------------------------------------------

func TestStatusRows(t *testing.T) {
	cfg := testConfig(t, "on-demand")
	board := buildBoard(t, strings.Join([]string{
		"## Backlog",
		"",
		"- [ ] No id yet",
		"",
		"## Todo",
		"",
		"- [ ] Matches `#1`",
		"- [ ] Diverged `#2`",
		"- [ ] Ghost `#3`",
		"",
		"## In Progress",
		"",
		"## Completed",
		"",
	}, "\n"))

	in := Input{
		Cfg:   cfg,
		Board: board,
		Remote: map[int]domain.Issue{
			1: {ID: 1, Title: "Matches", State: domain.IssueOpen, Labels: []string{"status:todo"}},
			2: {ID: 2, Title: "Diverged!!", State: domain.IssueOpen, Labels: []string{"status:in-progress"}},
			4: {ID: 4, Title: "Remote only", State: domain.IssueOpen},
		},
		Missing: map[int]bool{3: true},
		HasNote: noNote,
	}

	rows, warnings := Status(in)

	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}

	want := []RowKind{RowLocalOnly, RowOK, RowDiff, RowMissing, RowRemoteOnly}
	if len(rows) != len(want) {
		t.Fatalf("rows = %#v, want %d rows of kinds %v", rows, len(want), want)
	}
	for i, k := range want {
		if rows[i].Kind != k {
			t.Errorf("rows[%d].Kind = %v, want %v (row=%+v)", i, rows[i].Kind, k, rows[i])
		}
	}
	if rows[2].TitleDiff != true {
		t.Errorf("rows[2] (Diverged) TitleDiff = false, want true")
	}
	if rows[4].ID != 4 || rows[4].Title != "Remote only" {
		t.Errorf("rows[4] = %+v, want remote-only issue #4", rows[4])
	}
}

// --- Describe -------------------------------------------------------------

func TestDescribe(t *testing.T) {
	cfg := testConfig(t, "on-demand")
	closed := domain.IssueClosed
	open := domain.IssueOpen

	card := &kanban.Card{IssueID: 57}

	cases := []struct {
		name string
		c    Change
		want string
	}{
		{"move", MoveCard{Card: card, From: "todo", To: "in-progress"}, "#57 move: Todo → In Progress"},
		{
			"add",
			AddCard{Issue: domain.Issue{ID: 80, Title: "Title"}, To: "backlog", WithNote: false},
			`#80 add "Title" to Backlog`,
		},
		{
			"add with note",
			AddCard{Issue: domain.Issue{ID: 80, Title: "Title"}, To: "backlog", WithNote: true},
			`#80 add "Title" to Backlog with note`,
		},
		{"rename local", RenameLocal{ID: 42, From: "Old", To: "New"}, `#42 rename: "Old" → "New"`},
		{"link note", LinkNote{ID: 42, Title: "Title"}, `#42 create note for "Title"`},
		{
			"create issue",
			CreateIssue{Title: "New thing", Status: "todo", WithNote: false},
			`create issue "New thing" in Todo`,
		},
		{
			"create issue with note",
			CreateIssue{Title: "New thing", Status: "todo", WithNote: true},
			`create issue "New thing" in Todo with note`,
		},
		{
			"set status close",
			SetStatus{ID: 57, From: "todo", To: "completed", Update: provider.IssueUpdate{State: &closed}},
			"#57 status: Todo → Completed, close issue",
		},
		{
			"set status reopen",
			SetStatus{ID: 57, From: "completed", To: "todo", Update: provider.IssueUpdate{State: &open}},
			"#57 status: Completed → Todo, reopen issue",
		},
		{"rename remote", RenameRemote{ID: 42, From: "Old", To: "New"}, `#42 rename issue: "Old" → "New"`},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Describe(cfg, c.c); got != c.want {
				t.Errorf("Describe() = %q, want %q", got, c.want)
			}
		})
	}
}
