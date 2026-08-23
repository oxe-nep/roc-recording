package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/roc-recording/backend/internal/playout"
	"github.com/roc-recording/backend/internal/tcloop"
)

func registerPlayoutRoutes(r chi.Router, playMgr *playout.Manager, tcMgr *tcloop.Manager) {
	r.Get("/api/playout/devices", func(w http.ResponseWriter, r *http.Request) {
		refresh := r.URL.Query().Get("refresh") == "1" || r.URL.Query().Get("refresh") == "true"
		devs, err := playMgr.Devices(refresh)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		jsonOK(w, devs)
	})

	r.Get("/api/playout/media", func(w http.ResponseWriter, r *http.Request) {
		jsonOK(w, playMgr.Media().List())
	})

	r.Post("/api/playout/media", func(w http.ResponseWriter, r *http.Request) {
		// Cap total request body (16 GiB) so ParseMultipartForm cannot fill the disk unbounded.
		r.Body = http.MaxBytesReader(w, r.Body, 16<<30)
		const maxMem = 64 << 20 // 64 MiB in-memory; remainder spills to temp files
		if err := r.ParseMultipartForm(maxMem); err != nil {
			jsonError(w, "invalid multipart form: "+err.Error(), http.StatusBadRequest)
			return
		}
		file, hdr, err := r.FormFile("file")
		if err != nil {
			jsonError(w, "file field required", http.StatusBadRequest)
			return
		}
		defer file.Close()
		item, err := playMgr.Media().Add(hdr.Filename, file, hdr.Size)
		if err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonOK(w, item)
	})

	r.Delete("/api/playout/media/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if playMgr.MediaInUse(id) {
			jsonError(w, "media is in use by an active file playout channel", http.StatusConflict)
			return
		}
		if err := playMgr.Media().Delete(id); err != nil {
			jsonError(w, err.Error(), http.StatusNotFound)
			return
		}
		jsonOK(w, map[string]string{"status": "deleted"})
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

	r.Post("/api/playout/{id}/pause", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			jsonError(w, "invalid id", http.StatusBadRequest)
			return
		}
		info, err := playMgr.Pause(id)
		if err != nil {
			jsonError(w, err.Error(), http.StatusConflict)
			return
		}
		jsonOK(w, info)
	})

	r.Post("/api/playout/{id}/resume", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			jsonError(w, "invalid id", http.StatusBadRequest)
			return
		}
		info, err := playMgr.Resume(id)
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

	r.Get("/api/playout/{id}/audio", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			jsonError(w, "invalid id", http.StatusBadRequest)
			return
		}
		l, r2, ok := playMgr.AudioLevels(id)
		if !ok {
			jsonError(w, "decode client not active", http.StatusNotFound)
			return
		}
		jsonOK(w, map[string]float64{"l": l, "r": r2})
	})

	r.Get("/api/playout/{id}/tc-loop", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			jsonError(w, "invalid id", http.StatusBadRequest)
			return
		}
		if tcMgr == nil {
			jsonError(w, "TC Burn-in not available", http.StatusNotFound)
			return
		}
		info, err := tcMgr.Get(id)
		if err != nil {
			jsonError(w, err.Error(), http.StatusNotFound)
			return
		}
		jsonOK(w, info)
	})

	r.Put("/api/playout/{id}/tc-loop", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			jsonError(w, "invalid id", http.StatusBadRequest)
			return
		}
		if tcMgr == nil {
			jsonError(w, "TC Burn-in not available", http.StatusNotFound)
			return
		}
		var body tcloop.UpdateInput
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, "invalid json body", http.StatusBadRequest)
			return
		}
		info, err := tcMgr.Update(id, body)
		if err != nil {
			jsonError(w, err.Error(), http.StatusConflict)
			return
		}
		jsonOK(w, info)
	})
}
