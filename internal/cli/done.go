package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"kanban-sync/internal/domain"
	"kanban-sync/internal/provider"
	"kanban-sync/internal/reconcile"
)

// newDoneCommand builds the `yi done` command: it moves an issue
// (local and remote) into the project's doneStatus and closes it. See
// spec §6.7.
func newDoneCommand(d Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "done <id>",
		Short: "Mark an issue done and close it",
		Args:  asUsageError(cobra.ExactArgs(1)),
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runDone(cmd, d, args[0])
	}
	return cmd
}

func runDone(cmd *cobra.Command, d Deps, idArg string) error {
	id, err := domain.ParseIssueID(idArg)
	if err != nil {
		return usageError{err}
	}

	ctx := cmd.Context()
	flagProject, _ := cmd.Flags().GetString("project")

	p, err := loadProject(ctx, d, flagProject)
	if err != nil {
		return err
	}

	card := p.board.Find(id)
	if card == nil {
		return fmt.Errorf("#%d is not on the board", id)
	}

	done := p.cfg.DoneStatus
	doneSt, _ := p.cfg.Status(done)

	issue, err := p.prov.GetIssue(ctx, id)
	if err != nil {
		if errors.Is(err, provider.ErrNotFound) {
			return fmt.Errorf("#%d not found", id)
		}
		return err
	}

	var changes []reconcile.Change
	if card.Status != done {
		changes = append(changes, reconcile.MoveCard{Card: card, From: card.Status, To: done})
	}

	from, _ := p.cfg.StatusOf(issue)
	upd, remoteOK := reconcile.RemoteUpdateFor(p.cfg, issue, done)
	if remoteOK {
		changes = append(changes, reconcile.SetStatus{ID: id, From: from, To: done, Update: upd})
	}

	if len(changes) > 0 {
		if err := p.applier(io.Discard).Apply(ctx, reconcile.Plan{Changes: changes}); err != nil {
			return err
		}
	}

	fmt.Fprintf(d.Stdout, "✓ #%d moved to %s\n", id, doneSt.Heading)
	if remoteOK {
		fmt.Fprintln(d.Stdout, "✓ Remote issue closed")
	} else {
		fmt.Fprintln(d.Stdout, "✓ Remote issue already up to date")
	}
	return nil
}
