package reconcile

import (
	"testing"

	"github.com/HayKor/yi/internal/domain"
)

// With a status field, RemoteUpdateFor sets the field (and the state)
// and never touches labels, even status labels the issue carries.
func TestRemoteUpdateForStatusField(t *testing.T) {
	cfg := testConfig(t, "on-demand")
	field := func(status string, state domain.IssueState) domain.Issue {
		return domain.Issue{ID: 7, State: state, Labels: []string{"status:backlog"}, HasStatusField: true, Status: status}
	}
	cases := []struct {
		name       string
		issue      domain.Issue
		to         domain.StatusKey
		wantStatus string // "" = no Status change
		wantState  string // "", "closed", "open"
		wantOK     bool
	}{
		{"already there", field("todo", domain.IssueOpen), "todo", "", "", false},
		{"different option", field("Todo", domain.IssueOpen), "in-progress", "In Progress", "", true},
		{"not on the project", field("", domain.IssueOpen), "backlog", "Backlog", "", true},
		{"to the close column", field("Todo", domain.IssueOpen), "completed", "Completed", "closed", true},
		{"closed, field already right", field("Completed", domain.IssueClosed), "completed", "", "", false},
		{"reopen", field("Completed", domain.IssueClosed), "todo", "Todo", "open", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			upd, ok := RemoteUpdateFor(cfg, c.issue, c.to)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v (upd %+v)", ok, c.wantOK, upd)
			}
			if len(upd.AddLabels) != 0 || len(upd.RemoveLabels) != 0 {
				t.Errorf("labels changed: add %v, remove %v", upd.AddLabels, upd.RemoveLabels)
			}
			gotStatus := ""
			if upd.Status != nil {
				gotStatus = *upd.Status
			}
			if gotStatus != c.wantStatus {
				t.Errorf("Status = %q, want %q", gotStatus, c.wantStatus)
			}
			gotState := ""
			if upd.State != nil {
				gotState = upd.State.String()
			}
			if gotState != c.wantState {
				t.Errorf("State = %q, want %q", gotState, c.wantState)
			}
		})
	}
}
