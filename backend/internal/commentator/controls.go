package commentator

import (
	"log"
	"net/http"
	"strings"
)

type controlsMsg struct {
	Type    string  `json:"type"`
	Channel int     `json:"channel,omitempty"`
	Target  string  `json:"target,omitempty"`
	Slot    *int    `json:"slot,omitempty"`
	Delta   float64 `json:"delta,omitempty"`
}

// ServeControls handles Stream Deck plugin WebSocket (PTT, volume relay, layout push).
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

	m.deck.registerControls(channelID, conn)
	defer m.deck.unregisterControls(channelID, conn)

	settings := m.GetSettings(channelID)
	intercom := enabledIntercom(settings)
	layout := intercomToDeckLayout(intercom)
	_ = conn.WriteJSON(map[string]any{
		"type":     "ready",
		"intercom": intercom,
		"buttons":  layout,
	})

	conn.SetReadLimit(signalingMaxMsg)
	for {
		var msg controlsMsg
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		switch msg.Type {
		case "ptt":
			m.SetPTT(channelID, msg.Channel)
		case "volume":
			if msg.Target == "" || msg.Delta == 0 {
				continue
			}
			m.deck.notifyBrowserVolume(channelID, deckVolumeAdjust{
				Target: msg.Target,
				Slot:   msg.Slot,
				Delta:  msg.Delta,
			})
		case "ping":
			_ = conn.WriteJSON(map[string]string{"type": "pong"})
		}
	}
}

func controlsPath(token string) string {
	return "/ws/commentator/" + token + "/controls"
}
