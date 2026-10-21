package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	registryMu sync.Mutex
	registry   = map[string]Factory{}
)

// Register registers f as the factory for provider name, so New can
// later construct it. A provider package calls this from its own
// init(). Register panics if name is already registered or f is nil.
func Register(name string, f Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()

	if f == nil {
		panic(fmt.Sprintf("provider: Register(%q, nil)", name))
	}
	if _, ok := registry[name]; ok {
		panic(fmt.Sprintf("provider: Register called twice for %q", name))
	}
	registry[name] = f
}

// New constructs the provider registered under cfg.Name. It returns
// an error naming the registered providers if cfg.Name isn't one of
// them.
func New(ctx context.Context, cfg Config) (IssueProvider, error) {
	registryMu.Lock()
	f, ok := registry[cfg.Name]
	registryMu.Unlock()

	if !ok {
		return nil, fmt.Errorf("unknown provider %q (available: %s)", cfg.Name, formatAvailable(Names()))
	}
	return f(ctx, cfg)
}

// Names returns the names of all registered providers, sorted.
func Names() []string {
	registryMu.Lock()
	defer registryMu.Unlock()

	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// formatAvailable renders names (as returned by Names, but any order
// is accepted) for the "unknown provider" error message: comma
// separated, or "none" if there are none.
func formatAvailable(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
