package commentator

import (
	"crypto/rand"
	"encoding/json"
	"math/big"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const deckCodeTTL = 30 * time.Minute

type deckPairEntry struct {
	token     string
	pin       string
	channelID int
	expiresAt time.Time
}

type deckLayoutButton struct {
	Slot    int    `json:"slot"`
	Channel int    `json:"channel"`
	Label   string `json:"label"`
}

type deckVolumeAdjust struct {
	Target string  `json:"target"`
	Slot   *int    `json:"slot,omitempty"`
	Delta  float64 `json:"delta"`
}

type deckHub struct {
	mu       sync.Mutex
	codes    map[string]deckPairEntry
	controls map[int]map[*websocket.Conn]struct{}
	browsers map[int]func(any)
}

func newDeckHub() *deckHub {
	return &deckHub{
		codes:    make(map[string]deckPairEntry),
		controls: make(map[int]map[*websocket.Conn]struct{}),
		browsers: make(map[int]func(any)),
	}
}

func (h *deckHub) issueCode(token, pin string, channelID int) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.purgeExpiredLocked()
	for code, entry := range h.codes {
		if entry.token == token && entry.pin == pin && entry.channelID == channelID {
			return code
		}
	}
	code := randomDeckCode()
	h.codes[code] = deckPairEntry{
		token:     token,
		pin:       pin,
		channelID: channelID,
		expiresAt: time.Now().Add(deckCodeTTL),
	}
	return code
}

func (h *deckHub) claim(code string) (deckPairEntry, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.purgeExpiredLocked()
	entry, ok := h.codes[code]
	if !ok || time.Now().After(entry.expiresAt) {
		return deckPairEntry{}, false
	}
	return entry, true
}

func (h *deckHub) registerControls(channelID int, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.controls[channelID] == nil {
		h.controls[channelID] = make(map[*websocket.Conn]struct{})
	}
	h.controls[channelID][conn] = struct{}{}
}

func (h *deckHub) unregisterControls(channelID int, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if set := h.controls[channelID]; set != nil {
		delete(set, conn)
		if len(set) == 0 {
			delete(h.controls, channelID)
		}
	}
}

func (h *deckHub) controlsConnected(channelID int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.controls[channelID]) > 0
}

func (h *deckHub) registerBrowser(channelID int, push func(any)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.browsers[channelID] = push
}

func (h *deckHub) unregisterBrowser(channelID int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.browsers, channelID)
}

func (h *deckHub) pushLayout(channelID int, buttons []deckLayoutButton) {
	msg := map[string]any{"type": "layout", "buttons": buttons}
	h.broadcastControls(channelID, msg)
}

func (h *deckHub) pushVolumes(channelID int, pgm float64, intercom map[string]float64) {
	msg := map[string]any{"type": "volumes", "pgm": pgm, "intercom": intercom}
	h.broadcastControls(channelID, msg)
}

func (h *deckHub) notifyBrowserVolume(channelID int, adjust deckVolumeAdjust) {
	h.mu.Lock()
	push := h.browsers[channelID]
	h.mu.Unlock()
	if push == nil {
		return
	}
	push(map[string]any{
		"type":   "deck_volume",
		"target": adjust.Target,
		"slot":   adjust.Slot,
		"delta":  adjust.Delta,
	})
}

func (h *deckHub) notifyBrowserHosta(channelID int, active bool) {
	h.mu.Lock()
	push := h.browsers[channelID]
	h.mu.Unlock()
	if push == nil {
		return
	}
	push(map[string]any{
		"type":   "deck_hosta",
		"active": active,
	})
}

func (h *deckHub) broadcastControls(channelID int, msg any) {
	h.mu.Lock()
	set := h.controls[channelID]
	conns := make([]*websocket.Conn, 0, len(set))
	for conn := range set {
		conns = append(conns, conn)
	}
	h.mu.Unlock()
	raw, err := json.Marshal(msg)
	if err != nil {
		return
	}
	for _, conn := range conns {
		_ = conn.WriteMessage(websocket.TextMessage, raw)
	}
}

func (h *deckHub) purgeExpiredLocked() {
	now := time.Now()
	for code, entry := range h.codes {
		if now.After(entry.expiresAt) {
			delete(h.codes, code)
		}
	}
}

func randomDeckCode() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	out := make([]byte, 6)
	for i := range out {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			out[i] = alphabet[0]
			continue
		}
		out[i] = alphabet[n.Int64()]
	}
	return string(out)
}

func intercomToDeckLayout(slots []IntercomSlot) []deckLayoutButton {
	out := make([]deckLayoutButton, 0, len(slots))
	slot := 0
	for _, ic := range slots {
		if !ic.Enabled {
			continue
		}
		label := ic.Name
		if label == "" {
			label = "Intercom"
		}
		out = append(out, deckLayoutButton{
			Slot:    slot,
			Channel: ic.ID,
			Label:   label,
		})
		slot++
	}
	return out
}
