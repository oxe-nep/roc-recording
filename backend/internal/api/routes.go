package api

import (
	"encoding/json"
	"log"
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
	"github.com/roc-recording/backend/internal/playout"
	"github.com/roc-recording/backend/internal/recording"
	"github.com/roc-recording/backend/internal/srt"
	"github.com/roc-recording/backend/internal/sysmetrics"
	"github.com/roc-recording/backend/internal/tcloop"
)

type streamResponse struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
	Format       string `json:"format,omitempty"`
	EncodePreset string `json:"encode_preset"`
	HLSURL       string `json:"hls_url"`
}

type encodePresetResponse struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	VideoCodec   string `json:"video_codec"`
	VideoBitrate string `json:"video_bitrate"`
	VideoMaxrate string `json:"video_maxrate"`
	VideoBufsize string `json:"video_bufsize"`
	VideoPreset  string `json:"video_preset"`
	VideoGOP     int    `json:"video_gop"`
	AudioBitrate string `json:"audio_bitrate"`
}

type recordingFileResponse struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	URL     string    `json:"url"`
}

func NewRouter(mgr *capture.Manager, recMgr *recording.Manager, srtMgr *srt.Manager, playMgr *playout.Manager, tcMgr *tcloop.Manager, hlsHandler *hlshandler.Handler, apiKey, allowedOrigins, hlsBaseURL string, metrics *sysmetrics.Collector) http.Handler {
	r := chi.NewRouter()
	r.Use(quietRequestLogger())
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
		if tcMgr != nil {
			if l, r2, ok := tcMgr.AudioLevels(id); ok {
				w.Header().Set("Access-Control-Allow-Origin", allowedOrigins)
				w.Header().Set("Cache-Control", "no-cache, no-store")
				jsonOK(w, map[string]float64{"l": l, "r": r2})
				return
			}
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
	r.Get("/audio/playout/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if tcMgr != nil {
			if l, r2, ok := tcMgr.AudioLevels(id); ok {
				w.Header().Set("Access-Control-Allow-Origin", allowedOrigins)
				w.Header().Set("Cache-Control", "no-cache, no-store")
				jsonOK(w, map[string]float64{"l": l, "r": r2})
				return
			}
		}
		l, r2, ok := playMgr.AudioLevels(id)
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
		if tcMgr != nil && tcMgr.IsRunning(id) {
			w.Header().Set("Access-Control-Allow-Origin", allowedOrigins)
			w.Header().Set("Cache-Control", "no-cache, no-store")
			w.Header().Set("Content-Type", "image/jpeg")
			http.ServeFile(w, r, tcMgr.EncodeThumbPath(id))
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
	r.Get("/playout/audio/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if tcMgr != nil {
			if l, r2, ok := tcMgr.AudioLevels(id); ok {
				w.Header().Set("Access-Control-Allow-Origin", allowedOrigins)
				w.Header().Set("Cache-Control", "no-cache, no-store")
				jsonOK(w, map[string]float64{"l": l, "r": r2})
				return
			}
		}
		l, r2, ok := playMgr.AudioLevels(id)
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigins)
		w.Header().Set("Cache-Control", "no-cache, no-store")
		jsonOK(w, map[string]float64{"l": l, "r": r2})
	})
	r.Get("/playout/thumb/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(chi.URLParam(r, "id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if tcMgr != nil && tcMgr.IsRunning(id) {
			w.Header().Set("Access-Control-Allow-Origin", allowedOrigins)
			w.Header().Set("Cache-Control", "no-cache, no-store")
			w.Header().Set("Content-Type", "image/jpeg")
			http.ServeFile(w, r, tcMgr.PlayoutThumbPath(id))
			return
		}
		status, ok := playMgr.StatusByID(id)
		if !ok || (status != playout.StatusRunning && status != playout.StatusWaiting) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigins)
		w.Header().Set("Cache-Control", "no-cache, no-store")
		w.Header().Set("Content-Type", "image/jpeg")
		http.ServeFile(w, r, playMgr.ThumbPath(id))
	})

	// API – requires API key
	r.Group(func(r chi.Router) {
		r.Use(apiKeyMiddleware(apiKey))

		registerPlayoutRoutes(r, playMgr, tcMgr)

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
			_, _ = srtMgr.Stop(id)
			if err := mgr.Stop(id); err != nil {
				jsonError(w, err.Error(), http.StatusConflict)
				return
			}
			jsonOK(w, map[string]string{"status": "stopped"})
		})

		r.Get("/api/streams/{id}/logs", func(w http.ResponseWriter, r *http.Request) {
			id, err := strconv.Atoi(chi.URLParam(r, "id"))
			if err != nil {
				jsonError(w, "invalid channel id", http.StatusBadRequest)
				return
			}
			lines, ok := mgr.Logs(id)
			if !ok {
				jsonError(w, "channel not found", http.StatusNotFound)
				return
			}
			jsonOK(w, map[string]any{"id": id, "lines": lines})
		})

		r.Get("/api/srt", func(w http.ResponseWriter, r *http.Request) {
			list := srtMgr.ListAll()
			sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
			jsonOK(w, list)
		})

		r.Get("/api/srt/{id}", func(w http.ResponseWriter, r *http.Request) {
			id, err := strconv.Atoi(chi.URLParam(r, "id"))
			if err != nil {
				jsonError(w, "invalid channel id", http.StatusBadRequest)
				return
			}
			info, err := srtMgr.Get(id)
			if err != nil {
				jsonError(w, err.Error(), http.StatusNotFound)
				return
			}
			jsonOK(w, info)
		})

		r.Put("/api/srt/{id}", func(w http.ResponseWriter, r *http.Request) {
			id, err := strconv.Atoi(chi.URLParam(r, "id"))
			if err != nil {
				jsonError(w, "invalid channel id", http.StatusBadRequest)
				return
			}
			var body srt.UpdateInput
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				jsonError(w, "invalid json body", http.StatusBadRequest)
				return
			}
			info, err := srtMgr.Update(id, body)
			if err != nil {
				jsonError(w, err.Error(), http.StatusConflict)
				return
			}
			jsonOK(w, info)
		})

		r.Post("/api/srt/{id}/start", func(w http.ResponseWriter, r *http.Request) {
			id, err := strconv.Atoi(chi.URLParam(r, "id"))
			if err != nil {
				jsonError(w, "invalid channel id", http.StatusBadRequest)
				return
			}
			info, err := srtMgr.Start(id)
			if err != nil {
				jsonError(w, err.Error(), http.StatusConflict)
				return
			}
			jsonOK(w, info)
		})

		r.Post("/api/srt/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
			id, err := strconv.Atoi(chi.URLParam(r, "id"))
			if err != nil {
				jsonError(w, "invalid channel id", http.StatusBadRequest)
				return
			}
			info, err := srtMgr.Stop(id)
			if err != nil {
				jsonError(w, err.Error(), http.StatusConflict)
				return
			}
			jsonOK(w, info)
		})

		r.Get("/api/encode/presets", func(w http.ResponseWriter, r *http.Request) {
			jsonOK(w, presetsToResponse(mgr.ListPresets()))
		})

		r.Get("/api/encode/options", func(w http.ResponseWriter, r *http.Request) {
			jsonOK(w, map[string]any{"codecs": mgr.ListEncodeOptions()})
		})

		r.Post("/api/encode/presets", func(w http.ResponseWriter, r *http.Request) {
			var body capture.PresetInput
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				jsonError(w, "invalid json body", http.StatusBadRequest)
				return
			}
			p, err := mgr.UpsertPreset(body, true)
			if err != nil {
				jsonError(w, err.Error(), http.StatusConflict)
				return
			}
			jsonOK(w, presetToResponse(p))
		})

		r.Put("/api/encode/presets/{id}", func(w http.ResponseWriter, r *http.Request) {
			var body capture.PresetInput
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				jsonError(w, "invalid json body", http.StatusBadRequest)
				return
			}
			body.ID = chi.URLParam(r, "id")
			p, err := mgr.UpsertPreset(body, false)
			if err != nil {
				jsonError(w, err.Error(), http.StatusConflict)
				return
			}
			jsonOK(w, presetToResponse(p))
		})

		r.Delete("/api/encode/presets/{id}", func(w http.ResponseWriter, r *http.Request) {
			id := chi.URLParam(r, "id")
			// Block delete while any channel is recording (they may be remuxing that encode).
			for _, info := range recMgr.ListAll() {
				if info.Status == recording.StatusRecording {
					jsonError(w, "stop all recordings before deleting a preset", http.StatusConflict)
					return
				}
			}
			if err := mgr.DeletePreset(id); err != nil {
				jsonError(w, err.Error(), http.StatusConflict)
				return
			}
			jsonOK(w, map[string]string{"status": "deleted"})
		})

		r.Put("/api/streams/{id}/encode-preset", func(w http.ResponseWriter, r *http.Request) {
			id, err := strconv.Atoi(chi.URLParam(r, "id"))
			if err != nil {
				jsonError(w, "invalid channel id", http.StatusBadRequest)
				return
			}
			var body struct {
				Preset string `json:"preset"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Preset == "" {
				jsonError(w, "invalid json body (need preset)", http.StatusBadRequest)
				return
			}
			if recMgr.IsRecording(id) {
				jsonError(w, "stop recording before changing encode preset", http.StatusConflict)
				return
			}
			if err := mgr.SetEncodePreset(id, body.Preset); err != nil {
				jsonError(w, err.Error(), http.StatusBadRequest)
				return
			}
			s, ok := mgr.StreamByID(id)
			if !ok {
				jsonError(w, "channel not found", http.StatusNotFound)
				return
			}
			jsonOK(w, toResponse(s, hlsBaseURL))
		})

		// Recording endpoints
		r.Get("/api/system", func(w http.ResponseWriter, r *http.Request) {
			jsonOK(w, metrics.Snapshot())
		})

		r.Get("/api/recordings", func(w http.ResponseWriter, r *http.Request) {
			infos := recMgr.ListAll()
			sort.Slice(infos, func(i, j int) bool { return infos[i].ID < infos[j].ID })
			jsonOK(w, infos)
		})

		r.Put("/api/recordings/{id}/name", func(w http.ResponseWriter, r *http.Request) {
			id, err := strconv.Atoi(chi.URLParam(r, "id"))
			if err != nil {
				jsonError(w, "invalid channel id", http.StatusBadRequest)
				return
			}
			var body struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				jsonError(w, "invalid json body", http.StatusBadRequest)
				return
			}
			info, err := recMgr.SetName(id, body.Name)
			if err != nil {
				jsonError(w, err.Error(), http.StatusBadRequest)
				return
			}
			jsonOK(w, info)
		})

		r.Put("/api/recordings/{id}/category", func(w http.ResponseWriter, r *http.Request) {
			id, err := strconv.Atoi(chi.URLParam(r, "id"))
			if err != nil {
				jsonError(w, "invalid channel id", http.StatusBadRequest)
				return
			}
			var body struct {
				Category string `json:"category"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Category == "" {
				jsonError(w, "invalid json body (need category)", http.StatusBadRequest)
				return
			}
			info, err := recMgr.SetCategory(id, body.Category)
			if err != nil {
				jsonError(w, err.Error(), http.StatusConflict)
				return
			}
			jsonOK(w, info)
		})

		// Global library: categories = folders under recordings_dir
		r.Get("/api/library/categories", func(w http.ResponseWriter, r *http.Request) {
			cats, err := recMgr.ListCategories()
			if err != nil {
				jsonError(w, err.Error(), http.StatusInternalServerError)
				return
			}
			jsonOK(w, cats)
		})

		r.Post("/api/library/categories", func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				jsonError(w, "invalid json body", http.StatusBadRequest)
				return
			}
			cat, err := recMgr.CreateCategory(body.Name)
			if err != nil {
				jsonError(w, err.Error(), http.StatusConflict)
				return
			}
			jsonOK(w, cat)
		})

		r.Put("/api/library/categories/{name}", func(w http.ResponseWriter, r *http.Request) {
			oldName := chi.URLParam(r, "name")
			var body struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				jsonError(w, "invalid json body", http.StatusBadRequest)
				return
			}
			cat, err := recMgr.RenameCategory(oldName, body.Name)
			if err != nil {
				jsonError(w, err.Error(), http.StatusConflict)
				return
			}
			jsonOK(w, cat)
		})

		r.Delete("/api/library/categories/{name}", func(w http.ResponseWriter, r *http.Request) {
			if err := recMgr.DeleteCategory(chi.URLParam(r, "name")); err != nil {
				jsonError(w, err.Error(), http.StatusConflict)
				return
			}
			jsonOK(w, map[string]string{"status": "deleted"})
		})

		r.Get("/api/library/files", func(w http.ResponseWriter, r *http.Request) {
			files, err := recMgr.ListLibraryFiles(r.URL.Query().Get("category"))
			if err != nil {
				jsonError(w, err.Error(), http.StatusInternalServerError)
				return
			}
			jsonOK(w, files)
		})

		r.Get("/api/library/file/{category}/{name}", func(w http.ResponseWriter, r *http.Request) {
			path, err := recMgr.LibraryFilePath(chi.URLParam(r, "category"), chi.URLParam(r, "name"))
			if err != nil {
				jsonError(w, err.Error(), http.StatusBadRequest)
				return
			}
			f, err := os.Open(path)
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
			if r.URL.Query().Get("download") == "1" {
				w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(info.Name()))
			}
			http.ServeContent(w, r, info.Name(), info.ModTime(), f)
		})

		r.Delete("/api/library/file/{category}/{name}", func(w http.ResponseWriter, r *http.Request) {
			cat := chi.URLParam(r, "category")
			name := chi.URLParam(r, "name")
			if playMgr.MediaInUse(playout.EncodeLibraryRef(cat, name)) {
				jsonError(w, "file is in use by an active decode channel", http.StatusConflict)
				return
			}
			if err := recMgr.DeleteLibraryFile(cat, name); err != nil {
				status := http.StatusInternalServerError
				if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "invalid") {
					status = http.StatusBadRequest
				}
				jsonError(w, err.Error(), status)
				return
			}
			jsonOK(w, map[string]string{"status": "deleted"})
		})

		r.Post("/api/library/move", func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				FromCategory string `json:"from_category"`
				ToCategory   string `json:"to_category"`
				Name         string `json:"name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				jsonError(w, "invalid json body", http.StatusBadRequest)
				return
			}
			file, err := recMgr.MoveLibraryFile(body.FromCategory, body.ToCategory, body.Name)
			if err != nil {
				jsonError(w, err.Error(), http.StatusConflict)
				return
			}
			jsonOK(w, file)
		})

		r.Get("/api/settings/recordings-path", func(w http.ResponseWriter, r *http.Request) {
			jsonOK(w, map[string]string{"path": recMgr.RecordingDir()})
		})

		r.Put("/api/settings/recordings-path", func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Path string `json:"path"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				jsonError(w, "invalid json body", http.StatusBadRequest)
				return
			}
			path, err := recMgr.SetRecordingDir(body.Path)
			if err != nil {
				jsonError(w, err.Error(), http.StatusConflict)
				return
			}
			metrics.SetDiskPath(path)
			jsonOK(w, map[string]string{"path": path})
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
			dir := filepath.Join(recMgr.RecordingDir(), strconv.Itoa(id))
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
			fullPath := filepath.Join(recMgr.RecordingDir(), strconv.Itoa(id), name)
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

		r.Delete("/api/recordings/file/{id}/{name}", func(w http.ResponseWriter, r *http.Request) {
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
			fullPath := filepath.Join(recMgr.RecordingDir(), strconv.Itoa(id), name)
			if err := os.Remove(fullPath); err != nil {
				if os.IsNotExist(err) {
					jsonError(w, "file not found", http.StatusNotFound)
					return
				}
				jsonError(w, "failed to delete file", http.StatusInternalServerError)
				return
			}
			jsonOK(w, map[string]string{"status": "deleted"})
		})
	})

	return r
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// quietRequestLogger skips high-frequency polling endpoints to keep logs readable.
func quietRequestLogger() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if shouldSkipRequestLog(r) {
				next.ServeHTTP(w, r)
				return
			}

			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)

			if sw.status >= 400 || r.Method != http.MethodGet {
				log.Printf("%s %s %s %d %s",
					r.RemoteAddr, r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Microsecond))
			}
		})
	}
}

