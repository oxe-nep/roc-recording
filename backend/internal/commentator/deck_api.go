package commentator

import (
	"encoding/json"
	"net/http"
	"strings"
)

type deckClaimRequest struct {
	Code string `json:"code"`
}

type deckClaimResponse struct {
	Origin       string `json:"origin"`
	Token        string `json:"token"`
	Pin          string `json:"pin"`
	ControlsPath string `json:"controls_path"`
}

type deckStatusResponse struct {
	PluginConnected bool `json:"plugin_connected"`
}

func (m *Manager) ClaimDeckPair(code string) (deckClaimResponse, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return deckClaimResponse{}, ErrPinRequired
	}
	entry, ok := m.deck.claim(code)
	if !ok {
		return deckClaimResponse{}, ErrInvalidPin
	}
	return deckClaimResponse{
		Origin:       m.publicBaseURL,
		Token:        entry.token,
		Pin:          entry.pin,
		ControlsPath: controlsPath(entry.token),
	}, nil
}

func (m *Manager) DeckPluginConnected(token, pin string) (bool, error) {
	channelID, err := m.validateTokenAndPIN(token, pin)
	if err != nil {
		return false, err
	}
	return m.deck.controlsConnected(channelID), nil
}

func (m *Manager) ServeDeckClaim(w http.ResponseWriter, r *http.Request) {
	if !m.AllowJoin(r) {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	var body deckClaimRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	resp, err := m.ClaimDeckPair(body.Code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (m *Manager) ServeDeckStatus(w http.ResponseWriter, r *http.Request, token string) {
	if !m.AllowJoin(r) {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	pin := strings.TrimSpace(r.URL.Query().Get("pin"))
	connected, err := m.DeckPluginConnected(token, pin)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(deckStatusResponse{PluginConnected: connected})
}
