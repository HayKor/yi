package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HayKor/yi/internal/domain"
)

const fullExampleYAML = `provider: github
repository: my-org/yandex-ads

github:
  baseUrl: https://github.example.com/api/v3/

kanban: ./Kanban.md
tasks: ./Tasks

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
startStatus: in-progress
doneStatus: completed

notes:
  create: always

git:
  repository: ~/Projects/yandex-ads
  branches:
    template: "{type}/{issue}-{slug}"
    defaultType: feat
`

func writeConfig(t *testing.T, yamlContent string) (path, dir string) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, ".issue-sync.yml")
	if err := os.WriteFile(path, []byte(yamlContent), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path, dir
}

func TestParseFullExample(t *testing.T) {
	path, dir := writeConfig(t, fullExampleYAML)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	var gotKeys []string
	for _, s := range cfg.Statuses {
		gotKeys = append(gotKeys, string(s.Key))
	}
	wantKeys := []string{"backlog", "todo", "in-progress", "completed"}
	if strings.Join(gotKeys, ",") != strings.Join(wantKeys, ",") {
		t.Fatalf("status keys = %v, want %v", gotKeys, wantKeys)
	}

	if cfg.KanbanPath != filepath.Join(dir, "Kanban.md") {
		t.Errorf("KanbanPath = %q, want %q", cfg.KanbanPath, filepath.Join(dir, "Kanban.md"))
	}
	if cfg.TasksDir != filepath.Join(dir, "Tasks") {
		t.Errorf("TasksDir = %q, want %q", cfg.TasksDir, filepath.Join(dir, "Tasks"))
	}
	if cfg.Notes != NotesAlways {
		t.Errorf("Notes = %q, want %q", cfg.Notes, NotesAlways)
	}
	if cfg.DoneStatus != "completed" {
		t.Errorf("DoneStatus = %q, want completed", cfg.DoneStatus)
	}
	if cfg.StartStatus != "in-progress" {
		t.Errorf("StartStatus = %q, want in-progress", cfg.StartStatus)
	}
	if cfg.DefaultStatus != "backlog" {
		t.Errorf("DefaultStatus = %q, want backlog", cfg.DefaultStatus)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	if cfg.Git == nil {
		t.Fatalf("Git = nil, want non-nil")
	}
	wantGitRepo := filepath.Join(home, "Projects", "yandex-ads")
	if cfg.Git.Repository != wantGitRepo {
		t.Errorf("Git.Repository = %q, want %q", cfg.Git.Repository, wantGitRepo)
	}
	if cfg.Git.BranchTemplate != "{type}/{issue}-{slug}" {
		t.Errorf("Git.BranchTemplate = %q", cfg.Git.BranchTemplate)
	}
	if cfg.Git.DefaultType != "feat" {
		t.Errorf("Git.DefaultType = %q", cfg.Git.DefaultType)
	}

	var opts struct {
		BaseURL string `yaml:"baseUrl"`
	}
	if err := cfg.ProviderOptions.Decode(&opts); err != nil {
		t.Fatalf("ProviderOptions.Decode: %v", err)
	}
	if opts.BaseURL != "https://github.example.com/api/v3/" {
		t.Errorf("ProviderOptions.BaseURL = %q", opts.BaseURL)
	}
}

const defaultsYAML = `provider: github
repository: my-org/yandex-ads

statuses:
  a:
    heading: A
    label: "l:a"
  done:
    heading: Done
    label: "l:d"
    closeIssue: true
`

func TestDefaults(t *testing.T) {
	path, dir := writeConfig(t, defaultsYAML)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.KanbanPath != filepath.Join(dir, "Kanban.md") {
		t.Errorf("KanbanPath = %q, want %q", cfg.KanbanPath, filepath.Join(dir, "Kanban.md"))
	}
	if cfg.TasksDir != filepath.Join(dir, "Tasks") {
		t.Errorf("TasksDir = %q, want %q", cfg.TasksDir, filepath.Join(dir, "Tasks"))
	}
	if cfg.DefaultStatus != "a" {
		t.Errorf("DefaultStatus = %q, want a", cfg.DefaultStatus)
	}
	if cfg.DoneStatus != "done" {
		t.Errorf("DoneStatus = %q, want done", cfg.DoneStatus)
	}
	if cfg.StartStatus != "" {
		t.Errorf("StartStatus = %q, want empty", cfg.StartStatus)
	}
	if cfg.Notes != NotesAlways {
		t.Errorf("Notes = %q, want always", cfg.Notes)
	}
	if cfg.Git != nil {
		t.Errorf("Git = %+v, want nil", cfg.Git)
	}
}

func TestValidationErrors(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantSub string
	}{
		{
			name:    "missing repository",
			yaml:    strings.Replace(fullExampleYAML, "repository: my-org/yandex-ads\n", "", 1),
			wantSub: "repository is required",
		},
		{
			name:    "yi init placeholder repository",
			yaml:    strings.Replace(fullExampleYAML, "repository: my-org/yandex-ads\n", "repository: owner/repo\n", 1),
			wantSub: `"owner/repo" placeholder`,
		},
		{
			name: "no statuses",
			yaml: "provider: github\nrepository: my-org/yandex-ads\n",
			// repository present, so only the statuses problem should fire.
			wantSub: "statuses: at least one status is required",
		},
		{
			name:    "no closeIssue status",
			yaml:    strings.Replace(fullExampleYAML, "    closeIssue: true\n", "", 1),
			wantSub: "exactly one status must have closeIssue: true",
		},
		{
			name: "two closeIssue statuses",
			yaml: strings.Replace(fullExampleYAML,
				"  backlog:\n    heading: Backlog\n    label: status:backlog\n",
				"  backlog:\n    heading: Backlog\n    label: status:backlog\n    closeIssue: true\n",
				1),
			wantSub: "exactly one status must have closeIssue: true",
		},
		{
			name:    "duplicate heading",
			yaml:    strings.Replace(fullExampleYAML, "heading: Todo\n", "heading: Backlog\n", 1),
			wantSub: `duplicate heading "Backlog"`,
		},
		{
			name:    "duplicate label",
			yaml:    strings.Replace(fullExampleYAML, "label: status:todo\n", "label: status:backlog\n", 1),
			wantSub: `duplicate label "status:backlog"`,
		},
		{
			// Board lanes match headings case-insensitively, so these
			// two would map to the same lane (final review I3).
			name:    "duplicate heading differing only in case",
			yaml:    strings.Replace(fullExampleYAML, "heading: Todo\n", "heading: backlog\n", 1),
			wantSub: `duplicate heading "backlog"`,
		},
		{
			name:    "duplicate heading differing only in WIP limit",
			yaml:    strings.Replace(fullExampleYAML, "heading: Todo\n", "heading: \" Backlog (3)\"\n", 1),
			wantSub: `duplicate heading " Backlog (3)"`,
		},
		{
			// GitHub label names are case-insensitive (final review M2).
			name:    "duplicate label differing only in case",
			yaml:    strings.Replace(fullExampleYAML, "label: status:todo\n", "label: Status:Backlog\n", 1),
			wantSub: `duplicate label "Status:Backlog"`,
		},
		{
			name:    "git section without repository",
			yaml:    strings.Replace(fullExampleYAML, "  repository: ~/Projects/yandex-ads\n", "", 1),
			wantSub: "git.repository is required",
		},
		{
			name:    "missing heading",
			yaml:    strings.Replace(fullExampleYAML, "    heading: Todo\n", "", 1),
			wantSub: `statuses.todo: heading is required`,
		},
		{
			name:    "missing label",
			yaml:    strings.Replace(fullExampleYAML, "    label: status:todo\n", "", 1),
			wantSub: `statuses.todo: label is required`,
		},
		{
			name:    "unknown defaultStatus",
			yaml:    strings.Replace(fullExampleYAML, "defaultStatus: backlog\n", "defaultStatus: nope\n", 1),
			wantSub: `defaultStatus: unknown status "nope" (one of: backlog,`,
		},
		{
			name:    "defaultStatus given as a label",
			yaml:    strings.Replace(fullExampleYAML, "defaultStatus: backlog\n", "defaultStatus: status:todo\n", 1),
			wantSub: `defaultStatus: unknown status "status:todo" (use the status key "todo"`,
		},
		{
			name:    "startStatus given as a heading",
			yaml:    strings.Replace(fullExampleYAML, "startStatus: in-progress\n", "startStatus: In Progress\n", 1),
			wantSub: `startStatus: unknown status "In Progress" (use the status key "in-progress"`,
		},
		{
			name:    "doneStatus without closeIssue",
			yaml:    strings.Replace(fullExampleYAML, "doneStatus: completed\n", "doneStatus: todo\n", 1),
			wantSub: `doneStatus "todo" must have closeIssue: true`,
		},
		{
			name:    "bad notes.create",
			yaml:    strings.Replace(fullExampleYAML, "create: always\n", "create: sometimes\n", 1),
			wantSub: "notes.create: must be always or on-demand",
		},
		{
			name: "unknown branch placeholder",
			yaml: strings.Replace(fullExampleYAML,
				`template: "{type}/{issue}-{slug}"`,
				`template: "{type}/{issue}-{foo}"`,
				1),
			wantSub: "git.branches.template: unknown placeholder {foo}",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path, _ := writeConfig(t, c.yaml)
			_, err := Load(path)
			if err == nil {
				t.Fatalf("Load() error = nil, want error containing %q", c.wantSub)
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Fatalf("Load() error = %q, want substring %q", err.Error(), c.wantSub)
			}
		})
	}

	t.Run("multiple problems collected", func(t *testing.T) {
		path, _ := writeConfig(t, "provider: github\n")
		_, err := Load(path)
		if err == nil {
			t.Fatalf("Load() error = nil, want error")
		}
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("Load() error is not *ValidationError: %v", err)
		}
		if len(ve.Problems) != 2 {
			t.Fatalf("len(Problems) = %d, want 2 (got %v)", len(ve.Problems), ve.Problems)
		}
	})

	t.Run("two missing labels collect two required problems, not a duplicate", func(t *testing.T) {
		yamlContent := strings.Replace(fullExampleYAML, "    label: status:todo\n", "", 1)
		yamlContent = strings.Replace(yamlContent, "    label: status:in-progress\n", "", 1)
		path, _ := writeConfig(t, yamlContent)

		_, err := Load(path)
		if err == nil {
			t.Fatalf("Load() error = nil, want error")
		}
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("Load() error is not *ValidationError: %v", err)
		}

		wantProblems := []string{
			"statuses.todo: label is required",
			"statuses.in-progress: label is required",
		}
		if len(ve.Problems) != len(wantProblems) {
			t.Fatalf("Problems = %v, want %v", ve.Problems, wantProblems)
		}
		for i, want := range wantProblems {
			if ve.Problems[i] != want {
				t.Errorf("Problems[%d] = %q, want %q", i, ve.Problems[i], want)
			}
		}
		for _, p := range ve.Problems {
			if strings.Contains(p, "duplicate label") {
				t.Errorf("Problems unexpectedly contains a duplicate-label problem: %q", p)
			}
		}
	})
}

