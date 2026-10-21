package github

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strings"

	"kanban-sync/internal/provider"
)

// resolveToken finds a GitHub token to authenticate with, per spec
// §6.1 GitHub: GITHUB_TOKEN, then GH_TOKEN, then `gh auth token` if
// gh is on PATH, else an error wrapping provider.ErrAuth. The token
// is never persisted anywhere by yi.
//
// host is the GitHub Enterprise host (see tokenHost), or "" for
// github.com. With a host, gh is asked for that host's token
// (`gh auth token --hostname <host>`), never for the github.com one.
//
// getenv, lookPath and run are injected so tests can exercise every
// branch without touching the real environment or PATH.
func resolveToken(
	ctx context.Context,
	host string,
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
		args := []string{"auth", "token"}
		if host != "" {
			args = append(args, "--hostname", host)
		}
		if out, err := run(ctx, "gh", args...); err == nil {
			if tok := strings.TrimSpace(string(out)); tok != "" {
				return tok, nil
			}
		}
	}

	return "", fmt.Errorf(`no GitHub token: set GITHUB_TOKEN or run "gh auth login": %w`, provider.ErrAuth)
}

// tokenHost returns the host (with port, if any) of a configured
// baseUrl, for `gh auth token --hostname`, or "" if baseURL is empty
// (github.com). A baseUrl without a host is an error rather than a
// silent fallback to the github.com token.
func tokenHost(baseURL string) (string, error) {
	if baseURL == "" {
		return "", nil
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("github: invalid baseUrl %q: %w", baseURL, err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("github: invalid baseUrl %q: no host", baseURL)
	}
	return u.Host, nil
}

// runCommand is resolveToken's default run: it executes name with
// args and returns its standard output.
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}
