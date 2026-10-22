package github

import (
	"context"
	"os"
	"testing"

	"github.com/HayKor/yi/internal/domain"
	"github.com/HayKor/yi/internal/provider"
	"github.com/HayKor/yi/internal/provider/providertest"
)

// recordingProvider wraps a provider.IssueProvider and records the id
// of every issue CreateIssue successfully creates, into a slice shared
// with the test's cleanup. providertest.Run never assumes it starts
// from an empty repository (see the providertest package doc), so the
// smoke test can't just diff before/after; it has to track exactly
// what it made.
type recordingProvider struct {
	provider.IssueProvider
	created *[]int
}

func (r recordingProvider) CreateIssue(ctx context.Context, in provider.NewIssue) (domain.Issue, error) {
	issue, err := r.IssueProvider.CreateIssue(ctx, in)
	if err == nil {
		*r.created = append(*r.created, issue.ID)
	}
	return issue, err
}

// TestGitHubSmoke runs the full provider.IssueProvider conformance
// suite against a real GitHub repository, to catch anything a fake
// transport can't: token resolution, the real API's shapes and
// quirks, actual rate limiting. It's opt-in and skipped by default
// (spec §9/global constraints: tests don't touch the network except
// this one, explicitly): set YI_E2E_GITHUB_REPO to an "owner/repo" of
// a disposable repository you can create issues in, with a token
// resolveToken can find (GITHUB_TOKEN, GH_TOKEN, or `gh auth login`).
// Every issue it creates is closed again in t.Cleanup.
func TestGitHubSmoke(t *testing.T) {
	repo := os.Getenv("YI_E2E_GITHUB_REPO")
	if repo == "" {
		t.Skip("YI_E2E_GITHUB_REPO not set; skipping GitHub smoke test")
	}

	ctx := context.Background()
	var created []int

	t.Cleanup(func() {
		if len(created) == 0 {
			return
		}
		p, err := New(ctx, provider.Config{Name: "github", Repository: repo})
		if err != nil {
			t.Logf("cleanup: New() error = %v", err)
			return
		}
		closed := domain.IssueClosed
		for _, id := range created {
			if _, err := p.UpdateIssue(ctx, id, provider.IssueUpdate{State: &closed}); err != nil {
				t.Logf("cleanup: close #%d: %v", id, err)
			}
		}
	})

	providertest.Run(t, func(t *testing.T) provider.IssueProvider {
		p, err := New(ctx, provider.Config{Name: "github", Repository: repo})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		return recordingProvider{IssueProvider: p, created: &created}
	})
}
