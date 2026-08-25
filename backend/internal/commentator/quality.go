package commentator

import "strings"

// Video/audio quality presets for WebRTC legs.
const (
	VideoQualityMonitoring = "monitoring"
	VideoQualityStandard   = "standard"
	VideoQualityHigh       = "high"

	AudioQualityVoice     = "voice"
	AudioQualityStandard  = "standard"
	AudioQualityBroadcast = "broadcast"
)

// QualitySettings is persisted per channel and pushed to the commentator client.
type QualitySettings struct {
	ToCommentatorVideo   string `json:"to_commentator_video"`
	ToCommentatorAudio   string `json:"to_commentator_audio"`
	FromCommentatorVideo string `json:"from_commentator_video"`
	FromCommentatorAudio string `json:"from_commentator_audio"`
}

func DefaultQualitySettings() QualitySettings {
	return QualitySettings{
		ToCommentatorVideo:   VideoQualityStandard,
		ToCommentatorAudio:   AudioQualityStandard,
		FromCommentatorVideo: VideoQualityStandard,
		FromCommentatorAudio: AudioQualityVoice,
	}
}

func normalizeQuality(q QualitySettings) QualitySettings {
	out := DefaultQualitySettings()
	if v := normalizeVideoPreset(q.ToCommentatorVideo); v != "" {
		out.ToCommentatorVideo = v
	}
	if v := normalizeAudioPreset(q.ToCommentatorAudio); v != "" {
		out.ToCommentatorAudio = v
	}
	if v := normalizeVideoPreset(q.FromCommentatorVideo); v != "" {
		out.FromCommentatorVideo = v
	}
	if v := normalizeAudioPreset(q.FromCommentatorAudio); v != "" {
		out.FromCommentatorAudio = v
	}
	return out
}

func normalizeVideoPreset(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case VideoQualityMonitoring, "low":
		return VideoQualityMonitoring
	case VideoQualityHigh:
		return VideoQualityHigh
	case VideoQualityStandard, "medium", "":
		return VideoQualityStandard
	default:
		return ""
	}
}

func normalizeAudioPreset(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case AudioQualityVoice, "low":
		return AudioQualityVoice
	case AudioQualityBroadcast, "high":
		return AudioQualityBroadcast
	case AudioQualityStandard, "medium", "":
		return AudioQualityStandard
	default:
		return ""
	}
}

func qualityEqual(a, b QualitySettings) bool {
	a, b = normalizeQuality(a), normalizeQuality(b)
	return a.ToCommentatorVideo == b.ToCommentatorVideo &&
		a.ToCommentatorAudio == b.ToCommentatorAudio &&
		a.FromCommentatorVideo == b.FromCommentatorVideo &&
		a.FromCommentatorAudio == b.FromCommentatorAudio
}

// outboundVideoParams controls DeckLink → commentator H264 encode.
type outboundVideoParams struct {
	Width   int
	Height  int
	FPS     int
	Bitrate string
	Maxrate string
	Bufsize string
	Level   string
}

func outboundVideoFor(preset string) outboundVideoParams {
	switch normalizeVideoPreset(preset) {
	case VideoQualityMonitoring:
		return outboundVideoParams{
			Width: 960, Height: 540, FPS: 25,
			Bitrate: "1200k", Maxrate: "1500k", Bufsize: "300k", Level: "3.1",
		}
	case VideoQualityHigh:
		return outboundVideoParams{
			Width: 1920, Height: 1080, FPS: 25,
			Bitrate: "4500k", Maxrate: "5000k", Bufsize: "900k", Level: "4.0",
		}
	default:
		return outboundVideoParams{
			Width: 1280, Height: 720, FPS: 25,
			Bitrate: "2500k", Maxrate: "3000k", Bufsize: "500k", Level: "3.1",
		}
	}
}

func outboundAudioBitrate(preset string) int {
	switch normalizeAudioPreset(preset) {
	case AudioQualityVoice:
		return 32000
	case AudioQualityBroadcast:
		return 64000
	default:
		return 48000
	}
}

// ClientVideoQuality is sent to the browser for webcam capture/encode caps.
type ClientVideoQuality struct {
	Width      int `json:"width"`
	Height     int `json:"height"`
	FrameRate  int `json:"frame_rate"`
	MaxBitrate int `json:"max_bitrate"`
}

func clientVideoFor(preset string) ClientVideoQuality {
	switch normalizeVideoPreset(preset) {
	case VideoQualityMonitoring:
		return ClientVideoQuality{Width: 640, Height: 360, FrameRate: 15, MaxBitrate: 600_000}
	case VideoQualityHigh:
		return ClientVideoQuality{Width: 1280, Height: 720, FrameRate: 30, MaxBitrate: 2_000_000}
	default:
		return ClientVideoQuality{Width: 1280, Height: 720, FrameRate: 25, MaxBitrate: 1_200_000}
	}
}

// QualityClientView is the join/config payload for the commentator UI.
type QualityClientView struct {
	ToCommentatorVideo   string             `json:"to_commentator_video"`
	ToCommentatorAudio   string             `json:"to_commentator_audio"`
	FromCommentatorVideo string             `json:"from_commentator_video"`
	FromCommentatorAudio string             `json:"from_commentator_audio"`
	Webcam               ClientVideoQuality `json:"webcam"`
}

func qualityClientView(q QualitySettings) QualityClientView {
	q = normalizeQuality(q)
	return QualityClientView{
		ToCommentatorVideo:   q.ToCommentatorVideo,
		ToCommentatorAudio:   q.ToCommentatorAudio,
		FromCommentatorVideo: q.FromCommentatorVideo,
		FromCommentatorAudio: q.FromCommentatorAudio,
		Webcam:               clientVideoFor(q.FromCommentatorVideo),
	}
}
