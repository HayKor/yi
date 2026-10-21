package branch

import "testing"

func TestSlug(t *testing.T) {
	cases := []struct {
		name  string
		title string
		want  string
	}{
		{"ascii words", "Yandex OAuth", "yandex-oauth"},
		{"cyrillic phrase", "Добавить preview generated creative", "dobavit-preview-generated-creative"},
		{"shch and yo", "Щука и ёж", "shchuka-i-yozh"},
		{"hard sign dropped", "Объект", "obekt"},
		{"punctuation collapsed", "Fix: crash!!  (urgent)", "fix-crash-urgent"},
		{"all punctuation", "!!!", ""},
		{
			"long input cut at word boundary",
			"aaaa bbbb cccc dddd eeee ffff gggg hhhh iiii jjjj kkkk llll mmmm nnnn oooo",
			"aaaa-bbbb-cccc-dddd-eeee-ffff-gggg-hhhh-iiii-jjjj",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Slug(c.title)
			if got != c.want {
				t.Fatalf("Slug(%q) = %q, want %q", c.title, got, c.want)
			}
			if len(got) > 50 {
				t.Fatalf("Slug(%q) = %q, length %d > 50", c.title, got, len(got))
			}
			if len(got) > 0 && got[len(got)-1] == '-' {
				t.Fatalf("Slug(%q) = %q, ends with '-'", c.title, got)
			}
		})
	}
}

func TestName(t *testing.T) {
	cases := []struct {
		name    string
		tmpl    string
		typ     string
		id      int
		title   string
		want    string
		wantErr bool
	}{
		{"type issue slug", "{type}/{issue}-{slug}", "feat", 42, "Yandex OAuth", "feat/42-yandex-oauth", false},
		{"empty slug trims tail", "{type}/{issue}-{slug}", "fix", 7, "!!!", "fix/7", false},
		{"issue only", "{issue}", "feat", 3, "x", "3", false},
		{"unknown placeholder errors", "{type}/{nope}", "feat", 1, "x", "", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Name(c.tmpl, c.typ, c.id, c.title)
			if c.wantErr {
				if err == nil {
					t.Fatalf("Name(%q, ...) = %q, nil, want error", c.tmpl, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Name(%q, ...) returned error: %v", c.tmpl, err)
			}
			if got != c.want {
				t.Fatalf("Name(%q, %q, %d, %q) = %q, want %q", c.tmpl, c.typ, c.id, c.title, got, c.want)
			}
		})
	}
}
