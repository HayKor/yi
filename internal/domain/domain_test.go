package domain

import "testing"

func TestParseIssueID(t *testing.T) {
	ok := map[string]int{"42": 42, "#42": 42, " #7 ": 7}
	for in, want := range ok {
		got, err := ParseIssueID(in)
		if err != nil {
			t.Errorf("ParseIssueID(%q) returned error %v, want nil", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseIssueID(%q) = %d, want %d", in, got, want)
		}
	}

	for _, bad := range []string{"", "#", "0", "-1", "abc", "#4x"} {
		if _, err := ParseIssueID(bad); err == nil {
			t.Errorf("ParseIssueID(%q) returned nil error, want error", bad)
		}
	}
}

func TestIssueStateString(t *testing.T) {
	if got := IssueOpen.String(); got != "open" {
		t.Errorf("IssueOpen.String() = %q, want %q", got, "open")
	}
	if got := IssueClosed.String(); got != "closed" {
		t.Errorf("IssueClosed.String() = %q, want %q", got, "closed")
	}
}

func TestIssueHasLabel(t *testing.T) {
	issue := Issue{Labels: []string{"bug", "status:doing"}}
	if !issue.HasLabel("bug") {
		t.Error("HasLabel(\"bug\") = false, want true")
	}
	if issue.HasLabel("missing") {
		t.Error("HasLabel(\"missing\") = true, want false")
	}
}

func TestIssueHasLabelIgnoresCase(t *testing.T) {
	issue := Issue{Labels: []string{"bug", "Status:Todo"}}
	if !issue.HasLabel("status:todo") {
		t.Error(`HasLabel("status:todo") on "Status:Todo" = false, want true`)
	}
	got, ok := issue.FindLabel("STATUS:todo")
	if !ok || got != "Status:Todo" {
		t.Errorf(`FindLabel("STATUS:todo") = %q, %v, want "Status:Todo", true`, got, ok)
	}
	if _, ok := issue.FindLabel("missing"); ok {
		t.Error(`FindLabel("missing") found, want not found`)
	}
}

func TestNormalizeHeading(t *testing.T) {
	cases := map[string]string{
		"Todo":              "todo",
		"  In Progress  ":   "in progress",
		"In Progress (3)":   "in progress",
		"In Progress(12) ":  "in progress",
		"DONE":              "done",
		"Release (v2) work": "release (v2) work",
	}
	for in, want := range cases {
		if got := NormalizeHeading(in); got != want {
			t.Errorf("NormalizeHeading(%q) = %q, want %q", in, got, want)
		}
	}
	if NormalizeHeading("Todo") != NormalizeHeading("todo") {
		t.Error("NormalizeHeading must fold case")
	}
}
