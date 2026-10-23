// Package fake implements an in-memory provider.IssueProvider, used
// by the providertest conformance suite and by any package that
// needs a tracker double instead of talking to a real one.
package fake

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/HayKor/yi/internal/domain"
	"github.com/HayKor/yi/internal/provider"
)

// Provider is an in-memory, mutex-guarded provider.IssueProvider.
// Issues and their Labels slices are copied on the way in and out,
// so a caller can't mutate Provider's internal state through a
// returned domain.Issue.
type Provider struct {
	mu     sync.Mutex
	issues map[int]domain.Issue
	nextID int
	calls  []string

	// failAfter is the number of mutating calls (CreateIssue/
	// UpdateIssue) that still succeed; -1 means "never fail". failErr
	// is the error returned once that budget is exhausted.
	failAfter int
	failErr   error

	// statusField makes the provider keep status in a field (a GitHub
	// Project's Status) instead of labels; see NewWithStatusField.
	statusField bool
}

var (
	_ provider.IssueProvider = (*Provider)(nil)
	_ provider.StatusFielder = (*Provider)(nil)
)

// New returns an empty Provider.
func New() *Provider {
	return &Provider{
		issues:    make(map[int]domain.Issue),
		nextID:    1,
		failAfter: -1,
	}
}

// NewWithStatusField returns an empty Provider that keeps issue status
// in a field, like a GitHub provider configured with a project: every
// issue it returns has HasStatusField set, and UpdateIssue applies
// IssueUpdate.Status. A seeded issue's Status is its field value.
func NewWithStatusField() *Provider {
	p := New()
	p.statusField = true
	return p
}

// StatusField reports whether p was built by NewWithStatusField.
func (p *Provider) StatusField() bool { return p.statusField }

// Seed adds issues to the provider directly, bypassing CreateIssue
// and without recording a call. It also advances the id counter used
// by CreateIssue past the highest seeded id.
func (p *Provider) Seed(issues ...domain.Issue) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, iss := range issues {
		p.issues[iss.ID] = cloneIssue(iss)
		if iss.ID+1 > p.nextID {
			p.nextID = iss.ID + 1
		}
	}
}

// Issue returns a copy of the issue with id, or the zero domain.Issue
// if there is none. Unlike GetIssue, this does not record a call;
// it's for tests to inspect state directly.
func (p *Provider) Issue(id int) domain.Issue {
	p.mu.Lock()
	defer p.mu.Unlock()

	return cloneIssue(p.issues[id])
}

// Calls returns every call made to the provider so far, including
// ones that failed, in the order they were made. Entries look like
// "list", "get 42", "create Title", and "update 42".
func (p *Provider) Calls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]string(nil), p.calls...)
}

// FailAfter makes the first n mutating calls (CreateIssue and
// UpdateIssue, counted together) succeed normally, and every such
// call after that fail with err and leave state unchanged.
func (p *Provider) FailAfter(n int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.failAfter = n
	p.failErr = err
}

// ListOpenIssues returns the open issues, sorted by id ascending.
func (p *Provider) ListOpenIssues(ctx context.Context) ([]domain.Issue, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.calls = append(p.calls, "list")

	var open []domain.Issue
	for _, iss := range p.issues {
		if iss.State == domain.IssueOpen {
			open = append(open, p.out(iss))
		}
	}
	sort.Slice(open, func(i, j int) bool { return open[i].ID < open[j].ID })
	return open, nil
}

// GetIssue returns the issue with id, in any state.
func (p *Provider) GetIssue(ctx context.Context, id int) (domain.Issue, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.calls = append(p.calls, fmt.Sprintf("get %d", id))

	iss, ok := p.issues[id]
	if !ok {
		return domain.Issue{}, fmt.Errorf("get issue %d: %w", id, provider.ErrNotFound)
	}
	return p.out(iss), nil
}

// CreateIssue creates a new open issue with the next available id.
func (p *Provider) CreateIssue(ctx context.Context, in provider.NewIssue) (domain.Issue, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.calls = append(p.calls, fmt.Sprintf("create %s", in.Title))

	if err := p.consumeMutationLocked(); err != nil {
		return domain.Issue{}, err
	}

	id := p.nextID
	p.nextID++

	iss := domain.Issue{
		ID:        id,
		Title:     in.Title,
		State:     domain.IssueOpen,
		Labels:    append([]string(nil), in.Labels...),
		URL:       fmt.Sprintf("https://fake/issues/%d", id),
		UpdatedAt: time.Now(),
	}
	p.issues[id] = cloneIssue(iss)
	return p.out(iss), nil
}

// UpdateIssue applies upd to the issue with id: Title, then
// AddLabels (skipping labels already present, keeping insertion
// order), then RemoveLabels (removing an absent label is not an
// error), then State, then Status (an error unless the provider was
// built by NewWithStatusField). It always sets UpdatedAt to
// time.Now().
func (p *Provider) UpdateIssue(ctx context.Context, id int, upd provider.IssueUpdate) (domain.Issue, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.calls = append(p.calls, fmt.Sprintf("update %d", id))

	iss, ok := p.issues[id]
	if !ok {
		return domain.Issue{}, fmt.Errorf("update issue %d: %w", id, provider.ErrNotFound)
	}

	if upd.Status != nil && !p.statusField {
		return domain.Issue{}, fmt.Errorf("update issue %d: fake provider has no status field", id)
	}

	if err := p.consumeMutationLocked(); err != nil {
		return domain.Issue{}, err
	}

	if upd.Title != nil {
		iss.Title = *upd.Title
	}

	for _, label := range upd.AddLabels {
		if !iss.HasLabel(label) {
			iss.Labels = append(iss.Labels, label)
		}
	}

	if len(upd.RemoveLabels) > 0 {
		remove := make(map[string]bool, len(upd.RemoveLabels))
		for _, l := range upd.RemoveLabels {
			remove[l] = true
		}
		kept := make([]string, 0, len(iss.Labels))
		for _, l := range iss.Labels {
			if !remove[l] {
				kept = append(kept, l)
			}
		}
		iss.Labels = kept
	}

	if upd.State != nil {
		iss.State = *upd.State
	}

	if upd.Status != nil {
		iss.Status = *upd.Status
	}

	iss.UpdatedAt = time.Now()

	p.issues[id] = cloneIssue(iss)
	return p.out(iss), nil
}

// consumeMutationLocked reports whether the current mutating call
// should fail, per FailAfter. The caller must hold p.mu.
func (p *Provider) consumeMutationLocked() error {
	if p.failAfter < 0 {
		return nil
	}
	if p.failAfter == 0 {
		return p.failErr
	}
	p.failAfter--
	return nil
}

// cloneIssue returns a deep copy of iss so neither Provider nor its
// caller can mutate the other's Labels slice through it.
// out is the copy of iss the provider hands to a caller: in status
// field mode it is marked HasStatusField.
func (p *Provider) out(iss domain.Issue) domain.Issue {
	iss = cloneIssue(iss)
	iss.HasStatusField = p.statusField
	return iss
}

func cloneIssue(iss domain.Issue) domain.Issue {
	iss.Labels = append([]string(nil), iss.Labels...)
	return iss
}
