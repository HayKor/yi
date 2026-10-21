// Package providertest is a conformance test suite that any
// provider.IssueProvider implementation should pass. The fake
// provider's own tests run it against fake.New, and future real
// providers run it against their own constructor (see spec §12) —
// including, eventually, a smoke test against a real, shared
// tracker repo. For that reason the suite only ever creates issues
// and never assumes it starts from a clean/empty repo.
package providertest

import (
	"context"
	"errors"
	"sort"
	"testing"

	"kanban-sync/internal/domain"
	"kanban-sync/internal/provider"
)

// notFoundID is an issue id the suite never creates, used to
// exercise ErrNotFound paths without relying on a clean repo.
const notFoundID = 999999999

// Run exercises a provider.IssueProvider built by newProvider against
// the contract in spec §6.1. newProvider is called once per subtest
// so each one gets its own provider.
func Run(t *testing.T, newProvider func(t *testing.T) provider.IssueProvider) {
	t.Run("CreateGet", func(t *testing.T) { testCreateGet(t, newProvider(t)) })
	t.Run("ListOpenExcludesClosed", func(t *testing.T) { testListOpenExcludesClosed(t, newProvider(t)) })
	t.Run("LabelsAddRemove", func(t *testing.T) { testLabelsAddRemove(t, newProvider(t)) })
	t.Run("RemoveAbsentLabel", func(t *testing.T) { testRemoveAbsentLabel(t, newProvider(t)) })
	t.Run("CloseReopen", func(t *testing.T) { testCloseReopen(t, newProvider(t)) })
	t.Run("UpdateTitle", func(t *testing.T) { testUpdateTitle(t, newProvider(t)) })
	t.Run("NotFound", func(t *testing.T) { testNotFound(t, newProvider(t)) })
}

func testCreateGet(t *testing.T, p provider.IssueProvider) {
	ctx := context.Background()

	created, err := p.CreateIssue(ctx, provider.NewIssue{Title: "A", Labels: []string{"status:todo"}})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}

	got, err := p.GetIssue(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetIssue(%d) error = %v", created.ID, err)
	}
	if got.Title != "A" {
		t.Errorf("Title = %q, want %q", got.Title, "A")
	}
	if got.State != domain.IssueOpen {
		t.Errorf("State = %v, want %v", got.State, domain.IssueOpen)
	}
	if !got.HasLabel("status:todo") {
		t.Errorf("Labels = %v, want to contain %q", got.Labels, "status:todo")
	}
	if got.URL == "" {
		t.Error("URL is empty, want a non-empty issue URL")
	}
}

func testListOpenExcludesClosed(t *testing.T, p provider.IssueProvider) {
	ctx := context.Background()

	open, err := p.CreateIssue(ctx, provider.NewIssue{Title: "providertest open issue"})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}
	closed, err := p.CreateIssue(ctx, provider.NewIssue{Title: "providertest closed issue"})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}

	closedState := domain.IssueClosed
	if _, err := p.UpdateIssue(ctx, closed.ID, provider.IssueUpdate{State: &closedState}); err != nil {
		t.Fatalf("UpdateIssue(%d) error = %v", closed.ID, err)
	}

	list, err := p.ListOpenIssues(ctx)
	if err != nil {
		t.Fatalf("ListOpenIssues() error = %v", err)
	}

	if !containsID(list, open.ID) {
		t.Errorf("ListOpenIssues() = %v, want to contain open issue %d", idsOf(list), open.ID)
	}
	if containsID(list, closed.ID) {
		t.Errorf("ListOpenIssues() = %v, want to not contain closed issue %d", idsOf(list), closed.ID)
	}
}

func testLabelsAddRemove(t *testing.T, p provider.IssueProvider) {
	ctx := context.Background()

	created, err := p.CreateIssue(ctx, provider.NewIssue{Title: "providertest labels", Labels: []string{"bug", "status:todo"}})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}

	updated, err := p.UpdateIssue(ctx, created.ID, provider.IssueUpdate{
		AddLabels:    []string{"status:in-progress"},
		RemoveLabels: []string{"status:todo"},
	})
	if err != nil {
		t.Fatalf("UpdateIssue(%d) error = %v", created.ID, err)
	}
	wantLabels(t, updated.Labels, "bug", "status:in-progress")

	got, err := p.GetIssue(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetIssue(%d) error = %v", created.ID, err)
	}
	wantLabels(t, got.Labels, "bug", "status:in-progress")
}

