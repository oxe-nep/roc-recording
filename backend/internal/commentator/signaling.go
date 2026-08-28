package commentator

import (
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

const signalingMaxMsg = 256 * 1024

type signalMsg struct {
	Type      string          `json:"type"`
	SDP       string          `json:"sdp,omitempty"`
	Channel   int             `json:"channel,omitempty"`
	Candidate json.RawMessage `json:"candidate,omitempty"`
}

type joinResponse struct {
	ChannelID   int               `json:"channel_id"`
	DisplayName string            `json:"display_name,omitempty"`
	ICEServers  []map[string]any  `json:"ice_servers"`
	Intercom    []IntercomSlot    `json:"intercom"`
	Quality     QualityClientView `json:"quality"`
	WSPath      string            `json:"ws_path"`
}

var signalingUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func (m *Manager) AllowJoin(r *http.Request) bool {
	if m.joinLimit == nil {
		return true
	}
	return m.joinLimit.allow(clientIP(r))
}

func (m *Manager) JoinInfo(token, pin string) (joinResponse, error) {
	id, err := m.validateTokenAndPIN(token, pin)
	if err != nil {
		return joinResponse{}, err
	}
	settings := m.GetSettings(id)
	return joinResponse{
		ChannelID:   id,
		DisplayName: strings.TrimSpace(settings.DisplayName),
		ICEServers:  m.ice.ClientICEServers(),
		Intercom:    enabledIntercom(settings),
		Quality:     qualityClientView(settings.Quality),
		WSPath:      "/ws/commentator/" + token,
	}, nil
}

func enabledIntercom(s ChannelSettings) []IntercomSlot {
	out := make([]IntercomSlot, 0, intercomSlots)
	for _, slot := range s.Intercom {
		if slot.Enabled {
			out = append(out, slot)
		}
	}
	return out
}

func enabledIntercomIDs(slots []IntercomSlot) []int {
	ids := make([]int, 0, len(slots))
	for _, slot := range slots {
		if slot.Enabled {
			ids = append(ids, slot.ID)
		}
	}
	sort.Ints(ids)
	return ids
}

func intercomTracksStale(previous, current []IntercomSlot) bool {
	a := enabledIntercomIDs(previous)
	b := enabledIntercomIDs(current)
	if len(a) != len(b) {
		return true
	}
	for i := range a {
		if a[i] != b[i] {
			return true
		}
	}
	return false
}

func (m *Manager) pushConfigMessage(sess *rtcSession, channelID int, writeJSON func(any)) {
	settings := m.GetSettings(channelID)
	intercom := enabledIntercom(settings)
	quality := normalizeQuality(settings.Quality)
	reconnect := intercomTracksStale(sess.enabledIntercom, intercom) || !qualityEqual(sess.quality, quality)
	sess.enabledIntercom = intercom
	sess.quality = quality
	writeJSON(map[string]any{
		"type":               "config",
		"channel_id":         channelID,
		"intercom":           intercom,
		"quality":            qualityClientView(quality),
		"reconnect_required": reconnect,
	})
}

func (m *Manager) validateToken(token string) (int, error) {
	return m.validateTokenAndPIN(token, "")
}

func (m *Manager) validateTokenAndPIN(token, pin string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, ch := range m.byID {
		if ch.session == nil || ch.session.token != token {
			continue
		}
		if m.sessionExpired(ch.session) {
			return 0, ErrExpiredToken
		}
		if !ch.enabled {
			return 0, ErrSessionDisabled
		}
		if pin == "" {
			return 0, ErrPinRequired
		}
		if !pinMatches(ch.session.pin, pin) {
			return 0, ErrInvalidPin
		}
		return id, nil
	}
	return 0, ErrInvalidToken
}

func (m *Manager) ServeSignaling(w http.ResponseWriter, r *http.Request, token string) {
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
		log.Printf("[commentator] ws upgrade: %v", err)
		return
	}

	sess, err := m.startRTCSession(channelID, token)
	if err != nil {
		log.Printf("[commentator %d] start rtc: %v", channelID, err)
		_ = conn.WriteJSON(map[string]string{"type": "error", "message": err.Error()})
		_ = conn.Close()
		return
	}
	defer m.endRTCSession(channelID, sess)
	defer m.SetConnected(channelID, false)

	var writeMu sync.Mutex
	writeJSON := func(v any) {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := conn.WriteJSON(v); err != nil {
			log.Printf("[commentator %d] ws write: %v", channelID, err)
		}
	}

	sess.setPushConfig(func() {
		m.pushConfigMessage(sess, channelID, writeJSON)
	})

	m.pushConfigMessage(sess, channelID, writeJSON)

	sess.pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		init := c.ToJSON()
		raw, _ := json.Marshal(init)
		writeJSON(signalMsg{Type: "ice", Candidate: raw})
	})

	sess.pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("[commentator %d] pc state: %s", channelID, state.String())
		switch state {
		case webrtc.PeerConnectionStateConnected:
			m.SetConnected(channelID, true)
			m.startMediaPipelines(sess, channelID)
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
			m.SetConnected(channelID, false)
			_ = conn.Close()
		case webrtc.PeerConnectionStateDisconnected:
			m.SetConnected(channelID, false)
		}
	})

	offerSDP, err := m.createOffer(sess)
	if err != nil {
		log.Printf("[commentator %d] create offer: %v", channelID, err)
		writeJSON(map[string]string{"type": "error", "message": err.Error()})
		return
	}
	writeJSON(signalMsg{Type: "offer", SDP: offerSDP})

	conn.SetReadLimit(signalingMaxMsg)
	var pendingICE []webrtc.ICECandidateInit
	remoteSet := false
	flushICE := func() {
		for _, init := range pendingICE {
			if err := sess.pc.AddICECandidate(init); err != nil {
				log.Printf("[commentator %d] add ice: %v", channelID, err)
			}
		}
		pendingICE = pendingICE[:0]
	}
	for {
		var msg signalMsg
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		switch msg.Type {
		case "answer":
			if err := m.negotiateAnswer(sess, channelID, msg.SDP); err != nil {
				writeJSON(map[string]string{"type": "error", "message": err.Error()})
				continue
			}
			remoteSet = true
			flushICE()
			if sess.pc.ConnectionState() == webrtc.PeerConnectionStateConnected {
				m.startMediaPipelines(sess, channelID)
			}
		case "ice":
			if len(msg.Candidate) == 0 {
				continue
			}
			var init webrtc.ICECandidateInit
			if err := json.Unmarshal(msg.Candidate, &init); err != nil {
				continue
			}
			if !remoteSet {
				pendingICE = append(pendingICE, init)
				continue
			}
			if err := sess.pc.AddICECandidate(init); err != nil {
				log.Printf("[commentator %d] add ice: %v", channelID, err)
			}
		case "ptt":
			m.SetPTT(channelID, msg.Channel)
		}
	}
}
