package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/roc-recording/backend/internal/commentator"
)

type commentatorJoinBody struct {
	Pin string `json:"pin"`
}

func commentatorJoinError(w http.ResponseWriter, err error) {
	body := map[string]any{"error": err.Error()}
	switch {
	case errors.Is(err, commentator.ErrPinRequired):
		body["pin_required"] = true
	case errors.Is(err, commentator.ErrInvalidPin):
		body["invalid_pin"] = true
	case errors.Is(err, commentator.ErrExpiredToken):
		body["expired"] = true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(body)
}

func registerCommentatorPublicRoutes(r chi.Router, commMgr *commentator.Manager) {
	if commMgr == nil {
		return
	}
	joinHandler := func(w http.ResponseWriter, r *http.Request) {
		if !commMgr.AllowJoin(r) {
			jsonError(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		token := chi.URLParam(r, "token")
		pin := strings.TrimSpace(r.URL.Query().Get("pin"))
		if pin == "" && r.Method == http.MethodPost {
			var body commentatorJoinBody
			if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
				pin = strings.TrimSpace(body.Pin)
			}
		}
		info, err := commMgr.JoinInfo(token, pin)
		if err != nil {
			commentatorJoinError(w, err)
			return
		}
		jsonOK(w, info)
	}
	r.Get("/api/commentator/join/{token}", joinHandler)
	r.Post("/api/commentator/join/{token}", joinHandler)
	r.Post("/api/commentator/deck/claim", func(w http.ResponseWriter, r *http.Request) {
		commMgr.ServeDeckClaim(w, r)
	})
	r.Get("/api/commentator/join/{token}/deck-status", func(w http.ResponseWriter, r *http.Request) {
		commMgr.ServeDeckStatus(w, r, chi.URLParam(r, "token"))
	})
	serveCommentatorControls := func(w http.ResponseWriter, r *http.Request) {
		commMgr.ServeControls(w, r, chi.URLParam(r, "token"))
	}
	serveCommentatorWS := func(w http.ResponseWriter, r *http.Request) {
		commMgr.ServeSignaling(w, r, chi.URLParam(r, "token"))
	}
	// Controls must be registered before the generic signaling route.
	r.Get("/ws/commentator/{token}/controls", serveCommentatorControls)
	// Primary path: nginx already upgrades WebSockets on location /ws.
	r.Get("/ws/commentator/{token}", serveCommentatorWS)
	// Legacy alias (older frontends / cached join responses).
	r.Get("/api/commentator/ws/{token}", serveCommentatorWS)
}

func registerCommentatorRoutes(r chi.Router, commMgr *commentator.Manager) {
	if commMgr == nil {
		return
	}

	r.Get("/api/commentator/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			jsonError(w, "invalid channel id", http.StatusBadRequest)
			return
		}
		jsonOK(w, commMgr.Get(id))
	})

	r.Get("/api/commentator/{id}/settings", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			jsonError(w, "invalid channel id", http.StatusBadRequest)
			return
		}
		jsonOK(w, commMgr.GetSettings(id))
	})

	r.Put("/api/commentator/{id}/settings", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			jsonError(w, "invalid channel id", http.StatusBadRequest)
			return
		}
		var body commentator.SettingsUpdateInput
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, "invalid json body", http.StatusBadRequest)
			return
		}
		cfg, err := commMgr.UpdateSettings(id, body)
		if err != nil {
			jsonError(w, err.Error(), http.StatusConflict)
			return
		}
		jsonOK(w, cfg)
	})

	r.Post("/api/commentator/{id}/session", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			jsonError(w, "invalid channel id", http.StatusBadRequest)
			return
		}
		info, err := commMgr.CreateSession(id)
		if err != nil {
			jsonError(w, err.Error(), http.StatusConflict)
			return
		}
		jsonOK(w, info)
	})

	r.Delete("/api/commentator/{id}/session", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			jsonError(w, "invalid channel id", http.StatusBadRequest)
			return
		}
		commMgr.RevokeSession(id)
		jsonOK(w, commMgr.Get(id))
	})
}
