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
