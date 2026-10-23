package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HayKor/yi/internal/config"
	"github.com/HayKor/yi/internal/domain"
	"github.com/HayKor/yi/internal/kanban"
	"github.com/HayKor/yi/internal/provider"
	"github.com/HayKor/yi/internal/provider/fake"
)

// newInitEnv returns Deps rooted at an empty temp dir (Getwd), with a
// fake provider so a follow-up `pull` can run against the files init
// created.
func newInitEnv(t *testing.T) (string, Deps, *bytes.Buffer, *fake.Provider) {
	t.Helper()
	dir := t.TempDir()
	var out bytes.Buffer
	p := fake.New()
	d := Deps{
		Stdin:      strings.NewReader(""),
		Stdout:     &out,
		Stderr:     &bytes.Buffer{},
		Getenv:     func(string) string { return "" },
		Getwd:      func() (string, error) { return dir, nil },
		IsTerminal: false,
		Version:    "test",
		NewProvider: func(ctx context.Context, cfg provider.Config) (provider.IssueProvider, error) {
			return p, nil
		},
	}
	return dir, d, &out, p
}

// setRepository replaces the "owner/repo" placeholder in the
// .issue-sync.yml init generated in dir, as the user is told to.
func setRepository(t *testing.T, dir, repo string) {
	t.Helper()
	path := filepath.Join(dir, ".issue-sync.yml")
	cfg := readFile(t, path)
	if !strings.Contains(cfg, "repository: owner/repo") {
		t.Fatalf("generated config lacks the placeholder:\n%s", cfg)
	}
	cfg = strings.Replace(cfg, "repository: owner/repo", "repository: "+repo, 1)
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestInitCreatesConfigAndBoard(t *testing.T) {
	dir, d, out, _ := newInitEnv(t)

	if code := Main(context.Background(), d, []string{"init"}); code != 0 {
		t.Fatalf("init exit code = %d, want 0; stdout: %s", code, out)
	}
	for _, want := range []string{"created .issue-sync.yml", "created Kanban.md", "yi pull"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout missing %q:\n%s", want, out)
		}
	}

	setRepository(t, dir, "haykor/CarManager")
	cfg, err := config.Load(filepath.Join(dir, ".issue-sync.yml"))
	if err != nil {
		t.Fatalf("generated config doesn't load: %v", err)
	}
	if cfg.Provider != "github" {
		t.Errorf("provider = %q, want github", cfg.Provider)
	}
	var keys []domain.StatusKey
	for _, s := range cfg.Statuses {
		keys = append(keys, s.Key)
	}
	if got := len(keys); got != 4 {
		t.Fatalf("statuses = %v, want 4", keys)
	}
	if cfg.CloseStatus().Key != "done" {
		t.Errorf("close status = %q, want done", cfg.CloseStatus().Key)
	}

	board := readFile(t, filepath.Join(dir, "Kanban.md"))
	if !strings.HasPrefix(board, "---\n\nkanban-plugin: board\n\n---\n") {
		t.Errorf("Kanban.md lacks the plugin frontmatter:\n%s", board)
	}
	b, err := kanban.Parse([]byte(board), cfg.Statuses)
	if err != nil {
		t.Fatalf("generated Kanban.md doesn't parse: %v", err)
	}
	if n := len(b.Cards()); n != 0 {
		t.Errorf("generated board has %d cards, want 0", n)
	}
	for _, s := range cfg.Statuses {
		if strings.Count(board, "## "+s.Heading+"\n") != 1 {
			t.Errorf("board should have exactly one %q column:\n%s", s.Heading, board)
		}
	}
}

