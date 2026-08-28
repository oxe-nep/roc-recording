package commentator

import "testing"

func TestNewSessionPINFormat(t *testing.T) {
	pin, err := newSessionPIN()
	if err != nil {
		t.Fatalf("newSessionPIN: %v", err)
	}
	if len(pin) != sessionPINLength {
		t.Fatalf("pin length = %d, want %d", len(pin), sessionPINLength)
	}
	for _, c := range pin {
		if c < '0' || c > '9' {
			t.Fatalf("pin contains non-digit: %q", pin)
		}
	}
}

func TestPinMatches(t *testing.T) {
	if !pinMatches("123456", "123456") {
		t.Fatal("expected match")
	}
	if pinMatches("123456", "123457") {
		t.Fatal("expected mismatch")
	}
	if pinMatches("123456", "") {
		t.Fatal("empty pin should not match")
	}
}
