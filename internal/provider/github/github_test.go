package github

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"kanban-sync/internal/domain"
	"kanban-sync/internal/provider"
	"kanban-sync/internal/provider/providertest"
)

const testOwner = "octo"
const testRepo = "widgets"

func TestGitHubConformance(t *testing.T) {
	providertest.Run(t, func(t *testing.T) provider.IssueProvider {
		fake := newFakeGitHub(testOwner, testRepo)
		t.Cleanup(fake.Close)

		p, err := newWithClient(fake.Client(), fake.URL()+"/", testOwner+"/"+testRepo)
		if err != nil {
			t.Fatalf("newWithClient() error = %v", err)
		}
		return p
	})
}

func TestListFiltersPullRequests(t *testing.T) {
	fake := newFakeGitHub(testOwner, testRepo)
	defer fake.Close()

	fake.Seed(&fakeIssue{Number: 1, Title: "issue one", State: "open", UpdatedAt: time.Now()})
	fake.Seed(&fakeIssue{Number: 2, Title: "issue two", State: "open", UpdatedAt: time.Now()})
	fake.Seed(&fakeIssue{Number: 3, Title: "a pull request", State: "open", UpdatedAt: time.Now(), PullRequest: &fakePullRequest{URL: "https://example/pulls/3"}})

	p, err := newWithClient(fake.Client(), fake.URL()+"/", testOwner+"/"+testRepo)
	if err != nil {
		t.Fatalf("newWithClient() error = %v", err)
	}

	got, err := p.ListOpenIssues(context.Background())
	if err != nil {
		t.Fatalf("ListOpenIssues() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListOpenIssues() returned %d issues, want 2 (PR excluded): %+v", len(got), got)
	}
	for _, iss := range got {
		if iss.ID == 3 {
			t.Errorf("ListOpenIssues() included pull request #3")
		}
	}
}

func TestListPaginates(t *testing.T) {
	fake := newFakeGitHub(testOwner, testRepo)
	defer fake.Close()

	const total = 230
	for i := 1; i <= total; i++ {
		fake.Seed(&fakeIssue{Number: i, Title: "issue", State: "open", UpdatedAt: time.Now()})
	}

	p, err := newWithClient(fake.Client(), fake.URL()+"/", testOwner+"/"+testRepo)
	if err != nil {
		t.Fatalf("newWithClient() error = %v", err)
	}

	got, err := p.ListOpenIssues(context.Background())
	if err != nil {
		t.Fatalf("ListOpenIssues() error = %v", err)
	}
	if len(got) != total {
		t.Fatalf("ListOpenIssues() returned %d issues, want %d", len(got), total)
	}

	path := "/repos/" + testOwner + "/" + testRepo + "/issues"
	if n := fake.RequestCount(path); n != 3 {
		t.Errorf("RequestCount(%q) = %d, want 3 page requests (230 issues / 100 per page)", path, n)
	}
}

func TestUnauthorized(t *testing.T) {
	fake := newFakeGitHub(testOwner, testRepo)
	defer fake.Close()
	fake.SetUnauthorized(true)

	p, err := newWithClient(fake.Client(), fake.URL()+"/", testOwner+"/"+testRepo)
	if err != nil {
		t.Fatalf("newWithClient() error = %v", err)
	}

	_, err = p.ListOpenIssues(context.Background())
	if !errors.Is(err, provider.ErrAuth) {
		t.Fatalf("ListOpenIssues() error = %v, want wrapping ErrAuth", err)
	}
}

func TestRepositoryFormat(t *testing.T) {
	for _, bad := range []string{"noslash", "/repo", "owner/", "owner/repo/extra", ""} {
		_, _, err := splitRepository(bad)
		if err == nil {
			t.Errorf("splitRepository(%q) error = nil, want error", bad)
			continue
		}
		if !strings.Contains(err.Error(), "owner/repo") {
			t.Errorf("splitRepository(%q) error = %q, want to contain %q", bad, err.Error(), "owner/repo")
		}
	}

	owner, repo, err := splitRepository("owner/repo")
	if err != nil {
		t.Fatalf("splitRepository(owner/repo) error = %v, want nil", err)
	}
	if owner != "owner" || repo != "repo" {
		t.Errorf("splitRepository(owner/repo) = (%q, %q), want (owner, repo)", owner, repo)
	}
}

