package commentator

import (
	"context"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

type rtcSession struct {
	channelID int
	token     string
	pc        *webrtc.PeerConnection
	cancel    context.CancelFunc
	stopOnce  sync.Once
}

func (m *Manager) startRTCSession(channelID int, token string) (*rtcSession, error) {
	m.mu.Lock()
	if existing, ok := m.rtcByChannel[channelID]; ok {
		existing.stop()
		delete(m.rtcByChannel, channelID)
	}
	m.mu.Unlock()

	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterDefaultCodecs(); err != nil {
		return nil, fmt.Errorf("register codecs: %w", err)
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine))
	pc, err := api.NewPeerConnection(m.ice.PeerConfiguration())
	if err != nil {
		return nil, fmt.Errorf("peer connection: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	sess := &rtcSession{
		channelID: channelID,
		token:     token,
		pc:        pc,
		cancel:    cancel,
	}

	videoTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264},
		"video",
		"program",
	)
	if err != nil {
		_ = pc.Close()
		cancel()
		return nil, err
	}
	if _, err := pc.AddTrack(videoTrack); err != nil {
		_ = pc.Close()
		cancel()
		return nil, err
	}

	pgmTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
		"audio",
		"pgm",
	)
	if err != nil {
		_ = pc.Close()
		cancel()
		return nil, err
	}
	if _, err := pc.AddTrack(pgmTrack); err != nil {
		_ = pc.Close()
		cancel()
		return nil, err
	}

	settings := m.GetSettings(channelID)
	intercomTracks := make([]*webrtc.TrackLocalStaticSample, 0, intercomSlots)
	for _, slot := range settings.Intercom {
		if !slot.Enabled {
			continue
		}
		t, err := webrtc.NewTrackLocalStaticSample(
			webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
			fmt.Sprintf("intercom%d", slot.ID),
			fmt.Sprintf("intercom%d", slot.ID),
		)
		if err != nil {
			_ = pc.Close()
			cancel()
			return nil, err
		}
		if _, err := pc.AddTrack(t); err != nil {
			_ = pc.Close()
			cancel()
			return nil, err
		}
		intercomTracks = append(intercomTracks, t)
	}

	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if tr.Kind() == webrtc.RTPCodecTypeAudio {
			go m.consumeCommentatorMic(ctx, channelID, tr)
		}
		if tr.Kind() == webrtc.RTPCodecTypeVideo {
			go m.consumeCommentatorWebcam(ctx, channelID, tr)
		}
	})

	m.mu.Lock()
	m.rtcByChannel[channelID] = sess
	m.mu.Unlock()

	go m.runFFmpegInbound(ctx, channelID, videoTrack, pgmTrack, intercomTracks)
	go m.runSilenceFallback(ctx, pgmTrack, intercomTracks)

	return sess, nil
}

func (m *Manager) endRTCSession(channelID int, sess *rtcSession) {
	if sess == nil {
		return
	}
	sess.stop()
	m.mu.Lock()
	if cur, ok := m.rtcByChannel[channelID]; ok && cur == sess {
		delete(m.rtcByChannel, channelID)
	}
	m.mu.Unlock()
}

func (s *rtcSession) stop() {
	s.stopOnce.Do(func() {
		s.cancel()
		if s.pc != nil {
			_ = s.pc.Close()
		}
	})
}

func (m *Manager) consumeCommentatorMic(ctx context.Context, channelID int, tr *webrtc.TrackRemote) {
	log.Printf("[commentator %d] receiving mic track %s", channelID, tr.ID())
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if _, _, err := tr.ReadRTP(); err != nil {
			if err != io.EOF {
				log.Printf("[commentator %d] mic rtp: %v", channelID, err)
			}
			return
		}
	}
}

func (m *Manager) consumeCommentatorWebcam(ctx context.Context, channelID int, tr *webrtc.TrackRemote) {
	log.Printf("[commentator %d] receiving webcam track %s", channelID, tr.ID())
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if _, _, err := tr.ReadRTP(); err != nil {
			if err != io.EOF {
				log.Printf("[commentator %d] webcam rtp: %v", channelID, err)
			}
			return
		}
	}
}

// runSilenceFallback keeps opus tracks alive until FFmpeg supplies real PCM.
func (m *Manager) runSilenceFallback(ctx context.Context, pgm *webrtc.TrackLocalStaticSample, intercom []*webrtc.TrackLocalStaticSample) {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	silent := media.Sample{Data: []byte{0xf8, 0xff, 0xfe}, Duration: 20 * time.Millisecond}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = pgm.WriteSample(silent)
			for _, t := range intercom {
				_ = t.WriteSample(silent)
			}
		}
	}
}
