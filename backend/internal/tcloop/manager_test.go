package tcloop

import (
	"strings"
	"testing"
)

func TestBuildDrawtextUsesTextfile(t *testing.T) {
	got := buildDrawtext(Settings{FontSize: 48, Opacity: 0.9, Position: PosTopLeft}, "/tmp/roc-tcloop-1-tod.txt")
	if !strings.Contains(got, "textfile=/tmp/roc-tcloop-1-tod.txt") {
		t.Fatalf("expected textfile path, got: %s", got)
	}
	if !strings.Contains(got, "reload=1") {
		t.Fatalf("expected reload=1, got: %s", got)
	}
	if strings.Contains(got, "%H") || strings.Contains(got, "localtime") {
		t.Fatalf("should not embed clock format in filtergraph: %s", got)
	}
}

func TestEscapeFilterPath(t *testing.T) {
	got := escapeFilterPath(`C:\tmp:x`)
	if !strings.Contains(got, `\:`) {
		t.Fatalf("expected escaped colon, got %q", got)
	}
}

func TestSummarizeFFmpegErrShortOutput(t *testing.T) {
	// One informational line (no "error"/"failed" keywords) must not panic.
	lines := []string{"[aist#0:0/pcm_s16le] Guessed Channel Layout: stereo"}
	got := summarizeFFmpegErr(lines)
	if got != lines[0] {
		t.Fatalf("expected single line summary, got %q", got)
	}
}

func TestSummarizeFFmpegErrTail(t *testing.T) {
	lines := []string{"line1", "line2", "line3", "line4", "error: boom"}
	got := summarizeFFmpegErr(lines)
	if got != "error: boom" {
		t.Fatalf("expected error line, got %q", got)
	}
	got = summarizeFFmpegErr([]string{"only"})
	if got != "only" {
		t.Fatalf("expected fallback to sole line, got %q", got)
	}
}
