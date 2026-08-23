package hls

import (
	"os"
	"path/filepath"
	"strings"
)

// PreviewPaths returns low-latency A/V HLS paths under a channel output directory.
func PreviewPaths(dir string) (playlist, segmentPattern string) {
	return filepath.Join(dir, "preview.m3u8"), filepath.Join(dir, "preview_%03d.ts")
}

// AppendAVPreviewOutputs appends ffmpeg output args for a card preview stream.
func AppendAVPreviewOutputs(args []string, videoMap, audioMap, playlist, segPattern string) []string {
	return append(args,
		"-map", videoMap,
		"-map", audioMap,
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
		"-c:a", "aac",
		"-b:a", "96k",
		"-ar", "48000",
		"-ac", "2",
		"-f", "hls",
		"-hls_time", "0.5",
		"-hls_list_size", "4",
		"-hls_flags", "delete_segments+independent_segments+omit_endlist+program_date_time",
		"-hls_segment_filename", segPattern,
		playlist,
	)
}

// RemovePreviewArtifacts deletes preview HLS and legacy thumb/audio HLS files.
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
			strings.HasPrefix(name, "audio") {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}
