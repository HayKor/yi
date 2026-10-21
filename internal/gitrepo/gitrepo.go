// Package gitrepo adapts the local git CLI to a small Repository
// interface used to create and switch issue branches (spec §6.9). It is
// deliberately separate from IssueProvider.
package gitrepo

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Repository is the subset of git operations yi needs against a working
// tree. All methods run against the directory passed to Open.
type Repository interface {
	// CurrentBranch returns the short name of the currently checked out
	// branch.
	CurrentBranch(ctx context.Context) (string, error)
	// BranchExists reports whether refs/heads/<name> exists.
	BranchExists(ctx context.Context, name string) (bool, error)
	// Switch checks out an existing branch.
	Switch(ctx context.Context, name string) error
	// CreateAndSwitch creates a new branch from the current HEAD and
	// checks it out.
	CreateAndSwitch(ctx context.Context, name string) error
	// CheckBranchName reports whether name is a valid git branch name.
	CheckBranchName(ctx context.Context, name string) error
}

// repo is the exec-based Repository implementation.
type repo struct {
	dir string
}

// Open returns a Repository backed by the git working tree at dir.
func Open(dir string) Repository {
	return &repo{dir: dir}
}

// validateBranchArg rejects branch names that look like command-line
// options, so a crafted or malformed name (e.g. "--help", "-f") can never
// be parsed by git as a flag instead of a ref name.
func validateBranchArg(name string) error {
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("invalid branch name %q", name)
	}
	return nil
}

// gitError formats a failed git invocation as "git <args>: <stderr>".
type gitError struct {
	args   []string
	stderr string
}

func (e *gitError) Error() string {
	return fmt.Sprintf("git %s: %s", strings.Join(e.args, " "), e.stderr)
}

// run executes git with args against r.dir. On a non-zero exit it returns
// the *exec.ExitError alongside a gitError carrying trimmed stderr, so
// callers that care about the exit code (BranchExists) can inspect it.
func (r *repo) run(ctx context.Context, args ...string) (stdout string, exitErr *exec.ExitError, err error) {
	fullArgs := append([]string{"-C", r.dir}, args...)
	cmd := exec.CommandContext(ctx, "git", fullArgs...)

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	runErr := cmd.Run()
	if runErr == nil {
		return outBuf.String(), nil, nil
	}

	gerr := &gitError{args: args, stderr: strings.TrimSpace(errBuf.String())}
	if ee, ok := runErr.(*exec.ExitError); ok {
		return outBuf.String(), ee, gerr
	}
	return outBuf.String(), nil, gerr
}

func (r *repo) CurrentBranch(ctx context.Context) (string, error) {
	out, _, err := r.run(ctx, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (r *repo) BranchExists(ctx context.Context, name string) (bool, error) {
	if err := validateBranchArg(name); err != nil {
		return false, err
	}
	// The "refs/heads/" prefix already makes option injection impossible
	// here, but rev-parse's own "--verify ... --" breaks lookup of a ref
	// that otherwise exists (verified against real git), so we rely on
	// validateBranchArg alone rather than an end-of-options separator.
	_, exitErr, err := r.run(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	if err == nil {
		return true, nil
	}
	if exitErr != nil && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func (r *repo) Switch(ctx context.Context, name string) error {
	if err := validateBranchArg(name); err != nil {
		return err
	}
	_, _, err := r.run(ctx, "switch", "--", name)
	return err
}

func (r *repo) CreateAndSwitch(ctx context.Context, name string) error {
	if err := validateBranchArg(name); err != nil {
		return err
	}
	_, _, err := r.run(ctx, "switch", "-c", name, "--")
	return err
}

func (r *repo) CheckBranchName(ctx context.Context, name string) error {
	if err := validateBranchArg(name); err != nil {
		return err
	}
	// check-ref-format --branch doesn't accept an end-of-options "--"
	// (verified against real git: it errors with a usage message), so
	// validateBranchArg is the only guard here.
	_, _, err := r.run(ctx, "check-ref-format", "--branch", name)
	return err
}
