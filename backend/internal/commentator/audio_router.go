package commentator

import "sync"

const (
	sampleRate       = 48000
	samplesPerFrame  = 960 // 20 ms @ 48 kHz
	bytesPerSample   = 2
	frameBytes8ch    = 8 * samplesPerFrame * bytesPerSample
)

// AudioRouter mixes one mono mic stream into 8 discrete DeckLink channels.
// PTT 0 → mic on channels 1–2 (on air). PTT N → mic on channel N+2 only.
type AudioRouter struct {
	mu         sync.Mutex
	pttChannel int
	mic        [samplesPerFrame]int16
	hasMic     bool
}

func NewAudioRouter() *AudioRouter {
	return &AudioRouter{}
}

func (r *AudioRouter) SetPTT(channel int) {
	r.mu.Lock()
	if channel < 0 || channel > intercomSlots {
		channel = 0
	}
	r.pttChannel = channel
	r.mu.Unlock()
}

func (r *AudioRouter) PushMic(pcm []int16) {
	if len(pcm) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(pcm)
	if n > samplesPerFrame {
		n = samplesPerFrame
	}
	copy(r.mic[:n], pcm[:n])
	for i := n; i < samplesPerFrame; i++ {
		r.mic[i] = 0
	}
	r.hasMic = true
}

func (r *AudioRouter) ClearMic() {
	r.mu.Lock()
	r.hasMic = false
	for i := range r.mic {
		r.mic[i] = 0
	}
	r.mu.Unlock()
}

func (r *AudioRouter) PTT() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pttChannel
}

func (r *AudioRouter) Frame8ch() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]int16, 8*samplesPerFrame)
	ptt := r.pttChannel
	var mic [samplesPerFrame]int16
	if r.hasMic {
		mic = r.mic
	}
	if ptt == 0 {
		for i := 0; i < samplesPerFrame; i++ {
			out[i*8+0] = mic[i]
			out[i*8+1] = mic[i]
		}
		return int16ToLE(out)
	}
	ch := ptt + 1 // intercom slot 1 → decklink channel index 3 (0-based 2)
	if ch < 2 || ch > 7 {
		return int16ToLE(out)
	}
	for i := 0; i < samplesPerFrame; i++ {
		out[i*8+ch] = mic[i]
	}
	return int16ToLE(out)
}

func int16ToLE(samples []int16) []byte {
	b := make([]byte, len(samples)*bytesPerSample)
	for i, s := range samples {
		b[i*2] = byte(s)
		b[i*2+1] = byte(s >> 8)
	}
	return b
}
