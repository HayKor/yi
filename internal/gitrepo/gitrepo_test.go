package gitrepo

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// newTestRepo creates a temp git repository with a single empty commit on
// "main" and returns its directory. It skips the test if git isn't
// available, and isolates git from the user's real global/system config,
// hooks, and signing so tests behave the same everywhere.
func newTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH")
	}

	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir,
		"-c", "user.email=t@example.com",
		"-c", "user.name=t",
		"-c", "commit.gpgsign=false",
		"commit", "--allow-empty", "-m", "init")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestCurrentBranch(t *testing.T) {
	dir := newTestRepo(t)
	r := Open(dir)

	got, err := r.CurrentBranch(context.Background())
	if err != nil {
		t.Fatalf("CurrentBranch() error: %v", err)
	}
	if got != "main" {
		t.Fatalf("CurrentBranch() = %q, want %q", got, "main")
	}
}

func TestCreateAndSwitch(t *testing.T) {
	dir := newTestRepo(t)
	r := Open(dir)
	ctx := context.Background()

	if err := r.CreateAndSwitch(ctx, "feat/1-x"); err != nil {
		t.Fatalf("CreateAndSwitch() error: %v", err)
	}

	got, err := r.CurrentBranch(ctx)
	if err != nil {
		t.Fatalf("CurrentBranch() error: %v", err)
	}
	if got != "feat/1-x" {
		t.Fatalf("CurrentBranch() = %q, want %q", got, "feat/1-x")
	}

	exists, err := r.BranchExists(ctx, "feat/1-x")
	if err != nil {
		t.Fatalf("BranchExists() error: %v", err)
	}
	if !exists {
		t.Fatalf("BranchExists(%q) = false, want true", "feat/1-x")
	}
}

func TestSwitchExisting(t *testing.T) {
	dir := newTestRepo(t)
	r := Open(dir)
	ctx := context.Background()

	if err := r.CreateAndSwitch(ctx, "other"); err != nil {
		t.Fatalf("CreateAndSwitch() error: %v", err)
	}
	if err := r.Switch(ctx, "main"); err != nil {
		t.Fatalf("Switch() error: %v", err)
	}

	got, err := r.CurrentBranch(ctx)
	if err != nil {
		t.Fatalf("CurrentBranch() error: %v", err)
	}
	if got != "main" {
		t.Fatalf("CurrentBranch() = %q, want %q", got, "main")
	}
}

func TestBranchExistsFalse(t *testing.T) {
	dir := newTestRepo(t)
	r := Open(dir)

	exists, err := r.BranchExists(context.Background(), "nope")
	if err != nil {
		t.Fatalf("BranchExists() error: %v", err)
	}
	if exists {
		t.Fatalf("BranchExists(%q) = true, want false", "nope")
	}
}

func TestCheckBranchName(t *testing.T) {
	dir := newTestRepo(t)
	r := Open(dir)
	ctx := context.Background()

	if err := r.CheckBranchName(ctx, "feat/1-x"); err != nil {
		t.Fatalf("CheckBranchName(%q) error: %v", "feat/1-x", err)
	}
	if err := r.CheckBranchName(ctx, "bad..name"); err == nil {
		t.Fatalf("CheckBranchName(%q) = nil, want error", "bad..name")
	}
}

func TestGitErrorIncludesStderr(t *testing.T) {
	dir := newTestRepo(t)
	r := Open(dir)

	err := r.Switch(context.Background(), "nope")
	if err == nil {
		t.Fatalf("Switch(%q) = nil, want error", "nope")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("Switch(%q) error = %q, want it to mention %q", "nope", err.Error(), "nope")
	}
}
