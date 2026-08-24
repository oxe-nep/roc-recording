package playout

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/roc-recording/backend/internal/audiox"
)

const libraryRefPrefix = "lib:"

// EncodeLibraryRef builds a file_id that points at a recordings-library file.
func EncodeLibraryRef(category, name string) string {
	return libraryRefPrefix + url.PathEscape(strings.TrimSpace(category)) + "/" + url.PathEscape(strings.TrimSpace(name))
}

// ParseLibraryRef extracts category/name from a lib: file_id.
func ParseLibraryRef(fileID string) (category, name string, ok bool) {
	fileID = strings.TrimSpace(fileID)
	if !strings.HasPrefix(fileID, libraryRefPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(fileID, libraryRefPrefix)
	i := strings.IndexByte(rest, '/')
	if i <= 0 || i >= len(rest)-1 {
		return "", "", false
	}
	cat, err1 := url.PathUnescape(rest[:i])
	nam, err2 := url.PathUnescape(rest[i+1:])
	if err1 != nil || err2 != nil || cat == "" || nam == "" {
		return "", "", false
	}
	return cat, nam, true
}

// LibraryResolver resolves a recordings-library file to an absolute path.
type LibraryResolver func(category, name string) (absPath string, err error)

// SetLibraryResolver wires recordings library paths into file playout.
func (m *Manager) SetLibraryResolver(fn LibraryResolver) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.libraryResolve = fn
}

// ResolveFilePath returns the on-disk path and a display name for a file_id
// (uploaded media UUID or lib:category/name).
func (m *Manager) ResolveFilePath(fileID string) (path, displayName string, err error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return "", "", fmt.Errorf("no media file selected")
	}
	if cat, name, ok := ParseLibraryRef(fileID); ok {
		m.mu.RLock()
		fn := m.libraryResolve
		m.mu.RUnlock()
		if fn == nil {
			return "", "", fmt.Errorf("recordings library is not available")
		}
		abs, err := fn(cat, name)
		if err != nil {
			return "", "", err
		}
		if _, err := os.Stat(abs); err != nil {
			return "", "", fmt.Errorf("recording file missing on disk")
		}
		return abs, cat + "/" + name, nil
	}
	if m.media == nil {
		return "", "", fmt.Errorf("media store not available")
	}
	abs, err := m.media.Path(fileID)
	if err != nil {
		return "", "", err
	}
	display := fileID
	if it, ok := m.media.Get(fileID); ok {
		display = it.Name
	}
	return abs, display, nil
}

func fileHasAudioStream(ffmpegBin, path string) bool {
	return len(probeAudioChannels(ffmpegBin, path)) > 0
}

func probeAudioChannels(ffmpegBin, path string) []int {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if chs := probeAudioWithFfprobe(ffmpegBin, path); len(chs) > 0 {
		return chs
	}
	cmd := exec.Command(ffmpegBin,
		"-hide_banner",
		"-analyzeduration", "50M",
		"-probesize", "50M",
		"-i", path,
	)
	out, _ := cmd.CombinedOutput()
	return audiox.ParseAudioStreams(string(out))
}

func probeAudioWithFfprobe(ffmpegBin, path string) []int {
	probe := ffprobePath(ffmpegBin)
	cmd := exec.Command(probe,
		"-v", "error",
		"-analyzeduration", "50M",
		"-probesize", "50M",
		"-select_streams", "a",
		"-show_entries", "stream=channels",
		"-of", "csv=p=0",
		path,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}
	var chs []int
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		// csv may be "1" or "1,stereo"
		nStr := line
		if i := strings.IndexByte(line, ','); i >= 0 {
			nStr = line[:i]
		}
		n, err := strconv.Atoi(nStr)
		if err != nil || n < 1 {
			n = 1
		}
		chs = append(chs, n)
	}
	return chs
}

func ffprobePath(ffmpegBin string) string {
	ffmpegBin = strings.TrimSpace(ffmpegBin)
	if ffmpegBin == "" {
		return "ffprobe"
	}
	dir := filepath.Dir(ffmpegBin)
	base := filepath.Base(ffmpegBin)
	name := strings.Replace(base, "ffmpeg", "ffprobe", 1)
	name = strings.Replace(name, "FFmpeg", "FFprobe", 1)
	if dir == "." || dir == "" {
		return name
	}
	return filepath.Join(dir, name)
}

var reFFDuration = regexp.MustCompile(`(?i)Duration:\s*(\d+):(\d+):(\d+(?:\.\d+)?)`)

func probeFileDurationSec(ffmpegBin, path string) float64 {
	if strings.TrimSpace(path) == "" {
		return 0
	}
	cmd := exec.Command(ffmpegBin, "-hide_banner", "-i", path)
	out, _ := cmd.CombinedOutput()
	mm := reFFDuration.FindStringSubmatch(string(out))
	if mm == nil {
		return 0
	}
	h, _ := strconv.Atoi(mm[1])
	m, _ := strconv.Atoi(mm[2])
	s, _ := strconv.ParseFloat(mm[3], 64)
	sec := float64(h*3600+m*60) + s
	if sec < 0.1 {
		return 0
	}
	return sec
}
