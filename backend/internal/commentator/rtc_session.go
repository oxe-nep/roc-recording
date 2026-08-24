package commentator

import (
	"context"
	"fmt"
	"sync"

	"github.com/pion/webrtc/v4"
)

type rtcSession struct {
	channelID   int
	token       string
	pc          *webrtc.PeerConnection
	cancel      context.CancelFunc
	router      *AudioRouter
	videoFrames chan []byte
	stopOnce    sync.Once
}

func (m *Manager) newPeerConnection() (*webrtc.PeerConnection, error) {
	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterDefaultCodecs(); err != nil {
		return nil, fmt.Errorf("register codecs: %w", err)
	}
	opts := []func(*webrtc.API){webrtc.WithMediaEngine(mediaEngine)}
	if host := m.ice.PublicHost; host != "" {
		se := webrtc.SettingEngine{}
		se.SetNAT1To1IPs([]string{host}, webrtc.ICECandidateTypeHost)
		opts = append(opts, webrtc.WithSettingEngine(se))
	}
	api := webrtc.NewAPI(opts...)
	return api.NewPeerConnection(m.ice.PeerConfiguration())
}

func (m *Manager) startRTCSession(channelID int, token string) (*rtcSession, error) {
	m.mu.Lock()
	if existing, ok := m.rtcByChannel[channelID]; ok {
		existing.stop()
		delete(m.rtcByChannel, channelID)
	}
	m.mu.Unlock()

	pc, err := m.newPeerConnection()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	router := NewAudioRouter()
	videoFrames := make(chan []byte, 2)
	sess := &rtcSession{
		channelID:   channelID,
		token:       token,
		pc:          pc,
		cancel:      cancel,
		router:      router,
		videoFrames: videoFrames,
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
	enabled := enabledIntercom(settings)
	intercomTracks := make([]*webrtc.TrackLocalStaticSample, 0, len(enabled))
	for _, slot := range enabled {
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
			go m.consumeCommentatorMic(ctx, channelID, tr, router)
		}
		if tr.Kind() == webrtc.RTPCodecTypeVideo {
			go m.consumeCommentatorWebcam(ctx, channelID, tr, videoFrames)
		}
	})

	m.mu.Lock()
	ch := m.channelLocked(channelID)
	router.SetPTT(ch.pttChannel)
	m.rtcByChannel[channelID] = sess
	m.mu.Unlock()

	silenceCtx, silenceCancel := context.WithCancel(ctx)
	go m.runSilenceFallback(silenceCtx, pgmTrack, intercomTracks)
	go m.runFFmpegInbound(ctx, channelID, videoTrack, pgmTrack, enabled, intercomTracks, silenceCancel)
	go m.runFFmpegOutbound(ctx, channelID, router, videoFrames)

	return sess, nil
}

func (m *Manager) endRTCSession(channelID int, sess *rtcSession) {
	if sess == nil {
		return
	}
	if sess.router != nil {
		sess.router.SetPTT(0)
		sess.router.ClearMic()
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
