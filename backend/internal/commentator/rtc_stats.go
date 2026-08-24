package commentator

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/pion/webrtc/v4"
)

func debugStatsEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("COMMENTATOR_DEBUG_STATS")))
	return v == "1" || v == "true" || v == "yes"
}

func dumpH264Path() string {
	return strings.TrimSpace(os.Getenv("COMMENTATOR_DUMP_H264"))
}

// logPeerStats periodically prints outbound/inbound RTP counters while connected.
func (m *Manager) logPeerStats(ctxDone <-chan struct{}, channelID int, pc *webrtc.PeerConnection) {
	if !debugStatsEnabled() || pc == nil {
		return
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctxDone:
			return
		case <-ticker.C:
			for _, s := range pc.GetStats() {
				switch st := s.(type) {
				case webrtc.OutboundRTPStreamStats:
					if st.Kind != "video" {
						continue
					}
					log.Printf("[commentator %d] stats OUT video packets=%d bytes=%d nack=%d pli=%d fir=%d",
						channelID, st.PacketsSent, st.BytesSent, st.NACKCount, st.PLICount, st.FIRCount)
				case webrtc.InboundRTPStreamStats:
					if st.Kind != "video" {
						continue
					}
					log.Printf("[commentator %d] stats IN webcam packets=%d bytes=%d lost=%d nack=%d jitter=%.3f",
						channelID, st.PacketsReceived, st.BytesReceived, st.PacketsLost, st.NACKCount, st.Jitter)
				case webrtc.ICECandidatePairStats:
					if st.State != webrtc.StatsICECandidatePairStateSucceeded || !st.Nominated {
						continue
					}
					log.Printf("[commentator %d] stats ICE pair %s bytes(r/s)=%d/%d",
						channelID, st.ID, st.BytesReceived, st.BytesSent)
				default:
					_ = fmt.Sprintf("%T", s)
				}
			}
		}
	}
}
