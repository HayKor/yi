package reconcile

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"kanban-sync/internal/config"
	"kanban-sync/internal/domain"
	"kanban-sync/internal/kanban"
	"kanban-sync/internal/notes"
	"kanban-sync/internal/provider"
)

// localTitle returns the title to use for sync comparisons: for a
// linked card, the title derived from its note link, not whatever
// alias the card's wikilink currently displays. Without this, an
// aliased card like "[[Tasks/42 Yandex OAuth|OAuth]]" would never
// match a remote title of "Yandex OAuth" and would drift forever
// (controller ruling, alias drift). Unlinked cards use Title as-is.
func localTitle(c *kanban.Card) string {
	if c.Link == "" {
		return c.Title
	}
	base := path.Base(c.Link)
	prefix := strconv.Itoa(c.IssueID) + " "
	return strings.TrimPrefix(base, prefix)
}

// Applier executes a Plan against Provider and the local board/notes.
// Board must have been parsed from the file at Cfg.KanbanPath, and
// BoardHash must be the hash kanban.ReadFile returned for it (so Apply
// can detect a concurrent edit before writing). Log receives a "✓ ..."
// line for every successfully applied change, plus any warnings; a
// nil Log discards that output.
type Applier struct {
	Cfg       *config.Config
	Board     *kanban.Board
	BoardHash [32]byte
	Provider  provider.IssueProvider
	Notes     *notes.Store
	Index     *notes.Index
	Log       io.Writer
}

// createdIssue remembers an issue Apply created during the current
// run, so that a save failure (kanban.ErrModified) can still tell the
// user which ids to add to which cards by hand.
type createdIssue struct {
	id    int
	title string
}

// Apply executes p's changes against a.Provider and a.Board, in
// order, stopping at the first error. Whether or not every change
// applied, the board is always saved afterward (see save) — a push
// that fails partway through still keeps the issue ids it already
// created, so a re-run won't create duplicates. See spec §6.6.
func (a *Applier) Apply(ctx context.Context, p Plan) error {
	log := a.Log
	if log == nil {
		log = io.Discard
	}

	var created []createdIssue
	total := len(p.Changes)
	done := 0
	var applyErr error

	for _, c := range p.Changes {
		if err := ctx.Err(); err != nil {
			applyErr = err
			break
		}
		if err := a.applyOne(ctx, c, &created, log); err != nil {
			applyErr = err
			break
		}
		done++
		fmt.Fprintf(log, "✓ %s\n", Describe(a.Cfg, c))
	}

	saveErr := a.save(created, log)

	switch {
	case applyErr != nil && saveErr != nil:
		return errors.Join(fmt.Errorf("applied %d of %d changes: %w", done, total, applyErr), saveErr)
	case applyErr != nil:
		return fmt.Errorf("applied %d of %d changes: %w", done, total, applyErr)
	default:
		return saveErr
	}
}

// applyOne applies a single change, recording any issue it creates
// into *created and writing a warning (if any) to log.
func (a *Applier) applyOne(ctx context.Context, c Change, created *[]createdIssue, log io.Writer) error {
	switch v := c.(type) {
	case MoveCard:
		return ApplyMoveCard(a.Cfg, a.Board, v.Card, v.To)
	case AddCard:
		return a.applyAddCard(v)
	case LinkNote:
		return a.applyLinkNote(v)
	case RenameLocal:
		return a.applyRenameLocal(v, log)
	case CreateIssue:
		return a.applyCreateIssue(ctx, v, created)
	case SetStatus:
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := a.Provider.UpdateIssue(ctx, v.ID, v.Update)
		return err
	case RenameRemote:
		if err := ctx.Err(); err != nil {
			return err
		}
		to := v.To
		_, err := a.Provider.UpdateIssue(ctx, v.ID, provider.IssueUpdate{Title: &to})
		return err
	default:
		return fmt.Errorf("apply: unknown change type %T", c)
	}
}

// ApplyMoveCard moves c to column to on b, per §6.2's checkbox rule:
// moving into a CloseIssue status checks it, moving out of one
// unchecks it, otherwise the checkbox is left alone. b.Move already
// implements that rule from its own parsed statuses; this wrapper
// exists so callers go through one reconcile-level entry point for
// every local mutation a Plan can apply. (Named ApplyMoveCard rather
// than MoveCard: that name is already the Change type in changes.go,
// and Go disallows a type and a function sharing an identifier.)
func ApplyMoveCard(cfg *config.Config, b *kanban.Board, c *kanban.Card, to domain.StatusKey) error {
	return b.Move(c, to)
}

