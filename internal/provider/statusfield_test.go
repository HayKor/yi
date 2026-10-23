package provider

import (
	"strings"
	"testing"

	"github.com/HayKor/yi/internal/domain"
)

func TestRequireLabels(t *testing.T) {
	ok := []domain.Status{{Key: "todo", Label: "status:todo"}, {Key: "done", Label: "status:done"}}
	if err := RequireLabels(ok); err != nil {
		t.Errorf("RequireLabels(all labeled) = %v, want nil", err)
	}
	missing := []domain.Status{{Key: "todo"}, {Key: "doing", Label: "status:doing"}, {Key: "done"}}
	err := RequireLabels(missing)
	if err == nil || !strings.Contains(err.Error(), "statuses todo, done: label is required") {
		t.Errorf("RequireLabels(two unlabeled) = %v, want both keys named", err)
	}
}

type fielder struct {
	IssueProvider
	on bool
}

func (f fielder) StatusField() bool { return f.on }

func TestUsesStatusField(t *testing.T) {
	if UsesStatusField(fielder{on: false}) || !UsesStatusField(fielder{on: true}) {
		t.Errorf("UsesStatusField should follow StatusField()")
	}
	var plain IssueProvider = struct{ IssueProvider }{}
	if UsesStatusField(plain) {
		t.Errorf("UsesStatusField(provider without StatusField) = true, want false")
	}
}
