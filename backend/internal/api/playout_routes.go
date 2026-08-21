package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/roc-recording/backend/internal/playout"
)

func registerPlayoutRoutes(r chi.Router, playMgr *playout.Manager) {
	r.Get("/api/playout/devices", func(w http.ResponseWriter, r *http.Request) {
		devs, err := playMgr.Devices()
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, devs)
	})

	r.Get("/api/playout", func(w http.ResponseWriter, r *http.Request) {
		list := playMgr.List()
		sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
		jsonOK(w, list)
	})

	r.Post("/api/playout", func(w http.ResponseWriter, r *http.Request) {
		var body playout.CreateInput
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && r.ContentLength != 0 {
			jsonError(w, "invalid json body", http.StatusBadRequest)
			return
		}
		info, err := playMgr.Create(body)
		if err != nil {
			jsonError(w, err.Error(), http.StatusConflict)
			return
		}
		jsonOK(w, info)
	})

	r.Get("/api/playout/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			jsonError(w, "invalid id", http.StatusBadRequest)
			return
		}
		info, err := playMgr.Get(id)
		if err != nil {
			jsonError(w, err.Error(), http.StatusNotFound)
			return
		}
		jsonOK(w, info)
	})

	r.Put("/api/playout/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			jsonError(w, "invalid id", http.StatusBadRequest)
			return
		}
		var body playout.UpdateInput
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, "invalid json body", http.StatusBadRequest)
			return
		}
		info, err := playMgr.Update(id, body)
		if err != nil {
			jsonError(w, err.Error(), http.StatusConflict)
			return
		}
		jsonOK(w, info)
	})

	r.Delete("/api/playout/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			jsonError(w, "invalid id", http.StatusBadRequest)
			return
		}
		if err := playMgr.Delete(id); err != nil {
			jsonError(w, err.Error(), http.StatusConflict)
			return
		}
		jsonOK(w, map[string]string{"status": "deleted"})
	})

	r.Post("/api/playout/{id}/start", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			jsonError(w, "invalid id", http.StatusBadRequest)
			return
		}
		info, err := playMgr.Start(id)
		if err != nil {
			jsonError(w, err.Error(), http.StatusConflict)
			return
		}
		jsonOK(w, info)
	})

	r.Post("/api/playout/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			jsonError(w, "invalid id", http.StatusBadRequest)
			return
		}
		info, err := playMgr.Stop(id)
		if err != nil {
			jsonError(w, err.Error(), http.StatusConflict)
			return
		}
		jsonOK(w, info)
	})

	r.Get("/api/playout/{id}/logs", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			jsonError(w, "invalid id", http.StatusBadRequest)
			return
		}
		lines, err := playMgr.Logs(id)
		if err != nil {
			jsonError(w, err.Error(), http.StatusNotFound)
			return
		}
		jsonOK(w, map[string]any{"id": id, "lines": lines})
	})
}
