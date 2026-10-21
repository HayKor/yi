package reconcile

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"kanban-sync/internal/domain"
	"kanban-sync/internal/kanban"
	"kanban-sync/internal/provider"
)

// maxConcurrentFetches caps how many GetIssue calls FetchRemote makes
// at once, per spec §6.1's "ограничение 8 запросов".
const maxConcurrentFetches = 8

// FetchRemote loads everything the planner needs from p: every open
// issue (one ListOpenIssues call), plus, for every board card whose
// issue id isn't among those open issues, that issue individually
// (via GetIssue, at most maxConcurrentFetches at a time). Cards with
// no issue id yet are never fetched.
//
// remote holds every issue successfully loaded (open issues plus any
// individually-fetched ones); missing holds the ids that came back
// provider.ErrNotFound. Any other error aborts and is returned,
// wrapped as "fetch #<id>: %w"; which one is reported when more than
// one id fails concurrently is unspecified, but it is always one of
// them.
func FetchRemote(ctx context.Context, p provider.IssueProvider, b *kanban.Board) (remote map[int]domain.Issue, missing map[int]bool, err error) {
	open, err := p.ListOpenIssues(ctx)
	if err != nil {
		return nil, nil, err
	}

	remote = make(map[int]domain.Issue, len(open))
	for _, issue := range open {
		remote[issue.ID] = issue
	}

	var toFetch []int
	for _, card := range b.Cards() {
		if card.IssueID == 0 {
			continue
		}
		if _, ok := remote[card.IssueID]; ok {
			continue
		}
		toFetch = append(toFetch, card.IssueID)
	}

	missing = make(map[int]bool)
	if len(toFetch) == 0 {
		return remote, missing, nil
	}

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		sem      = make(chan struct{}, maxConcurrentFetches)
		firstErr error
	)

	for _, id := range toFetch {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			issue, getErr := p.GetIssue(ctx, id)

			mu.Lock()
			defer mu.Unlock()
			switch {
			case getErr == nil:
				remote[id] = issue
			case errors.Is(getErr, provider.ErrNotFound):
				missing[id] = true
			case firstErr == nil:
				firstErr = fmt.Errorf("fetch #%d: %w", id, getErr)
			}
		}()
	}
	wg.Wait()

	if firstErr != nil {
		return nil, nil, firstErr
	}
	return remote, missing, nil
}
