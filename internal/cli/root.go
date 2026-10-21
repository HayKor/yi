// Package cli wires up the yi command-line interface. It is the only
// package allowed to depend on every other internal package.
package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// Deps collects yi's side-effecting dependencies so they can be faked in
// tests. Fields for the issue provider and git repository are added by
// later tasks.
type Deps struct {
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
	Getenv     func(string) string
	Getwd      func() (string, error)
	IsTerminal bool
	Version    string
}

// usageError marks an error that originates from command-line usage
// (unknown command, unknown flag, wrong number of arguments) rather than
// from a command's own execution. Main uses it to pick exit code 2
// instead of 1.
type usageError struct{ err error }

func (u usageError) Error() string { return u.err.Error() }
func (u usageError) Unwrap() error { return u.err }

// asUsageError wraps a cobra.PositionalArgs validator so any error it
// returns is tagged as a usage error.
func asUsageError(fn cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := fn(cmd, args); err != nil {
			return usageError{err}
		}
		return nil
	}
}

// NewRootCommand builds the yi root command and its subcommands.
func NewRootCommand(d Deps) *cobra.Command {
	root := &cobra.Command{
		Use:           "yi",
		Short:         "Sync an Obsidian Kanban board with issue trackers",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true

	root.SetOut(d.Stdout)
	root.SetErr(d.Stderr)
	if d.Stdin != nil {
		root.SetIn(d.Stdin)
	}

	root.PersistentFlags().String("project", "", "project directory (defaults to the current directory or nearest ancestor)")
	root.PersistentFlags().BoolP("verbose", "v", false, "log HTTP requests and git commands to stderr")

	// A bare "yi" invocation is runnable (it prints help), so Runnable()
	// doesn't short-circuit execution before this Args check runs. That
	// lets us flag unrecognized subcommands as usage errors.
	root.Args = asUsageError(func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())
		}
		return nil
	})
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return usageError{err}
	})

	root.AddCommand(newVersionCommand(d))

	return root
}

func newVersionCommand(d Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the yi version",
		Args:  asUsageError(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(d.Stdout, "yi %s\n", d.Version)
			return nil
		},
	}
}

// Main runs the yi CLI with the given dependencies and arguments, and
// returns the process exit code: 0 on success, 1 on a command error
// (printed to Stderr as "error: <msg>"), 2 on usage errors.
func Main(d Deps, args []string) int {
	cmd := NewRootCommand(d)
	cmd.SetArgs(args)

	err := cmd.Execute()
	if err == nil {
		return 0
	}

	fmt.Fprintf(d.Stderr, "error: %s\n", err)

	var ue usageError
	if errors.As(err, &ue) {
		return 2
	}
	return 1
}
