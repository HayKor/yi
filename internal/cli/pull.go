package cli

import (
	"github.com/spf13/cobra"

	"github.com/HayKor/yi/internal/reconcile"
)

// newPullCommand builds the `yi pull` command (remote wins): local
// changes are applied without confirmation (only local files are
// touched), but --dry-run prints the plan without applying it.
func newPullCommand(d Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Pull remote issue changes into the board (remote wins)",
		Args:  asUsageError(cobra.NoArgs),
	}
	dryRun := cmd.Flags().Bool("dry-run", false, "print the plan without applying it")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runPull(cmd, d, *dryRun)
	}
	return cmd
}

func runPull(cmd *cobra.Command, d Deps, dryRun bool) error {
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

	plan := reconcile.PlanPull(p.input(remote, missing))
	renderPlan(d.Stdout, p.cfg, plan)

	if dryRun || plan.Empty() {
		return nil
	}

	return p.applier(d.Stdout).Apply(ctx, plan)
}
