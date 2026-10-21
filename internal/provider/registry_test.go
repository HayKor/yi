package provider

import (
	"context"
	"strings"
	"testing"
)

func noopFactory(ctx context.Context, cfg Config) (IssueProvider, error) {
	return nil, nil
}

func TestRegisterAndNew(t *testing.T) {
	var gotCfg Config
	Register("rt-echo", func(ctx context.Context, cfg Config) (IssueProvider, error) {
		gotCfg = cfg
		return nil, nil
	})

	_, err := New(context.Background(), Config{Name: "rt-echo", Repository: "owner/repo"})
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	if gotCfg.Repository != "owner/repo" {
		t.Errorf("factory got Repository = %q, want %q", gotCfg.Repository, "owner/repo")
	}
}

func TestNewUnknownProvider(t *testing.T) {
	Register("rt-known", noopFactory)

	_, err := New(context.Background(), Config{Name: "nope"})
	if err == nil {
		t.Fatal("New() error = nil, want error")
	}
	if !strings.Contains(err.Error(), `unknown provider "nope"`) {
		t.Errorf("New() error = %q, want to contain `unknown provider \"nope\"`", err.Error())
	}
	if !strings.Contains(err.Error(), "rt-known") {
		t.Errorf("New() error = %q, want to list registered provider names", err.Error())
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	Register("rt-dup", noopFactory)

	defer func() {
		if recover() == nil {
			t.Fatal("Register() did not panic on duplicate name")
		}
	}()
	Register("rt-dup", noopFactory)
}

func TestRegisterNilFactoryPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Register() did not panic on nil factory")
		}
	}()
	Register("rt-nil", nil)
}

func TestNamesSorted(t *testing.T) {
	Register("rt-zebra", noopFactory)
	Register("rt-alpha", noopFactory)

	names := Names()
	zi, ai := -1, -1
	for i, n := range names {
		if n == "rt-zebra" {
			zi = i
		}
		if n == "rt-alpha" {
			ai = i
		}
	}
	if zi == -1 || ai == -1 {
		t.Fatalf("Names() = %v, missing registered names", names)
	}
	if ai > zi {
		t.Errorf("Names() = %v, not sorted", names)
	}
}

func TestFormatAvailable(t *testing.T) {
	if got := formatAvailable(nil); got != "none" {
		t.Errorf("formatAvailable(nil) = %q, want %q", got, "none")
	}
	if got := formatAvailable([]string{"b", "a"}); got != "b, a" {
		t.Errorf("formatAvailable([b,a]) = %q, want %q", got, "b, a")
	}
}
