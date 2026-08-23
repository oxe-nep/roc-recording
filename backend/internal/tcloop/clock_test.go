package tcloop

import "testing"

func TestNormalizeTimecodeStripsFrames(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"12:34:56:12", "12:34:56"},
		{"12:34:56;12", "12:34:56"},
		{"12:34:56", "12:34:56"},
		{"  01:02:03:04  ", "01:02:03"},
		{"--:--:--:--", "--:--:--"},
		{"bad", ""},
	}
	for _, tt := range tests {
		got := normalizeTimecode(tt.in)
		if got != tt.want {
			t.Fatalf("normalizeTimecode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
