package github

import (
	"context"
	"errors"
	"strings"
	"testing"

	"kanban-sync/internal/provider"
)

func TestResolveToken(t *testing.T) {
	ctx := context.Background()

	noLookPath := func(string) (string, error) { return "", errors.New("not found") }
	noRun := func(context.Context, string, ...string) ([]byte, error) {
		t.Helper()
		t.Fatal("run should not be called")
		return nil, nil
	}

	t.Run("GITHUB_TOKEN wins", func(t *testing.T) {
		getenv := func(key string) string {
			switch key {
			case "GITHUB_TOKEN":
				return "a"
			case "GH_TOKEN":
				return "b"
			}
			return ""
		}
		got, err := resolveToken(ctx, getenv, noLookPath, noRun)
		if err != nil {
			t.Fatalf("resolveToken() error = %v, want nil", err)
		}
		if got != "a" {
			t.Errorf("resolveToken() = %q, want %q", got, "a")
		}
	})

	t.Run("GH_TOKEN fallback", func(t *testing.T) {
		getenv := func(key string) string {
			if key == "GH_TOKEN" {
				return "b"
			}
			return ""
		}
		got, err := resolveToken(ctx, getenv, noLookPath, noRun)
		if err != nil {
			t.Fatalf("resolveToken() error = %v, want nil", err)
		}
		if got != "b" {
			t.Errorf("resolveToken() = %q, want %q", got, "b")
		}
	})

	t.Run("gh auth token fallback", func(t *testing.T) {
		getenv := func(string) string { return "" }
		lookPath := func(name string) (string, error) {
			if name == "gh" {
				return "/usr/bin/gh", nil
			}
			return "", errors.New("not found")
		}
		run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if name != "gh" || len(args) != 2 || args[0] != "auth" || args[1] != "token" {
				t.Fatalf("run called with name=%q args=%v, want gh auth token", name, args)
			}
			return []byte("c\n"), nil
		}
		got, err := resolveToken(ctx, getenv, lookPath, run)
		if err != nil {
			t.Fatalf("resolveToken() error = %v, want nil", err)
		}
		if got != "c" {
			t.Errorf("resolveToken() = %q, want %q", got, "c")
		}
	})

	t.Run("no env, no gh: ErrAuth with hints", func(t *testing.T) {
		getenv := func(string) string { return "" }
		_, err := resolveToken(ctx, getenv, noLookPath, noRun)
		if !errors.Is(err, provider.ErrAuth) {
			t.Fatalf("resolveToken() error = %v, want wrapping ErrAuth", err)
		}
		if !strings.Contains(err.Error(), "GITHUB_TOKEN") {
			t.Errorf("resolveToken() error = %q, want to contain %q", err.Error(), "GITHUB_TOKEN")
		}
		if !strings.Contains(err.Error(), "gh auth login") {
			t.Errorf("resolveToken() error = %q, want to contain %q", err.Error(), "gh auth login")
		}
	})

	t.Run("gh on PATH but run fails: ErrAuth with hints", func(t *testing.T) {
		getenv := func(string) string { return "" }
		lookPath := func(name string) (string, error) { return "/usr/bin/gh", nil }
		run := func(context.Context, string, ...string) ([]byte, error) {
			return nil, errors.New("not logged in")
		}
		_, err := resolveToken(ctx, getenv, lookPath, run)
		if !errors.Is(err, provider.ErrAuth) {
			t.Fatalf("resolveToken() error = %v, want wrapping ErrAuth", err)
		}
	})

	t.Run("gh on PATH but run returns empty: ErrAuth", func(t *testing.T) {
		getenv := func(string) string { return "" }
		lookPath := func(name string) (string, error) { return "/usr/bin/gh", nil }
		run := func(context.Context, string, ...string) ([]byte, error) {
			return []byte("  \n"), nil
		}
		_, err := resolveToken(ctx, getenv, lookPath, run)
		if !errors.Is(err, provider.ErrAuth) {
			t.Fatalf("resolveToken() error = %v, want wrapping ErrAuth", err)
		}
	})
}
