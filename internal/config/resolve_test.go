package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// minimalConfigYAML is a valid .issue-sync.yml with no git section: every
// status has a heading and label, and exactly one has closeIssue: true.
const minimalConfigYAML = `provider: github
repository: org/repo
statuses:
  todo:
    heading: Todo
    label: status:todo
  done:
    heading: Done
    label: status:done
    closeIssue: true
`

// writeResolveConfig writes a minimal valid config into dir, optionally
// with a git.repository section pointing at gitRepo (when non-empty).
func writeResolveConfig(t *testing.T, dir, gitRepo string) {
	t.Helper()
	content := minimalConfigYAML
	if gitRepo != "" {
		content += "\ngit:\n  repository: " + gitRepo + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func TestResolveFlagWins(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	writeResolveConfig(t, dirA, "")
	writeResolveConfig(t, dirB, "")

	got, err := Resolve(ResolveInput{Flag: dirA, Env: dirB})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := filepath.Join(dirA, FileName)
	if got != want {
		t.Errorf("Resolve() = %q, want %q", got, want)
	}
}

func TestResolveFlagWithoutConfig(t *testing.T) {
	dir := t.TempDir()

	_, err := Resolve(ResolveInput{Flag: dir})
	if err == nil {
		t.Fatalf("Resolve() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "no "+FileName+" in") {
		t.Errorf("Resolve() error = %q, want it to contain %q", err.Error(), "no "+FileName+" in")
	}
}

func TestResolveEnv(t *testing.T) {
	dirB := t.TempDir()
	writeResolveConfig(t, dirB, "")

	got, err := Resolve(ResolveInput{Env: dirB})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := filepath.Join(dirB, FileName)
	if got != want {
		t.Errorf("Resolve() = %q, want %q", got, want)
	}
}

func TestResolveWalkUp(t *testing.T) {
	root := t.TempDir()
	writeResolveConfig(t, root, "")

	cwd := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	got, err := Resolve(ResolveInput{Cwd: cwd})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := filepath.Join(root, FileName)
	if got != want {
		t.Errorf("Resolve() = %q, want %q", got, want)
	}
}

func TestResolveGlobalRegistry(t *testing.T) {
	projDir := t.TempDir()
	codeDir := t.TempDir()
	writeResolveConfig(t, projDir, codeDir)

	globalDir := t.TempDir()
	globalPath := filepath.Join(globalDir, "config.yml")
	globalContent := "projects:\n  - " + projDir + "\n"
	if err := os.WriteFile(globalPath, []byte(globalContent), 0o644); err != nil {
		t.Fatalf("write global config: %v", err)
	}

	cwd := filepath.Join(codeDir, "sub")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Cwd has no .issue-sync.yml anywhere up its tree, so the walk-up
	// step fails and we fall through to the global registry.
	got, err := Resolve(ResolveInput{Cwd: cwd, GlobalConfigPath: globalPath})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := filepath.Join(projDir, FileName)
	if got != want {
		t.Errorf("Resolve() = %q, want %q", got, want)
	}
}

func TestResolveGlobalRegistryPicksMostSpecific(t *testing.T) {
	codeDir := t.TempDir()
	subCodeDir := filepath.Join(codeDir, "nested")
	if err := os.MkdirAll(subCodeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	outerProj := t.TempDir()
	innerProj := t.TempDir()
	writeResolveConfig(t, outerProj, codeDir)
	writeResolveConfig(t, innerProj, subCodeDir)

	globalDir := t.TempDir()
	globalPath := filepath.Join(globalDir, "config.yml")
	globalContent := "projects:\n  - " + outerProj + "\n  - " + innerProj + "\n"
	if err := os.WriteFile(globalPath, []byte(globalContent), 0o644); err != nil {
		t.Fatalf("write global config: %v", err)
	}

	cwd := filepath.Join(subCodeDir, "deeper")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	got, err := Resolve(ResolveInput{Cwd: cwd, GlobalConfigPath: globalPath})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := filepath.Join(innerProj, FileName)
	if got != want {
		t.Errorf("Resolve() = %q, want %q (most specific git.repository)", got, want)
	}
}

func TestResolveNotFound(t *testing.T) {
	cwd := t.TempDir()
	globalPath := filepath.Join(t.TempDir(), "config.yml") // does not exist

	_, err := Resolve(ResolveInput{Cwd: cwd, GlobalConfigPath: globalPath})
	if err == nil {
		t.Fatalf("Resolve() error = nil, want error")
	}
	msg := err.Error()
	for _, want := range []string{"--project", "YI_PROJECT", "searched up from", globalPath} {
		if !strings.Contains(msg, want) {
			t.Errorf("Resolve() error = %q, want it to contain %q", msg, want)
		}
	}
}

func TestGlobalConfigPath(t *testing.T) {
	t.Run("XDG_CONFIG_HOME set", func(t *testing.T) {
		getenv := func(k string) string {
			if k == "XDG_CONFIG_HOME" {
				return "/custom/xdg"
			}
			return ""
		}
		got := GlobalConfigPath(getenv)
		want := filepath.Join("/custom/xdg", "yi", "config.yml")
		if got != want {
			t.Errorf("GlobalConfigPath() = %q, want %q", got, want)
		}
	})

	t.Run("XDG_CONFIG_HOME unset falls back to home", func(t *testing.T) {
		getenv := func(string) string { return "" }
		got := GlobalConfigPath(getenv)
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("UserHomeDir: %v", err)
		}
		want := filepath.Join(home, ".config", "yi", "config.yml")
		if got != want {
			t.Errorf("GlobalConfigPath() = %q, want %q", got, want)
		}
	})
}
