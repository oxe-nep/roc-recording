package capture

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Status string

const (
	StatusStopped Status = "stopped"
	StatusRunning Status = "running"
	StatusError   Status = "error"
)

type Stream struct {
	ID          int
	Name        string
	Status      Status
	Error       string
	ffmpegInput string
	cmd         *exec.Cmd
	stopCh      chan struct{}
	mu          sync.Mutex
}

type Manager struct {
	streams    map[int]*Stream
	mu         sync.RWMutex
	hlsDir     string
	ffmpegBin  string
	videoCodec string
}

func NewManager(hlsDir, ffmpegBin, videoCodec string) *Manager {
	return &Manager{
		streams:    make(map[int]*Stream),
		hlsDir:     hlsDir,
		ffmpegBin:  ffmpegBin,
		videoCodec: videoCodec,
	}
}

func (m *Manager) Register(id int, name, ffmpegInput string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streams[id] = &Stream{
		ID:          id,
		Name:        name,
		Status:      StatusStopped,
		ffmpegInput: ffmpegInput,
	}
}

func (m *Manager) Start(id int) error {
	m.mu.RLock()
	s, ok := m.streams[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("channel %d not found", id)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Status == StatusRunning {
		return fmt.Errorf("channel %d is already running", id)
	}

	s.stopCh = make(chan struct{})
	s.Status = StatusRunning
	s.Error = ""

	go m.runLoop(s)
	return nil
}

func (m *Manager) Stop(id int) error {
	m.mu.RLock()
	s, ok := m.streams[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("channel %d not found", id)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Status != StatusRunning {
		return fmt.Errorf("channel %d is not running", id)
	}

	close(s.stopCh)
	return nil
}

func (m *Manager) List() []*Stream {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*Stream, 0, len(m.streams))
	for _, s := range m.streams {
		result = append(result, s)
	}
	return result
}

func (m *Manager) runLoop(s *Stream) {
	const (
		restartDelay   = 5 * time.Second
		stableAfter    = 10 * time.Second
	)
	consecutiveFails := 0

	for {
		select {
		case <-s.stopCh:
			m.killStream(s)
			s.mu.Lock()
			s.Status = StatusStopped
			s.mu.Unlock()
			return
		default:
		}

		start := time.Now()
		err := m.runFFmpeg(s)

		select {
		case <-s.stopCh:
			s.mu.Lock()
			s.Status = StatusStopped
			s.mu.Unlock()
			return
		default:
		}

		if err == nil {
			// Clean exit (stopped externally)
			consecutiveFails = 0
			continue
		}

		uptime := time.Since(start)
		if uptime >= stableAfter {
			// Ran long enough – likely lost signal, reset fail counter
			consecutiveFails = 0
		} else {
			consecutiveFails++
		}

		delay := restartDelay
		if consecutiveFails > 5 {
			delay = 30 * time.Second
		}

		s.mu.Lock()
		s.Status = StatusError
		s.Error = err.Error()
		s.mu.Unlock()

		log.Printf("[channel %d] FFmpeg exited after %s with error: %v (fail #%d) – restarting in %s",
			s.ID, uptime.Round(time.Millisecond), err, consecutiveFails, delay)

		select {
		case <-s.stopCh:
			s.mu.Lock()
			s.Status = StatusStopped
			s.mu.Unlock()
			return
		case <-time.After(delay):
			s.mu.Lock()
			s.Status = StatusRunning
			s.Error = ""
			s.mu.Unlock()
		}
	}
}

func (m *Manager) runFFmpeg(s *Stream) error {
	outDir := filepath.Join(m.hlsDir, fmt.Sprintf("%d", s.ID))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create HLS directory: %w", err)
	}

	playlist := filepath.Join(outDir, "index.m3u8")
	segPattern := filepath.Join(outDir, "%03d.ts")

	// Build args: configurable input part + encoder + HLS output
	inputArgs := shellSplit(s.ffmpegInput)
	encoder := m.videoCodec
	if encoder == "" {
		encoder = "h264_nvenc"
	}
	encoderArgs := []string{
		"-vf", "scale=1280:720",
		"-c:v", encoder,
	}
	// Codec-specific options
	switch encoder {
	case "h264_nvenc":
		encoderArgs = append(encoderArgs, "-preset", "p1", "-tune", "ll", "-rc", "cbr", "-b:v", "800k")
	case "libx264":
		encoderArgs = append(encoderArgs, "-preset", "ultrafast", "-tune", "zerolatency", "-b:v", "800k")
	default:
		encoderArgs = append(encoderArgs, "-b:v", "800k")
	}
	encoderArgs = append(encoderArgs,
		"-g", "50", "-keyint_min", "50",
		"-c:a", "aac", "-b:a", "64k",
		"-f", "hls",
		"-hls_time", "0.5",
		"-hls_list_size", "6",
		"-hls_flags", "delete_segments+append_list+low_latency",
		"-hls_segment_type", "mpegts",
		"-hls_segment_filename", segPattern,
		playlist,
	)

	args := append(inputArgs, encoderArgs...)
	cmd := exec.Command(m.ffmpegBin, args...)

	s.mu.Lock()
	s.cmd = cmd
	s.mu.Unlock()

	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	// Log stderr
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			log.Printf("[channel %d ffmpeg] %s", s.ID, scanner.Text())
		}
	}()

	// Watch stopCh in parallel
	doneCh := make(chan error, 1)
	go func() { doneCh <- cmd.Wait() }()

	select {
	case <-s.stopCh:
		_ = cmd.Process.Kill()
		return nil
	case err := <-doneCh:
		return err
	}
}

// shellSplit splits a string into tokens respecting single-quoted strings,
// so that device names like 'DeckLink IP 100G (1)' are kept as one argument.
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

func (m *Manager) killStream(s *Stream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}
