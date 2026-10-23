package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HayKor/yi/internal/domain"
	"github.com/HayKor/yi/internal/gitrepo"
)

// gitConfigYAMLTemplate is testConfigYAML plus a "git" section pointing
// at a disposable repo created by withGit.
const gitConfigYAMLTemplate = `
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

git:
  repository: %s
`

// runGit runs git with args against dir, isolated from the user's real
// git config (so tests never depend on - or pollute - ~/.gitconfig).
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// withGit creates a disposable git repo (skipping the test if git isn't
// installed), points env's .issue-sync.yml "git.repository" at it, and
// wires env.deps.OpenGit to the real gitrepo.Open so `start` drives a
// real repository instead of a fake. It returns the repo's directory.
func withGit(t *testing.T, env *testEnv) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	repoDir := t.TempDir()
	runGit(t, repoDir, "init", "-b", "main")
	runGit(t, repoDir,
		"-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false",
		"commit", "--allow-empty", "-m", "init")

	cfg := fmt.Sprintf(gitConfigYAMLTemplate, repoDir)
	if err := os.WriteFile(filepath.Join(env.dir, ".issue-sync.yml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	env.deps.OpenGit = gitrepo.Open

	return repoDir
}

// currentBranch returns the short name of dir's currently checked out
// branch, via gitrepo.Open (not a parsed test assumption about .git).
func currentBranch(t *testing.T, dir string) string {
	t.Helper()
	b, err := gitrepo.Open(dir).CurrentBranch(context.Background())
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	return b
}

// --- new -------------------------------------------------------------

func TestNewCommand(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	code := env.run("new", "Creative Preview", "--status", "todo")
	if code != 0 {
		t.Fatalf("Main(new) = %d, want 0; stdout=%s stderr=%s", code, env.stdout.String(), env.stderr.String())
	}

	issue := env.prov.Issue(1)
	if issue.Title != "Creative Preview" {
		t.Errorf("issue.Title = %q, want %q", issue.Title, "Creative Preview")
	}
	if !issue.HasLabel("status:todo") {
		t.Errorf("issue.Labels = %v, want status:todo", issue.Labels)
	}

	out := env.stdout.String()
	if !strings.Contains(out, "✓ Created #1 https://fake/issues/1") {
		t.Errorf("stdout = %q, want the created line", out)
	}
	if !strings.Contains(out, "✓ Note Tasks/1 Creative Preview.md") {
		t.Errorf("stdout = %q, want the note line", out)
	}

	kb := env.kanban()
	if !strings.Contains(kb, "## Todo") {
		t.Fatalf("Kanban.md = %q", kb)
	}
	if !strings.Contains(kb, "[[Tasks/1 Creative Preview|Creative Preview]] `#1`") {
		t.Errorf("Kanban.md = %q, want the linked card", kb)
	}

	if _, err := os.Stat(filepath.Join(env.dir, "Tasks", "1 Creative Preview.md")); err != nil {
		t.Errorf("note file not created: %v", err)
	}
}

func TestNewDefaultStatus(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	code := env.run("new", "Some task")
	if code != 0 {
		t.Fatalf("Main(new) = %d, want 0; stdout=%s stderr=%s", code, env.stdout.String(), env.stderr.String())
	}

	issue := env.prov.Issue(1)
	if !issue.HasLabel("status:backlog") {
		t.Errorf("issue.Labels = %v, want status:backlog", issue.Labels)
	}

	kb := env.kanban()
	backlogIdx := strings.Index(kb, "## Backlog")
	todoIdx := strings.Index(kb, "## Todo")
	cardIdx := strings.Index(kb, "`#1`")
	if backlogIdx < 0 || todoIdx < 0 || cardIdx < 0 || !(backlogIdx < cardIdx && cardIdx < todoIdx) {
		t.Errorf("Kanban.md = %q, want #1 under Backlog", kb)
	}
}

func TestNewUnknownStatus(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	code := env.run("new", "Title", "--status", "nope")
	if code != 1 {
		t.Fatalf("Main(new) = %d, want 1; stdout=%s stderr=%s", code, env.stdout.String(), env.stderr.String())
	}
	if !strings.Contains(env.stderr.String(), `unknown status "nope"`) {
		t.Errorf("stderr = %q, want the unknown status message", env.stderr.String())
	}
}

func TestNewRequiresTitle(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	code := env.run("new")
	if code != 2 {
		t.Fatalf("Main(new) = %d, want 2; stderr=%s", code, env.stderr.String())
	}

	code = env.run("new", "  ")
	if code != 2 {
		t.Fatalf("Main(new \"  \") = %d, want 2; stderr=%s", code, env.stderr.String())
	}
}

// --- start -------------------------------------------------------------

func TestStartCommand(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n- [ ] Yandex OAuth `#42`\n\n## In Progress\n\n## Completed\n")
	env.prov.Seed(domain.Issue{ID: 42, Title: "Yandex OAuth", State: domain.IssueOpen, Labels: []string{"status:todo"}})
	repoDir := withGit(t, env)

	code := env.run("start", "#42")
	if code != 0 {
		t.Fatalf("Main(start) = %d, want 0; stdout=%s stderr=%s", code, env.stdout.String(), env.stderr.String())
	}

	out := env.stdout.String()
	for _, want := range []string{"✓ #42 moved to In Progress", "✓ Remote issue updated", "✓ Switched to feat/42-yandex-oauth"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout = %q, want it to contain %q", out, want)
		}
	}

	issue := env.prov.Issue(42)
	if !issue.HasLabel("status:in-progress") {
		t.Errorf("issue.Labels = %v, want status:in-progress", issue.Labels)
	}
	if issue.HasLabel("status:todo") {
		t.Errorf("issue.Labels = %v, want status:todo removed", issue.Labels)
	}

	kb := env.kanban()
	if !strings.Contains(kb, "## In Progress") {
		t.Fatalf("Kanban.md = %q", kb)
	}
	if !strings.Contains(kb, "[[Tasks/42 Yandex OAuth|Yandex OAuth]]") {
		t.Errorf("Kanban.md = %q, want a note link for #42", kb)
	}

	if got, want := currentBranch(t, repoDir), "feat/42-yandex-oauth"; got != want {
		t.Errorf("CurrentBranch = %q, want %q", got, want)
	}
}

