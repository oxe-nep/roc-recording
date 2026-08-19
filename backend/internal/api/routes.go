package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/roc-recording/backend/internal/capture"
	hlshandler "github.com/roc-recording/backend/internal/hls"
	"github.com/roc-recording/backend/internal/recording"
)

type streamResponse struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Format string `json:"format,omitempty"`
	HLSURL string `json:"hls_url"`
}

type recordingFileResponse struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	URL     string    `json:"url"`
}

func NewRouter(mgr *capture.Manager, recMgr *recording.Manager, hlsHandler *hlshandler.Handler, recordingsDir, apiKey, allowedOrigins, hlsBaseURL string) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware(allowedOrigins))

	// HLS, thumbnails and audio meters – no API key required
	r.Mount("/hls/", hlsHandler)
	r.Get("/audio/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		l, r2, ok := mgr.AudioLevels(id)
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigins)
		w.Header().Set("Cache-Control", "no-cache, no-store")
		jsonOK(w, map[string]float64{"l": l, "r": r2})
	})
	r.Get("/thumb/{id}", func(w http.ResponseWriter, r *http.Request) {
		idParam := chi.URLParam(r, "id")
		id, err := strconv.Atoi(idParam)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		status, ok := mgr.StatusByID(id)
		if !ok || status != capture.StatusRunning {
			http.NotFound(w, r)
			return
		}
		thumbPath := hlsHandler.ThumbPath(idParam)
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigins)
		w.Header().Set("Cache-Control", "no-cache, no-store")
		w.Header().Set("Content-Type", "image/jpeg")
		http.ServeFile(w, r, thumbPath)
	})

	// API – requires API key
	r.Group(func(r chi.Router) {
		r.Use(apiKeyMiddleware(apiKey))

		r.Get("/api/streams", func(w http.ResponseWriter, r *http.Request) {
			streams := mgr.List()
			sort.Slice(streams, func(i, j int) bool {
				return streams[i].ID < streams[j].ID
			})
			resp := make([]streamResponse, 0, len(streams))
			for _, s := range streams {
				resp = append(resp, toResponse(s, hlsBaseURL))
			}
			jsonOK(w, resp)
		})

		r.Post("/api/streams/{id}/start", func(w http.ResponseWriter, r *http.Request) {
			id, err := strconv.Atoi(chi.URLParam(r, "id"))
			if err != nil {
				jsonError(w, "invalid channel id", http.StatusBadRequest)
				return
			}
			if err := mgr.Start(id); err != nil {
				jsonError(w, err.Error(), http.StatusConflict)
				return
			}
			jsonOK(w, map[string]string{"status": "started"})
		})

		r.Post("/api/streams/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
			id, err := strconv.Atoi(chi.URLParam(r, "id"))
			if err != nil {
				jsonError(w, "invalid channel id", http.StatusBadRequest)
				return
			}
			if err := mgr.Stop(id); err != nil {
				jsonError(w, err.Error(), http.StatusConflict)
				return
			}
			jsonOK(w, map[string]string{"status": "stopped"})
		})

		// Recording endpoints
		r.Get("/api/recordings", func(w http.ResponseWriter, r *http.Request) {
			infos := recMgr.ListAll()
			sort.Slice(infos, func(i, j int) bool { return infos[i].ID < infos[j].ID })
			jsonOK(w, infos)
		})

		r.Post("/api/recordings/{id}/start", func(w http.ResponseWriter, r *http.Request) {
			id, err := strconv.Atoi(chi.URLParam(r, "id"))
			if err != nil {
				jsonError(w, "invalid channel id", http.StatusBadRequest)
				return
			}
			status, ok := mgr.StatusByID(id)
			if !ok {
				jsonError(w, "invalid channel id", http.StatusBadRequest)
				return
			}
			if status != capture.StatusRunning {
				jsonError(w, "channel must be running with valid signal before recording can start", http.StatusConflict)
				return
			}
			info, err := recMgr.Start(id)
			if err != nil {
				jsonError(w, err.Error(), http.StatusConflict)
				return
			}
			jsonOK(w, info)
		})

		r.Post("/api/recordings/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
			id, err := strconv.Atoi(chi.URLParam(r, "id"))
			if err != nil {
				jsonError(w, "invalid channel id", http.StatusBadRequest)
				return
			}
			info, err := recMgr.Stop(id)
			if err != nil {
				jsonError(w, err.Error(), http.StatusConflict)
				return
			}
			jsonOK(w, info)
		})

		r.Post("/api/recordings/start-all", func(w http.ResponseWriter, r *http.Request) {
			errs := recMgr.StartAll()
			if len(errs) > 0 {
				msgs := make([]string, len(errs))
				for i, e := range errs {
					msgs[i] = e.Error()
				}
				jsonOK(w, map[string]any{"started": true, "errors": msgs})
				return
			}
			jsonOK(w, map[string]string{"status": "all started"})
		})

		r.Post("/api/recordings/stop-all", func(w http.ResponseWriter, r *http.Request) {
			errs := recMgr.StopAll()
			if len(errs) > 0 {
				msgs := make([]string, len(errs))
				for i, e := range errs {
					msgs[i] = e.Error()
				}
				jsonOK(w, map[string]any{"stopped": true, "errors": msgs})
				return
			}
			jsonOK(w, map[string]string{"status": "all stopped"})
		})

		r.Get("/api/recordings/files/{id}", func(w http.ResponseWriter, r *http.Request) {
			id, err := strconv.Atoi(chi.URLParam(r, "id"))
			if err != nil || id < 1 {
				jsonError(w, "invalid channel id", http.StatusBadRequest)
				return
			}
			dir := filepath.Join(recordingsDir, strconv.Itoa(id))
			entries, err := os.ReadDir(dir)
			if err != nil {
				if os.IsNotExist(err) {
					jsonOK(w, []recordingFileResponse{})
					return
				}
				jsonError(w, "failed to read recordings directory", http.StatusInternalServerError)
				return
			}

			files := make([]recordingFileResponse, 0, len(entries))
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				name := e.Name()
				if !strings.HasSuffix(strings.ToLower(name), ".mp4") {
					continue
				}
				info, err := e.Info()
				if err != nil {
					continue
				}
				files = append(files, recordingFileResponse{
					Name:    name,
					Size:    info.Size(),
					ModTime: info.ModTime(),
					URL:     "/api/recordings/file/" + strconv.Itoa(id) + "/" + name,
				})
			}
			sort.Slice(files, func(i, j int) bool { return files[i].ModTime.After(files[j].ModTime) })
			jsonOK(w, files)
		})

		r.Get("/api/recordings/file/{id}/{name}", func(w http.ResponseWriter, r *http.Request) {
			id, err := strconv.Atoi(chi.URLParam(r, "id"))
			if err != nil || id < 1 {
				jsonError(w, "invalid channel id", http.StatusBadRequest)
				return
			}
			name := chi.URLParam(r, "name")
			if name == "" || name != filepath.Base(name) || strings.Contains(name, "..") {
				jsonError(w, "invalid file name", http.StatusBadRequest)
				return
			}
			fullPath := filepath.Join(recordingsDir, strconv.Itoa(id), name)
			f, err := os.Open(fullPath)
			if err != nil {
				if os.IsNotExist(err) {
					jsonError(w, "file not found", http.StatusNotFound)
					return
				}
				jsonError(w, "failed to open file", http.StatusInternalServerError)
				return
			}
			defer f.Close()
			info, err := f.Stat()
			if err != nil {
				jsonError(w, "failed to read file info", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "video/mp4")
			w.Header().Set("Cache-Control", "no-store")
			http.ServeContent(w, r, name, info.ModTime(), f)
		})
	})

	return r
}

func corsMiddleware(allowedOrigins string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", allowedOrigins)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func apiKeyMiddleware(key string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-API-Key") != key {
				jsonError(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func toResponse(s *capture.Stream, hlsBaseURL string) streamResponse {
	return streamResponse{
		ID:     s.ID,
		Name:   s.Name,
		Status: string(s.Status),
		Error:  s.Error,
		Format: s.Format,
		HLSURL: hlsBaseURL + "/hls/" + strconv.Itoa(s.ID) + "/index.m3u8",
	}
}

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
