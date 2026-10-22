package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/HayKor/yi/internal/reconcile"
)

// newStatusCommand builds the `yi status` command: it never changes
// anything, local or remote.
func newStatusCommand(d Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show differences between the board and the remote tracker",
		Args:  asUsageError(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd, d)
		},
	}
}

func runStatus(cmd *cobra.Command, d Deps) error {
	ctx := cmd.Context()
	flagProject, _ := cmd.Flags().GetString("project")
	verbose, _ := cmd.Flags().GetBool("verbose")

	p, err := loadProject(ctx, d, flagProject)
	if err != nil {
		return err
	}

	remote, missing, err := reconcile.FetchRemote(ctx, p.prov, p.board)
	if err != nil {
		return err
	}
	if verbose {
		logVerbose(d.Stderr, p.cfg.Path, remote, missing)
	}

	rows, warns := reconcile.Status(p.input(remote, missing))

	// Row.Title only ever holds the remote title; splice in the local
	// (card) title for rows whose title differs, per spec §6.6's
	// "<local> → <remote>" display.
	for i := range rows {
		if rows[i].TitleDiff {
			if card := p.board.Find(rows[i].ID); card != nil {
				rows[i].Title = fmt.Sprintf("%s → %s", card.Title, rows[i].Title)
			}
		}
	}

	color := d.IsTerminal && d.Getenv("NO_COLOR") == ""
	renderStatus(d.Stdout, p.cfg, rows, warns, color)
	return nil
}
