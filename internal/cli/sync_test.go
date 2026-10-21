package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kanban-sync/internal/domain"
	"kanban-sync/internal/provider"
	"kanban-sync/internal/provider/fake"
)

// testConfigYAML is a project config with the four statuses from spec
// §4, notes always on, and a provider name ("fake") that's never
// actually resolved through the registry: every test wires up
// Deps.NewProvider to return a shared fake.Provider directly.
const testConfigYAML = `
provider: fake
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
  create: always
`

// testEnv is a temp project directory wired up with a shared fake
// provider, ready to drive Main with.
type testEnv struct {
	t      *testing.T
	dir    string
	prov   *fake.Provider
	deps   Deps
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

// newTestEnv creates a temp project (.issue-sync.yml, Kanban.md,
// Tasks/) and Deps pointing at it: Getwd returns the project dir,
// Getenv returns "" for everything (no real env leaks in), IsTerminal
// is false, and NewProvider always returns the same fake.Provider.
func newTestEnv(t *testing.T, kanbanSrc string) *testEnv {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".issue-sync.yml"), []byte(testConfigYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Kanban.md"), []byte(kanbanSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "Tasks"), 0o755); err != nil {
		t.Fatal(err)
	}

	p := fake.New()
	var out, errBuf bytes.Buffer

	env := &testEnv{t: t, dir: dir, prov: p, stdout: &out, stderr: &errBuf}
	env.deps = Deps{
		Stdin:      strings.NewReader(""),
		Stdout:     &out,
		Stderr:     &errBuf,
		Getenv:     func(string) string { return "" },
		Getwd:      func() (string, error) { return dir, nil },
		IsTerminal: false,
		Version:    "test",
		NewProvider: func(ctx context.Context, cfg provider.Config) (provider.IssueProvider, error) {
			return p, nil
		},
	}
	return env
}

// run invokes Main with env's Deps, resetting stdout/stderr first.
func (e *testEnv) run(args ...string) int {
	e.stdout.Reset()
	e.stderr.Reset()
	return Main(context.Background(), e.deps, args)
}

// kanban returns the current contents of the project's Kanban.md.
func (e *testEnv) kanban() string {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.dir, "Kanban.md"))
	if err != nil {
		e.t.Fatalf("reading Kanban.md: %v", err)
	}
	return string(data)
}

// --- status -------------------------------------------------------------

func TestStatusCommand(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n- [ ] Some title `#57`\n\n## In Progress\n\n## Completed\n")
	before := env.kanban()

	env.prov.Seed(
		domain.Issue{ID: 57, Title: "Some title", State: domain.IssueOpen, Labels: []string{"status:in-progress"}},
		domain.Issue{ID: 80, Title: "New thing", State: domain.IssueOpen, Labels: []string{"status:todo"}},
	)

	code := env.run("status")
	if code != 0 {
		t.Fatalf("Main(status) = %d, want 0; stderr=%s", code, env.stderr.String())
	}

	out := env.stdout.String()
	for _, want := range []string{"#57", "Todo", "In Progress", "!", "+remote"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output = %q, want it to contain %q", out, want)
		}
	}

	if got := env.kanban(); got != before {
		t.Errorf("status modified Kanban.md: got %q, want %q", got, before)
	}
}

// TestStatusOpenInCompletedMarker covers spec §6.5/§6.6's "open, but
// in Completed" case: an issue that's still open but carries the
// closeIssue column's label. Status/push must treat it as belonging
// to that column (it's a RowOK, not RowDiff, since both Local and the
// resolved Remote are "completed"), but the state marker still needs
// to flag the contradiction so it isn't mistaken for "closed".
func TestStatusOpenInCompletedMarker(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n\n- [ ] Already done `#9`\n")
	env.prov.Seed(domain.Issue{ID: 9, Title: "Already done", State: domain.IssueOpen, Labels: []string{"status:completed"}})

	code := env.run("status")
	if code != 0 {
		t.Fatalf("Main(status) = %d, want 0; stderr=%s", code, env.stderr.String())
	}

	out := env.stdout.String()
	if !strings.Contains(out, "✓ (open)") {
		t.Errorf("status output = %q, want a \"✓ (open)\" marker for #9", out)
	}
}

// --- pull -------------------------------------------------------------

func TestPullCommand(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n")
	env.prov.Seed(domain.Issue{ID: 80, Title: "New thing", State: domain.IssueOpen, Labels: []string{"status:todo"}})

	code := env.run("pull")
	if code != 0 {
		t.Fatalf("Main(pull) = %d, want 0; stderr=%s", code, env.stderr.String())
	}
	if !strings.Contains(env.stdout.String(), "Changes:") {
		t.Errorf("pull output = %q, want it to contain %q", env.stdout.String(), "Changes:")
	}

	got := env.kanban()
	if !strings.Contains(got, "[[Tasks/80 ") {
		t.Errorf("Kanban.md = %q, want a note link for #80", got)
	}

	code = env.run("pull")
	if code != 0 {
		t.Fatalf("second Main(pull) = %d, want 0; stderr=%s", code, env.stderr.String())
	}
	if !strings.Contains(env.stdout.String(), "No changes.") {
		t.Errorf("second pull output = %q, want it to contain %q", env.stdout.String(), "No changes.")
	}
}

