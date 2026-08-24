package commentator

import (
	"testing"

	"github.com/pion/webrtc/v4"
)

func TestH264TrackWriterPrependsSPSPPSToIDR(t *testing.T) {
	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264},
		"video",
		"test",
	)
	if err != nil {
		t.Fatal(err)
	}
	w := newH264TrackWriter(track)

	sps := []byte{0, 0, 0, 1, 0x67, 0x42, 0x00, 0x1f}
	pps := []byte{0, 0, 0, 1, 0x68, 0xce, 0x38, 0x80}
	idr := []byte{0, 0, 0, 1, 0x65, 0x88, 0x84, 0x21}

	w.feed(sps)
	w.feed(pps)
	w.feed(idr)

	if len(w.spsPps) == 0 {
		t.Fatal("expected cached sps/pps")
	}
}

func TestToAnnexB(t *testing.T) {
	got := toAnnexB([]byte{0x65, 0x01, 0x02})
	if len(got) != 7 || got[0] != 0 || got[3] != 1 || got[4] != 0x65 {
		t.Fatalf("unexpected annex-b prefix: %v", got)
	}
}
