package reconcile

import (
	"context"
	"testing"

	"github.com/HayKor/yi/internal/domain"
	"github.com/HayKor/yi/internal/provider/fake"
)

func TestFetchRemote(t *testing.T) {
	p := fake.New()
	p.Seed(
		domain.Issue{ID: 1, Title: "Open one", State: domain.IssueOpen},
		domain.Issue{ID: 2, Title: "Closed two", State: domain.IssueClosed},
	)

	board := buildBoard(t, "## Backlog\n\n- [ ] One `#1`\n- [ ] Two `#2`\n- [ ] Three `#3`\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	remote, missing, err := FetchRemote(context.Background(), p, board)
	if err != nil {
		t.Fatalf("FetchRemote() error = %v", err)
	}

	if _, ok := remote[1]; !ok {
		t.Errorf("remote[1] missing, want the open issue from ListOpenIssues")
	}
	if _, ok := remote[2]; !ok {
		t.Errorf("remote[2] missing, want the closed issue fetched individually")
	}
	if missing[3] != true {
		t.Errorf("missing[3] = false, want true (issue #3 doesn't exist)")
	}
	if len(remote) != 2 {
		t.Errorf("remote = %v, want exactly {1, 2}", remote)
	}

	calls := p.Calls()
	hasGet2, hasGet3, hasGet1 := false, false, false
	for _, c := range calls {
		switch c {
		case "get 2":
			hasGet2 = true
		case "get 3":
			hasGet3 = true
		case "get 1":
			hasGet1 = true
		}
	}
	if !hasGet2 {
		t.Errorf("Calls() = %v, want \"get 2\" (closed issue not in the open list)", calls)
	}
	if !hasGet3 {
		t.Errorf("Calls() = %v, want \"get 3\" (board id not in the open list)", calls)
	}
	if hasGet1 {
		t.Errorf("Calls() = %v, want no \"get 1\" (already loaded via ListOpenIssues)", calls)
	}
}

func TestFetchRemoteSkipsCardsWithoutID(t *testing.T) {
	p := fake.New()
	p.Seed(domain.Issue{ID: 1, Title: "Open one", State: domain.IssueOpen})

	board := buildBoard(t, "## Backlog\n\n- [ ] No id yet\n- [ ] One `#1`\n\n## Todo\n\n## In Progress\n\n## Completed\n")

	_, _, err := FetchRemote(context.Background(), p, board)
	if err != nil {
		t.Fatalf("FetchRemote() error = %v", err)
	}

	for _, c := range p.Calls() {
		if c == "get 0" {
			t.Errorf("Calls() = %v, should never fetch a card with no issue id", p.Calls())
		}
	}
}
