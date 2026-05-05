package main

import (
	"flag"
	"testing"
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