// applyAddCard implements AddCard: create a task note first (if
// WithNote), then add the canonical card line to column To.
func (a *Applier) applyAddCard(v AddCard) error {
	toStatus, ok := a.Cfg.Status(v.To)
	if !ok {
		return fmt.Errorf("unknown status %q", v.To)
	}

	var link string
	if v.WithNote {
		notePath, err := a.Notes.Create(a.Index, v.Issue.ID, v.Issue.Title)
		if err != nil {
			return err
		}
		link = a.Notes.LinkTarget(notePath)
	}

	line := kanban.FormatCard(toStatus.CloseIssue, v.Issue.Title, link, v.Issue.ID)
	_, err := a.Board.Add(v.To, line)
	return err
}

// applyLinkNote implements LinkNote: create the note, then rewrite
// the card's first line so it links to it.
func (a *Applier) applyLinkNote(v LinkNote) error {
	notePath, err := a.Notes.Create(a.Index, v.ID, v.Title)
	if err != nil {
		return err
	}
	link := a.Notes.LinkTarget(notePath)
	line := kanban.FormatCard(v.Card.Checked, v.Title, link, v.ID)
	return a.Board.RewriteFirstLine(v.Card, line)
}

// applyRenameLocal implements RenameLocal: if the card has a note,
// rename it (keeping the card's alias, if any) and warn that other
// vault links to the old title weren't updated; otherwise it's a
// plain card and its first line is simply rewritten.
func (a *Applier) applyRenameLocal(v RenameLocal, log io.Writer) error {
	if notePath, ok := a.Notes.Resolve(a.Index, v.ID, v.Card.Link); ok {
		newPath, err := a.Notes.Rename(a.Index, notePath, v.ID, v.From, v.To)
		if err != nil {
			return err
		}
		if err := a.Board.SetLink(v.Card, a.Notes.LinkTarget(newPath)); err != nil {
			return err
		}
		fmt.Fprintf(log, "warning: other links to %q were not updated\n", v.From)
		return nil
	}

	line := kanban.FormatCard(v.Card.Checked, v.To, "", v.ID)
	return a.Board.RewriteFirstLine(v.Card, line)
}

// applyCreateIssue implements CreateIssue: create the remote issue,
// then immediately record its id on the card (and in *created) before
// doing anything else — so a failing close (below) can never lose a
// created issue's id.
func (a *Applier) applyCreateIssue(ctx context.Context, v CreateIssue, created *[]createdIssue) error {
	toStatus, ok := a.Cfg.Status(v.Status)
	if !ok {
		return fmt.Errorf("unknown status %q", v.Status)
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	newIssue, err := a.Provider.CreateIssue(ctx, provider.NewIssue{
		Title:  v.Title,
		Body:   v.Body,
		Labels: []string{toStatus.Label},
	})
	if err != nil {
		return err
	}
	id := newIssue.ID

	if v.WithNote {
		notePath, err := a.Notes.Create(a.Index, id, v.Title)
		if err != nil {
			return err
		}
		link := a.Notes.LinkTarget(notePath)
		line := kanban.FormatCard(v.Card.Checked, v.Title, link, id)
		if err := a.Board.RewriteFirstLine(v.Card, line); err != nil {
			return err
		}
	} else if err := a.Board.SetIssueID(v.Card, id); err != nil {
		return err
	}
	*created = append(*created, createdIssue{id: id, title: v.Title})

	if toStatus.CloseIssue {
		if err := ctx.Err(); err != nil {
			return err
		}
		closed := domain.IssueClosed
		if _, err := a.Provider.UpdateIssue(ctx, id, provider.IssueUpdate{State: &closed}); err != nil {
			return err
		}
	}
	return nil
}

// save writes the board back to Cfg.KanbanPath if (and only if) it
// changed, detecting a concurrent edit the same way WriteFileAtomic
// always does: by re-hashing the file right before renaming over it.
// On that conflict, it tells the user which issues Apply created so
// they can be added to their cards by hand, since the board that
// would have recorded them can't be written.
func (a *Applier) save(created []createdIssue, log io.Writer) error {
	data := a.Board.Bytes()
	if sha256.Sum256(data) == a.BoardHash {
		return nil
	}

	if err := kanban.WriteFileAtomic(a.Cfg.KanbanPath, data, a.BoardHash); err != nil {
		if errors.Is(err, kanban.ErrModified) {
			for _, ci := range created {
				fmt.Fprintf(log, "add %q to card %q manually\n", fmt.Sprintf("#%d", ci.id), ci.title)
			}
			return modifiedError{}
		}
		return err
	}
	return nil
}

// modifiedError is returned by save when Kanban.md changed on disk
// since it was read, instead of kanban.WriteFileAtomic's own error:
// its message names the file by its well-known path and tells the
// user to re-run, while still unwrapping to kanban.ErrModified so
// errors.Is(err, kanban.ErrModified) keeps working for callers.
type modifiedError struct{}

func (modifiedError) Error() string { return "Kanban.md was modified during sync; re-run" }
func (modifiedError) Unwrap() error { return kanban.ErrModified }