func TestStartExistingBranch(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n- [ ] Yandex OAuth `#42`\n\n## In Progress\n\n## Completed\n")
	env.prov.Seed(domain.Issue{ID: 42, Title: "Yandex OAuth", State: domain.IssueOpen, Labels: []string{"status:todo"}})
	repoDir := withGit(t, env)
	runGit(t, repoDir, "branch", "feat/42-yandex-oauth")

	code := env.run("start", "42")
	if code != 0 {
		t.Fatalf("Main(start) = %d, want 0; stdout=%s stderr=%s", code, env.stdout.String(), env.stderr.String())
	}

	if got, want := currentBranch(t, repoDir), "feat/42-yandex-oauth"; got != want {
		t.Errorf("CurrentBranch = %q, want %q", got, want)
	}
}

func TestStartNotOnBoard(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n")
	env.prov.Seed(domain.Issue{ID: 7, Title: "Remote Only", State: domain.IssueOpen})

	code := env.run("start", "7")
	if code != 0 {
		t.Fatalf("Main(start) = %d, want 0; stdout=%s stderr=%s", code, env.stdout.String(), env.stderr.String())
	}

	kb := env.kanban()
	if !strings.Contains(kb, "## In Progress") {
		t.Fatalf("Kanban.md = %q", kb)
	}
	if !strings.Contains(kb, "[[Tasks/7 Remote Only|Remote Only]] `#7`") {
		t.Errorf("Kanban.md = %q, want the added card with its note", kb)
	}
}

func TestStartType(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n- [ ] Yandex OAuth `#42`\n\n## In Progress\n\n## Completed\n")
	env.prov.Seed(domain.Issue{ID: 42, Title: "Yandex OAuth", State: domain.IssueOpen, Labels: []string{"status:todo"}})
	repoDir := withGit(t, env)

	code := env.run("start", "42", "--type", "fix")
	if code != 0 {
		t.Fatalf("Main(start) = %d, want 0; stdout=%s stderr=%s", code, env.stdout.String(), env.stderr.String())
	}

	if got, want := currentBranch(t, repoDir), "fix/42-yandex-oauth"; got != want {
		t.Errorf("CurrentBranch = %q, want %q", got, want)
	}
}

func TestStartNoBranch(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n- [ ] Yandex OAuth `#42`\n\n## In Progress\n\n## Completed\n")
	env.prov.Seed(domain.Issue{ID: 42, Title: "Yandex OAuth", State: domain.IssueOpen, Labels: []string{"status:todo"}})
	repoDir := withGit(t, env)

	code := env.run("start", "42", "--no-branch")
	if code != 0 {
		t.Fatalf("Main(start) = %d, want 0; stdout=%s stderr=%s", code, env.stdout.String(), env.stderr.String())
	}

	if got, want := currentBranch(t, repoDir), "main"; got != want {
		t.Errorf("CurrentBranch = %q, want %q (unchanged)", got, want)
	}
	if strings.Contains(env.stdout.String(), "Switched") {
		t.Errorf("stdout = %q, want no Switched line with --no-branch", env.stdout.String())
	}
}

func TestStartReopensClosed(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n\n- [x] Yandex OAuth `#42`\n")
	env.prov.Seed(domain.Issue{ID: 42, Title: "Yandex OAuth", State: domain.IssueClosed, Labels: []string{"status:completed"}})

	code := env.run("start", "42")
	if code != 0 {
		t.Fatalf("Main(start) = %d, want 0; stdout=%s stderr=%s", code, env.stdout.String(), env.stderr.String())
	}

	issue := env.prov.Issue(42)
	if issue.State != domain.IssueOpen {
		t.Errorf("issue.State = %v, want open (reopened)", issue.State)
	}
	if !issue.HasLabel("status:in-progress") {
		t.Errorf("issue.Labels = %v, want status:in-progress", issue.Labels)
	}

	kb := env.kanban()
	if !strings.Contains(kb, "## In Progress") || !strings.Contains(kb, "`#42`") {
		t.Fatalf("Kanban.md = %q, want #42 moved to In Progress", kb)
	}
}

