// Package reconcile is the pure decision core behind `yi status`,
// `yi pull`, and `yi push`: it compares a parsed Kanban board against
// fetched remote issues and produces a Plan of Changes, without doing
// any IO or mutating the board itself. The package is named reconcile,
// not sync, to avoid colliding with the stdlib sync package (spec
// §6.6).
//
// reconcile depends on domain, config, kanban, notes, and provider
// (interfaces only).
package reconcile

import (
	"fmt"

	"kanban-sync/internal/config"
	"kanban-sync/internal/domain"
	"kanban-sync/internal/kanban"
	"kanban-sync/internal/provider"
)

// Change is one atomic action a Plan can apply, either to the local
// board/notes or to the remote tracker. Every concrete type below
// implements it via the unexported isChange method, which exists only
// to close the set of implementations.
type Change interface {
	isChange()
}

// MoveCard moves Card from its current column (From) to To, because
// the remote issue's status (per config.StatusOf) no longer matches
// where the card lives. Applying it is a pull-side change.
type MoveCard struct {
	Card     *kanban.Card
	From, To domain.StatusKey
}

func (MoveCard) isChange() {}

// AddCard adds a new card for an open remote issue that has no card on
// the board yet, in column To. WithNote mirrors config.NotesAlways.
// Applying it is a pull-side change.
type AddCard struct {
	Issue    domain.Issue
	To       domain.StatusKey
	WithNote bool
}

func (AddCard) isChange() {}

// RenameLocal renames Card (and its task note, if any) to match the
// remote issue's title, because the two titles differ once both are
// run through notes.Sanitize (spec §6.4). Applying it is a pull-side
// change.
type RenameLocal struct {
	Card     *kanban.Card
	ID       int
	From, To string
}

func (RenameLocal) isChange() {}

// LinkNote creates a task note for Card's issue and rewrites the card
// to link to it, because config.Notes is NotesAlways and the card has
// no note yet. Applying it is a pull-side change.
type LinkNote struct {
	Card  *kanban.Card
	ID    int
	Title string
}

func (LinkNote) isChange() {}

// CreateIssue creates a new remote issue for Card, which has no
// IssueID yet. Status is the column the card currently lives in;
// WithNote mirrors config.NotesAlways. Applying it is a push-side
// change.
type CreateIssue struct {
	Card     *kanban.Card
	Title    string
	Body     string
	Status   domain.StatusKey
	WithNote bool
}

func (CreateIssue) isChange() {}

// SetStatus updates the remote issue ID's labels (and, if needed, its
// open/closed state) so it matches column To instead of From. Update
// is the provider.IssueUpdate computed by RemoteUpdateFor. Applying it
// is a push-side change.
type SetStatus struct {
	ID       int
	From, To domain.StatusKey
	Update   provider.IssueUpdate
}

func (SetStatus) isChange() {}

// RenameRemote renames the remote issue ID to match the local card's
// title, because the two titles differ once both are run through
// notes.Sanitize (spec §6.4). Applying it is a push-side change.
type RenameRemote struct {
	ID       int
	From, To string
}

func (RenameRemote) isChange() {}

// Describe renders a one-line, human-readable description of c, using
// cfg to resolve status keys to their configured headings. It is used
// to print a plan before it's applied.
func Describe(cfg *config.Config, c Change) string {
	heading := func(k domain.StatusKey) string {
		if s, ok := cfg.Status(k); ok {
			return s.Heading
		}
		return string(k)
	}

	switch v := c.(type) {
	case MoveCard:
		return fmt.Sprintf("#%d move: %s → %s", v.Card.IssueID, heading(v.From), heading(v.To))
	case AddCard:
		s := fmt.Sprintf("#%d add %q to %s", v.Issue.ID, v.Issue.Title, heading(v.To))
		if v.WithNote {
			s += " with note"
		}
		return s
	case RenameLocal:
		return fmt.Sprintf("#%d rename: %q → %q", v.ID, v.From, v.To)
	case LinkNote:
		return fmt.Sprintf("#%d create note for %q", v.ID, v.Title)
	case CreateIssue:
		s := fmt.Sprintf("create issue %q in %s", v.Title, heading(v.Status))
		if v.WithNote {
			s += " with note"
		}
		return s
	case SetStatus:
		s := fmt.Sprintf("#%d status: %s → %s", v.ID, heading(v.From), heading(v.To))
		if v.Update.State != nil {
			if *v.Update.State == domain.IssueClosed {
				s += ", close issue"
			} else {
				s += ", reopen issue"
			}
		}
		return s
	case RenameRemote:
		return fmt.Sprintf("#%d rename issue: %q → %q", v.ID, v.From, v.To)
	default:
		return fmt.Sprintf("%v", c)
	}
}
