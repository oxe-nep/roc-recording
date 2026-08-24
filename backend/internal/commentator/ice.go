package commentator

import (
	"os"
	"strings"

	"github.com/pion/webrtc/v4"
)

// ICEConfig holds STUN/TURN settings for commentator WebRTC sessions.
type ICEConfig struct {
	STUNURLs       []string
	TURNURLs       []string
	TURNUsername   string
	TURNCredential string
	PublicHost     string
}

func ICEConfigFromEnv(publicURL string) ICEConfig {
	cfg := ICEConfig{
		STUNURLs: []string{"stun:stun.l.google.com:19302"},
	}
	if v := strings.TrimSpace(os.Getenv("WEBRTC_STUN_URLS")); v != "" {
		cfg.STUNURLs = splitCSV(v)
	}
	if v := strings.TrimSpace(os.Getenv("WEBRTC_TURN_URLS")); v != "" {
		cfg.TURNURLs = splitCSV(v)
	}
	cfg.TURNUsername = strings.TrimSpace(os.Getenv("WEBRTC_TURN_USERNAME"))
	cfg.TURNCredential = strings.TrimSpace(os.Getenv("WEBRTC_TURN_CREDENTIAL"))
	cfg.PublicHost = strings.TrimSpace(os.Getenv("WEBRTC_PUBLIC_HOST"))
	if cfg.PublicHost == "" {
		cfg.PublicHost = hostFromURL(publicURL)
	}
	return cfg
}

func (c ICEConfig) PeerConfiguration() webrtc.Configuration {
	servers := make([]webrtc.ICEServer, 0, 2)
	if len(c.STUNURLs) > 0 {
		servers = append(servers, webrtc.ICEServer{URLs: c.STUNURLs})
	}
	if len(c.TURNURLs) > 0 {
		turn := webrtc.ICEServer{URLs: c.TURNURLs}
		if c.TURNUsername != "" {
			turn.Username = c.TURNUsername
			turn.Credential = c.TURNCredential
		}
		servers = append(servers, turn)
	}
	return webrtc.Configuration{ICEServers: servers}
}

func (c ICEConfig) ClientICEServers() []map[string]any {
	out := make([]map[string]any, 0, 2)
	if len(c.STUNURLs) > 0 {
		out = append(out, map[string]any{"urls": c.STUNURLs})
	}
	if len(c.TURNURLs) > 0 {
		entry := map[string]any{"urls": c.TURNURLs}
		if c.TURNUsername != "" {
			entry["username"] = c.TURNUsername
			entry["credential"] = c.TURNCredential
		}
		out = append(out, entry)
	}
	return out
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func hostFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")
	if i := strings.Index(raw, "/"); i >= 0 {
		raw = raw[:i]
	}
	if h, _, ok := strings.Cut(raw, ":"); ok {
		return h
	}
	return raw
}
