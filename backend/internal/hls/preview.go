package hls

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/roc-recording/backend/internal/audiox"
)

// PreviewPaths returns low-latency video HLS paths under a channel output directory.
func PreviewPaths(dir string) (playlist, segmentPattern string) {
	return filepath.Join(dir, "preview.m3u8"), filepath.Join(dir, "preview_%03d.ts")
}

func listenPaths(dir string, pair int) (playlist, segmentPattern string) {
	return filepath.Join(dir, fmt.Sprintf("listen_%d.m3u8", pair)),
		filepath.Join(dir, fmt.Sprintf("listen_%d_%%03d.ts", pair))
}

func appendHLSFlags(args []string, playlist, segPattern string) []string {
	return append(args,
		"-f", "hls",
		"-hls_time", "0.5",
		"-hls_list_size", "4",
		"-hls_flags", "delete_segments+independent_segments+omit_endlist+program_date_time",
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
	out = appendHLSFlags(out, playlist, segPattern)

	dir := filepath.Dir(playlist)
	for i, pad := range audiox.PreviewPairMaps() {
		lp, ls := listenPaths(dir, i)
		out = append(out,
			"-map", pad,
			"-vn",
			"-c:a", "aac",
			"-b:a", "96k",
			"-ar", "48000",
			"-ac", "2",
		)
		out = appendHLSFlags(out, lp, ls)
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
		name := e.Name()
		if name == "thumb.jpg" ||
			name == "preview.m3u8" ||
			strings.HasPrefix(name, "preview_") ||
			strings.HasPrefix(name, "listen_") ||
			strings.HasPrefix(name, "audio") {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}
