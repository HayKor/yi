package providertest

import (
	"context"
	"testing"

	"github.com/HayKor/yi/internal/domain"
	"github.com/HayKor/yi/internal/provider"
)

// RunStatusField exercises a provider that keeps issue status in a
// tracker field (provider.StatusFielder reporting true), such as a
// GitHub provider configured with a project. newProvider must build
// one whose field has the options "Todo" and "Done". Like Run, it only
// creates issues and never assumes an empty tracker.
func RunStatusField(t *testing.T, newProvider func(t *testing.T) provider.IssueProvider) {
	t.Run("ReportsStatusField", func(t *testing.T) { testReportsStatusField(t, newProvider(t)) })
	t.Run("SetStatus", func(t *testing.T) { testSetStatus(t, newProvider(t)) })
	t.Run("SetStatusAndClose", func(t *testing.T) { testSetStatusAndClose(t, newProvider(t)) })
}

func testReportsStatusField(t *testing.T, p provider.IssueProvider) {
	if !provider.UsesStatusField(p) {
		t.Fatalf("UsesStatusField() = false, want true")
	}
	created, err := p.CreateIssue(context.Background(), provider.NewIssue{Title: "providertest field"})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}
	if !created.HasStatusField || created.Status != "" {
		t.Errorf("created issue HasStatusField=%v Status=%q, want true and \"\" (not on the project yet)", created.HasStatusField, created.Status)
	}
}

// statusIn reports the Status of id as GetIssue and ListOpenIssues see
// it ("" from the list if id isn't listed).
func statusIn(t *testing.T, p provider.IssueProvider, id int) (got, listed string) {
	t.Helper()
	ctx := context.Background()
	iss, err := p.GetIssue(ctx, id)
	if err != nil {
		t.Fatalf("GetIssue(%d) error = %v", id, err)
	}
	if !iss.HasStatusField {
		t.Errorf("GetIssue(%d).HasStatusField = false, want true", id)
	}
	open, err := p.ListOpenIssues(ctx)
	if err != nil {
		t.Fatalf("ListOpenIssues() error = %v", err)
	}
	for _, o := range open {
		if o.ID == id {
			if !o.HasStatusField {
				t.Errorf("ListOpenIssues() #%d HasStatusField = false, want true", id)
			}
			listed = o.Status
		}
	}
	return iss.Status, listed
}

func testSetStatus(t *testing.T, p provider.IssueProvider) {
	ctx := context.Background()
	created, err := p.CreateIssue(ctx, provider.NewIssue{Title: "providertest set status"})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}

	todo := "Todo"
	updated, err := p.UpdateIssue(ctx, created.ID, provider.IssueUpdate{Status: &todo})
	if err != nil {
		t.Fatalf("UpdateIssue(Status=Todo) error = %v", err)
	}
	if updated.Status != "Todo" {
		t.Errorf("UpdateIssue() Status = %q, want %q", updated.Status, "Todo")
	}
	if got, listed := statusIn(t, p, created.ID); got != "Todo" || listed != "Todo" {
		t.Errorf("Status after set: GetIssue=%q, ListOpenIssues=%q, want both %q", got, listed, "Todo")
	}

	// Setting it again (now that the issue is on the project) works too.
	done := "Done"
	if _, err := p.UpdateIssue(ctx, created.ID, provider.IssueUpdate{Status: &done}); err != nil {
		t.Fatalf("UpdateIssue(Status=Done) error = %v", err)
	}
	if got, _ := statusIn(t, p, created.ID); got != "Done" {
		t.Errorf("GetIssue() Status = %q after second set, want %q", got, "Done")
	}
}

func testSetStatusAndClose(t *testing.T, p provider.IssueProvider) {
	ctx := context.Background()
	created, err := p.CreateIssue(ctx, provider.NewIssue{Title: "providertest status and close"})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}
	done, closed := "Done", domain.IssueClosed
	updated, err := p.UpdateIssue(ctx, created.ID, provider.IssueUpdate{Status: &done, State: &closed})
	if err != nil {
		t.Fatalf("UpdateIssue(Status=Done, closed) error = %v", err)
	}
	if updated.State != domain.IssueClosed || updated.Status != "Done" {
		t.Errorf("UpdateIssue() = state %v, Status %q; want closed, %q", updated.State, updated.Status, "Done")
	}
	got, err := p.GetIssue(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetIssue() error = %v", err)
	}
	if got.State != domain.IssueClosed || got.Status != "Done" {
		t.Errorf("GetIssue() = state %v, Status %q; want closed, %q", got.State, got.Status, "Done")
	}
}
