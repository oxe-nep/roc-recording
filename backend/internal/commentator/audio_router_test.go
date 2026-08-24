package commentator

import "testing"

func TestAudioRouterOnAir(t *testing.T) {
	r := NewAudioRouter()
	r.PushMic([]int16{1000, 2000})
	frame := r.Frame8ch()
	if len(frame) != frameBytes8ch {
		t.Fatalf("frame size: got %d want %d", len(frame), frameBytes8ch)
	}
	samples := bytesToInt16(frame)
	if samples[0] != 1000 || samples[1] != 2000 {
		t.Fatalf("on-air stereo: got %d %d", samples[0], samples[1])
	}
	if samples[2] != 0 || samples[7] != 0 {
		t.Fatalf("other channels should be silent")
	}
}

func TestAudioRouterPTTIntercom(t *testing.T) {
	r := NewAudioRouter()
	r.SetPTT(2) // intercom slot 2 → decklink channel 4 (index 3)
	r.PushMic([]int16{500})
	frame := r.Frame8ch()
	samples := bytesToInt16(frame)
	if samples[0] != 0 || samples[1] != 0 {
		t.Fatalf("on-air muted during PTT")
	}
	if samples[3] != 500 {
		t.Fatalf("intercom channel 4: got %d want 500", samples[3])
	}
}

func TestAudioRouterClearMic(t *testing.T) {
	r := NewAudioRouter()
	r.PushMic([]int16{1000})
	r.ClearMic()
	frame := r.Frame8ch()
	samples := bytesToInt16(frame)
	for i, s := range samples {
		if s != 0 {
			t.Fatalf("sample %d should be silent, got %d", i, s)
		}
	}
}

func bytesToInt16(b []byte) []int16 {
	out := make([]int16, len(b)/2)
	for i := range out {
		out[i] = int16(b[i*2]) | int16(b[i*2+1])<<8
	}
	return out
}
