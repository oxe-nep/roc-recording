package recording

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type RecordingStatus string

const (
	StatusIdle      RecordingStatus = "idle"
	StatusRecording RecordingStatus = "recording"
)

type Recording struct {
	ID        int
	StartedAt time.Time
	FilePath  string
}

type channel struct {
	id          int
	ffmpegInput string
	mu          sync.Mutex
	cmd         *exec.Cmd
	status      RecordingStatus
	startedAt   time.Time
	filePath    string
}

type Manager struct {
	mu          sync.RWMutex
	channels    map[int]*channel
	recordingDir string
	ffmpegBin   string
}

func NewManager(recordingDir, ffmpegBin string) *Manager {
	return &Manager{
		channels:    make(map[int]*channel),
		recordingDir: recordingDir,
		ffmpegBin:   ffmpegBin,
	}
}

func (m *Manager) Register(id int, ffmpegInput string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels[id] = &channel{
		id:          id,
		ffmpegInput: ffmpegInput,
		status:      StatusIdle,
	}
}

type ChannelInfo struct {
	ID        int             `json:"id"`
	Status    RecordingStatus `json:"status"`
	StartedAt *time.Time      `json:"started_at,omitempty"`
	FilePath  string          `json:"file_path,omitempty"`
}

func (m *Manager) Info(id int) (ChannelInfo, bool) {
	m.mu.RLock()
	ch, ok := m.channels[id]
	m.mu.RUnlock()
	if !ok {
		return ChannelInfo{}, false
	}
	ch.mu.Lock()
	defer ch.mu.Unlock()
	info := ChannelInfo{ID: ch.id, Status: ch.status}
	if ch.status == StatusRecording {
		t := ch.startedAt
		info.StartedAt = &t
		info.FilePath = ch.filePath
	}
	return info, true
}

func (m *Manager) ListAll() []ChannelInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ChannelInfo, 0, len(m.channels))
	for _, ch := range m.channels {
		ch.mu.Lock()
		info := ChannelInfo{ID: ch.id, Status: ch.status}
		if ch.status == StatusRecording {
			t := ch.startedAt
			info.StartedAt = &t
			info.FilePath = ch.filePath
		}
		ch.mu.Unlock()
		out = append(out, info)
	}
	return out
}

func (m *Manager) Start(id int) (ChannelInfo, error) {
	m.mu.RLock()
	ch, ok := m.channels[id]
	m.mu.RUnlock()
	if !ok {
		return ChannelInfo{}, fmt.Errorf("channel %d not found", id)
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()

	if ch.status == StatusRecording {
		return ChannelInfo{}, fmt.Errorf("channel %d is already recording", id)
	}

	outDir := filepath.Join(m.recordingDir, fmt.Sprintf("%d", id))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return ChannelInfo{}, fmt.Errorf("create recording dir: %w", err)
	}

	ts := time.Now()
	filename := ts.Format("2006-01-02_15-04-05") + ".mp4"
	filePath := filepath.Join(outDir, filename)

	cmd, err := m.startFFmpeg(ch.ffmpegInput, filePath)
	if err != nil {
		return ChannelInfo{}, fmt.Errorf("start ffmpeg: %w", err)
	}

	ch.cmd = cmd
	ch.status = StatusRecording
	ch.startedAt = ts
	ch.filePath = filePath

	// Watch process in background; update status when it exits
	go func() {
		err := cmd.Wait()
		ch.mu.Lock()
		ch.cmd = nil
		ch.status = StatusIdle
		ch.mu.Unlock()
		if err != nil {
			log.Printf("[recording %d] FFmpeg exited with error: %v", id, err)
		} else {
			log.Printf("[recording %d] FFmpeg finished cleanly: %s", id, filePath)
		}
	}()

	log.Printf("[recording %d] Started: %s", id, filePath)
	t := ch.startedAt
	return ChannelInfo{ID: ch.id, Status: ch.status, StartedAt: &t, FilePath: ch.filePath}, nil
}

func (m *Manager) Stop(id int) (ChannelInfo, error) {
	m.mu.RLock()
	ch, ok := m.channels[id]
	m.mu.RUnlock()
	if !ok {
		return ChannelInfo{}, fmt.Errorf("channel %d not found", id)
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()

	if ch.status != StatusRecording {
		return ChannelInfo{}, fmt.Errorf("channel %d is not recording", id)
	}

	// Send SIGINT so FFmpeg can finalize the MP4 moov atom
	if ch.cmd != nil && ch.cmd.Process != nil {
		if err := ch.cmd.Process.Signal(os.Interrupt); err != nil {
			// Fallback: kill if interrupt not supported (Windows)
			_ = ch.cmd.Process.Kill()
		}
	}

	filePath := ch.filePath
	ch.status = StatusIdle
	log.Printf("[recording %d] Stopped: %s", id, filePath)
	return ChannelInfo{ID: ch.id, Status: StatusIdle}, nil
}

func (m *Manager) StartAll() []error {
	m.mu.RLock()
	ids := make([]int, 0, len(m.channels))
	for id := range m.channels {
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
	ids := make([]int, 0, len(m.channels))
	for id := range m.channels {
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

func (m *Manager) startFFmpeg(ffmpegInput, outputPath string) (*exec.Cmd, error) {
	inputArgs := shellSplit(ffmpegInput)

	// H.264 @ 10 Mbit/s, fragmented MP4 so file is readable if interrupted
	args := []string{"-y"}
	args = append(args, inputArgs...)
	args = append(args,
		"-vf", "yadif=mode=0:deint=interlaced,format=yuv420p",
		"-c:v", "h264_nvenc",
		"-b:v", "10M",
		"-maxrate", "12M",
		"-bufsize", "20M",
		"-preset", "p4",
		"-an",
		"-movflags", "frag_keyframe+empty_moov+default_base_moof",
		outputPath,
	)

	cmd := exec.Command(m.ffmpegBin, args...)
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func shellSplit(s string) []string {
	var args []string
	var current strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' && !inQuote:
			inQuote = true
		case c == '\'' && inQuote:
			inQuote = false
		case c == ' ' && !inQuote:
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(c)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args
}