func TestStatusOf(t *testing.T) {
	path, _ := writeConfig(t, fullExampleYAML)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	cases := []struct {
		name        string
		issue       domain.Issue
		wantKey     domain.StatusKey
		wantWarning bool
	}{
		{
			name:    "closed with status label",
			issue:   domain.Issue{ID: 1, State: domain.IssueClosed, Labels: []string{"status:todo"}},
			wantKey: "completed",
		},
		{
			name:    "open with one status label",
			issue:   domain.Issue{ID: 2, State: domain.IssueOpen, Labels: []string{"bug", "status:todo"}},
			wantKey: "todo",
		},
		{
			name:    "open with a status label spelled in another case",
			issue:   domain.Issue{ID: 2, State: domain.IssueOpen, Labels: []string{"Status:Todo"}},
			wantKey: "todo",
		},
		{
			name:    "open with no status label",
			issue:   domain.Issue{ID: 3, State: domain.IssueOpen, Labels: nil},
			wantKey: "backlog",
		},
		{
			name:        "open with multiple status labels",
			issue:       domain.Issue{ID: 4, State: domain.IssueOpen, Labels: []string{"status:in-progress", "status:todo"}},
			wantKey:     "todo",
			wantWarning: true,
		},
		{
			name:    "open with closeIssue status label",
			issue:   domain.Issue{ID: 5, State: domain.IssueOpen, Labels: []string{"status:completed"}},
			wantKey: "completed",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			key, warnings := cfg.StatusOf(c.issue)
			if key != c.wantKey {
				t.Errorf("StatusOf() key = %q, want %q", key, c.wantKey)
			}
			if c.wantWarning {
				if len(warnings) != 1 {
					t.Fatalf("warnings = %v, want 1 warning", warnings)
				}
				if !strings.Contains(warnings[0], "#4 has multiple status labels") {
					t.Errorf("warning = %q, want substring %q", warnings[0], "#4 has multiple status labels")
				}
			} else if len(warnings) != 0 {
				t.Errorf("warnings = %v, want none", warnings)
			}
		})
	}
}
