// Command yi syncs an Obsidian Kanban board with issue trackers.
package main

import (
	"os"

	"kanban-sync/internal/cli"
	_ "kanban-sync/internal/provider/github"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(cli.Main(cli.Deps{
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		Getenv:     os.Getenv,
		Getwd:      os.Getwd,
		IsTerminal: isTerminal(os.Stdout),
		Version:    version,
	}, os.Args[1:]))
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