func TestStartStatusNotConfigured(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n## Completed\n")
	cfg := `
provider: fake
repository: my-org/repo

statuses:
  backlog:
    heading: Backlog
    label: status:backlog
  todo:
    heading: Todo
    label: status:todo
  completed:
    heading: Completed
    label: status:completed
    closeIssue: true

defaultStatus: backlog
notes:
  create: always
`
	if err := os.WriteFile(filepath.Join(env.dir, ".issue-sync.yml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	code := env.run("start", "1")
	if code != 1 {
		t.Fatalf("Main(start) = %d, want 1; stdout=%s stderr=%s", code, env.stdout.String(), env.stderr.String())
	}
	if !strings.Contains(env.stderr.String(), "startStatus is not configured") {
		t.Errorf("stderr = %q, want the startStatus message", env.stderr.String())
	}
}

func TestStartInvalidID(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	code := env.run("start", "abc")
	if code != 2 {
		t.Fatalf("Main(start abc) = %d, want 2; stderr=%s", code, env.stderr.String())
	}
}

func TestStartNotFoundRemote(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	code := env.run("start", "999")
	if code != 1 {
		t.Fatalf("Main(start 999) = %d, want 1; stdout=%s stderr=%s", code, env.stdout.String(), env.stderr.String())
	}
	if !strings.Contains(env.stderr.String(), "#999 not found") {
		t.Errorf("stderr = %q, want the not found message", env.stderr.String())
	}
}

func TestStartGitError(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n- [ ] Yandex OAuth `#42`\n\n## In Progress\n\n## Completed\n")
	env.prov.Seed(domain.Issue{ID: 42, Title: "Yandex OAuth", State: domain.IssueOpen, Labels: []string{"status:todo"}})

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	// A directory that's never been `git init`-ed: any git command
	// against it fails, which is enough to exercise the "local and
	// remote changes were applied" path without needing a real dirty
	// working tree.
	notARepo := t.TempDir()
	cfg := fmt.Sprintf(gitConfigYAMLTemplate, notARepo)
	if err := os.WriteFile(filepath.Join(env.dir, ".issue-sync.yml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	env.deps.OpenGit = gitrepo.Open

	code := env.run("start", "42")
	if code != 1 {
		t.Fatalf("Main(start) = %d, want 1; stdout=%s stderr=%s", code, env.stdout.String(), env.stderr.String())
	}
	if !strings.Contains(env.stderr.String(), "local and remote changes were applied") {
		t.Errorf("stderr = %q, want the applied-changes reminder", env.stderr.String())
	}

	// The local move and remote update must still have gone through
	// despite the later git failure.
	issue := env.prov.Issue(42)
	if !issue.HasLabel("status:in-progress") {
		t.Errorf("issue.Labels = %v, want status:in-progress despite the git error", issue.Labels)
	}
}

// --- done -------------------------------------------------------------

func TestDoneCommand(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n- [ ] Yandex OAuth `#42`\n\n## Completed\n")
	env.prov.Seed(domain.Issue{ID: 42, Title: "Yandex OAuth", State: domain.IssueOpen, Labels: []string{"status:in-progress"}})

	code := env.run("done", "42")
	if code != 0 {
		t.Fatalf("Main(done) = %d, want 0; stdout=%s stderr=%s", code, env.stdout.String(), env.stderr.String())
	}

	out := env.stdout.String()
	for _, want := range []string{"✓ #42 moved to Completed", "✓ Remote issue closed"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout = %q, want it to contain %q", out, want)
		}
	}

	issue := env.prov.Issue(42)
	if issue.State != domain.IssueClosed {
		t.Errorf("issue.State = %v, want closed", issue.State)
	}
	if !issue.HasLabel("status:completed") {
		t.Errorf("issue.Labels = %v, want status:completed", issue.Labels)
	}

	kb := env.kanban()
	if !strings.Contains(kb, "## Completed") {
		t.Fatalf("Kanban.md = %q", kb)
	}
	if !strings.Contains(kb, "- [x]") {
		t.Errorf("Kanban.md = %q, want the card checked", kb)
	}
}

func TestDoneNotOnBoard(t *testing.T) {
	env := newTestEnv(t, "## Backlog\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	code := env.run("done", "42")
	if code != 1 {
		t.Fatalf("Main(done) = %d, want 1; stdout=%s", code, env.stdout.String())
	}
	if !strings.Contains(env.stderr.String(), "#42 is not on the board") {
		t.Errorf("stderr = %q, want the not-on-board message", env.stderr.String())
	}
}
