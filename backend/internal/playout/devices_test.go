package playout

import "testing"

func TestParseSinkLine(t *testing.T) {
	cases := []struct {
		in        string
		wantID    string
		wantLabel string
		ok        bool
	}{
		{`106:25fb7120:00000000 [DeckLink IP 100G (1)] (none)`, "106:25fb7120:00000000", "DeckLink IP 100G (1)", true},
		{`25fb7120:00000000 [DeckLink IP 100G (1)] (none)`, "25fb7120:00000000", "DeckLink IP 100G (1)", true},
		{`55:00000000:00000000 [DeckLink Duo (1)]`, "55:00000000:00000000", "DeckLink Duo (1)", true},
		{`'DeckLink Mini Monitor'`, "DeckLink Mini Monitor", "DeckLink Mini Monitor", true},
		{`0: 25fb7120:00000000 [DeckLink IP 100G (2)] (none)`, "25fb7120:00000000", "DeckLink IP 100G (2)", true},
		{`Auto-detected sinks for decklink:`, "", "", false},
	}
	for _, tc := range cases {
		got, ok := ParseSinkLine(tc.in)
		if ok != tc.ok {
			t.Errorf("ParseSinkLine(%q) ok=%v want %v", tc.in, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if got.ID != tc.wantID || got.Label != tc.wantLabel {
			t.Errorf("ParseSinkLine(%q) = {%q,%q}, want {%q,%q}", tc.in, got.ID, got.Label, tc.wantID, tc.wantLabel)
		}
	}
}

func TestDeviceRefMatch(t *testing.T) {
	if !deviceRefMatch("25fb7120:00000000", "106:25fb7120:00000000", "DeckLink IP 100G (1)") {
		t.Fatal("short id should match full sink handle")
	}
	if !deviceRefMatch("106:25fb7120:00000000", "106:25fb7120:00000000", "DeckLink IP 100G (1)") {
		t.Fatal("full id should match")
	}
	if deviceRefMatch("25fb7120:00000000", "106:25fb7121:00000000", "DeckLink IP 100G (2)") {
		t.Fatal("short id must not match a different sink")
	}
}

func TestNormalizeOpenDevice(t *testing.T) {
	cases := map[string]string{
		`106:25fb7120:00000000 [DeckLink IP 100G (1)] (none)`: "106:25fb7120:00000000",
		`25fb7120:00000000 [DeckLink IP 100G (1)] (none)`:     "25fb7120:00000000",
		`'DeckLink IP 100G (2)'`:                              "DeckLink IP 100G (2)",
		`DeckLink IP 100G (3)`:                                "DeckLink IP 100G (3)",
		`25fb7120:00000000`:                                   "25fb7120:00000000",
	}
	for in, want := range cases {
		if got := NormalizeOpenDevice(in); got != want {
			t.Errorf("NormalizeOpenDevice(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractDeckLinkName(t *testing.T) {
	in := `-f decklink -signal_loss_action repeat -i 'DeckLink IP 100G (1)'`
	want := "DeckLink IP 100G (1)"
	if got := ExtractDeckLinkName(in); got != want {
		t.Errorf("ExtractDeckLinkName = %q, want %q", got, want)
	}
}
