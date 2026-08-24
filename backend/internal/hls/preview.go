package hls

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/roc-recording/backend/internal/audiox"
)

// PreviewPaths returns video HLS paths. Segment names include a unique generation
// so a new play cannot reuse cached preview_000.ts from the previous file.
func PreviewPaths(dir string) (playlist, segmentPattern string) {
	gen := time.Now().UnixMilli()
	return filepath.Join(dir, "preview.m3u8"),
		filepath.Join(dir, fmt.Sprintf("pv%d_%%03d.ts", gen))
}

func genFromVideoSeg(segPattern string) int64 {
	base := filepath.Base(segPattern)
	var gen int64
	if _, err := fmt.Sscanf(base, "pv%d", &gen); err != nil || gen <= 0 {
		return time.Now().UnixMilli()
	}
	return gen
}

func listenPaths(dir string, pair int, gen int64) (playlist, segmentPattern string) {
	return filepath.Join(dir, fmt.Sprintf("listen_%d.m3u8", pair)),
		filepath.Join(dir, fmt.Sprintf("l%d_%d_%%03d.ts", gen, pair))
}

func appendHLSFlags(args []string, playlist, segPattern string, segTime string, listSize string) []string {
	return append(args,
		"-f", "hls",
		"-hls_time", segTime,
		"-hls_list_size", listSize,
		"-hls_flags", "delete_segments+independent_segments+omit_endlist+program_date_time+temp_file",
		"-hls_segment_filename", segPattern,
		playlist,
	)
}

// AppendAVPreviewOutputs writes a muted video HLS plus four stereo listen playlists
// (listen_0.m3u8 … listen_3.m3u8 = pairs 1–2 … 7–8). Separate playlists are required
// because a single MPEG-TS HLS file only exposes the first audio PID in hls.js.
func AppendAVPreviewOutputs(args []string, videoMap, playlist, segPattern string) []string {
	out := append(args,
		"-map", videoMap,
		"-an",
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-profile:v", "baseline",
		"-bf", "0",
		"-g", "10",
		"-keyint_min", "10",
		"-sc_threshold", "0",
		"-b:v", "800k",
		"-maxrate", "1000k",
		"-bufsize", "400k",
		"-pix_fmt", "yuv420p",
	)
	out = appendHLSFlags(out, playlist, segPattern, "1", "6")

	dir := filepath.Dir(playlist)
	gen := genFromVideoSeg(segPattern)
	for i, pad := range audiox.PreviewPairMaps() {
		lp, ls := listenPaths(dir, i, gen)
		out = append(out,
			"-map", pad,
			"-vn",
			"-c:a", "aac",
			"-b:a", "128k",
			"-ar", "48000",
			"-ac", "2",
		)
		out = appendHLSFlags(out, lp, ls, "1", "6")
	}
	return out
}

// RemovePreviewArtifacts deletes preview HLS, listen HLS, and legacy thumbs.
func RemovePreviewArtifacts(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		_ = os.Remove(filepath.Join(dir, "thumb.jpg"))
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if name == "thumb.jpg" || ext == ".m3u8" || ext == ".ts" {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}
