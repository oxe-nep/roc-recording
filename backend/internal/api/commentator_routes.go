package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/roc-recording/backend/internal/commentator"
)

func registerCommentatorPublicRoutes(r chi.Router, commMgr *commentator.Manager) {
	if commMgr == nil {
		return
	}
	r.Get("/api/commentator/join/{token}", func(w http.ResponseWriter, r *http.Request) {
		if !commMgr.AllowJoin(r) {
			jsonError(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		token := chi.URLParam(r, "token")
		info, err := commMgr.JoinInfo(token)
		if err != nil {
			jsonError(w, err.Error(), http.StatusUnauthorized)
			return
		}
		jsonOK(w, info)
	})
	serveCommentatorWS := func(w http.ResponseWriter, r *http.Request) {
		commMgr.ServeSignaling(w, r, chi.URLParam(r, "token"))
	}
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
