package tsl

import (
	"encoding/binary"
	"testing"
)

func TestParsePacketASCII(t *testing.T) {
	text := "CAM 1"
	payload := make([]byte, 0, 32)
	payload = append(payload, 0, 0, 0, 0, 0, 0) // PBC + VER + FLAGS + SCREEN placeholders
	pbcPos := 0

	// Build body first, then patch PBC.
	body := make([]byte, 0, 24)
	body = append(body, 0, 0) // index 0
	body = append(body, 0, 0) // control: text, LH off
	body = append(body, byte(len(text)), 0)
	body = append(body, []byte(text)...)

	payload = append(payload, 0, 0, 0, 0, 0, 0)
	payload = append(payload, body...)
	pbc := uint16(len(payload) - 2)
	binary.LittleEndian.PutUint16(payload[pbcPos:], pbc)

	msgs, err := ParsePacket(payload)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Text != "CAM 1" {
		t.Fatalf("text = %q", msgs[0].Text)
	}
}
