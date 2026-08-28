package commentator

import "testing"

func TestDeckHubIssueAndClaim(t *testing.T) {
	h := newDeckHub()
	code := h.issueCode("tok", "123456", 2)
	if len(code) != 6 {
		t.Fatalf("code length = %d", len(code))
	}
	entry, ok := h.claim(code)
	if !ok || entry.token != "tok" || entry.pin != "123456" || entry.channelID != 2 {
		t.Fatalf("claim mismatch: %+v ok=%v", entry, ok)
	}
}

func TestIntercomToDeckLayout(t *testing.T) {
	layout := intercomToDeckLayout([]IntercomSlot{
		{ID: 1, Name: "Producer", Enabled: true},
		{ID: 3, Name: "", Enabled: true},
		{ID: 2, Name: "Off", Enabled: false},
	})
	if len(layout) != 2 {
		t.Fatalf("len = %d", len(layout))
	}
	if layout[0].slot != 0 || layout[0].channel != 1 || layout[0].label != "Producer" {
		t.Fatalf("slot0 = %+v", layout[0])
	}
	if layout[1].slot != 1 || layout[1].channel != 3 {
		t.Fatalf("slot1 = %+v", layout[1])
	}
}
