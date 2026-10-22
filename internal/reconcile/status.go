package reconcile

import (
	"sort"

	"kanban-sync/internal/domain"
)

// RowKind classifies one line of `yi status` output.
type RowKind int

const (
	// RowOK is a card with an issue id whose status and title both
	// match the remote issue.
	RowOK RowKind = iota
	// RowDiff is a card with an issue id whose status and/or title
	// differ from the remote issue.
	RowDiff
	// RowLocalOnly is a card with no issue id yet (`yi push` would
	// create one).
	RowLocalOnly
	// RowRemoteOnly is an open remote issue with no card on the board
	// (`yi pull` would add one).
	RowRemoteOnly
	// RowMissing is a card whose issue id was looked up but doesn't
	// exist remotely.
	RowMissing
)

// Row is one line of `yi status` output.
type Row struct {
	Kind      RowKind
	ID        int
	Title     string
	Local     domain.StatusKey
	Remote    domain.StatusKey
	TitleDiff bool

	// RemoteOpen reports whether the matched remote issue (if any) is
	// currently open. It is only meaningful when Remote != "" (RowOK,
	// RowDiff, RowRemoteOnly): the CLI uses it to flag an issue that's
	// open but mapped into a closeIssue column (via a status label),
	// e.g. "open, but in Completed" (spec §6.5/§6.6). It is always
	// true for RowRemoteOnly, since Status only lists open remote
	// issues there.
	RemoteOpen bool
}

// Status builds the `yi status` table: one row per board card (in
// document order), then one row per open remote issue not on the
// board (ascending id). It returns warnings the same way PlanPull and
// PlanPush do (deduplicated, first-seen order). See spec §6.6.
func Status(in Input) ([]Row, []string) {
	var rows []Row
	var warn warningSet

	for _, card := range in.Board.Cards() {
		if card.IssueID == 0 {
			rows = append(rows, Row{Kind: RowLocalOnly, Title: card.Title, Local: card.Status})
			continue
		}
		id := card.IssueID

		if in.Missing[id] {
			rows = append(rows, Row{Kind: RowMissing, ID: id, Title: card.Title, Local: card.Status})
			continue
		}

		remote, ok := in.Remote[id]
		if !ok {
			rows = append(rows, Row{Kind: RowMissing, ID: id, Title: card.Title, Local: card.Status})
			continue
		}

		to, warns := in.Cfg.StatusOf(remote)
		warn.add(warns...)

		titleDiff := titlesDiffer(remote.Title, localTitle(card))
		kind := RowOK
		if to != card.Status || titleDiff {
			kind = RowDiff
		}
		rows = append(rows, Row{
			Kind:       kind,
			ID:         id,
			Title:      remote.Title,
			Local:      card.Status,
			Remote:     to,
			TitleDiff:  titleDiff,
			RemoteOpen: remote.State == domain.IssueOpen,
		})
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
		rows = append(rows, Row{Kind: RowRemoteOnly, ID: id, Title: issue.Title, Remote: to, RemoteOpen: true})
	}

	return rows, warn.list
}
