package commentator

import (
	"testing"
)

func TestControlsPath(t *testing.T) {
	got := controlsPath("abc123")
	want := "/ws/commentator/abc123/controls"
	if got != want {
		t.Fatalf("controlsPath = %q, want %q", got, want)
	}
}
