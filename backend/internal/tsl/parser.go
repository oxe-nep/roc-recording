package tsl

import (
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf16"
)

const maximumPacketSize = 2048

// Message is one TSL v5 display update.
type Message struct {
	Index uint16
	Text  string
}

// ParsePacket decodes a TSL UMD v5 UDP payload.
func ParsePacket(buf []byte) ([]Message, error) {
	if len(buf) < 6 {
		return nil, fmt.Errorf("tsl packet too short")
	}
	pbc := binary.LittleEndian.Uint16(buf[0:2])
	if int(pbc) < 4 || len(buf) < int(pbc)+2 {
		return nil, fmt.Errorf("tsl packet size mismatch")
	}
	flags := buf[3]
	unicodeStrings := flags&0x01 != 0
	controlData := flags&0x02 != 0
	if controlData {
		return nil, nil
	}

	var out []Message
	ptr := 6
	limit := int(pbc) + 2
	if limit > len(buf) {
		limit = len(buf)
	}
	for ptr+6 <= limit && ptr < maximumPacketSize {
		msg, next := parseDisplayMessage(buf, ptr, unicodeStrings)
		if msg == nil || next <= ptr {
			break
		}
		if strings.TrimSpace(msg.Text) != "" {
			out = append(out, *msg)
		}
		ptr = next
	}
	return out, nil
}

func parseDisplayMessage(buffer []byte, start int, unicodeStrings bool) (*Message, int) {
	if start+4 > len(buffer) {
		return nil, 0
	}
	controlFlags := binary.LittleEndian.Uint16(buffer[start+2 : start+4])

	msg := &Message{
		Index: binary.LittleEndian.Uint16(buffer[start : start+2]),
	}

	if controlFlags&0x8000 != 0 {
		return msg, start + 4
	}
	if start+6 > len(buffer) {
		return msg, start + 6
	}
	textLen := int(binary.LittleEndian.Uint16(buffer[start+4 : start+6]))
	end := start + 6 + textLen
	if end > len(buffer) {
		return msg, start + 6
	}
	data := buffer[start+6:end]
	if unicodeStrings {
		if textLen%2 != 0 {
			return msg, end
		}
		u := make([]uint16, textLen/2)
		for i := 0; i < textLen; i += 2 {
			u[i/2] = binary.LittleEndian.Uint16(data[i : i+2])
		}
		msg.Text = strings.TrimSpace(string(utf16.Decode(u)))
	} else {
		msg.Text = strings.TrimSpace(string(data))
	}
	return msg, end
}
