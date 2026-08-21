package playout

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
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
	if strings.TrimSpace(path) == "" {
		return false
	}
	cmd := exec.Command(ffmpegBin, "-hide_banner", "-i", path)
	out, _ := cmd.CombinedOutput()
	return strings.Contains(strings.ToLower(string(out)), "audio:")
}
