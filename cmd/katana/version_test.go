package main

import (
	"runtime/debug"
	"testing"
)

func TestVersionString(t *testing.T) {
	if version != "dev" {
		t.Fatalf("default version = %q, want dev", version)
	}
	if got := versionString(version, "2026-11-26T00:00:00Z"); got != "dev (commit 2026-11-26T00:00:00Z)" {
		t.Fatalf("versionString() = %q", got)
	}

	// The linker replaces this package variable in release builds.
	original := version
	version = "v1.2.3"
	defer func() { version = original }()
	if got := versionString(version, "unknown"); got != "v1.2.3 (commit unknown)" {
		t.Fatalf("injected version output = %q", got)
	}
}

func TestCommitTimeFromSettings(t *testing.T) {
	cases := []struct {
		name     string
		settings []debug.BuildSetting
		want     string
	}{
		{name: "present", settings: []debug.BuildSetting{{Key: "vcs.time", Value: "2026-11-26T00:00:00Z"}}, want: "2026-11-26T00:00:00Z"},
		{name: "missing", settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}}, want: "unknown"},
		{name: "empty", settings: []debug.BuildSetting{{Key: "vcs.time", Value: ""}}, want: "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := commitTimeFromSettings(tc.settings); got != tc.want {
				t.Fatalf("commitTimeFromSettings() = %q, want %q", got, tc.want)
			}
		})
	}
}
