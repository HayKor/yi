package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"kanban-sync/internal/domain"
	"kanban-sync/internal/kanban"
	"kanban-sync/internal/reconcile"
)

// newNewCommand builds the `yi new` command: it creates a remote
// issue and the matching board card (plus its task note) in one step,
// without asking for confirmation (the command itself is the explicit
// request). See spec §6.7.
func newNewCommand(d Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "new <title>",
		Short: "Create a new issue and board card",
		Args:  asUsageError(cobra.ExactArgs(1)),
	}
	status := cmd.Flags().String("status", "", "status key to create the issue in (default: defaultStatus)")
	body := cmd.Flags().String("body", "", "issue body")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runNew(cmd, d, args[0], *status, *body)
	}
	return cmd
}

func runNew(cmd *cobra.Command, d Deps, titleArg, statusFlag, body string) error {
	title := strings.TrimSpace(titleArg)
	if title == "" {
		return usageError{fmt.Errorf("title must not be empty")}
	}

	ctx := cmd.Context()
	flagProject, _ := cmd.Flags().GetString("project")

	p, err := loadProject(ctx, d, flagProject)
	if err != nil {
		return err
	}

	statusKey := p.cfg.DefaultStatus
	if statusFlag != "" {
		statusKey = domain.StatusKey(statusFlag)
	}
	st, ok := p.cfg.Status(statusKey)
	if !ok {
		return fmt.Errorf("unknown status %q", statusKey)
	}

	card, err := p.board.Add(statusKey, kanban.FormatCard(st.CloseIssue, title, "", 0))
	if err != nil {
		return err
	}

	plan := reconcile.Plan{Changes: []reconcile.Change{
		reconcile.CreateIssue{Card: card, Title: title, Body: body, Status: statusKey, WithNote: true},
	}}
	if err := p.applier(io.Discard).Apply(ctx, plan); err != nil {
		return err
	}

	id := card.IssueID
	issue, err := p.prov.GetIssue(ctx, id)
	if err != nil {
		return err
	}

	notePath, ok := p.store.Resolve(p.index, id, card.Link)
	if !ok {
		return fmt.Errorf("note for #%d not found after creation", id)
	}
	relPath, err := filepath.Rel(p.cfg.Dir, notePath)
	if err != nil {
		relPath = notePath
	}

	fmt.Fprintf(d.Stdout, "✓ Created #%d %s\n", id, issue.URL)
	fmt.Fprintf(d.Stdout, "✓ Note %s\n", filepath.ToSlash(relPath))
	return nil
}
