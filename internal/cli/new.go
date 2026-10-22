package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"kanban-sync/internal/domain"
	"kanban-sync/internal/kanban"
	"kanban-sync/internal/provider"
	"kanban-sync/internal/reconcile"
)

// newNewCommand builds the `yi new` command: it creates a remote
// issue and then the matching board card (plus its task note, always)
// in one step, without asking for confirmation (the command itself is
// the explicit request). See spec §6.7.
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

	// Create the issue before touching the board: if the tracker call
	// fails, nothing is written locally, so there's no id-less card
	// left behind for the next push to turn into a duplicate issue.
	issue, err := p.prov.CreateIssue(ctx, provider.NewIssue{Title: title, Body: body, Labels: []string{st.Label}})
	if err != nil {
		return err
	}
	id := issue.ID

	// A failed close doesn't stop the card from being added: the issue
	// exists, so the board must record it (a later push closes it).
	var closeErr error
	if st.CloseIssue {
		closed := domain.IssueClosed
		if _, err := p.prov.UpdateIssue(ctx, id, provider.IssueUpdate{State: &closed}); err != nil {
			closeErr = fmt.Errorf("#%d was created but not closed (run yi push to close it): %w", id, err)
		}
	}

	plan := reconcile.Plan{Changes: []reconcile.Change{
		reconcile.AddCard{Issue: issue, To: statusKey, WithNote: true},
	}}
	if err := p.applier(io.Discard).Apply(ctx, plan); err != nil {
		advice := "run yi pull to add it"
		if st.CloseIssue && closeErr == nil {
			// pull never adds a card for a closed issue: say exactly
			// which line to add by hand.
			advice = fmt.Sprintf("add this line under %q: %s", "## "+st.Heading, manualCardLine(title, id))
		}
		return errors.Join(fmt.Errorf("#%d %s was created, but adding its card failed (%s): %w", id, issue.URL, advice, err), closeErr)
	}

	notePath, ok := p.store.Resolve(p.index, id, "")
	if !ok {
		return fmt.Errorf("note for #%d not found after creation", id)
	}
	relPath, err := filepath.Rel(p.cfg.Dir, notePath)
	if err != nil {
		relPath = notePath
	}

	fmt.Fprintf(d.Stdout, "✓ Created #%d %s\n", id, issue.URL)
	fmt.Fprintf(d.Stdout, "✓ Note %s\n", filepath.ToSlash(relPath))
	return closeErr
}

// manualCardLine is the plain, checked card line for a closed issue,
// with the title as yi would write it on a card (no metadata tokens).
func manualCardLine(title string, id int) string {
	if t := kanban.StripMeta(title); t != "" {
		title = t
	}
	return kanban.FormatCard(true, title, "", id)
}
