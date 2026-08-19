package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/roc-recording/backend/internal/capture"
	hlshandler "github.com/roc-recording/backend/internal/hls"
)

type streamResponse struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	HLSURL string `json:"hls_url"`
}

func NewRouter(mgr *capture.Manager, hlsHandler *hlshandler.Handler, apiKey, allowedOrigins, hlsBaseURL string) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware(allowedOrigins))

	// HLS and thumbnails – no API key required
	r.Mount("/hls/", hlsHandler)
	r.Get("/thumb/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		thumbPath := hlsHandler.ThumbPath(id)
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
