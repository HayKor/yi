package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the canonical name of a project's config file.
const FileName = ".issue-sync.yml"

// ResolveInput holds every source Resolve consults, in priority order:
// Flag, then Env, then a walk up from Cwd, then the global registry at
// GlobalConfigPath.
type ResolveInput struct {
	Flag             string
	Env              string
	Cwd              string
	GlobalConfigPath string
}

// Resolve finds the path to the project's .issue-sync.yml, per spec §4:
//
//  1. Flag (e.g. --project), if set.
//  2. Env (e.g. YI_PROJECT), if set.
//  3. Walking up from Cwd looking for FileName.
//  4. The global registry at GlobalConfigPath: the project whose
//     git.repository contains Cwd (most specific match wins).
//
// Flag and Env may each point at either the project directory or
// directly at the config file. When either is set, it is authoritative:
// if it doesn't resolve to a config file, Resolve fails immediately
// rather than falling through to the next source.
func Resolve(in ResolveInput) (string, error) {
	if in.Flag != "" {
		return resolveExplicit(in.Flag)
	}
	if in.Env != "" {
		return resolveExplicit(in.Env)
	}

	if path, ok := walkUp(in.Cwd); ok {
		return path, nil
	}

	if path, ok := resolveGlobalRegistry(in.GlobalConfigPath, in.Cwd); ok {
		return path, nil
	}

	return "", fmt.Errorf(
		"no project found: pass --project, set YI_PROJECT, searched up from %s, or register the project in %s",
		in.Cwd, in.GlobalConfigPath,
	)
}

// GlobalConfigPath returns the path to the global project registry:
// $XDG_CONFIG_HOME/yi/config.yml if XDG_CONFIG_HOME is set, otherwise
// ~/.config/yi/config.yml.
func GlobalConfigPath(getenv func(string) string) string {
	if xdg := getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "yi", "config.yml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "yi", "config.yml")
	}
	return filepath.Join(home, ".config", "yi", "config.yml")
}

// resolveExplicit resolves a value that may point directly at a config
// file, or at the directory that contains one.
func resolveExplicit(value string) (string, error) {
	if info, err := os.Stat(value); err == nil && !info.IsDir() {
		return value, nil
	}
	path := filepath.Join(value, FileName)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("no %s in %s", FileName, value)
	}
	return path, nil
}

// walkUp looks for FileName in dir, then each of its ancestors in turn.
func walkUp(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	for {
		path := filepath.Join(dir, FileName)
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// globalRegistry is the shape of the global config: a flat list of
// project directories.
type globalRegistry struct {
	Projects []string `yaml:"projects"`
}

// resolveGlobalRegistry reads the global registry at globalConfigPath
// and returns the config file of the registered project whose
// git.repository contains cwd. Projects whose config fails to load, or
// has no git section, are skipped. A missing global config file is not
// an error: it simply yields no match. When several projects match,
// the one with the longest (most specific) git.repository wins.
func resolveGlobalRegistry(globalConfigPath, cwd string) (string, bool) {
	if globalConfigPath == "" {
		return "", false
	}
	data, err := os.ReadFile(globalConfigPath)
	if err != nil {
		return "", false
	}

	var reg globalRegistry
	if err := yaml.Unmarshal(data, &reg); err != nil {
		return "", false
	}

	baseDir := filepath.Dir(globalConfigPath)
	bestPath := ""
	bestLen := -1
	for _, projDir := range reg.Projects {
		expanded, err := resolvePath(projDir, baseDir)
		if err != nil {
			continue
		}
		cfg, err := Load(filepath.Join(expanded, FileName))
		if err != nil || cfg.Git == nil {
			continue
		}
		if !pathContains(cfg.Git.Repository, cwd) {
			continue
		}
		if len(cfg.Git.Repository) > bestLen {
			bestLen = len(cfg.Git.Repository)
			bestPath = cfg.Path
		}
	}
	if bestPath == "" {
		return "", false
	}
	return bestPath, true
}

// pathContains reports whether target is base itself or a descendant of
// it, per filepath.Rel(base, target) having no ".." component.
func pathContains(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
