package main

import (
	"flag"
	"testing"
	"time"

	"github.com/yashau/nvrclip/internal/config"
)

func TestResolveModeFlagUsesFormatAlias(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	mode := fs.String("mode", "copy", "")
	format := fs.String("format", "", "")
	if err := fs.Parse([]string{"--format", "exact"}); err != nil {
		t.Fatal(err)
	}
	got, err := resolveModeFlag(fs, *mode, *format)
	if err != nil {
		t.Fatal(err)
	}
	if got != "exact" {
		t.Fatalf("mode = %q, want exact", got)
	}
}

func TestResolveTimeRangeAround(t *testing.T) {
	from, to, err := resolveTimeRange("", "", "2026-05-20 14:00", 120)
	if err != nil {
		t.Fatal(err)
	}
	wantFrom := time.Date(2026, 5, 20, 13, 0, 0, 0, time.Local)
	wantTo := time.Date(2026, 5, 20, 15, 0, 0, 0, time.Local)
	if !from.Equal(wantFrom) || !to.Equal(wantTo) {
		t.Fatalf("range = %s - %s, want %s - %s", from, to, wantFrom, wantTo)
	}
}

func TestResolveTimeRangeRejectsMixedForms(t *testing.T) {
	_, _, err := resolveTimeRange(
		"2026-05-20 13:00",
		"2026-05-20 15:00",
		"2026-05-20 14:00",
		120,
	)
	if err == nil {
		t.Fatal("expected mixed time forms to fail")
	}
}

func TestResolveModeFlagRejectsConflict(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	mode := fs.String("mode", "copy", "")
	format := fs.String("format", "", "")
	if err := fs.Parse([]string{"--mode", "copy", "--format", "exact"}); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveModeFlag(fs, *mode, *format); err == nil {
		t.Fatal("expected conflicting mode and format to fail")
	}
}

func TestResolveTimeOffset(t *testing.T) {
	off := false
	on := true

	tests := []struct {
		name         string
		noTimeOffset bool
		configured   *bool
		want         bool
	}{
		{name: "on when nothing is configured", want: true},
		{name: "on when the config asks for it", configured: &on, want: true},
		{name: "off when the config opts out", configured: &off, want: false},
		{name: "off when the flag opts out", noTimeOffset: true, want: false},
		{name: "flag beats a config that asks for it", noTimeOffset: true, configured: &on, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := resolveTimeOffset(test.noTimeOffset, config.NVR{AutoTimeOffset: test.configured})
			if got != test.want {
				t.Fatalf("resolveTimeOffset = %v, want %v", got, test.want)
			}
		})
	}
}
