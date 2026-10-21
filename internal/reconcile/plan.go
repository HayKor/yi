package reconcile

import (
	"fmt"
	"path"
	"sort"
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

// Input bundles everything the planner needs: the config, the parsed
// board, and the remote state already fetched by FetchRemote.
//
// Remote holds every issue the caller managed to load: all open
// issues, plus any board issue id that had to be fetched individually
// because it wasn't open. Missing holds the board issue ids that were
// looked up but don't exist remotely (provider.ErrNotFound). An id
// that is in neither map was never looked up at all (a closed issue
// with no card on the board — FetchRemote never fetches those).
//
// HasNote reports whether a card already has a linked task note; the
// planner never looks at the filesystem itself.
type Input struct {
	Cfg     *config.Config
	Board   *kanban.Board
	Remote  map[int]domain.Issue
	Missing map[int]bool
	HasNote func(*kanban.Card) bool
}

// Plan is the result of PlanPull or PlanPush: the changes to apply,
// in deterministic order, plus any warnings worth surfacing (e.g. an
// issue carrying more than one status label).
type Plan struct {
	Changes  []Change
	Warnings []string
}

// Empty reports whether the plan has no changes to apply. A plan can
// have warnings but no changes.
func (p Plan) Empty() bool {
	return len(p.Changes) == 0
}

// warningSet collects warnings, deduplicated, in first-seen order.
type warningSet struct {
	seen map[string]bool
	list []string
}

func (w *warningSet) add(msgs ...string) {
	if w.seen == nil {
		w.seen = map[string]bool{}
	}
	for _, m := range msgs {
		if m == "" || w.seen[m] {
			continue
		}
		w.seen[m] = true
		w.list = append(w.list, m)
	}
}

// missingWarning is the warning emitted for a board card whose issue
// id was looked up but doesn't exist remotely (spec §6.6 pull/push).
func missingWarning(id int) string {
	return fmt.Sprintf("#%d not found in remote; card left unchanged", id)
}

// PlanPull computes the changes `yi pull` (remote wins) would apply:
// moving/renaming/annotating local cards to match remote issues, and
// adding cards for open remote issues that aren't on the board yet.
// It never changes the remote tracker. See spec §6.6.
func PlanPull(in Input) Plan {
	var changes []Change
	var warn warningSet

	for _, card := range in.Board.Cards() {
		if card.IssueID == 0 {
			continue
		}
		id := card.IssueID

		if in.Missing[id] {
			warn.add(missingWarning(id))
			continue
		}

		remote, ok := in.Remote[id]
		if !ok {
			continue
		}

		to, warns := in.Cfg.StatusOf(remote)
		warn.add(warns...)

		if to != card.Status {
			changes = append(changes, MoveCard{Card: card, From: card.Status, To: to})
		}
		local := localTitle(card)
		if notes.Sanitize(remote.Title) != notes.Sanitize(local) {
			changes = append(changes, RenameLocal{Card: card, ID: id, From: local, To: remote.Title})
		}
		if in.Cfg.Notes == config.NotesAlways && !in.HasNote(card) {
			changes = append(changes, LinkNote{Card: card, ID: id, Title: remote.Title})
		}
	}

	var newIDs []int
	for id, issue := range in.Remote {
		if issue.State != domain.IssueOpen {
			continue
		}
		if in.Board.Find(id) != nil {
			continue
		}
		newIDs = append(newIDs, id)
	}
	sort.Ints(newIDs)

	for _, id := range newIDs {
		issue := in.Remote[id]
		to, warns := in.Cfg.StatusOf(issue)
		warn.add(warns...)
		changes = append(changes, AddCard{Issue: issue, To: to, WithNote: in.Cfg.Notes == config.NotesAlways})
	}

	return Plan{Changes: changes, Warnings: warn.list}
}

// PlanPush computes the changes `yi push` (local wins) would apply:
// creating remote issues for id-less cards, and updating remote
// labels/state/title to match the column and title of cards that
// already have an issue. It never changes the local board. Remote
// issues with no card on the board are left untouched. See spec
// §6.6.
func PlanPush(in Input) Plan {
	var changes []Change
	var warn warningSet

	for _, card := range in.Board.Cards() {
		if card.IssueID == 0 {
			if card.Title == "" {
				warn.add(fmt.Sprintf("card at line %d has no title; skipped", card.Line))
				continue
			}
			// A card that already has its own [[link]] (e.g. a draft
			// note linked by hand before ever being pushed) must not
			// get a second, duplicate note: only create one when the
			// card has no link yet and doesn't already resolve to one.
			withNote := in.Cfg.Notes == config.NotesAlways && card.Link == "" && !in.HasNote(card)
			changes = append(changes, CreateIssue{
				Card:     card,
				Title:    localTitle(card),
				Status:   card.Status,
				WithNote: withNote,
			})
			continue
		}

		id := card.IssueID
		if in.Missing[id] {
			warn.add(missingWarning(id))
			continue
		}

		remote, ok := in.Remote[id]
		if !ok {
			continue
		}

		from, warns := in.Cfg.StatusOf(remote)
		warn.add(warns...)

		if upd, ok := RemoteUpdateFor(in.Cfg, remote, card.Status); ok {
			changes = append(changes, SetStatus{ID: id, From: from, To: card.Status, Update: upd})
		}
		local := localTitle(card)
		if notes.Sanitize(remote.Title) != notes.Sanitize(local) {
			changes = append(changes, RenameRemote{ID: id, From: remote.Title, To: local})
		}
	}

	return Plan{Changes: changes, Warnings: warn.list}
}

// RemoteUpdateFor computes the provider.IssueUpdate needed to make
// issue `is` match column `to`: add `to`'s label if the issue doesn't
// carry it, remove every other configured status label it does carry
// (non-status labels are left alone), and close/reopen it if `to`'s
// CloseIssue disagrees with the issue's current state. ok is false if
// none of that is necessary.
func RemoteUpdateFor(cfg *config.Config, is domain.Issue, to domain.StatusKey) (provider.IssueUpdate, bool) {
	toStatus, found := cfg.Status(to)
	if !found {
		return provider.IssueUpdate{}, false
	}

	var add []string
	if !is.HasLabel(toStatus.Label) {
		add = []string{toStatus.Label}
	}

	var remove []string
	for _, label := range cfg.StatusLabels() {
		if label == toStatus.Label {
			continue
		}
		if is.HasLabel(label) {
			remove = append(remove, label)
		}
	}

	var state *domain.IssueState
	switch {
	case toStatus.CloseIssue && is.State == domain.IssueOpen:
		s := domain.IssueClosed
		state = &s
	case !toStatus.CloseIssue && is.State == domain.IssueClosed:
		s := domain.IssueOpen
		state = &s
	}

	ok := len(add) > 0 || len(remove) > 0 || state != nil
	return provider.IssueUpdate{AddLabels: add, RemoveLabels: remove, State: state}, ok
}