func TestPullDryRun(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n")
	env.prov.Seed(domain.Issue{ID: 80, Title: "New thing", State: domain.IssueOpen, Labels: []string{"status:todo"}})
	before := env.kanban()

	code := env.run("pull", "--dry-run")
	if code != 0 {
		t.Fatalf("Main(pull --dry-run) = %d, want 0; stderr=%s", code, env.stderr.String())
	}
	if !strings.Contains(env.stdout.String(), "Changes:") {
		t.Errorf("dry-run output = %q, want it to contain the plan", env.stdout.String())
	}

	if got := env.kanban(); got != before {
		t.Errorf("--dry-run modified Kanban.md: got %q, want %q", got, before)
	}
}

// --- push -------------------------------------------------------------

func TestPushRequiresYesWithoutTTY(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n- [ ] New thing\n\n## In Progress\n\n## Completed\n")

	code := env.run("push")
	if code != 1 {
		t.Fatalf("Main(push) = %d, want 1; stdout=%s stderr=%s", code, env.stdout.String(), env.stderr.String())
	}
	if !strings.Contains(env.stderr.String(), "refusing to push without confirmation") {
		t.Errorf("stderr = %q, want the refusal message", env.stderr.String())
	}

	for _, c := range env.prov.Calls() {
		if strings.HasPrefix(c, "create") || strings.HasPrefix(c, "update") {
			t.Errorf("Calls() = %v, want no create/update", env.prov.Calls())
			break
		}
	}
}

func TestPushYes(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n- [ ] New thing\n\n## In Progress\n\n## Completed\n")

	code := env.run("push", "--yes")
	if code != 0 {
		t.Fatalf("Main(push --yes) = %d, want 0; stdout=%s stderr=%s", code, env.stdout.String(), env.stderr.String())
	}

	got := env.kanban()
	if !strings.Contains(got, "`#1`") {
		t.Errorf("Kanban.md = %q, want the new issue's id recorded", got)
	}
	if issue := env.prov.Issue(1); issue.Title != "New thing" {
		t.Errorf("fake issue #1 = %+v, want it created", issue)
	}
}

func TestPushConfirmPrompt(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n- [ ] New thing\n\n## In Progress\n\n## Completed\n")
	env.deps.IsTerminal = true
	env.deps.Stdin = strings.NewReader("n\n")
	before := env.kanban()

	code := env.run("push")
	if code != 0 {
		t.Fatalf("Main(push) = %d, want 0; stdout=%s stderr=%s", code, env.stdout.String(), env.stderr.String())
	}
	if !strings.Contains(env.stdout.String(), "Aborted.") {
		t.Errorf("stdout = %q, want %q", env.stdout.String(), "Aborted.")
	}

	if got := env.kanban(); got != before {
		t.Errorf("aborted push modified Kanban.md: got %q, want %q", got, before)
	}
	for _, c := range env.prov.Calls() {
		if strings.HasPrefix(c, "create") || strings.HasPrefix(c, "update") {
			t.Errorf("Calls() = %v, want no create/update", env.prov.Calls())
			break
		}
	}
}

// --- errors -------------------------------------------------------------

func TestNoConfig(t *testing.T) {
	dir := t.TempDir() // no .issue-sync.yml here

	var out, errBuf bytes.Buffer
	d := Deps{
		Stdin:   strings.NewReader(""),
		Stdout:  &out,
		Stderr:  &errBuf,
		Getenv:  func(string) string { return "" },
		Getwd:   func() (string, error) { return dir, nil },
		Version: "test",
		NewProvider: func(ctx context.Context, cfg provider.Config) (provider.IssueProvider, error) {
			t.Fatal("NewProvider should not be called when the project can't be resolved")
			return nil, nil
		},
	}

	code := Main(context.Background(), d, []string{"status", "--project", dir})
	if code != 1 {
		t.Fatalf("Main(status) = %d, want 1; stderr=%s", code, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), ".issue-sync.yml") {
		t.Errorf("stderr = %q, want it to mention .issue-sync.yml", errBuf.String())
	}
}

func TestDuplicateCardsError(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n- [ ] First `#42`\n\n## Todo\n\n- [ ] Second `#42`\n\n## In Progress\n\n## Completed\n")

	code := env.run("status")
	if code != 1 {
		t.Fatalf("Main(status) = %d, want 1; stdout=%s", code, env.stdout.String())
	}
	errOut := env.stderr.String()
	if !strings.Contains(errOut, "#42") {
		t.Errorf("stderr = %q, want it to mention #42", errOut)
	}
	if !strings.Contains(errOut, "3") || !strings.Contains(errOut, "7") {
		t.Errorf("stderr = %q, want it to mention the duplicate's line numbers (3, 7)", errOut)
	}
}
