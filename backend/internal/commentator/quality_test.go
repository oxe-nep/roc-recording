package commentator

import "testing"

func TestNormalizeQualityDefaults(t *testing.T) {
	got := normalizeQuality(QualitySettings{})
	want := DefaultQualitySettings()
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestOutboundVideoPresets(t *testing.T) {
	m := outboundVideoFor(VideoQualityMonitoring)
	if m.Width != 960 || m.Height != 540 {
		t.Fatalf("monitoring: %+v", m)
	}
	h := outboundVideoFor(VideoQualityHigh)
	if h.Width != 1920 || h.Bitrate != "4500k" {
		t.Fatalf("high: %+v", h)
	}
	s := outboundVideoFor("")
	if s.Width != 1280 || s.Bitrate != "2500k" {
		t.Fatalf("standard: %+v", s)
	}
}

func TestClientVideoPresets(t *testing.T) {
	c := clientVideoFor(VideoQualityMonitoring)
	if c.Width != 640 || c.MaxBitrate != 600_000 {
		t.Fatalf("monitoring webcam: %+v", c)
	}
}