func shouldSkipRequestLog(r *http.Request) bool {
	if r.Method == http.MethodOptions {
		return true
	}
	if r.Method != http.MethodGet {
		return false
	}
	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/thumb/"),
		strings.HasPrefix(path, "/audio/"),
		strings.HasPrefix(path, "/hls/"):
		return true
	case path == "/api/streams", path == "/api/recordings", path == "/api/srt", path == "/api/system", path == "/api/encode/presets",
		path == "/api/encode/options",
		path == "/api/library/categories", path == "/api/library/files",
		path == "/api/playout", path == "/api/playout/devices", path == "/api/playout/media":
		return true
	case strings.HasPrefix(path, "/api/streams/") && strings.HasSuffix(path, "/logs"):
		return true
	case strings.HasPrefix(path, "/api/playout/") && strings.HasSuffix(path, "/logs"):
		return true
	case strings.HasPrefix(path, "/api/playout/") && strings.HasSuffix(path, "/audio"):
		return true
	case strings.HasPrefix(path, "/api/playout/") && strings.HasSuffix(path, "/tc-loop"):
		return true
	case strings.HasPrefix(path, "/api/playout/") && !strings.Contains(path[len("/api/playout/"):], "/"):
		// GET /api/playout/{id}
		return true
	case strings.HasPrefix(path, "/playout/thumb/"), strings.HasPrefix(path, "/playout/audio/"):
		return true
	case strings.HasPrefix(path, "/api/recordings/files/"):
		return true
	default:
		return false
	}
}

