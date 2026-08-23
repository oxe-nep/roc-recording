package tcloop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeTimecodeStripsFrames(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"12:34:56:12", "12:34:56"},
		{"12:34:56;12", "12:34:56"},
		{"12:34:56", "12:34:56"},
		{"  01:02:03:04  ", "01:02:03"},
		{"--:--:--:--", ""},
		{"bad", ""},
		{"12:34:56%{pts}", ""},
	}
	for _, tt := range tests {
		got := normalizeTimecode(tt.in)
		if got != tt.want {
			t.Fatalf("normalizeTimecode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestWriteClockFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tc.txt")
	if err := writeClockFile(path, "01:02:03"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "01:02:03" {
		t.Fatalf("got %q", got)
	}
	if err := writeClockFile(path, "02:03:04"); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "02:03:04" {
		t.Fatalf("got %q", got)
	}
}
