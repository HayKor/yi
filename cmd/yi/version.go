package main

import (
	"runtime/debug"
	"strings"
)

// version is set at release time via -ldflags "-X main.version=...".
// When empty, the version comes from the Go build info instead.
var version = ""

// resolveVersion picks the version yi reports, without a leading "v":
//   - the -ldflags value (goreleaser release builds);
//   - else the main module version Go records: the tag for
//     `go install …@v0.0.1`, a pseudo-version for `go build` between tags;
//   - else "devel", plus the short commit and "-dirty" when Go recorded them.
func resolveVersion(ldflag string, info *debug.BuildInfo, ok bool) string {
	if ldflag != "" {
		return strings.TrimPrefix(ldflag, "v")
	}
	if !ok || info == nil {
		return "devel"
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return strings.TrimPrefix(v, "v")
	}

	out := "devel"
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) > 7 {
				out += "-" + s.Value[:7]
			} else if s.Value != "" {
				out += "-" + s.Value
			}
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if dirty {
		out += "-dirty"
	}
	return out
}
