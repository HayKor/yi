package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/HayKor/yi/internal/config"
)

// initConfigTemplate is the .issue-sync.yml `yi init` writes. Its
// headings must match initBoardTemplate's columns.
const initConfigTemplate = `provider: github
repository: owner/repo        # <- your GitHub repository

statuses:                     # order = column order on the board
  backlog:
    heading: Backlog          # column heading in Kanban.md
    label: status:backlog     # label on the issue
  todo:
    heading: Todo
    label: status:todo
  in-progress:
    heading: In Progress
    label: status:in-progress
  done:
    heading: Done
    label: status:done
    closeIssue: true          # cards here close their issue

# github:
#   project: 1                # keep status in a GitHub Project's Status field
#                             # instead of labels (options = headings)

# notes:
#   create: always            # always | on-demand

# git:                        # lets "yi start" create branches
#   repository: ~/Projects/my-repo
`

// initBoardTemplate is an empty Obsidian Kanban board with the
// template's four columns; Done is marked complete the way the plugin
// writes it.
const initBoardTemplate = "---\n\nkanban-plugin: board\n\n---\n\n" +
	"## Backlog\n\n\n\n" +
	"## Todo\n\n\n\n" +
	"## In Progress\n\n\n\n" +
	"## Done\n\n**Complete**\n\n\n\n" +
	"%% kanban:settings\n```\n{\"kanban-plugin\":\"board\"}\n```\n%%\n"

// newInitCommand builds `yi init`: it writes a starter .issue-sync.yml
// and Kanban.md into the project directory and never overwrites
// existing files. No network, no git.
func newInitCommand(d Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create .issue-sync.yml and Kanban.md in the project directory",
		Args:  asUsageError(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, _ := cmd.Flags().GetString("project")
			if dir == "" {
				wd, err := d.Getwd()
				if err != nil {
					return err
				}
				dir = wd
			}
			return runInit(d, dir)
		},
	}
}

func runInit(d Deps, dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("project directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("project directory %s is not a directory", dir)
	}

	files := []struct{ name, content string }{
		{config.FileName, initConfigTemplate},
		{"Kanban.md", initBoardTemplate},
	}
	for _, f := range files {
		created, err := writeNewFile(filepath.Join(dir, f.name), f.content)
		if err != nil {
			return err
		}
		if created {
			fmt.Fprintf(d.Stdout, "created %s\n", f.name)
		} else {
			fmt.Fprintf(d.Stdout, "skipped %s (already exists)\n", f.name)
		}
	}

	fmt.Fprintf(d.Stdout, "next: set \"repository\" in %s, then run: yi pull\n", config.FileName)
	return nil
}

// writeNewFile creates path with content, or reports created=false if
// it already exists. O_EXCL makes "never overwrite" hold even if the
// file appears between a check and the write.
func writeNewFile(path, content string) (created bool, err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return false, err
	}
	return true, f.Close()
}
