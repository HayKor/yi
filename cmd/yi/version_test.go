package main

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	info := func(v string, settings ...debug.BuildSetting) *debug.BuildInfo {
		return &debug.BuildInfo{Main: debug.Module{Version: v}, Settings: settings}
	}
	rev := debug.BuildSetting{Key: "vcs.revision", Value: "e9f75161234567890"}
	dirty := debug.BuildSetting{Key: "vcs.modified", Value: "true"}

	tests := []struct {
		name   string
		ldflag string
		info   *debug.BuildInfo
		ok     bool
		want   string
	}{
		{"ldflags win (goreleaser)", "0.0.1", info("v9.9.9"), true, "0.0.1"},
		{"ldflags v prefix stripped", "v0.0.1", nil, false, "0.0.1"},
		{"go install @v0.0.1", "", info("v0.0.1"), true, "0.0.1"},
		{"go build between tags", "", info("v0.0.2-0.20261022070000-e9f751612345+dirty"), true, "0.0.2-0.20261022070000-e9f751612345+dirty"},
		{"devel with vcs info", "", info("(devel)", rev), true, "devel-e9f7516"},
		{"devel dirty", "", info("(devel)", rev, dirty), true, "devel-e9f7516-dirty"},
		{"devel without vcs info", "", info("(devel)"), true, "devel"},
		{"no build info", "", nil, false, "devel"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveVersion(tt.ldflag, tt.info, tt.ok); got != tt.want {
				t.Errorf("resolveVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}