// After init, a pull must fill the generated columns rather than add
// new ones: the template's headings and the config's must agree.
func TestInitThenPullUsesGeneratedColumns(t *testing.T) {
	dir, d, out, p := newInitEnv(t)
	if code := Main(context.Background(), d, []string{"init"}); code != 0 {
		t.Fatalf("init exit code = %d; stdout: %s", code, out)
	}
	setRepository(t, dir, "haykor/CarManager")
	p.Seed(
		domain.Issue{ID: 1, Title: "Removing a Car", State: domain.IssueOpen, Labels: []string{"status:todo"}},
		domain.Issue{ID: 2, Title: "Adding a Car", State: domain.IssueOpen, Labels: []string{"status:in-progress"}},
	)

	if code := Main(context.Background(), d, []string{"pull"}); code != 0 {
		t.Fatalf("pull exit code = %d; stdout: %s", code, out)
	}

	board := readFile(t, filepath.Join(dir, "Kanban.md"))
	for _, h := range []string{"## Backlog\n", "## Todo\n", "## In Progress\n", "## Done\n"} {
		if c := strings.Count(board, h); c != 1 {
			t.Errorf("%q appears %d times after pull, want 1:\n%s", h, c, board)
		}
	}
	todo := strings.Index(board, "## Todo\n")
	inProgress := strings.Index(board, "## In Progress\n")
	card1 := strings.Index(board, "`#1`")
	card2 := strings.Index(board, "`#2`")
	if !(todo < card1 && card1 < inProgress && inProgress < card2) {
		t.Errorf("cards not in their columns:\n%s", board)
	}
}

// Running pull before replacing the placeholder must say so, not
// fail with a confusing "not found" from the provider.
func TestPullRejectsInitPlaceholder(t *testing.T) {
	_, d, out, _ := newInitEnv(t)
	var stderr bytes.Buffer
	d.Stderr = &stderr
	if code := Main(context.Background(), d, []string{"init"}); code != 0 {
		t.Fatalf("init exit code = %d; stdout: %s", code, out)
	}
	if code := Main(context.Background(), d, []string{"pull"}); code != 1 {
		t.Fatalf("pull with the placeholder exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), `"owner/repo" placeholder`) {
		t.Errorf("stderr doesn't mention the placeholder:\n%s", &stderr)
	}
}

func TestInitKeepsExistingFiles(t *testing.T) {
	dir, d, out, _ := newInitEnv(t)
	custom := "---\n\nkanban-plugin: board\n\n---\n\n## Mine\n\n- [ ] keep me\n"
	if err := os.WriteFile(filepath.Join(dir, "Kanban.md"), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}

	if code := Main(context.Background(), d, []string{"init"}); code != 0 {
		t.Fatalf("init exit code = %d; stdout: %s", code, out)
	}
	if got := readFile(t, filepath.Join(dir, "Kanban.md")); got != custom {
		t.Errorf("existing Kanban.md was modified:\n%s", got)
	}
	if !strings.Contains(out.String(), "skipped Kanban.md (already exists)") {
		t.Errorf("stdout missing skip notice:\n%s", out)
	}
	if !strings.Contains(out.String(), "created .issue-sync.yml") {
		t.Errorf("stdout missing config creation:\n%s", out)
	}

	// A second run touches nothing.
	cfgBefore := readFile(t, filepath.Join(dir, ".issue-sync.yml"))
	if code := Main(context.Background(), d, []string{"init"}); code != 0 {
		t.Fatalf("second init exit code = %d", code)
	}
	if got := readFile(t, filepath.Join(dir, ".issue-sync.yml")); got != cfgBefore {
		t.Errorf("existing config was modified by a second init")
	}
	if !strings.Contains(out.String(), "skipped .issue-sync.yml (already exists)") {
		t.Errorf("stdout missing config skip notice:\n%s", out)
	}
}

func TestInitProjectFlag(t *testing.T) {
	_, d, out, _ := newInitEnv(t)
	target := filepath.Join(t.TempDir(), "CarManager")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}

	if code := Main(context.Background(), d, []string{"init", "--project", target}); code != 0 {
		t.Fatalf("init exit code = %d; stdout: %s", code, out)
	}
	for _, name := range []string{".issue-sync.yml", "Kanban.md"} {
		if _, err := os.Stat(filepath.Join(target, name)); err != nil {
			t.Errorf("%s not created in --project dir: %v", name, err)
		}
	}
}

func TestInitMissingProjectDir(t *testing.T) {
	_, d, _, _ := newInitEnv(t)
	missing := filepath.Join(t.TempDir(), "nope")
	if code := Main(context.Background(), d, []string{"init", "--project", missing}); code != 1 {
		t.Fatalf("init into a missing dir exit code = %d, want 1", code)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Errorf("init created the missing project dir")
	}
}

func TestInitRejectsArgs(t *testing.T) {
	_, d, _, _ := newInitEnv(t)
	if code := Main(context.Background(), d, []string{"init", "extra"}); code != 2 {
		t.Fatalf("init with an argument exit code = %d, want 2", code)
	}
}
