// Package config loads and validates a project's .issue-sync.yml. It
// depends on domain and branch only.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/HayKor/yi/internal/branch"
	"github.com/HayKor/yi/internal/domain"
)

// NotesMode controls when yi creates a task note for an issue.
type NotesMode string

const (
	// NotesAlways creates a task note for every issue yi knows about.
	NotesAlways NotesMode = "always"
	// NotesOnDemand only creates a task note when explicitly requested.
	NotesOnDemand NotesMode = "on-demand"
)

// GitConfig holds the optional git integration settings read from the
// config's "git" section.
type GitConfig struct {
	Repository     string
	BranchTemplate string
	DefaultType    string
}

// Config is a parsed and validated .issue-sync.yml.
type Config struct {
	// Path is the absolute path to the config file. Dir is its directory,
	// used to resolve relative paths within the config.
	Path string
	Dir  string

	Provider        string
	Repository      string
	ProviderOptions yaml.Node

	KanbanPath string
	TasksDir   string

	Statuses []domain.Status

	DefaultStatus domain.StatusKey
	StartStatus   domain.StatusKey
	DoneStatus    domain.StatusKey

	Notes NotesMode

	// Git is nil when the config has no "git" section, meaning yi start
	// must not touch git at all.
	Git *GitConfig
}

// rawStatus is the shape of one entry under the "statuses" mapping.
type rawStatus struct {
	Heading    string `yaml:"heading"`
	Label      string `yaml:"label"`
	CloseIssue bool   `yaml:"closeIssue"`
}

// rawGitBranches is the shape of "git.branches".
type rawGitBranches struct {
	Template    string `yaml:"template"`
	DefaultType string `yaml:"defaultType"`
}

// rawGit is the shape of the optional "git" section.
type rawGit struct {
	Repository string         `yaml:"repository"`
	Branches   rawGitBranches `yaml:"branches"`
}

// rawNotes is the shape of the "notes" section.
type rawNotes struct {
	Create string `yaml:"create"`
}

// rawConfig mirrors the on-disk YAML shape, before defaults are applied
// and before validation.
type rawConfig struct {
	Provider      string    `yaml:"provider"`
	Repository    string    `yaml:"repository"`
	Kanban        string    `yaml:"kanban"`
	Tasks         string    `yaml:"tasks"`
	Statuses      yaml.Node `yaml:"statuses"`
	DefaultStatus string    `yaml:"defaultStatus"`
	StartStatus   string    `yaml:"startStatus"`
	DoneStatus    string    `yaml:"doneStatus"`
	Notes         rawNotes  `yaml:"notes"`
	Git           *rawGit   `yaml:"git"`
}

// Load reads and parses the config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data, path)
}

// Parse parses config data as if it were read from path. path need not
// exist on disk (tests may use it as a synthetic location); it is only
// used to compute Config.Path and Config.Dir, and to resolve relative
// paths inside the config.
func Parse(data []byte, path string) (*Config, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("config path %s: %w", path, err)
	}
	dir := filepath.Dir(absPath)

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", absPath, err)
	}

	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", absPath, err)
	}

	cfg := &Config{
		Path:       absPath,
		Dir:        dir,
		Provider:   raw.Provider,
		Repository: raw.Repository,
	}

	if raw.Provider != "" {
		if section := findMappingValue(&doc, raw.Provider); section != nil {
			cfg.ProviderOptions = *section
		}
	}

	kanban := raw.Kanban
	if kanban == "" {
		kanban = "./Kanban.md"
	}
	if cfg.KanbanPath, err = resolvePath(kanban, dir); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", absPath, err)
	}

	tasks := raw.Tasks
	if tasks == "" {
		tasks = "./Tasks"
	}
	if cfg.TasksDir, err = resolvePath(tasks, dir); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", absPath, err)
	}

	if cfg.Statuses, err = parseStatuses(&raw.Statuses); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", absPath, err)
	}

	cfg.DefaultStatus = domain.StatusKey(raw.DefaultStatus)
	if cfg.DefaultStatus == "" && len(cfg.Statuses) > 0 {
		cfg.DefaultStatus = cfg.Statuses[0].Key
	}

	cfg.StartStatus = domain.StatusKey(raw.StartStatus)
	if cfg.StartStatus == "" {
		if _, ok := cfg.Status("in-progress"); ok {
			cfg.StartStatus = "in-progress"
		}
	}

	cfg.DoneStatus = domain.StatusKey(raw.DoneStatus)
	if cfg.DoneStatus == "" {
		for _, s := range cfg.Statuses {
			if s.CloseIssue {
				cfg.DoneStatus = s.Key
				break
			}
		}
	}

	notesCreate := raw.Notes.Create
	if notesCreate == "" {
		notesCreate = string(NotesAlways)
	}
	cfg.Notes = NotesMode(notesCreate)

	if raw.Git != nil {
		gitRepo, err := resolvePath(raw.Git.Repository, dir)
		if err != nil {
			return nil, fmt.Errorf("parse config %s: %w", absPath, err)
		}
		tmpl := raw.Git.Branches.Template
		if tmpl == "" {
			tmpl = branch.DefaultTemplate
		}
		defType := raw.Git.Branches.DefaultType
		if defType == "" {
			defType = branch.DefaultType
		}
		cfg.Git = &GitConfig{
			Repository:     gitRepo,
			BranchTemplate: tmpl,
			DefaultType:    defType,
		}
	}

	if err := validate(cfg, raw); err != nil {
		return nil, err
	}

	return cfg, nil
}

