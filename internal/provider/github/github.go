// Package github implements provider.IssueProvider against the
// GitHub REST API, via google/go-github (spec §6.1 GitHub). It
// depends only on domain and provider; only cmd/yi imports this
// package (blank-import, for its init-time Register call).
package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	gh "github.com/google/go-github/v93/github"
	"gopkg.in/yaml.v3"

	"github.com/HayKor/yi/internal/domain"
	"github.com/HayKor/yi/internal/provider"
)

func init() {
	provider.Register("github", New)
}

// Provider implements provider.IssueProvider against a single GitHub
// repository.
type Provider struct {
	client *gh.Client
	owner  string
	repo   string
}

var _ provider.IssueProvider = (*Provider)(nil)

// options is the provider-specific "github:" config section (spec
// §6.1 GitHub).
type options struct {
	BaseURL string `yaml:"baseUrl"`
}

// knownOptionKeys lists every recognized key in the "github:" options
// section. A misspelt key (e.g. "baseURL" instead of "baseUrl") would
// otherwise decode to a silently-empty options struct, sending the
// token to the public API instead of the intended Enterprise host, so
// decodeOptions rejects anything not in this list instead of ignoring
// it. yaml.v3's Node.Decode has no equivalent of Decoder.KnownFields,
// so the mapping's keys are checked by hand.
var knownOptionKeys = map[string]bool{"baseUrl": true}

// decodeOptions decodes node into options, per knownOptionKeys.
func decodeOptions(node yaml.Node) (options, error) {
	var opts options
	if node.IsZero() {
		return opts, nil
	}

	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i].Value
			if !knownOptionKeys[key] {
				return options{}, fmt.Errorf("github: unknown option %q (known: baseUrl)", key)
			}
		}
	}

	if err := node.Decode(&opts); err != nil {
		return options{}, fmt.Errorf("github: decode options: %w", err)
	}
	return opts, nil
}

// New constructs a Provider from cfg. cfg.Repository must be
// "owner/repo". The GitHub token is resolved via resolveToken and is
// never persisted. If cfg.Options sets baseUrl, requests go to that
// GitHub Enterprise instance instead of the public API.
func New(ctx context.Context, cfg provider.Config) (provider.IssueProvider, error) {
	owner, repo, err := splitRepository(cfg.Repository)
	if err != nil {
		return nil, err
	}

	opts, err := decodeOptions(cfg.Options)
	if err != nil {
		return nil, err
	}

	host, err := tokenHost(opts.BaseURL)
	if err != nil {
		return nil, err
	}
	token, err := resolveToken(ctx, host, os.Getenv, exec.LookPath, runCommand)
	if err != nil {
		return nil, err
	}

	clientOpts := []gh.ClientOptionsFunc{gh.WithAuthToken(token)}
	if opts.BaseURL != "" {
		clientOpts = append(clientOpts, gh.WithEnterpriseURLs(opts.BaseURL, opts.BaseURL))
	}
	client, err := gh.NewClient(clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("github: new client: %w", err)
	}

	return &Provider{client: client, owner: owner, repo: repo}, nil
}

// newWithClient builds a Provider that sends requests to baseURL
// (which must end with "/") using hc, without go-github's GitHub
// Enterprise path suffixing (unlike New with a baseUrl option). Used
// by tests to point at an httptest.Server.
func newWithClient(hc *http.Client, baseURL, repo string) (*Provider, error) {
	owner, name, err := splitRepository(repo)
	if err != nil {
		return nil, err
	}

	client, err := gh.NewClient(gh.WithHTTPClient(hc), gh.WithURLs(&baseURL, &baseURL))
	if err != nil {
		return nil, fmt.Errorf("github: new client: %w", err)
	}

	return &Provider{client: client, owner: owner, repo: name}, nil
}

// splitRepository parses "owner/repo" into its two parts, requiring
// exactly one "/" and both parts non-empty.
func splitRepository(repo string) (owner, name string, err error) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("github: repository must be in owner/repo form, got %q", repo)
	}
	return parts[0], parts[1], nil
}

// ListOpenIssues returns every open issue, pull requests excluded,
// paginating internally.
func (p *Provider) ListOpenIssues(ctx context.Context) ([]domain.Issue, error) {
	var all []domain.Issue

	opt := &gh.IssueListByRepoOptions{
		State:       "open",
		ListOptions: gh.ListOptions{PerPage: 100},
	}
	for {
		issues, resp, err := p.client.Issues.ListByRepo(ctx, p.owner, p.repo, opt)
		if err != nil {
			return nil, p.repoErr("list open issues", resp, err)
		}
		for _, iss := range issues {
			if iss.PullRequestLinks != nil {
				continue
			}
			all = append(all, toDomainIssue(iss))
		}
		if resp.NextPage == 0 {
			break
		}
		opt.ListOptions.Page = resp.NextPage
	}

	return all, nil
}

// GetIssue returns the issue with id, in any state. GitHub serves pull
// requests from the same endpoint; one is reported as not found (an
// error wrapping provider.ErrNotFound), so yi never relabels or closes
// a PR whose number happens to be on the board.
func (p *Provider) GetIssue(ctx context.Context, id int) (domain.Issue, error) {
	op := fmt.Sprintf("get issue #%d", id)
	iss, resp, err := p.client.Issues.Get(ctx, p.owner, p.repo, id)
	if err != nil {
		return domain.Issue{}, mapErr(op, resp, err)
	}
	if iss.PullRequestLinks != nil {
		return domain.Issue{}, fmt.Errorf("github: %s: #%d is a pull request: %w", op, id, provider.ErrNotFound)
	}
	return toDomainIssue(iss), nil
}

