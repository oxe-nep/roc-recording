package recording

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/roc-recording/backend/internal/capture"
)

type RecordingStatus string

const (
	StatusIdle      RecordingStatus = "idle"
	StatusRecording RecordingStatus = "recording"
)

// FFmpeg progress lines look like:
// frame= 100 fps=50 ... time=00:00:02.00 bitrate=5120.5kbits/s speed=1.0x
var (
	reFFTime    = regexp.MustCompile(`time=(\d+):(\d+):(\d+(?:\.\d+)?)`)
	reFFBitrate = regexp.MustCompile(`bitrate=\s*([0-9.]+)kbits/s`)
)

type recState struct {
	mu           sync.Mutex
	status       RecordingStatus
	startedAt    time.Time
	filePath     string
	label        string // user-facing recording name prefix
	cmd          *exec.Cmd
	elapsedSec   float64
	bitrateKbps  float64
}

type Manager struct {
	mu           sync.RWMutex
	states       map[int]*recState
	captureMgr   *capture.Manager
	recordingDir string
	ffmpegBin    string
}

func NewManager(recordingDir, ffmpegBin string, captureMgr *capture.Manager) *Manager {
	return &Manager{
		states:       make(map[int]*recState),
		captureMgr:   captureMgr,
		recordingDir: recordingDir,
		ffmpegBin:    ffmpegBin,
	}
}

func (m *Manager) Register(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[id] = &recState{status: StatusIdle, label: fmt.Sprintf("ch%d", id)}
}

type ChannelInfo struct {
	ID          int             `json:"id"`
	Status      RecordingStatus `json:"status"`
	Name        string          `json:"name"`
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	FilePath    string          `json:"file_path,omitempty"`
	ElapsedSec  float64         `json:"elapsed_sec,omitempty"`
	BitrateKbps float64         `json:"bitrate_kbps,omitempty"`
}

func (m *Manager) buildInfo(id int, st *recState) ChannelInfo {
	info := ChannelInfo{
		ID:     id,
		Status: st.status,
		Name:   st.label,
	}
	if st.status == StatusRecording {
		t := st.startedAt
		info.StartedAt = &t
		info.FilePath = st.filePath
		info.ElapsedSec = st.elapsedSec
		info.BitrateKbps = st.bitrateKbps
		// Fallback wall-clock if FFmpeg has not reported time yet.
		if info.ElapsedSec <= 0 && !st.startedAt.IsZero() {
			info.ElapsedSec = time.Since(st.startedAt).Seconds()
		}
	}
	return info
}

func (m *Manager) ListAll() []ChannelInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ChannelInfo, 0, len(m.states))
	for id, st := range m.states {
		st.mu.Lock()
		out = append(out, m.buildInfo(id, st))
		st.mu.Unlock()
	}
	return out
}