func corsMiddleware(allowedOrigins string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", allowedOrigins)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
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
			got := r.Header.Get("X-API-Key")
			if got == "" {
				got = r.URL.Query().Get("api_key")
			}
			if got != key {
				jsonError(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func toResponse(s *capture.Stream, hlsBaseURL string) streamResponse {
	status, errStr, format, preset := s.Snapshot()
	return streamResponse{
		ID:           s.ID,
		Name:         s.Name,
		Status:       string(status),
		Error:        errStr,
		Format:       format,
		EncodePreset: preset,
		HLSURL:       hlsBaseURL + "/hls/" + strconv.Itoa(s.ID) + "/index.m3u8",
	}
}

func presetToResponse(p capture.NamedPreset) encodePresetResponse {
	return encodePresetResponse{
		ID:           p.ID,
		Label:        p.Label,
		VideoCodec:   p.Profile.VideoCodec,
		VideoBitrate: p.Profile.VideoBitrate,
		VideoMaxrate: p.Profile.VideoMaxrate,
		VideoBufsize: p.Profile.VideoBufsize,
		VideoPreset:  p.Profile.VideoPreset,
		VideoGOP:     p.Profile.VideoGOP,
		AudioBitrate: p.Profile.AudioBitrate,
	}
}

func presetsToResponse(presets []capture.NamedPreset) []encodePresetResponse {
	resp := make([]encodePresetResponse, 0, len(presets))
	for _, p := range presets {
		resp = append(resp, presetToResponse(p))
	}
	return resp
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
