package commentator

import (
	"log"
	"net/http"
	"strings"
)

// ServeControls handles a lightweight WebSocket for PTT-only control (Stream Deck, etc.).
// Requires the same invite token + PIN as join; does not start WebRTC.
func (m *Manager) ServeControls(w http.ResponseWriter, r *http.Request, token string) {
	if !m.AllowJoin(r) {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	pin := strings.TrimSpace(r.URL.Query().Get("pin"))
	channelID, err := m.validateTokenAndPIN(token, pin)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	conn, err := signalingUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[commentator] controls ws upgrade: %v", err)
		return
	}
	defer conn.Close()

	settings := m.GetSettings(channelID)
	_ = conn.WriteJSON(map[string]any{
		"type":     "ready",
		"intercom": enabledIntercom(settings),
	})

	conn.SetReadLimit(signalingMaxMsg)
	for {
		var msg signalMsg
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		switch msg.Type {
		case "ptt":
			m.SetPTT(channelID, msg.Channel)
		case "ping":
			_ = conn.WriteJSON(map[string]string{"type": "pong"})
		}
	}
}

func controlsPath(token string) string {
	return "/ws/commentator/" + token + "/controls"
}
