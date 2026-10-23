package reconcile

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/HayKor/yi/internal/config"
	"github.com/HayKor/yi/internal/domain"
	"github.com/HayKor/yi/internal/kanban"
	"github.com/HayKor/yi/internal/notes"
	"github.com/HayKor/yi/internal/provider"
)

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
		err := a.applyOne(ctx, c, &created, log)
		if errors.Is(err, errSkipped) {
			continue // already warned; nothing was applied
		}
		if err != nil {
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

// errSkipped is returned by a change's apply step when it deliberately
// did nothing (after logging a warning saying why): Apply then neither
// prints a "✓" line for it nor counts it as applied.
var errSkipped = errors.New("change skipped")

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
	case AliasLink:
		return a.Board.SetAlias(v.Card, v.Alias)
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
// WithNote), then add the canonical card line to column To. The card
// and note use the issue title without metadata tokens (cleanTitle).
func (a *Applier) applyAddCard(v AddCard) error {
	toStatus, ok := a.Cfg.Status(v.To)
	if !ok {
		return fmt.Errorf("unknown status %q", v.To)
	}
	title := cleanTitle(v.Issue.Title)

	var link string
	if v.WithNote {
		notePath, err := a.Notes.Create(a.Index, v.Issue.ID, title)
		if err != nil {
			return err
		}
		link = a.Notes.LinkTarget(notePath)
	}

	text := title
	if link != "" {
		text = aliasText(title)
	}
	line := kanban.FormatCard(toStatus.CloseIssue, text, link, v.Issue.ID)
	_, err := a.Board.Add(v.To, line)
	return err
}

// applyLinkNote implements LinkNote: create the note, then replace the
// card's title with a link to it, keeping its dates, tags and id.
func (a *Applier) applyLinkNote(v LinkNote) error {
	notePath, err := a.Notes.Create(a.Index, v.ID, v.Title)
	if err != nil {
		return err
	}
	return a.Board.SetTitle(v.Card, wikilink(a.Notes.LinkTarget(notePath), v.Title))
}

// wikilink formats a link to a task note as "[[target|title]]", so the
// board shows the issue title instead of the note's path, or as
// "[[target]]" if title leaves no alias text (see aliasText).
func wikilink(target, title string) string {
	if alias := aliasText(title); alias != "" {
		return "[[" + target + "|" + alias + "]]"
	}
	return "[[" + target + "]]"
}

// applyRenameLocal implements RenameLocal. A card linking a note yi
// doesn't manage (see managedLink) only gets the new title as its link's
// alias: the user's note is never renamed and the link target never
// rewritten. A yi-managed link's note is renamed (keeping the card's
// alias, if any) and the link updated, with a warning that other vault
// links to the old title weren't updated. If such a card's note can't
// be resolved (deleted, moved, never existed), the card is left exactly
// as it was rather than being silently rewritten into a plain, link-less
// card: only a warning is logged and the change counts as skipped. A
// plain card just gets its title replaced (keeping its dates, tags and
// id), and its note, if one exists unlinked, is renamed along with it.
func (a *Applier) applyRenameLocal(v RenameLocal, log io.Writer) error {
	if v.Card.Link != "" && !managedLink(v.Card) {
		alias := aliasText(v.To)
		if alias == "" {
			fmt.Fprintf(log, "warning: #%d: title %q can't be a link alias; card not renamed\n", v.ID, v.To)
			return errSkipped
		}
		return a.Board.SetAlias(v.Card, alias)
	}

	notePath, ok := a.Notes.Resolve(a.Index, v.ID, v.Card.Link)
	if !ok && v.Card.Link != "" {
		fmt.Fprintf(log, "warning: #%d: note %q not found; card not renamed\n", v.ID, v.Card.Link)
		return errSkipped
	}

	if ok {
		newPath, err := a.Notes.Rename(a.Index, notePath, v.ID, v.From, v.To)
		if err != nil {
			return err
		}
		fmt.Fprintf(log, "warning: other links to %q were not updated\n", v.From)
		if v.Card.Link != "" {
			// An alias yi wrote (the old title) or a missing one follows
			// the new title; an alias the user chose is kept.
			oldAlias := v.Card.Alias
			if err := a.Board.SetLink(v.Card, a.Notes.LinkTarget(newPath)); err != nil {
				return err
			}
			if alias := aliasText(v.To); alias != "" && (oldAlias == "" || !titlesDiffer(oldAlias, v.From)) {
				return a.Board.SetAlias(v.Card, alias)
			}
			return nil
		}
	}
	return a.Board.SetTitle(v.Card, v.To)
}

// aliasText makes title usable as a wikilink alias: "[" and "]" are
// dropped (a "]" would end the link). Titles are compared sanitized,
// which drops them too, so the card stays in sync with the remote.
func aliasText(title string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("[", "", "]", "").Replace(title)), " ")
}

// applyCreateIssue implements CreateIssue: create the remote issue,
// then immediately record its id on the card (and in *created) before
// doing anything else — including before creating its note — so that
// a failing follow-up step (the note's target path squatted by a
// different issue, or a failing close call below) can never lose a
// created issue's id. The note (if WithNote) is created and linked
// only after the id is already safely on the card.
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

	if err := a.Board.SetIssueID(v.Card, id); err != nil {
		return err
	}
	*created = append(*created, createdIssue{id: id, title: v.Title})

	if v.WithNote {
		notePath, err := a.Notes.Create(a.Index, id, v.Title)
		if err != nil {
			return err
		}
		if err := a.Board.SetTitle(v.Card, wikilink(a.Notes.LinkTarget(notePath), v.Title)); err != nil {
			return err
		}
	}

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
// On that conflict, it says that nothing was saved (the "✓" lines for
// local changes are already printed) and tells the user which issues
// Apply created so they can be added to their cards by hand, since the
// board that would have recorded them can't be written.
func (a *Applier) save(created []createdIssue, log io.Writer) error {
	data := a.Board.Bytes()
	if sha256.Sum256(data) == a.BoardHash {
		return nil
	}

	if err := kanban.WriteFileAtomic(a.Cfg.KanbanPath, data, a.BoardHash); err != nil {
		if errors.Is(err, kanban.ErrModified) {
			// The "✓" lines for local changes were already printed;
			// none of them reached the file.
			fmt.Fprintln(log, "warning: Kanban.md was changed by someone else during sync; nothing was saved to it, so the local board changes above were NOT applied")
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
