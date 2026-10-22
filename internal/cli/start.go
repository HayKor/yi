package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/HayKor/yi/internal/branch"
	"github.com/HayKor/yi/internal/config"
	"github.com/HayKor/yi/internal/domain"
	"github.com/HayKor/yi/internal/gitrepo"
	"github.com/HayKor/yi/internal/kanban"
	"github.com/HayKor/yi/internal/provider"
	"github.com/HayKor/yi/internal/reconcile"
)

// newStartCommand builds the `yi start` command: it moves an issue
// (local and remote) into the project's startStatus, creating a card
// and/or task note as needed, then switches to its git branch unless
// --no-branch is given or the project has no "git" section. See spec
// §6.7.
func newStartCommand(d Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start <id>",
		Short: "Move an issue into the start status and check out its branch",
		Args:  asUsageError(cobra.ExactArgs(1)),
	}
	typ := cmd.Flags().String("type", "", "branch type (default: git.branches.defaultType)")
	noBranch := cmd.Flags().Bool("no-branch", false, "don't touch git")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runStart(cmd, d, args[0], *typ, *noBranch)
	}
	return cmd
}

func runStart(cmd *cobra.Command, d Deps, idArg, typeFlag string, noBranch bool) error {
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

	start := p.cfg.StartStatus
	if start == "" {
		return fmt.Errorf("startStatus is not configured")
	}
	startSt, _ := p.cfg.Status(start)

	issue, err := p.prov.GetIssue(ctx, id)
	if err != nil {
		if errors.Is(err, provider.ErrNotFound) {
			return fmt.Errorf("#%d not found", id)
		}
		return err
	}

	card := p.board.Find(id)

	var changes []reconcile.Change
	switch {
	case card == nil:
		changes = append(changes, reconcile.AddCard{Issue: issue, To: start, WithNote: true})
	case card.Status != start:
		changes = append(changes, reconcile.MoveCard{Card: card, From: card.Status, To: start})
	}

	if card != nil && card.Link == "" && !cardHasNote(p, card) {
		changes = append(changes, reconcile.LinkNote{Card: card, ID: id, Title: issue.Title})
	}

	from, _ := p.cfg.StatusOf(issue)
	upd, remoteOK := reconcile.RemoteUpdateFor(p.cfg, issue, start)
	if remoteOK {
		changes = append(changes, reconcile.SetStatus{ID: id, From: from, To: start, Update: upd})
	}

	if len(changes) > 0 {
		if err := p.applier(io.Discard).Apply(ctx, reconcile.Plan{Changes: changes}); err != nil {
			return err
		}
	}

	fmt.Fprintf(d.Stdout, "✓ #%d moved to %s\n", id, startSt.Heading)
	if remoteOK {
		fmt.Fprintln(d.Stdout, "✓ Remote issue updated")
	} else {
		fmt.Fprintln(d.Stdout, "✓ Remote issue already up to date")
	}

	if p.cfg.Git == nil || noBranch {
		return nil
	}
	return startBranch(ctx, d, p.cfg.Git, id, issue.Title, typeFlag)
}

// cardHasNote reports whether c already resolves to a task note,
// without looking at c.Link (callers only call this once they've
// already established c.Link == "").
func cardHasNote(p *project, c *kanban.Card) bool {
	_, ok := p.store.Resolve(p.index, c.IssueID, c.Link)
	return ok
}

// startBranch computes the branch name for id/title and switches to
// it, creating it from the current HEAD if it doesn't exist yet. Any
// git error is reported alongside a reminder that the local/remote
// changes above were already applied, per spec §6.7.
func startBranch(ctx context.Context, d Deps, cfg *config.GitConfig, id int, title, typeFlag string) error {
	typ := typeFlag
	if typ == "" {
		typ = cfg.DefaultType
	}

	name, err := branch.Name(cfg.BranchTemplate, typ, id, title)
	if err != nil {
		return gitFailed(d, err)
	}

	openGit := d.OpenGit
	if openGit == nil {
		openGit = gitrepo.Open
	}
	repo := openGit(cfg.Repository)

	if err := repo.CheckBranchName(ctx, name); err != nil {
		return gitFailed(d, err)
	}

	exists, err := repo.BranchExists(ctx, name)
	if err != nil {
		return gitFailed(d, err)
	}

	if exists {
		err = repo.Switch(ctx, name)
	} else {
		err = repo.CreateAndSwitch(ctx, name)
	}
	if err != nil {
		return gitFailed(d, err)
	}

	fmt.Fprintf(d.Stdout, "✓ Switched to %s\n", name)
	return nil
}

// gitFailed tells the user (on Stderr) that the local/remote changes
// already went through before returning err as-is, so Main's "error:
// ..." line reports the git failure verbatim (spec §6.7).
func gitFailed(d Deps, err error) error {
	fmt.Fprintln(d.Stderr, "local and remote changes were applied")
	return err
}
