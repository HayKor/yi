package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"kanban-sync/internal/reconcile"
)

// newPushCommand builds the `yi push` command (local wins): it prints
// a diff and asks for confirmation before touching the remote
// tracker, unless --yes or --dry-run is given.
func newPushCommand(d Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Push local board changes to the remote tracker (local wins)",
		Args:  asUsageError(cobra.NoArgs),
	}
	dryRun := cmd.Flags().Bool("dry-run", false, "print the plan without applying it")
	yes := cmd.Flags().Bool("yes", false, "apply without asking for confirmation")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runPush(cmd, d, *dryRun, *yes)
	}
	return cmd
}

func runPush(cmd *cobra.Command, d Deps, dryRun, yes bool) error {
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

	plan := reconcile.PlanPush(p.input(remote, missing))
	renderPlan(d.Stdout, p.cfg, plan)

	if dryRun || plan.Empty() {
		return nil
	}

	if !yes {
		if !d.IsTerminal {
			return fmt.Errorf("refusing to push without confirmation; re-run with --yes")
		}
		fmt.Fprintf(d.Stdout, "Apply %d changes? [y/N] ", len(plan.Changes))
		ok, err := confirm(d.Stdin)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(d.Stdout, "Aborted.")
			return nil
		}
	}

	return p.applier(d.Stdout).Apply(ctx, plan)
}

// confirm reads a single line from r and reports whether it is "y" or
// "yes", case-insensitively and with surrounding whitespace trimmed.
// Anything else (including EOF with no input at all, or no reader) is
// "no".
func confirm(r io.Reader) (bool, error) {
	if r == nil {
		return false, nil
	}
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		return false, scanner.Err()
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes", nil
}