func (m *Manager) SetName(id int, name string) (ChannelInfo, error) {
	m.mu.RLock()
	st, ok := m.states[id]
	m.mu.RUnlock()
	if !ok {
		return ChannelInfo{}, fmt.Errorf("channel %d not found", id)
	}
	clean := sanitizeLabel(name)
	if clean == "" {
		return ChannelInfo{}, fmt.Errorf("invalid recording name")
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.label = clean
	return m.buildInfo(id, st), nil
}

func (m *Manager) Start(id int) (ChannelInfo, error) {
	m.mu.RLock()
	st, ok := m.states[id]
	m.mu.RUnlock()
	if !ok {
		return ChannelInfo{}, fmt.Errorf("channel %d not found", id)
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	if st.status == StatusRecording {
		return ChannelInfo{}, fmt.Errorf("channel %d is already recording", id)
	}

	feedURL, ok := m.captureMgr.FeedURL(id)
	if !ok {
		return ChannelInfo{}, fmt.Errorf("channel %d has no feed url", id)
	}

	outDir := filepath.Join(m.recordingDir, fmt.Sprintf("%d", id))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return ChannelInfo{}, fmt.Errorf("create recording dir: %w", err)
	}

	ts := time.Now()
	label := st.label
	if label == "" {
		label = fmt.Sprintf("ch%d", id)
	}
	baseName := fmt.Sprintf("%s_%s", label, ts.Format("2006-01-02_15-04-05"))
	mp4Path := filepath.Join(outDir, baseName+".mp4")
	args := []string{
		"-y",
		"-fflags", "+genpts",
		"-analyzeduration", "3M",
		"-probesize", "3M",
		"-i", feedURL,
		"-vf", "format=yuv420p",
		"-c:v", "h264_nvenc",
		"-b:v", "10M",
		"-maxrate", "12M",
		"-bufsize", "20M",
		"-preset", "p4",
		"-g", "50",
		"-forced-idr", "1",
		"-c:a", "aac",
		"-b:a", "192k",
		"-ar", "48000",
		"-ac", "2",
		"-movflags", "frag_keyframe+empty_moov+default_base_moof",
		mp4Path,
	}
	cmd := exec.Command(m.ffmpegBin, args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return ChannelInfo{}, fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return ChannelInfo{}, fmt.Errorf("start recording ffmpeg: %w", err)
	}

	st.status = StatusRecording
	st.startedAt = ts
	st.filePath = mp4Path
	st.cmd = cmd
	st.elapsedSec = 0
	st.bitrateKbps = 0

	go m.watchProgress(id, st, stderr)
	go func(chID int, state *recState, c *exec.Cmd) {
		err := c.Wait()
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.cmd == c {
			state.cmd = nil
			state.status = StatusIdle
			state.elapsedSec = 0
			state.bitrateKbps = 0
		}
		if err != nil {
			log.Printf("[recording %d] FFmpeg exited with error: %v", chID, err)
		}
	}(id, st, cmd)

	log.Printf("[recording %d] Started MP4 recording: %s", id, mp4Path)
	return m.buildInfo(id, st), nil
}

func (m *Manager) watchProgress(id int, st *recState, stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		elapsed, bitrate, ok := parseProgress(line)
		if !ok {
			if strings.Contains(line, "Error") || strings.Contains(line, "error:") {
				log.Printf("[recording %d] %s", id, line)
			}
			continue
		}
		st.mu.Lock()
		if st.status == StatusRecording {
			if elapsed > 0 {
				st.elapsedSec = elapsed
			}
			if bitrate > 0 {
				st.bitrateKbps = bitrate
			}
		}
		st.mu.Unlock()
	}
}

func parseProgress(line string) (elapsedSec, bitrateKbps float64, ok bool) {
	tm := reFFTime.FindStringSubmatch(line)
	br := reFFBitrate.FindStringSubmatch(line)
	if tm == nil && br == nil {
		return 0, 0, false
	}
	if tm != nil {
		h, _ := strconv.ParseFloat(tm[1], 64)
		m, _ := strconv.ParseFloat(tm[2], 64)
		s, _ := strconv.ParseFloat(tm[3], 64)
		elapsedSec = h*3600 + m*60 + s
		ok = true
	}
	if br != nil {
		bitrateKbps, _ = strconv.ParseFloat(br[1], 64)
		ok = true
	}
	return elapsedSec, bitrateKbps, ok
}

func (m *Manager) Stop(id int) (ChannelInfo, error) {
	m.mu.RLock()
	st, ok := m.states[id]
	m.mu.RUnlock()
	if !ok {
		return ChannelInfo{}, fmt.Errorf("channel %d not found", id)
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	if st.status != StatusRecording {
		return ChannelInfo{}, fmt.Errorf("channel %d is not recording", id)
	}

	if st.cmd != nil && st.cmd.Process != nil {
		if err := st.cmd.Process.Signal(os.Interrupt); err != nil {
			_ = st.cmd.Process.Kill()
		}
	}
	st.cmd = nil
	st.status = StatusIdle
	st.elapsedSec = 0
	st.bitrateKbps = 0
	log.Printf("[recording %d] Stop requested", id)
	return m.buildInfo(id, st), nil
}

func (m *Manager) StartAll() []error {
	m.mu.RLock()
	ids := make([]int, 0, len(m.states))
	for id := range m.states {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	var errs []error
	for _, id := range ids {
		if _, err := m.Start(id); err != nil {
			errs = append(errs, fmt.Errorf("ch%d: %w", id, err))
		}
	}
	return errs
}

func (m *Manager) StopAll() []error {
	m.mu.RLock()
	ids := make([]int, 0, len(m.states))
	for id := range m.states {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	var errs []error
	for _, id := range ids {
		if _, err := m.Stop(id); err != nil {
			errs = append(errs, fmt.Errorf("ch%d: %w", id, err))
		}
	}
	return errs
}

// sanitizeLabel keeps filesystem-safe characters for recording filename prefixes.
func sanitizeLabel(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_-")
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}
