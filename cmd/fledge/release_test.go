package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The publish action documents that its ipa input accepts a glob, and it hands
// that input straight to `fledge upload` as an argument. An argument taken
// literally fails with "no such file" naming the pattern itself.
func TestSoleMatchExpandsAPattern(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "CooperTheCop.ipa")
	if err := os.WriteFile(archive, []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := soleMatch(filepath.Join(dir, "*.ipa"))
	if err != nil {
		t.Fatalf("soleMatch() error = %v", err)
	}
	if got != archive {
		t.Errorf("soleMatch() = %q, want %q", got, archive)
	}
}

func TestSoleMatchAcceptsAnExactPath(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "CooperWatch.ipa")
	if err := os.WriteFile(archive, []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := soleMatch(archive)
	if err != nil {
		t.Fatalf("soleMatch() error = %v", err)
	}
	if got != archive {
		t.Errorf("soleMatch() = %q, want %q", got, archive)
	}
}

// Refusing to guess matters more than convenience here: publishing the wrong
// archive is only noticed once it is on a device.
func TestSoleMatchRefusesAnythingButOneFile(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"One.ipa", "Two.ipa"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("archive"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name    string
		pattern string
		want    string
	}{
		{name: "several", pattern: filepath.Join(dir, "*.ipa"), want: "matched 2 files"},
		{name: "none", pattern: filepath.Join(dir, "missing.ipa"), want: "no archive matched"},
		{name: "empty", pattern: "", want: "usage:"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := soleMatch(tt.pattern)
			if err == nil {
				t.Fatalf("soleMatch(%q) = nil error", tt.pattern)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("soleMatch() error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}
