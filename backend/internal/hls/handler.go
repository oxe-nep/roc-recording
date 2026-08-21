package hls

import (
	"net/http"
	"path/filepath"
	"strings"
)

// Handler serves HLS files with correct CORS and cache headers.
type Handler struct {
	hlsDir         string
	allowedOrigins string
}

func NewHandler(hlsDir, allowedOrigins string) *Handler {
	return &Handler{hlsDir: hlsDir, allowedOrigins: allowedOrigins}
}

func (h *Handler) ThumbPath(id string) string {
	return filepath.Join(h.hlsDir, id, "thumb.jpg")
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", h.allowedOrigins)
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// /hls/{id}/... → strip prefix and serve from hlsDir (never escape root)
	path := strings.TrimPrefix(r.URL.Path, "/hls/")
	if path == "" || strings.Contains(path, "..") {
		http.NotFound(w, r)
		return
	}

	fullAbs, err := filepath.Abs(filepath.Join(h.hlsDir, filepath.FromSlash(path)))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rootAbs, err := filepath.Abs(h.hlsDir)
	if err != nil {
		http.Error(w, "hls root unavailable", http.StatusInternalServerError)
		return
	}
	rel, err := filepath.Rel(rootAbs, fullAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		http.NotFound(w, r)
		return
	}

	if strings.HasSuffix(fullAbs, ".m3u8") {
		w.Header().Set("Cache-Control", "no-cache, no-store")
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	} else if strings.HasSuffix(fullAbs, ".ts") {
		w.Header().Set("Cache-Control", "max-age=60")
		w.Header().Set("Content-Type", "video/MP2T")
	} else if strings.HasSuffix(fullAbs, ".jpg") || strings.HasSuffix(fullAbs, ".jpeg") {
		w.Header().Set("Cache-Control", "no-cache, no-store")
		w.Header().Set("Content-Type", "image/jpeg")
	}

	http.ServeFile(w, r, fullAbs)
}
