package commentator

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/pion/webrtc/v4"
)

type rtcSession struct {
	channelID       int
	token           string
	pc              *webrtc.PeerConnection
	ctx             context.Context
	cancel          context.CancelFunc
	router          *AudioRouter
	videoFrames     chan []byte
	videoTrack      *webrtc.TrackLocalStaticSample
	pgmTrack        *webrtc.TrackLocalStaticSample
	intercomTracks  []*webrtc.TrackLocalStaticSample
	enabledIntercom []IntercomSlot
	quality         QualitySettings
	pushConfig      func()
	stopOnce        sync.Once
	negotiated      bool
	mediaStarted    sync.Once
	negotiateMu     sync.Mutex
	pushConfigMu    sync.Mutex
}

func (s *rtcSession) setPushConfig(fn func()) {
	s.pushConfigMu.Lock()
	s.pushConfig = fn
	s.pushConfigMu.Unlock()
}

func (s *rtcSession) notifyConfig() {
	s.pushConfigMu.Lock()
	fn := s.pushConfig
	s.pushConfigMu.Unlock()
	if fn != nil {
		fn()
	}
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

// preferH264Recv puts H264 ahead of VP8 on the webcam m-line.
// Do not drop VP8/other codecs — H264-only + orphaned RTX makes Chrome reject the offer
// ("Failed to set remote video description send parameters").
func preferH264Recv(tr *webrtc.RTPTransceiver) {
	if tr == nil || tr.Receiver() == nil {
		return
	}
	codecs := tr.Receiver().GetParameters().Codecs
	if len(codecs) == 0 {
		return
	}
	var h264, rest []webrtc.RTPCodecParameters
	for _, c := range codecs {
		if strings.Contains(strings.ToLower(c.MimeType), "h264") {
			h264 = append(h264, c)
		} else {
			rest = append(rest, c)
		}
	}
	if len(h264) == 0 {
		return
	}
	if err := tr.SetCodecPreferences(append(h264, rest...)); err != nil {
		log.Printf("[commentator] SetCodecPreferences(H264 first): %v", err)
	}
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
	videoFrames := make(chan []byte, 3)
	sess := &rtcSession{
		channelID:   channelID,
		token:       token,
		pc:          pc,
		ctx:         ctx,
		cancel:      cancel,
		router:      router,
		videoFrames: videoFrames,
	}

	videoTrack, pgmTrack, intercomTracks, enabled, err := m.addCommentatorOutgoingTracks(pc, channelID)
	if err != nil {
		_ = pc.Close()
		cancel()
		return nil, err
	}
	sess.videoTrack = videoTrack
	sess.pgmTrack = pgmTrack
	sess.intercomTracks = intercomTracks
	sess.enabledIntercom = enabled
	sess.quality = m.GetSettings(channelID).Quality

	// Receive webcam + mic from the commentator browser (server is SDP offerer).
	webcamTr, err := pc.AddTransceiverFromKind(
		webrtc.RTPCodecTypeVideo,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly},
	)
	if err != nil {
		_ = pc.Close()
		cancel()
		return nil, err
	}
	preferH264Recv(webcamTr)
	if _, err := pc.AddTransceiverFromKind(
		webrtc.RTPCodecTypeAudio,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly},
	); err != nil {
		_ = pc.Close()
		cancel()
		return nil, err
	}

	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if tr.Kind() == webrtc.RTPCodecTypeAudio {
			go m.consumeCommentatorMic(ctx, channelID, tr, router)
		}
		if tr.Kind() == webrtc.RTPCodecTypeVideo {
			go m.consumeCommentatorWebcam(ctx, channelID, tr, pc, videoFrames)
		}
	})

	m.mu.Lock()
	ch := m.channelLocked(channelID)
	router.SetPTT(ch.pttChannel)
	m.rtcByChannel[channelID] = sess
	m.mu.Unlock()

	return sess, nil
}

func (m *Manager) createOffer(sess *rtcSession) (string, error) {
	offer, err := sess.pc.CreateOffer(nil)
	if err != nil {
		return "", err
	}
	if err := sess.pc.SetLocalDescription(offer); err != nil {
		return "", err
	}
	return offer.SDP, nil
}

func (m *Manager) negotiateAnswer(sess *rtcSession, channelID int, answerSDP string) error {
	sess.negotiateMu.Lock()
	defer sess.negotiateMu.Unlock()
	if sess.negotiated {
		return fmt.Errorf("session already negotiated")
	}

	if err := sess.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  answerSDP,
	}); err != nil {
		return err
	}

	sess.negotiated = true
	return nil
}

func (m *Manager) startMediaPipelines(sess *rtcSession, channelID int) {
	sess.mediaStarted.Do(func() {
		log.Printf("[commentator %d] WebRTC connected — starting DeckLink/ffmpeg pipelines", channelID)
		go m.logPeerStats(sess.ctx.Done(), channelID, sess.pc)
		go m.runFFmpegInbound(sess.ctx, channelID, sess.videoTrack, sess.pgmTrack, sess.enabledIntercom, sess.intercomTracks, nil)
		go m.runFFmpegOutbound(sess.ctx, channelID, sess.router, sess.videoFrames)
	})
}

func (m *Manager) addCommentatorOutgoingTracks(
	pc *webrtc.PeerConnection,
	channelID int,
) (*webrtc.TrackLocalStaticSample, *webrtc.TrackLocalStaticSample, []*webrtc.TrackLocalStaticSample, []IntercomSlot, error) {
	videoTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeH264,
			ClockRate:   90000,
			SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
		},
		"video",
		"program",
	)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if _, err := pc.AddTrack(videoTrack); err != nil {
		return nil, nil, nil, nil, err
	}

	opusCaps := webrtc.RTPCodecCapability{
		MimeType:    webrtc.MimeTypeOpus,
		ClockRate:   48000,
		Channels:    2, // RFC 7587: Opus always uses 2 in SDP; we still send mono frames
		SDPFmtpLine: "minptime=10;useinbandfec=1",
	}
	pgmTrack, err := webrtc.NewTrackLocalStaticSample(opusCaps, "audio", "pgm")
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if _, err := pc.AddTrack(pgmTrack); err != nil {
		return nil, nil, nil, nil, err
	}

	settings := m.GetSettings(channelID)
	enabled := enabledIntercom(settings)
	intercomTracks := make([]*webrtc.TrackLocalStaticSample, 0, len(enabled))
	for _, slot := range enabled {
		t, err := webrtc.NewTrackLocalStaticSample(
			opusCaps,
			fmt.Sprintf("intercom%d", slot.ID),
			fmt.Sprintf("intercom%d", slot.ID),
		)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		if _, err := pc.AddTrack(t); err != nil {
			return nil, nil, nil, nil, err
		}
		intercomTracks = append(intercomTracks, t)
	}

	return videoTrack, pgmTrack, intercomTracks, enabled, nil
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
