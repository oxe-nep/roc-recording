package recording

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/roc-recording/backend/internal/capture"
)

type RecordingStatus string

const (
	StatusIdle      RecordingStatus = "idle"
	StatusRecording RecordingStatus = "recording"
)

type recState struct {
	mu        sync.Mutex
	status    RecordingStatus
	startedAt time.Time
	filePath  string // final .mp4 path (after remux)
	cmd       *exec.Cmd
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
	m.states[id] = &recState{status: StatusIdle}
}

type ChannelInfo struct {
	ID        int             `json:"id"`
	Status    RecordingStatus `json:"status"`
	StartedAt *time.Time      `json:"started_at,omitempty"`
	FilePath  string          `json:"file_path,omitempty"`
}

func (m *Manager) buildInfo(id int, st *recState) ChannelInfo {
	info := ChannelInfo{ID: id, Status: st.status}
	if st.status == StatusRecording {
		t := st.startedAt
		info.StartedAt = &t
		info.FilePath = st.filePath
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
	baseName := ts.Format("2006-01-02_15-04-05")
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
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return ChannelInfo{}, fmt.Errorf("start recording ffmpeg: %w", err)
	}

	st.status = StatusRecording
	st.startedAt = ts
	st.filePath = mp4Path
	st.cmd = cmd

	go func(chID int, state *recState, c *exec.Cmd) {
		err := c.Wait()
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.cmd == c {
			state.cmd = nil
			state.status = StatusIdle
		}
		if err != nil {
			log.Printf("[recording %d] FFmpeg exited with error: %v", chID, err)
		}
	}(id, st, cmd)

	log.Printf("[recording %d] Started MP4 recording: %s", id, mp4Path)
	return m.buildInfo(id, st), nil
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

