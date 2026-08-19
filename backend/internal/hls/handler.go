package hls

import (
	"net/http"
	"path/filepath"
	"strings"
)

// Handler serverar HLS-filer med rätt CORS och cache-headers.
type Handler struct {
	hlsDir         string
	allowedOrigins string
}

func NewHandler(hlsDir, allowedOrigins string) *Handler {
	return &Handler{hlsDir: hlsDir, allowedOrigins: allowedOrigins}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", h.allowedOrigins)
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// /hls/{id}/... → strip prefix och serva från hlsDir
	path := strings.TrimPrefix(r.URL.Path, "/hls/")
	full := filepath.Join(h.hlsDir, filepath.FromSlash(path))

	// m3u8-playlister ska aldrig cachas
	if strings.HasSuffix(full, ".m3u8") {
		w.Header().Set("Cache-Control", "no-cache, no-store")
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	} else if strings.HasSuffix(full, ".ts") {
		w.Header().Set("Cache-Control", "max-age=60")
		w.Header().Set("Content-Type", "video/MP2T")
	}

	http.ServeFile(w, r, full)
}
