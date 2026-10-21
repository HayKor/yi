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
