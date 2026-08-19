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

func (m *Manager) StatusByID(id int) (Status, bool) {
	m.mu.RLock()
	s, ok := m.streams[id]
	m.mu.RUnlock()
	if !ok {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Status, true
}

func (m *Manager) runLoop(s *Stream) {
	const (
		restartDelay = 3 * time.Second
		stableAfter  = 10 * time.Second
	)
	consecutiveFails := 0

	for {
		select {
		case <-s.stopCh:
			m.killStream(s)
			m.removeThumb(s.ID)
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
			consecutiveFails = 0
			continue
		}

		uptime := time.Since(start)
		if uptime >= stableAfter {
			consecutiveFails = 0
		} else {
			consecutiveFails++
		}

		delay := restartDelay
		if consecutiveFails > 5 {
			delay = 15 * time.Second
		}

		s.mu.Lock()
		s.Status = StatusError
		s.Error = err.Error()
		s.mu.Unlock()
		m.removeThumb(s.ID)

		log.Printf("[channel %d] FFmpeg exited after %s: %v (fail #%d) – restarting in %s",
			s.ID, uptime.Round(time.Millisecond), err, consecutiveFails, delay)

		select {
		case <-s.stopCh:
			s.mu.Lock()
			s.Status = StatusStopped
			s.mu.Unlock()
			m.removeThumb(s.ID)
			return
		case <-time.After(delay):
			s.mu.Lock()
			s.Status = StatusRunning
			s.Error = ""
			s.mu.Unlock()
		}
	}
}

// runFFmpeg runs FFmpeg capturing from Blackmagic and writing a JPEG thumbnail
// every second. yadif=deint=interlaced handles both 1080i and 1080p transparently.
// On source format change FFmpeg exits and runLoop restarts it automatically.
func (m *Manager) runFFmpeg(s *Stream) error {
	outDir := filepath.Join(m.hlsDir, fmt.Sprintf("%d", s.ID))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	thumbPath := filepath.Join(outDir, "thumb.jpg")
	inputArgs := shellSplit(s.ffmpegInput)

	// yadif=deint=interlaced: deinterlace only interlaced frames (1080i),
	// pass progressive frames through unchanged (1080p).
	vfFilter := "yadif=mode=0:deint=interlaced,scale=640:360,format=yuv420p"

	args := []string{"-y"}
	args = append(args, inputArgs...)
	args = append(args,
		"-vf", vfFilter,
		"-r", "1",
		"-q:v", "4",
		"-update", "1",
		"-f", "image2",
		thumbPath,
	)

	cmd := exec.Command(m.ffmpegBin, args...)

	s.mu.Lock()
	s.cmd = cmd
	s.mu.Unlock()

	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "rror") {
				log.Printf("[channel %d] %s", s.ID, line)
			}
		}
	}()

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

func (m *Manager) removeThumb(id int) {
	thumbPath := filepath.Join(m.hlsDir, fmt.Sprintf("%d", id), "thumb.jpg")
	_ = os.Remove(thumbPath)
}
