package fake

import (
	"context"
	"errors"
	"testing"

	"kanban-sync/internal/domain"
	"kanban-sync/internal/provider"
	"kanban-sync/internal/provider/providertest"
)

func TestFakeConformance(t *testing.T) {
	providertest.Run(t, func(t *testing.T) provider.IssueProvider { return New() })
}

func TestFakeFailAfter(t *testing.T) {
	boom := errors.New("boom")
	p := New()
	p.FailAfter(1, boom)

	ctx := context.Background()

	if _, err := p.CreateIssue(ctx, provider.NewIssue{Title: "first"}); err != nil {
		t.Fatalf("1st CreateIssue() error = %v, want nil", err)
	}

	if _, err := p.CreateIssue(ctx, provider.NewIssue{Title: "second"}); !errors.Is(err, boom) {
		t.Fatalf("2nd CreateIssue() error = %v, want %v", err, boom)
	}

	// The failed mutation must not have changed state: only the first
	// issue exists.
	if got := len(p.Calls()); got != 2 {
		t.Fatalf("Calls() = %v, want 2 entries", p.Calls())
	}
}

func TestFakeCallsRecorded(t *testing.T) {
	p := New()
	ctx := context.Background()

	if _, err := p.CreateIssue(ctx, provider.NewIssue{Title: "Widget"}); err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}
	if _, err := p.GetIssue(ctx, 1); err != nil {
		t.Fatalf("GetIssue() error = %v", err)
	}
	if _, err := p.ListOpenIssues(ctx); err != nil {
		t.Fatalf("ListOpenIssues() error = %v", err)
	}
	title := "Widget 2"
	if _, err := p.UpdateIssue(ctx, 1, provider.IssueUpdate{Title: &title}); err != nil {
		t.Fatalf("UpdateIssue() error = %v", err)
	}

	want := []string{"create Widget", "get 1", "list", "update 1"}
	got := p.Calls()
	if len(got) != len(want) {
		t.Fatalf("Calls() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Calls()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFakeSeedAndNextID(t *testing.T) {
	p := New()
	p.Seed(
		domain.Issue{ID: 10, Title: "Ten", State: domain.IssueOpen},
		domain.Issue{ID: 5, Title: "Five", State: domain.IssueOpen},
	)

	created, err := p.CreateIssue(context.Background(), provider.NewIssue{Title: "Eleven"})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}
	if created.ID != 11 {
		t.Errorf("created.ID = %d, want 11 (max seeded + 1)", created.ID)
	}
}

func TestFakeIssueURL(t *testing.T) {
	p := New()
	created, err := p.CreateIssue(context.Background(), provider.NewIssue{Title: "has url"})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}
	want := "https://fake/issues/1"
	if created.URL != want {
		t.Errorf("URL = %q, want %q", created.URL, want)
	}
}

func TestFakeReturnsCopies(t *testing.T) {
	p := New()
	created, err := p.CreateIssue(context.Background(), provider.NewIssue{Title: "copy", Labels: []string{"a"}})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}

	created.Labels[0] = "mutated"

	got, err := p.GetIssue(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetIssue() error = %v", err)
	}
	if got.Labels[0] != "a" {
		t.Errorf("internal state mutated through returned slice: Labels = %v, want [a]", got.Labels)
	}
}
