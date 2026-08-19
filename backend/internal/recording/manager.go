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

	stream, ok := m.captureMgr.StreamByID(id)
	if !ok {
		return ChannelInfo{}, fmt.Errorf("channel %d not found in capture manager", id)
	}

	outDir := filepath.Join(m.recordingDir, fmt.Sprintf("%d", id))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return ChannelInfo{}, fmt.Errorf("create recording dir: %w", err)
	}

	ts := time.Now()
	baseName := ts.Format("2006-01-02_15-04-05")
	tsPath := filepath.Join(outDir, baseName+".ts")
	mp4Path := filepath.Join(outDir, baseName+".mp4")

	f, err := os.Create(tsPath)
	if err != nil {
		return ChannelInfo{}, fmt.Errorf("create recording file: %w", err)
	}

	stream.SetRecordingDst(&tsWriter{
		File:      f,
		tsPath:    tsPath,
		mp4Path:   mp4Path,
		ffmpegBin: m.ffmpegBin,
	})

	st.status = StatusRecording
	st.startedAt = ts
	st.filePath = mp4Path

	log.Printf("[recording %d] Started TS capture: %s", id, tsPath)
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

	stream, ok := m.captureMgr.StreamByID(id)
	if ok {
		// Detaching closes the tsWriter which triggers remux
		stream.SetRecordingDst(nil)
	}

	log.Printf("[recording %d] Stopped, remuxing to MP4…", id)
	st.status = StatusIdle
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

// tsWriter wraps an os.File and remuxes the TS to MP4 when closed.
type tsWriter struct {
	*os.File
	tsPath    string
	mp4Path   string
	ffmpegBin string
}

func (t *tsWriter) Write(p []byte) (int, error) { return t.File.Write(p) }

func (t *tsWriter) Close() error {
	err := t.File.Close()
	go remuxToMP4(t.ffmpegBin, t.tsPath, t.mp4Path)
	return err
}

// remuxToMP4 stream-copies a raw TS to MP4 with faststart. No re-encode.
func remuxToMP4(ffmpegBin, tsPath, mp4Path string) {
	log.Printf("[remux] %s → %s", tsPath, mp4Path)
	cmd := exec.Command(ffmpegBin,
		"-y",
		"-i", tsPath,
		"-c", "copy",
		"-movflags", "faststart",
		mp4Path,
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Printf("[remux] error: %v", err)
		return
	}
	_ = os.Remove(tsPath)
	log.Printf("[remux] done: %s", mp4Path)
}
