package capture

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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

// reSignalFormat matches lines like:
// "Found Decklink mode 1920 x 1080 with rate 25.00(i)" or "50.00"
var reSignalFormat = regexp.MustCompile(`Found Decklink mode (\d+) x (\d+) with rate ([\d.]+)(\(i\))?`)

// reAstats matches ametadata print lines like:
// "lavfi.astats.1.RMS_level=-18.32" (channel 1 = left, channel 2 = right)
var reAstats = regexp.MustCompile(`lavfi\.astats\.(\d+)\.RMS_level=([-\d.]+|inf)`)

const audioSilence = -90.0 // treat -inf as this value

type Stream struct {
	ID          int
	Name        string
	Status      Status
	Error       string
	Format      string  // e.g. "1080i50" or "1080p50", empty when unknown
	AudioL      float64 // dBFS RMS left channel
	AudioR      float64 // dBFS RMS right channel
	ffmpegInput string
	fifoPath    string
	cmd         *exec.Cmd
	stopCh      chan struct{}
	// recDst is set to a non-nil writer when recording is active.
	// The drain goroutine copies FIFO data here instead of discarding it.
	recDst   io.WriteCloser
	recMu    sync.Mutex
	mu       sync.Mutex
}

// SetRecordingDst atomically swaps the recording destination.
// Pass nil to stop recording (drain goroutine resumes discarding).
func (s *Stream) SetRecordingDst(w io.WriteCloser) {
	s.recMu.Lock()
	old := s.recDst
	s.recDst = w
	s.recMu.Unlock()
	if old != nil {
		_ = old.Close()
	}
}

type Manager struct {
	streams   map[int]*Stream
	mu        sync.RWMutex
	hlsDir    string
	ffmpegBin string
}

