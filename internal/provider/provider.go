// Package provider defines the interface yi uses to talk to an issue
// tracker (GitHub, GitLab, ...), plus a registry so a provider
// package can register itself without the core depending on it
// directly (see spec §6.1, §12). It depends only on domain.
package provider

import (
	"context"
	"errors"

	"gopkg.in/yaml.v3"

	"kanban-sync/internal/domain"
)

// IssueProvider talks to an issue tracker on behalf of yi.
type IssueProvider interface {
	// ListOpenIssues returns only open issues; pull requests are
	// filtered out. Pagination happens internally.
	ListOpenIssues(ctx context.Context) ([]domain.Issue, error)
	// GetIssue returns an issue in any state. It returns an error
	// wrapping ErrNotFound if the issue doesn't exist, was deleted, or
	// was moved.
	GetIssue(ctx context.Context, id int) (domain.Issue, error)
	// CreateIssue creates a new issue.
	CreateIssue(ctx context.Context, in NewIssue) (domain.Issue, error)
	// UpdateIssue applies only the fields set in upd and returns the
	// resulting issue state.
	UpdateIssue(ctx context.Context, id int, upd IssueUpdate) (domain.Issue, error)
}

// NewIssue is the input to IssueProvider.CreateIssue.
type NewIssue struct {
	Title  string
	Body   string
	Labels []string
}

// IssueUpdate is the input to IssueProvider.UpdateIssue. Only
// non-nil/non-empty fields are applied; the rest are left alone.
type IssueUpdate struct {
	Title        *string
	AddLabels    []string
	RemoveLabels []string
	State        *domain.IssueState
}

// Config configures a provider instance, as read from a project's
// .issue-sync.yml.
type Config struct {
	// Name is the provider name, e.g. "github".
	Name string
	// Repository is the "owner/repo" (or equivalent) identifier.
	Repository string
	// Options is the raw provider-specific config section (the
	// "github:"/"gitlab:" mapping); the provider decodes it itself.
	Options yaml.Node
}

// Factory constructs an IssueProvider from a Config. A provider
// package calls Register with its Factory from its own init().
type Factory func(ctx context.Context, cfg Config) (IssueProvider, error)

var (
	// ErrNotFound indicates an issue doesn't exist, was deleted, or
	// was moved.
	ErrNotFound = errors.New("issue not found")
	// ErrAuth indicates the provider failed to authenticate.
	ErrAuth = errors.New("authentication failed")
)
