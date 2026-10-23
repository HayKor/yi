package github

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/HayKor/yi/internal/domain"
	"github.com/HayKor/yi/internal/provider"
	"github.com/HayKor/yi/internal/provider/providertest"
)

// projectStatuses are board columns mapped to the options of the fake
// project newProjectProvider sets up.
var projectStatuses = []domain.Status{
	{Key: "todo", Heading: "Todo", ProjectStatus: "Todo"},
	{Key: "done", Heading: "Done", ProjectStatus: "Done", CloseIssue: true},
}

// newProjectProvider returns a fake server hosting project octo/3 with
// Todo/Doing/Done options, and a Provider configured with project "3".
func newProjectProvider(t *testing.T) (*fakeGitHub, *Provider) {
	t.Helper()
	fake := newFakeGitHub(testOwner, testRepo)
	t.Cleanup(fake.Close)
	fake.SetProject(testOwner, 3, "Todo", "Doing", "Done")
	p, err := newWithClientOptions(context.Background(), fake.Client(), fake.URL()+"/", testOwner+"/"+testRepo, options{Project: "3"}, projectStatuses)
	if err != nil {
		t.Fatalf("newWithClientOptions() error = %v", err)
	}
	return fake, p
}

func TestGitHubStatusFieldConformance(t *testing.T) {
	providertest.RunStatusField(t, func(t *testing.T) provider.IssueProvider {
		_, p := newProjectProvider(t)
		return p
	})
}

func TestProjectStatusOnListAndGet(t *testing.T) {
	fake := newFakeGitHub(testOwner, testRepo)
	defer fake.Close()
	proj := fake.SetProject(testOwner, 3, "Todo", "Doing", "Done")
	proj.pageSize = 2 // make the items query paginate
	for n := 1; n <= 4; n++ {
		fake.Seed(&fakeIssue{Number: n, Title: "i", State: "open", UpdatedAt: time.Now()})
	}
	fake.Seed(&fakeIssue{Number: 5, Title: "closed", State: "closed", UpdatedAt: time.Now()})
	fake.AddItem(0, "", "Todo")                      // a draft issue
	fake.AddItem(1, "other/repo", "Done")            // same number, another repo
	fake.AddItem(1, testOwner+"/"+testRepo, "Doing") // #1
	fake.AddItem(2, testOwner+"/"+testRepo, "")      // #2 on the project, no Status
	fake.AddItem(5, testOwner+"/"+testRepo, "Done")  // closed #5
	// #3, #4 aren't on the project.

	p, err := newWithClientOptions(context.Background(), fake.Client(), fake.URL()+"/", testOwner+"/"+testRepo, options{Project: "3"}, projectStatuses)
	if err != nil {
		t.Fatal(err)
	}
	open, err := p.ListOpenIssues(context.Background())
	if err != nil {
		t.Fatalf("ListOpenIssues() error = %v", err)
	}
	want := map[int]string{1: "Doing", 2: "", 3: "", 4: ""}
	for _, iss := range open {
		if !iss.HasStatusField || iss.Status != want[iss.ID] {
			t.Errorf("#%d HasStatusField=%v Status=%q, want true, %q", iss.ID, iss.HasStatusField, iss.Status, want[iss.ID])
		}
	}
	closed, err := p.GetIssue(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != "Done" {
		t.Errorf("#5 Status = %q, want Done", closed.Status)
	}
	if n := fake.OpCount("yiProjectItems"); n != 3 {
		t.Errorf("yiProjectItems requests = %d, want 3 (5 items, 2 per page, loaded once)", n)
	}
}

func TestUpdateIssueStatusAddsToProject(t *testing.T) {
	fake, p := newProjectProvider(t)
	fake.Seed(&fakeIssue{Number: 9, Title: "t", State: "open", Labels: []fakeLabel{{Name: "status:todo"}}, UpdatedAt: time.Now()})

	doing := "doing" // option names match case-insensitively
	got, err := p.UpdateIssue(context.Background(), 9, provider.IssueUpdate{Status: &doing})
	if err != nil {
		t.Fatalf("UpdateIssue() error = %v", err)
	}
	if got.Status != "Doing" {
		t.Errorf("returned Status = %q, want the option's own spelling %q", got.Status, "Doing")
	}
	if status, on := fake.ItemStatus(9); !on || status != "Doing" {
		t.Errorf("project item = %q (on project: %v), want Doing", status, on)
	}
	if len(got.Labels) != 1 || got.Labels[0] != "status:todo" {
		t.Errorf("labels = %v, want untouched", got.Labels)
	}

	review := "Review"
	if _, err := p.UpdateIssue(context.Background(), 9, provider.IssueUpdate{Status: &review}); err == nil || !strings.Contains(err.Error(), `no Status option "Review"`) {
		t.Errorf("UpdateIssue(unknown option) error = %v, want it named", err)
	}
}

func TestProjectSetupErrors(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(f *fakeGitHub)
		project string
		want    string
	}{
		{"no such project", func(f *fakeGitHub) { f.SetProject(testOwner, 3, "Todo", "Done") }, "7", "Could not resolve to a ProjectV2 with the number 7"},
		{"no such owner", func(f *fakeGitHub) {}, "ghost/1", `no user or organization "ghost"`},
		{"no Status field", func(f *fakeGitHub) { f.SetProject(testOwner, 3).noStatus = true }, "3", `has no single-select field "Status"`},
		{"missing option", func(f *fakeGitHub) { f.SetProject(testOwner, 3, "Todo", "In review") }, "3", `has no Status option for statuses.done ("Done"); its options: Todo, In review (set projectStatus`},
		{"bad ref", func(f *fakeGitHub) {}, "octo/x", "project must be a project number or owner/number"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := newFakeGitHub(testOwner, testRepo)
			defer fake.Close()
			c.setup(fake)
			_, err := newWithClientOptions(context.Background(), fake.Client(), fake.URL()+"/", testOwner+"/"+testRepo, options{Project: c.project}, projectStatuses)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %v, want it to contain %q", err, c.want)
			}
		})
	}
}

