package branch

import "testing"

func TestValidateTemplate(t *testing.T) {
	cases := []struct {
		name    string
		tmpl    string
		wantErr bool
	}{
		{"default template", DefaultTemplate, false},
		{"only type and issue", "{type}/{issue}", false},
		{"no placeholders", "release", false},
		{"unknown placeholder", "{type}/{issue}-{foo}", true},
		{"unknown placeholder only", "{bar}", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateTemplate(c.tmpl)
			if c.wantErr && err == nil {
				t.Fatalf("ValidateTemplate(%q) = nil, want error", c.tmpl)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("ValidateTemplate(%q) = %v, want nil", c.tmpl, err)
			}
		})
	}
}

func TestDefaults(t *testing.T) {
	if DefaultTemplate != "{type}/{issue}-{slug}" {
		t.Fatalf("DefaultTemplate = %q", DefaultTemplate)
	}
	if DefaultType != "feat" {
		t.Fatalf("DefaultType = %q", DefaultType)
	}
}
