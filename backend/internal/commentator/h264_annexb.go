package commentator

import (
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

const h264FrameDuration = 40 * time.Millisecond

type h264TrackWriter struct {
	track   *webrtc.TrackLocalStaticSample
	spsPps  []byte
	buf     []byte
	au      []byte
	start   time.Time
	frameN  int
	started bool
}

func newH264TrackWriter(track *webrtc.TrackLocalStaticSample) *h264TrackWriter {
	return &h264TrackWriter{track: track}
}

func (w *h264TrackWriter) feed(data []byte) {
	if len(data) == 0 {
		return
	}
	w.buf = append(w.buf, data...)
	for {
		start := findAnnexBStart(w.buf, 0)
		if start < 0 {
			if len(w.buf) > 4 {
				w.buf = w.buf[len(w.buf)-4:]
			}
			return
		}
		if start > 0 {
			w.buf = w.buf[start:]
			start = 0
		}
		next := findAnnexBStart(w.buf, 3)
		if next < 0 {
			return
		}
		nal := append([]byte(nil), w.buf[:next]...)
		w.buf = w.buf[next:]
		w.writeNAL(nal)
	}
}

func (w *h264TrackWriter) writeNAL(nal []byte) {
	nalType := h264NALType(nal)
	switch nalType {
	case 7: // SPS
		w.spsPps = append([]byte(nil), nal...)
	case 8: // PPS
		w.spsPps = append(w.spsPps, nal...)
	case 5: // IDR
		payload := append([]byte(nil), w.spsPps...)
		payload = append(payload, nal...)
		w.emitFrame(payload)
	case 1: // non-IDR
		w.emitFrame(append([]byte(nil), nal...))
	default:
		// AUD / SEI — ignore
	}
}

func (w *h264TrackWriter) emitFrame(payload []byte) {
	if len(payload) == 0 {
		return
	}
	if !w.started {
		w.started = true
		w.start = time.Now()
		w.frameN = 0
	}
	// Pace to 25 fps wall clock so RTP timestamps stay realtime (avoids Chrome drops/freezes).
	w.frameN++
	if wait := time.Until(w.start.Add(time.Duration(w.frameN) * h264FrameDuration)); wait > 0 {
		time.Sleep(wait)
	}
	_ = w.track.WriteSample(media.Sample{Data: payload, Duration: h264FrameDuration})
}

func h264NALType(nal []byte) byte {
	payload := stripAnnexB(nal)
	if len(payload) == 0 {
		return 0
	}
	return payload[0] & 0x1f
}

func stripAnnexB(nal []byte) []byte {
	if len(nal) >= 4 && nal[0] == 0 && nal[1] == 0 && nal[2] == 0 && nal[3] == 1 {
		return nal[4:]
	}
	if len(nal) >= 3 && nal[0] == 0 && nal[1] == 0 && nal[2] == 1 {
		return nal[3:]
	}
	return nal
}

func toAnnexB(nal []byte) []byte {
	if len(nal) >= 4 && nal[0] == 0 && nal[1] == 0 && (nal[2] == 1 || (nal[2] == 0 && nal[3] == 1)) {
		return append([]byte(nil), nal...)
	}
	out := []byte{0, 0, 0, 1}
	return append(out, nal...)
}
