package commentator

import "strings"

func TestDeckLinkVideoFilterInterlaced(t *testing.T) {
	got := deckLinkVideoFilter(1920, 1080, 25, true)
	if !strings.Contains(got, "tinterlace=interleave_top") {
		t.Fatalf("expected tinterlace in filter, got %q", got)
	}
	if !strings.Contains(got, "fps=50") {
		t.Fatalf("expected field rate fps=50, got %q", got)
	}
}

func TestDeckLinkVideoFilterProgressive(t *testing.T) {
	got := deckLinkVideoFilter(1920, 1080, 50, false)
	if strings.Contains(got, "tinterlace") {
		t.Fatalf("unexpected tinterlace in progressive filter: %q", got)
	}
	if !strings.Contains(got, "fps=50") {
		t.Fatalf("expected fps=50, got %q", got)
	}
}
