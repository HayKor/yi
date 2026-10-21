package branch

import (
	"regexp"
	"strconv"
	"strings"
)

// maxSlugLen is the maximum length of a slug produced by Slug.
const maxSlugLen = 50

// dashRun matches one or more consecutive '-' characters.
var dashRun = regexp.MustCompile(`-+`)

// Slug turns an issue title into a branch-safe slug: lowercase, Cyrillic
// transliterated per §6.8, every other non [a-z0-9] rune collapsed to '-',
// trimmed, and cut to at most maxSlugLen characters at a '-' boundary.
func Slug(title string) string {
	lower := strings.ToLower(title)

	var sb strings.Builder
	for _, r := range lower {
		if repl, ok := cyrillicTranslit[r]; ok {
			sb.WriteString(repl)
			continue
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			sb.WriteRune(r)
			continue
		}
		sb.WriteByte('-')
	}

	s := dashRun.ReplaceAllString(sb.String(), "-")
	s = strings.Trim(s, "-")

	if len(s) > maxSlugLen {
		cut := s[:maxSlugLen]
		if idx := strings.LastIndex(cut, "-"); idx >= 0 {
			cut = cut[:idx]
		}
		s = strings.Trim(cut, "-")
	}

	return s
}

// Name renders a branch template with typ, id, and a slug derived from
// title, then cleans up separators left behind by an empty slug.
func Name(template, typ string, id int, title string) (string, error) {
	if err := ValidateTemplate(template); err != nil {
		return "", err
	}

	slug := Slug(title)

	name := template
	name = strings.ReplaceAll(name, "{type}", typ)
	name = strings.ReplaceAll(name, "{issue}", strconv.Itoa(id))
	name = strings.ReplaceAll(name, "{slug}", slug)

	return cleanSeparators(name), nil
}

// cleanSeparators removes separator artifacts left by an empty slug: a
// '/' directly next to a leftover '-' collapses to '/', runs of '-'
// collapse to one, and leading/trailing '-' and '/' are trimmed.
func cleanSeparators(s string) string {
	s = strings.ReplaceAll(s, "/-", "/")
	s = strings.ReplaceAll(s, "-/", "/")
	s = dashRun.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-/")
	return s
}