// parseStatuses decodes the "statuses" mapping node into an ordered
// slice of domain.Status, preserving document order (which is the order
// of kanban columns).
func parseStatuses(node *yaml.Node) ([]domain.Status, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, nil
	}
	statuses := make([]domain.Status, 0, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode, valNode := node.Content[i], node.Content[i+1]
		var rs rawStatus
		if err := valNode.Decode(&rs); err != nil {
			return nil, fmt.Errorf("statuses.%s: %w", keyNode.Value, err)
		}
		statuses = append(statuses, domain.Status{
			Key:        domain.StatusKey(keyNode.Value),
			Heading:    rs.Heading,
			Label:      rs.Label,
			CloseIssue: rs.CloseIssue,
		})
	}
	return statuses, nil
}

// findMappingValue walks a YAML document node looking for a top-level
// mapping key equal to key, returning its value node, or nil if there's
// no such key (or the document isn't a mapping).
func findMappingValue(doc *yaml.Node, key string) *yaml.Node {
	root := doc
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return nil
		}
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			return root.Content[i+1]
		}
	}
	return nil
}

// resolvePath expands a leading "~" to the user's home directory and,
// for relative paths, resolves them against dir. An empty path resolves
// to "".
func resolvePath(p, dir string) (string, error) {
	if p == "" {
		return "", nil
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand %q: %w", p, err)
		}
		if p == "~" {
			p = home
		} else {
			p = filepath.Join(home, strings.TrimPrefix(p, "~/"))
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return filepath.Clean(p), nil
}

// Status looks up a status by its key.
func (c *Config) Status(k domain.StatusKey) (domain.Status, bool) {
	for _, s := range c.Statuses {
		if s.Key == k {
			return s, true
		}
	}
	return domain.Status{}, false
}

// CloseStatus returns the status with CloseIssue set, i.e. the one a
// closed issue maps to. Validation guarantees exactly one exists.
func (c *Config) CloseStatus() domain.Status {
	for _, s := range c.Statuses {
		if s.CloseIssue {
			return s
		}
	}
	return domain.Status{}
}

// StatusLabels returns the labels of all configured statuses, in config
// order. Validation guarantees every status has a non-empty label.
func (c *Config) StatusLabels() []string {
	labels := make([]string, 0, len(c.Statuses))
	for _, s := range c.Statuses {
		labels = append(labels, s.Label)
	}
	return labels
}

// StatusOf maps a remote issue to the status key it belongs to, per the
// rules in spec §6.5:
//
//  1. A closed issue always maps to CloseStatus.
//  2. An open issue maps to the first (in config order) status whose
//     label it carries. If more than one status label matches, a
//     warning is returned alongside the first match.
//  3. An open issue with no status label maps to DefaultStatus.
func (c *Config) StatusOf(issue domain.Issue) (domain.StatusKey, []string) {
	if issue.State == domain.IssueClosed {
		return c.CloseStatus().Key, nil
	}

	var matched []domain.Status
	for _, s := range c.Statuses {
		if issue.HasLabel(s.Label) {
			matched = append(matched, s)
		}
	}
	if len(matched) == 0 {
		return c.DefaultStatus, nil
	}

	var warnings []string
	if len(matched) > 1 {
		warnings = append(warnings, fmt.Sprintf("#%d has multiple status labels", issue.ID))
	}
	return matched[0].Key, warnings
}
