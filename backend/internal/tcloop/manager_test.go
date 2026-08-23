package tcloop

import (
	"strings"
	"testing"
)

func TestBuildDrawtextQuotedStrftime(t *testing.T) {
	got := buildDrawtext(Settings{FontSize: 48, Opacity: 0.9, Position: PosTopLeft})
	if !strings.Contains(got, "expansion=strftime") {
		t.Fatalf("missing strftime expansion: %s", got)
	}
	if !strings.Contains(got, "text='%H:%M:%S'") {
		t.Fatalf("expected quoted HH:MM:SS without backslash escapes, got: %s", got)
	}
	if strings.Contains(got, `\:`) {
		t.Fatalf("backslash-colon should not appear inside quoted text: %s", got)
	}
}
