package github

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"kanban-sync/internal/provider"
)

// resolveToken finds a GitHub token to authenticate with, per spec
// §6.1 GitHub: GITHUB_TOKEN, then GH_TOKEN, then `gh auth token` if
// gh is on PATH, else an error wrapping provider.ErrAuth. The token
// is never persisted anywhere by yi.
//
// getenv, lookPath and run are injected so tests can exercise every
// branch without touching the real environment or PATH.
func resolveToken(
	ctx context.Context,
	getenv func(string) string,
	lookPath func(string) (string, error),
	run func(ctx context.Context, name string, args ...string) ([]byte, error),
) (string, error) {
	if tok := getenv("GITHUB_TOKEN"); tok != "" {
		return tok, nil
	}
	if tok := getenv("GH_TOKEN"); tok != "" {
		return tok, nil
	}

	if _, err := lookPath("gh"); err == nil {
		if out, err := run(ctx, "gh", "auth", "token"); err == nil {
			if tok := strings.TrimSpace(string(out)); tok != "" {
				return tok, nil
			}
		}
	}

	return "", fmt.Errorf(`no GitHub token: set GITHUB_TOKEN or run "gh auth login": %w`, provider.ErrAuth)
}

// runCommand is resolveToken's default run: it executes name with
// args and returns its standard output.
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}