// yamlOptionsNode builds the yaml.Node New() would receive as
// cfg.Options for the given "github:" options section body, matching
// the shape config.go hands providers (a bare mapping node, document
// wrapper unwrapped).
func yamlOptionsNode(t *testing.T, body string) yaml.Node {
	t.Helper()

	var node yaml.Node
	if err := yaml.Unmarshal([]byte(body), &node); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	if node.Kind == yaml.DocumentNode {
		node = *node.Content[0]
	}
	return node
}

func TestBaseURLOption(t *testing.T) {
	fake := newFakeGitHub(testOwner, testRepo)
	defer fake.Close()
	fake.Seed(&fakeIssue{Number: 1, Title: "issue one", State: "open", UpdatedAt: time.Now()})

	t.Setenv("GITHUB_TOKEN", "test")

	p, err := New(context.Background(), provider.Config{
		Name:       "github",
		Repository: testOwner + "/" + testRepo,
		Options:    yamlOptionsNode(t, "baseUrl: "+fake.URL()+"\n"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got, err := p.ListOpenIssues(context.Background())
	if err != nil {
		t.Fatalf("ListOpenIssues() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListOpenIssues() = %d issues, want 1", len(got))
	}

	// The request must have gone through the "/api/v3" prefix
	// go-github adds for a non-default base URL (WithEnterpriseURLs).
	path := "/api/v3/repos/" + testOwner + "/" + testRepo + "/issues"
	if n := fake.RawRequestCount(path); n == 0 {
		t.Errorf("fake server saw no requests at %q, want at least one", path)
	}
}

func TestUnknownOptionRejected(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test")

	// A plausible typo: "baseURL" instead of "baseUrl". Decoding this
	// permissively would silently leave options.BaseURL empty, so New
	// would send the token to the public API instead of the intended
	// Enterprise host - a security-relevant surprise, not just a
	// shrugged-off typo.
	_, err := New(context.Background(), provider.Config{
		Name:       "github",
		Repository: testOwner + "/" + testRepo,
		Options:    yamlOptionsNode(t, "baseURL: https://github.example.com\n"),
	})
	if err == nil {
		t.Fatal("New() error = nil, want error for unknown option \"baseURL\"")
	}
	if !strings.Contains(err.Error(), `unknown option "baseURL"`) {
		t.Errorf("New() error = %q, want to contain %q", err.Error(), `unknown option "baseURL"`)
	}
	if !strings.Contains(err.Error(), "baseUrl") {
		t.Errorf("New() error = %q, want to mention the correct key %q", err.Error(), "baseUrl")
	}
}

func TestKnownOptionAccepted(t *testing.T) {
	fake := newFakeGitHub(testOwner, testRepo)
	defer fake.Close()
	fake.Seed(&fakeIssue{Number: 1, Title: "issue one", State: "open", UpdatedAt: time.Now()})

	t.Setenv("GITHUB_TOKEN", "test")

	// A zero-value Options node (no "github:" section at all) must
	// still work: decodeOptions must not require a mapping node.
	_, err := New(context.Background(), provider.Config{
		Name:       "github",
		Repository: testOwner + "/" + testRepo,
	})
	if err != nil {
		t.Fatalf("New() with zero Options error = %v, want nil", err)
	}

	p, err := New(context.Background(), provider.Config{
		Name:       "github",
		Repository: testOwner + "/" + testRepo,
		Options:    yamlOptionsNode(t, "baseUrl: "+fake.URL()+"\n"),
	})
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	if _, err := p.ListOpenIssues(context.Background()); err != nil {
		t.Fatalf("ListOpenIssues() error = %v, want nil", err)
	}
}

func TestRateLimitError(t *testing.T) {
	fake := newFakeGitHub(testOwner, testRepo)
	defer fake.Close()
	fake.SetRateLimited(true)

	p, err := newWithClient(fake.Client(), fake.URL()+"/", testOwner+"/"+testRepo)
	if err != nil {
		t.Fatalf("newWithClient() error = %v", err)
	}

	_, err = p.ListOpenIssues(context.Background())
	if err == nil {
		t.Fatal("ListOpenIssues() error = nil, want rate limit error")
	}
	if !strings.Contains(err.Error(), "github: rate limit exceeded, resets at") {
		t.Errorf("ListOpenIssues() error = %q, want to contain %q", err.Error(), "github: rate limit exceeded, resets at")
	}
}

func TestNotFoundWrapped(t *testing.T) {
	fake := newFakeGitHub(testOwner, testRepo)
	defer fake.Close()

	p, err := newWithClient(fake.Client(), fake.URL()+"/", testOwner+"/"+testRepo)
	if err != nil {
		t.Fatalf("newWithClient() error = %v", err)
	}

	_, err = p.GetIssue(context.Background(), 42)
	if !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("GetIssue() error = %v, want wrapping ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "get issue #42") {
		t.Errorf("GetIssue() error = %q, want to contain %q", err.Error(), "get issue #42")
	}
}

func TestUpdateIssueLabelsAndState(t *testing.T) {
	fake := newFakeGitHub(testOwner, testRepo)
	defer fake.Close()
	fake.Seed(&fakeIssue{Number: 7, Title: "t", State: "open", Labels: []fakeLabel{{Name: "bug"}, {Name: "status:todo"}}, UpdatedAt: time.Now()})

	p, err := newWithClient(fake.Client(), fake.URL()+"/", testOwner+"/"+testRepo)
	if err != nil {
		t.Fatalf("newWithClient() error = %v", err)
	}

	got, err := p.UpdateIssue(context.Background(), 7, provider.IssueUpdate{
		AddLabels:    []string{"status:in-progress"},
		RemoveLabels: []string{"status:todo", "does-not-exist"},
	})
	if err != nil {
		t.Fatalf("UpdateIssue() error = %v", err)
	}
	if !got.HasLabel("bug") || !got.HasLabel("status:in-progress") || got.HasLabel("status:todo") {
		t.Errorf("UpdateIssue() labels = %v, want [bug status:in-progress]", got.Labels)
	}

	closed := domain.IssueClosed
	title := "renamed"
	got, err = p.UpdateIssue(context.Background(), 7, provider.IssueUpdate{Title: &title, State: &closed})
	if err != nil {
		t.Fatalf("UpdateIssue() error = %v", err)
	}
	if got.Title != "renamed" || got.State != domain.IssueClosed {
		t.Errorf("UpdateIssue() = title %q state %v, want %q closed", got.Title, got.State, "renamed")
	}
}

func TestRemoveLabelWithSpecialCharacters(t *testing.T) {
	fake := newFakeGitHub(testOwner, testRepo)
	defer fake.Close()
	fake.Seed(&fakeIssue{Number: 9, Title: "t", State: "open", UpdatedAt: time.Now()})

	p, err := newWithClient(fake.Client(), fake.URL()+"/", testOwner+"/"+testRepo)
	if err != nil {
		t.Fatalf("newWithClient() error = %v", err)
	}

	for _, label := range []string{"kind/bug", "needs#triage", "blocked?"} {
		t.Run(label, func(t *testing.T) {
			ctx := context.Background()

			added, err := p.UpdateIssue(ctx, 9, provider.IssueUpdate{AddLabels: []string{label}})
			if err != nil {
				t.Fatalf("UpdateIssue(add %q) error = %v", label, err)
			}
			if !added.HasLabel(label) {
				t.Fatalf("UpdateIssue(add %q) labels = %v, want to contain %q", label, added.Labels, label)
			}

			removed, err := p.UpdateIssue(ctx, 9, provider.IssueUpdate{RemoveLabels: []string{label}})
			if err != nil {
				t.Fatalf("UpdateIssue(remove %q) error = %v", label, err)
			}
			if removed.HasLabel(label) {
				t.Fatalf("UpdateIssue(remove %q) labels = %v, want %q gone", label, removed.Labels, label)
			}

			got, err := p.GetIssue(ctx, 9)
			if err != nil {
				t.Fatalf("GetIssue() error = %v", err)
			}
			if got.HasLabel(label) {
				t.Fatalf("GetIssue() labels = %v, want %q gone", got.Labels, label)
			}
		})
	}
}

func TestMapErrStatusCodes(t *testing.T) {
	fake := newFakeGitHub(testOwner, testRepo)
	defer fake.Close()

	p, err := newWithClient(fake.Client(), fake.URL()+"/", testOwner+"/"+testRepo)
	if err != nil {
		t.Fatalf("newWithClient() error = %v", err)
	}

	fake.SetUnauthorized(true)
	_, err = p.CreateIssue(context.Background(), provider.NewIssue{Title: "x"})
	if !errors.Is(err, provider.ErrAuth) {
		t.Fatalf("CreateIssue() error = %v, want wrapping ErrAuth", err)
	}
}
