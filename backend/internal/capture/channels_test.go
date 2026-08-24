package capture

import "testing"

func TestEnsureDeckLinkChannels(t *testing.T) {
	in := []string{"-f", "decklink", "-i", "DeckLink IP 100G (4)"}
	got := ensureDeckLinkChannels(in, 8)
	want := []string{"-f", "decklink", "-channels", "8", "-i", "DeckLink IP 100G (4)"}
	if len(got) != len(want) {
		t.Fatalf("len=%d want %v got %v", len(got), want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("arg %d: got %q want %q (%v)", i, got[i], want[i], got)
		}
	}
	again := ensureDeckLinkChannels(got, 8)
	if len(again) != len(got) {
		t.Fatalf("should be idempotent: %v", again)
	}
}