// CreateIssue creates a new issue.
func (p *Provider) CreateIssue(ctx context.Context, in provider.NewIssue) (domain.Issue, error) {
	req := gh.CreateIssueRequest{
		Title:  in.Title,
		Labels: in.Labels,
	}
	if in.Body != "" {
		req.Body = &in.Body
	}

	iss, resp, err := p.client.Issues.Create(ctx, p.owner, p.repo, req)
	if err != nil {
		return domain.Issue{}, p.repoErr("create issue", resp, err)
	}
	return toDomainIssue(iss), nil
}

// UpdateIssue applies only the fields set in upd: title/state via a
// single Issues.Update call, then AddLabels via AddLabelsToIssue,
// then each RemoveLabels entry via RemoveLabelForIssue (a 404 there
// means the label was already gone, which is not an error). It
// always finishes with a fresh GetIssue so the caller sees the
// current state, even if there was nothing to change.
func (p *Provider) UpdateIssue(ctx context.Context, id int, upd provider.IssueUpdate) (domain.Issue, error) {
	op := fmt.Sprintf("update issue #%d", id)

	if upd.Title != nil || upd.State != nil {
		req := gh.UpdateIssueRequest{Title: upd.Title}
		if upd.State != nil {
			state := "open"
			if *upd.State == domain.IssueClosed {
				state = "closed"
			}
			req.State = &state
		}
		if _, resp, err := p.client.Issues.Update(ctx, p.owner, p.repo, id, req); err != nil {
			return domain.Issue{}, mapErr(op, resp, err)
		}
	}

	if len(upd.AddLabels) > 0 {
		if _, resp, err := p.client.Issues.AddLabelsToIssue(ctx, p.owner, p.repo, id, upd.AddLabels); err != nil {
			return domain.Issue{}, mapErr(op, resp, err)
		}
	}

	for _, label := range upd.RemoveLabels {
		// go-github does not escape label for us (it's interpolated
		// directly into the URL path - see issues_labels.go), so a
		// label containing "/", "#" or "?" would otherwise split the
		// path, get truncated at a fragment, or turn into a query
		// string. url.PathEscape round-trips cleanly through
		// Client.NewRequest (verified against v93: the escaped
		// segment survives as the request's RawPath/EscapedPath
		// without being re-escaped).
		resp, err := p.client.Issues.RemoveLabelForIssue(ctx, p.owner, p.repo, id, url.PathEscape(label))
		if err != nil && !isNotFound(resp) {
			return domain.Issue{}, mapErr(op, resp, err)
		}
	}

	return p.GetIssue(ctx, id)
}

// isNotFound reports whether resp carries a 404, so UpdateIssue can
// ignore RemoveLabelForIssue failing because the label was already
// absent.
func isNotFound(resp *gh.Response) bool {
	return resp != nil && resp.Response != nil && resp.StatusCode == http.StatusNotFound
}

// toDomainIssue converts a go-github Issue into a domain.Issue, per
// spec §6.1 GitHub: Number->ID, Title, State "closed"->IssueClosed
// (else open), label names, HTMLURL->URL, UpdatedAt.
func toDomainIssue(iss *gh.Issue) domain.Issue {
	state := domain.IssueOpen
	if iss.GetState() == "closed" {
		state = domain.IssueClosed
	}

	labels := make([]string, 0, len(iss.Labels))
	for _, l := range iss.Labels {
		labels = append(labels, l.GetName())
	}

	return domain.Issue{
		ID:        iss.GetNumber(),
		Title:     iss.GetTitle(),
		State:     state,
		Labels:    labels,
		URL:       iss.GetHTMLURL(),
		UpdatedAt: iss.GetUpdatedAt().Time,
	}
}

// repoErr is mapErr for requests on the repository itself (listing,
// creating issues), where a 404 means the repository doesn't exist or
// the token can't see it, rather than a missing issue.
func (p *Provider) repoErr(op string, resp *gh.Response, err error) error {
	if isNotFound(resp) {
		return fmt.Errorf("github: %s: %w: %s/%s (check \"repository\" in .issue-sync.yml and that your token can access it)",
			op, provider.ErrRepoNotFound, p.owner, p.repo)
	}
	return mapErr(op, resp, err)
}

// mapErr translates a go-github error into yi's provider error
// vocabulary, prefixed with op for context. Rate limit errors are
// checked first (they arrive as 403/429 too, so status-code checks
// must not shadow them): *gh.RateLimitError and *gh.AbuseRateLimitError
// both become a fixed "resets at" message, without the op prefix,
// per spec §8. Otherwise 401/403 wrap provider.ErrAuth, 404/410 wrap
// provider.ErrNotFound, and anything else is just wrapped with op.
func mapErr(op string, resp *gh.Response, err error) error {
	if err == nil {
		return nil
	}

	var rateErr *gh.RateLimitError
	if errors.As(err, &rateErr) {
		return fmt.Errorf("github: rate limit exceeded, resets at %s", rateErr.Rate.Reset.Time.Format(time.RFC3339))
	}
	var abuseErr *gh.AbuseRateLimitError
	if errors.As(err, &abuseErr) {
		resetAt := time.Now()
		if abuseErr.RetryAfter != nil {
			resetAt = resetAt.Add(*abuseErr.RetryAfter)
		}
		return fmt.Errorf("github: rate limit exceeded, resets at %s", resetAt.Format(time.RFC3339))
	}

	status := 0
	if resp != nil && resp.Response != nil {
		status = resp.StatusCode
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("github: %s: %w: %w", op, provider.ErrAuth, err)
	case http.StatusNotFound, http.StatusGone:
		return fmt.Errorf("github: %s: %w: %w", op, provider.ErrNotFound, err)
	default:
		return fmt.Errorf("github: %s: %w", op, err)
	}
}