func testRemoveAbsentLabel(t *testing.T, p provider.IssueProvider) {
	ctx := context.Background()

	created, err := p.CreateIssue(ctx, provider.NewIssue{Title: "providertest remove absent label"})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}

	if _, err := p.UpdateIssue(ctx, created.ID, provider.IssueUpdate{RemoveLabels: []string{"nope"}}); err != nil {
		t.Errorf("UpdateIssue(%d, remove absent label) error = %v, want nil", created.ID, err)
	}
}

func testCloseReopen(t *testing.T, p provider.IssueProvider) {
	ctx := context.Background()

	created, err := p.CreateIssue(ctx, provider.NewIssue{Title: "providertest close reopen"})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}

	closed := domain.IssueClosed
	updated, err := p.UpdateIssue(ctx, created.ID, provider.IssueUpdate{State: &closed})
	if err != nil {
		t.Fatalf("UpdateIssue(%d, close) error = %v", created.ID, err)
	}
	if updated.State != domain.IssueClosed {
		t.Fatalf("State after close = %v, want %v", updated.State, domain.IssueClosed)
	}
	got, err := p.GetIssue(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetIssue(%d) error = %v", created.ID, err)
	}
	if got.State != domain.IssueClosed {
		t.Fatalf("GetIssue(%d).State after close = %v, want %v", created.ID, got.State, domain.IssueClosed)
	}

	open := domain.IssueOpen
	updated, err = p.UpdateIssue(ctx, created.ID, provider.IssueUpdate{State: &open})
	if err != nil {
		t.Fatalf("UpdateIssue(%d, reopen) error = %v", created.ID, err)
	}
	if updated.State != domain.IssueOpen {
		t.Fatalf("State after reopen = %v, want %v", updated.State, domain.IssueOpen)
	}

	got, err = p.GetIssue(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetIssue(%d) error = %v", created.ID, err)
	}
	if got.State != domain.IssueOpen {
		t.Fatalf("GetIssue(%d).State after reopen = %v, want %v", created.ID, got.State, domain.IssueOpen)
	}
}

func testUpdateTitle(t *testing.T, p provider.IssueProvider) {
	ctx := context.Background()

	created, err := p.CreateIssue(ctx, provider.NewIssue{Title: "providertest A", Labels: []string{"bug"}})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}

	newTitle := "providertest B"
	updated, err := p.UpdateIssue(ctx, created.ID, provider.IssueUpdate{Title: &newTitle})
	if err != nil {
		t.Fatalf("UpdateIssue(%d) error = %v", created.ID, err)
	}
	if updated.Title != newTitle {
		t.Errorf("Title = %q, want %q", updated.Title, newTitle)
	}
	wantLabels(t, updated.Labels, "bug")

	got, err := p.GetIssue(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetIssue(%d) error = %v", created.ID, err)
	}
	if got.Title != newTitle {
		t.Errorf("GetIssue(%d).Title = %q, want %q", created.ID, got.Title, newTitle)
	}
	wantLabels(t, got.Labels, "bug")
}

func testNotFound(t *testing.T, p provider.IssueProvider) {
	ctx := context.Background()

	if _, err := p.GetIssue(ctx, notFoundID); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("GetIssue(%d) error = %v, want wrapping ErrNotFound", notFoundID, err)
	}

	title := "x"
	if _, err := p.UpdateIssue(ctx, notFoundID, provider.IssueUpdate{Title: &title}); !errors.Is(err, provider.ErrNotFound) {
		t.Errorf("UpdateIssue(%d) error = %v, want wrapping ErrNotFound", notFoundID, err)
	}
}

// containsID reports whether issues contains one with the given id.
func containsID(issues []domain.Issue, id int) bool {
	for _, iss := range issues {
		if iss.ID == id {
			return true
		}
	}
	return false
}

// idsOf returns the ids of issues, for diagnostic test output.
func idsOf(issues []domain.Issue) []int {
	ids := make([]int, len(issues))
	for i, iss := range issues {
		ids[i] = iss.ID
	}
	return ids
}

// wantLabels asserts got has exactly the given labels, ignoring
// order.
func wantLabels(t *testing.T, got []string, want ...string) {
	t.Helper()

	gotSorted := append([]string(nil), got...)
	sort.Strings(gotSorted)
	wantSorted := append([]string(nil), want...)
	sort.Strings(wantSorted)

	if len(gotSorted) != len(wantSorted) {
		t.Errorf("Labels = %v, want %v (order-insensitive)", got, want)
		return
	}
	for i := range gotSorted {
		if gotSorted[i] != wantSorted[i] {
			t.Errorf("Labels = %v, want %v (order-insensitive)", got, want)
			return
		}
	}
}