func NewManager(hlsDir, ffmpegBin string) *Manager {
	return &Manager{
		streams:   make(map[int]*Stream),
		hlsDir:    hlsDir,
		ffmpegBin: ffmpegBin,
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
		fifoPath:    fmt.Sprintf("/tmp/roc-feed-%d.ts", id),
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

func (m *Manager) StreamByID(id int) (*Stream, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.streams[id]
	return s, ok
}

func (m *Manager) AudioLevels(id int) (l, r float64, ok bool) {
	m.mu.RLock()
	s, found := m.streams[id]
	m.mu.RUnlock()
	if !found {
		return 0, 0, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.AudioL, s.AudioR, s.Status == StatusRunning
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

	// Ensure FIFO exists before the first run
	ensureFIFO(s.fifoPath)

	for {
		select {
		case <-s.stopCh:
			m.killStream(s)
			m.removeThumb(s.ID)
			s.SetRecordingDst(nil)
			s.mu.Lock()
			s.Status = StatusStopped
			s.Format = ""
			s.AudioL = 0
			s.AudioR = 0
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
		// Stop any in-progress recording – signal is gone
		s.SetRecordingDst(nil)

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

// runFFmpeg starts FFmpeg with two outputs:
//  1. JPEG thumbnail updated every second (for grid preview)
//  2. Low-overhead MPEG-TS written to a named FIFO (for recording)
//
// A drain goroutine reads the FIFO continuously and either discards the data
// or writes it to the active recording destination.
func (m *Manager) runFFmpeg(s *Stream) error {
	outDir := filepath.Join(m.hlsDir, fmt.Sprintf("%d", s.ID))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	thumbPath := filepath.Join(outDir, "thumb.jpg")
	inputArgs := sanitizeInputArgs(shellSplit(s.ffmpegInput))

	// astats prints RMS levels per channel to stderr every ~1 s.
	afFilter := "astats=metadata=1:reset=1,ametadata=print:key=lavfi.astats.1.RMS_level:key=lavfi.astats.2.RMS_level"

	// tee muxer: output 1 = JPEG thumbnail, output 2 = MPEG-TS to FIFO
	// The FIFO output uses the full input resolution/codec so recording gets full quality.
	teeOut := fmt.Sprintf(
		// thumbnail branch: scale down, 1 fps, JPEG
		"[select=v:f=image2:r=1:q\\:v=4:update=1]%s"+
			"|"+
			// FIFO branch: full resolution h264_nvenc TS for recording
			"[select=v:f=mpegts:c:v=h264_nvenc:b:v=12M:maxrate=14M:bufsize=20M:preset=p4:vf=yadif\\=mode=0\\:deint=interlaced\\,format=yuv420p]%s",
		thumbPath, s.fifoPath,
	)

	args := []string{"-y"}
	args = append(args, inputArgs...)
	args = append(args,
		"-map", "0:v",
		"-map", "0:a?",
		// astats audio meter (null output)
		"-af", afFilter,
		"-f", "null", "-",
		// tee video output (thumbnail + FIFO)
		"-f", "tee", teeOut,
	)

	cmd := exec.Command(m.ffmpegBin, args...)

	s.mu.Lock()
	s.cmd = cmd
	s.mu.Unlock()

	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	// Drain goroutine: reads from FIFO continuously and routes data to the
	// active recording destination (or discards if none is set).
	// Closing fifoFile from the main goroutine unblocks any pending Read.
	fifoFile, fifoErr := os.Open(s.fifoPath)
	if fifoErr != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("open FIFO: %w", fifoErr)
	}
	go func() {
		defer fifoFile.Close()
		buf := make([]byte, 188*256) // TS packet-aligned buffer
		for {
			n, err := fifoFile.Read(buf)
			if n > 0 {
				s.recMu.Lock()
				dst := s.recDst
				s.recMu.Unlock()
				if dst != nil {
					if _, werr := dst.Write(buf[:n]); werr != nil {
						log.Printf("[channel %d] recording write error: %v", s.ID, werr)
						s.SetRecordingDst(nil)
					}
				}
			}
			if err != nil {
				if err != io.EOF {
					log.Printf("[channel %d] drain read: %v", s.ID, err)
				}
				return
			}
		}
	}()

	// Stderr parser: signal format + audio levels + signal loss detection
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "rror") {
				log.Printf("[channel %d] %s", s.ID, line)
			}
			// Parse signal format from DeckLink mode line
			if mm := reSignalFormat.FindStringSubmatch(line); mm != nil {
				height := mm[2]
				rateStr := mm[3]
				interlaced := mm[4] != ""
				rateFloat, _ := strconv.ParseFloat(rateStr, 64)
				rateInt := int(math.Round(rateFloat))
				// DeckLink reports interlaced as frame-rate with (i) suffix,
				// multiply by 2 to get the conventional field-rate display (25i → 1080i50).
				if interlaced {
					rateInt *= 2
				}
				scanType := "p"
				if interlaced {
					scanType = "i"
				}
				format := fmt.Sprintf("%s%s%d", height, scanType, rateInt)
				s.mu.Lock()
				s.Format = format
				s.mu.Unlock()
				log.Printf("[channel %d] detected format: %s", s.ID, format)
			}
			// Parse per-channel RMS audio levels from astats metadata
			if mm := reAstats.FindStringSubmatch(line); mm != nil {
				ch, _ := strconv.Atoi(mm[1])
				val := audioSilence
				if mm[2] != "inf" && mm[2] != "-inf" {
					val, _ = strconv.ParseFloat(mm[2], 64)
				}
				s.mu.Lock()
				if ch == 1 {
					s.AudioL = val
				} else if ch == 2 {
					s.AudioR = val
				}
				s.mu.Unlock()
			}
			// If DeckLink reports signal loss, force a restart so stale preview is removed
			if strings.Contains(line, "No input signal detected") {
				s.mu.Lock()
				s.Format = ""
				s.AudioL = 0
				s.AudioR = 0
				s.mu.Unlock()
				s.SetRecordingDst(nil)
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
			}
		}
	}()

	doneCh := make(chan error, 1)
	go func() { doneCh <- cmd.Wait() }()

	select {
	case <-s.stopCh:
		_ = cmd.Process.Kill()
		_ = fifoFile.Close() // unblocks drain goroutine
		return nil
	case err := <-doneCh:
		_ = fifoFile.Close()
		return err
	}
}

// ensureFIFO creates a named pipe at path if it doesn't already exist.
func ensureFIFO(path string) {
	// Remove any regular file that might be there from a previous crash
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeNamedPipe == 0 {
			_ = os.Remove(path)
		} else {
			return // already a FIFO
		}
	}
	if err := mkfifo(path, 0o600); err != nil {
		log.Printf("warning: could not create FIFO %s: %v", path, err)
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

// sanitizeInputArgs removes options that keep stale frames alive on signal loss.
func sanitizeInputArgs(args []string) []string {
	clean := make([]string, 0, len(args))
	i := 0
	for i < len(args) {
		if args[i] == "-signal_loss_action" || args[i] == "-draw_bars" {
			i++
			if i < len(args) {
				i++
			}
			continue
		}
		clean = append(clean, args[i])
		i++
	}
	return clean
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
