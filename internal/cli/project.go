package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"kanban-sync/internal/config"
	"kanban-sync/internal/domain"
	"kanban-sync/internal/kanban"
	"kanban-sync/internal/notes"
	"kanban-sync/internal/provider"
	"kanban-sync/internal/reconcile"
)

// project bundles everything a sync command (status/pull/push) needs
// once a project has been located and loaded: the parsed config and
// board, the notes store/index, and a connected provider.
type project struct {
	cfg   *config.Config
	board *kanban.Board
	hash  [32]byte
	store *notes.Store
	index *notes.Index
	prov  provider.IssueProvider
}

// loadProject resolves the project's config (flag is the --project
// flag's value, "" if unset), loads it, reads and parses Kanban.md,
// loads the notes index, and constructs the configured provider.
func loadProject(ctx context.Context, d Deps, flag string) (*project, error) {
	cwd, err := d.Getwd()
	if err != nil {
		return nil, err
	}

	path, err := config.Resolve(config.ResolveInput{
		Flag:             flag,
		Env:              d.Getenv("YI_PROJECT"),
		Cwd:              cwd,
		GlobalConfigPath: config.GlobalConfigPath(d.Getenv),
	})
	if err != nil {
		return nil, err
	}

	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}

	data, hash, err := kanban.ReadFile(cfg.KanbanPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("Kanban.md not found at %s (run \"yi init\" to create one)", cfg.KanbanPath)
		}
		return nil, err
	}
	board, err := kanban.Parse(data, cfg.Statuses)
	if err != nil {
		return nil, err
	}

	store := notes.NewStore(cfg.Dir, cfg.TasksDir)
	index, err := store.LoadIndex()
	if err != nil {
		return nil, err
	}

	newProvider := d.NewProvider
	if newProvider == nil {
		newProvider = provider.New
	}
	prov, err := newProvider(ctx, provider.Config{
		Name:       cfg.Provider,
		Repository: cfg.Repository,
		Options:    cfg.ProviderOptions,
	})
	if err != nil {
		return nil, err
	}

	return &project{cfg: cfg, board: board, hash: hash, store: store, index: index, prov: prov}, nil
}

// input builds a reconcile.Input from the project's state plus remote
// data already fetched by reconcile.FetchRemote.
func (p *project) input(remote map[int]domain.Issue, missing map[int]bool) reconcile.Input {
	return reconcile.Input{
		Cfg:     p.cfg,
		Board:   p.board,
		Remote:  remote,
		Missing: missing,
		HasNote: func(c *kanban.Card) bool {
			_, ok := p.store.Resolve(p.index, c.IssueID, c.Link)
			return ok
		},
	}
}

// applier returns a *reconcile.Applier wired to this project's board,
// notes, and provider, logging applied changes (and any warnings) to
// log.
func (p *project) applier(log io.Writer) *reconcile.Applier {
	return &reconcile.Applier{
		Cfg:       p.cfg,
		Board:     p.board,
		BoardHash: p.hash,
		Provider:  p.prov,
		Notes:     p.store,
		Index:     p.index,
		Log:       log,
	}
}

// logVerbose writes the -v diagnostic lines to w: the config path that
// was loaded, and a breakdown of what FetchRemote came back with.
// Individually-fetched issues only ever happen for board cards whose
// id wasn't among the open issues, so (in steady state) they're the
// closed ones; that's good enough for a diagnostic line, not a precise
// accounting of FetchRemote's own fetch strategy.
func logVerbose(w io.Writer, cfgPath string, remote map[int]domain.Issue, missing map[int]bool) {
	fmt.Fprintf(w, "config: %s\n", cfgPath)

	var open, fetchedIndividually int
	for _, issue := range remote {
		if issue.State == domain.IssueOpen {
			open++
		} else {
			fetchedIndividually++
		}
	}
	fmt.Fprintf(w, "remote: %d open issues, %d fetched individually, %d missing\n", open, fetchedIndividually, len(missing))
}