func TestParseProjectRef(t *testing.T) {
	for _, c := range []struct {
		in, owner string
		number    int
	}{{"1", "octo", 1}, {"acme/12", "acme", 12}} {
		owner, number, err := parseProjectRef(c.in, "octo")
		if err != nil || owner != c.owner || number != c.number {
			t.Errorf("parseProjectRef(%q) = %q, %d, %v; want %q, %d", c.in, owner, number, err, c.owner, c.number)
		}
	}
	for _, bad := range []string{"", "x", "0", "-1", "acme/", "/3", "a/b/3"} {
		if _, _, err := parseProjectRef(bad, "octo"); err == nil {
			t.Errorf("parseProjectRef(%q) error = nil, want error", bad)
		}
	}
}

func TestGraphQLURL(t *testing.T) {
	for base, want := range map[string]string{
		"https://api.github.com/":         "https://api.github.com/graphql",
		"https://ghe.example.com/api/v3/": "https://ghe.example.com/api/graphql",
		"http://127.0.0.1:1234/":          "http://127.0.0.1:1234/graphql",
	} {
		if got, err := graphqlURL(base); err != nil || got != want {
			t.Errorf("graphqlURL(%q) = %q, %v; want %q", base, got, err, want)
		}
	}
}

// The project option is accepted from YAML as a bare number (an int
// scalar) as well as "owner/number", and over an Enterprise baseUrl
// GraphQL goes to /api/graphql.
func TestProjectOptionFromYAML(t *testing.T) {
	fake := newFakeGitHub(testOwner, testRepo)
	defer fake.Close()
	fake.SetProject(testOwner, 3, "Todo", "Done")
	t.Setenv("GITHUB_TOKEN", "test")

	for _, opt := range []string{"project: 3\n", "project: " + testOwner + "/3\n"} {
		p, err := New(context.Background(), provider.Config{
			Name:       "github",
			Repository: testOwner + "/" + testRepo,
			Statuses:   projectStatuses,
			Options:    yamlOptionsNode(t, "baseUrl: "+fake.URL()+"\n"+opt),
		})
		if err != nil {
			t.Fatalf("New(%q) error = %v", opt, err)
		}
		if !provider.UsesStatusField(p) {
			t.Errorf("New(%q): UsesStatusField = false, want true", opt)
		}
	}
	if n := fake.RawRequestCount("/api/graphql"); n == 0 {
		t.Errorf("no GraphQL requests at /api/graphql")
	}
}

func TestLabelsModeRequiresLabels(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test")
	_, err := New(context.Background(), provider.Config{
		Name:       "github",
		Repository: testOwner + "/" + testRepo,
		Statuses:   []domain.Status{{Key: "todo", Heading: "Todo"}},
	})
	if err == nil || !strings.Contains(err.Error(), "label is required") {
		t.Errorf("New() without project or labels error = %v, want label is required", err)
	}
}

func TestStatusWithoutProjectRejected(t *testing.T) {
	fake := newFakeGitHub(testOwner, testRepo)
	defer fake.Close()
	fake.Seed(&fakeIssue{Number: 1, Title: "t", State: "open", UpdatedAt: time.Now()})
	p, err := newWithClient(fake.Client(), fake.URL()+"/", testOwner+"/"+testRepo)
	if err != nil {
		t.Fatal(err)
	}
	todo := "Todo"
	if _, err := p.UpdateIssue(context.Background(), 1, provider.IssueUpdate{Status: &todo}); err == nil {
		t.Errorf("UpdateIssue(Status) without a project = nil error, want an error")
	}
	if provider.UsesStatusField(p) {
		t.Errorf("UsesStatusField = true without a project")
	}
}
