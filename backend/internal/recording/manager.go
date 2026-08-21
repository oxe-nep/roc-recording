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
	reFFTime    = regexp.MustCompile(`(?:time|out_time)=(\d+):(\d+):(\d+(?:\.\d+)?)`)
	reFFBitrate = regexp.MustCompile(`bitrate=\s*([0-9.]+)\s*([kKmM])?bits/s`)
	reFFOutMS   = regexp.MustCompile(`out_time_ms=(\d+)`)
)

type recState struct {
	mu          sync.Mutex
	status      RecordingStatus
	startedAt   time.Time
	filePath    string
	label       string // user-facing recording name prefix
	category    string // global library folder under recordings_dir
	cmd         *exec.Cmd
	elapsedSec  float64
	bitrateKbps float64
	encoding    bool // true after FFmpeg reports real progress
}

type Manager struct {
	mu                 sync.RWMutex
	states             map[int]*recState
	captureMgr         *capture.Manager
	recordingDir       string
	ffmpegBin          string
	categoryAssignPath string
	namesAssignPath    string
	pathSettingsPath   string
}

func NewManager(recordingDir, ffmpegBin string, captureMgr *capture.Manager, categoryAssignPath, pathSettingsPath string) *Manager {
	namesPath := ""
	if categoryAssignPath != "" {
		namesPath = filepath.Join(filepath.Dir(categoryAssignPath), "channel-names.json")
	}
	m := &Manager{
		states:             make(map[int]*recState),
		captureMgr:         captureMgr,
		recordingDir:       recordingDir,
		ffmpegBin:          ffmpegBin,
		categoryAssignPath: categoryAssignPath,
		namesAssignPath:    namesPath,
		pathSettingsPath:   pathSettingsPath,
	}
	m.loadRecordingPath()
	_ = m.EnsureLibrary()
	return m
}

func (m *Manager) Register(id int, defaultName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	label := sanitizeLabel(defaultName)
	if label == "" {
		label = fmt.Sprintf("ch%d", id)
	}
	m.states[id] = &recState{
		status:   StatusIdle,
		label:    label,
		category: DefaultCategory,
	}
}

// LoadCategoryAssignments applies persisted per-channel category and name choices.
func (m *Manager) LoadCategoryAssignments() {
	m.loadChannelCategories()
	m.loadChannelNames()
}

type ChannelInfo struct {
	ID          int             `json:"id"`
	Status      RecordingStatus `json:"status"`
	Name        string          `json:"name"`
	Category    string          `json:"category"`
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	FilePath    string          `json:"file_path,omitempty"`
	ElapsedSec  float64         `json:"elapsed_sec,omitempty"`
	BitrateKbps float64         `json:"bitrate_kbps,omitempty"`
	Encoding    bool            `json:"encoding"`
}

func (m *Manager) buildInfo(id int, st *recState) ChannelInfo {
	info := ChannelInfo{
		ID:       id,
		Status:   st.status,
		Name:     st.label,
		Category: st.category,
	}
	if info.Category == "" {
		info.Category = DefaultCategory
	}
	if st.status == StatusRecording {
		t := st.startedAt
		info.StartedAt = &t
		info.FilePath = st.filePath
		info.Encoding = st.encoding
		if st.encoding {
			info.ElapsedSec = st.elapsedSec
			info.BitrateKbps = st.bitrateKbps
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

func (m *Manager) IsRecording(id int) bool {
	m.mu.RLock()
	st, ok := m.states[id]
	m.mu.RUnlock()
	if !ok {
		return false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.status == StatusRecording
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
	st.label = clean
	info := m.buildInfo(id, st)
	st.mu.Unlock()

	m.mu.Lock()
	_ = m.saveChannelNamesLocked()
	m.mu.Unlock()
	return info, nil
}

func (m *Manager) SetCategory(id int, category string) (ChannelInfo, error) {
	m.mu.RLock()
	st, ok := m.states[id]
	m.mu.RUnlock()
	if !ok {
		return ChannelInfo{}, fmt.Errorf("channel %d not found", id)
	}
	clean := sanitizeCategory(category)
	if clean == "" {
		return ChannelInfo{}, fmt.Errorf("invalid category")
	}
	if err := os.MkdirAll(filepath.Join(m.RecordingDir(), clean), 0o755); err != nil {
		return ChannelInfo{}, fmt.Errorf("ensure category dir: %w", err)
	}
	st.mu.Lock()
	if st.status == StatusRecording {
		st.mu.Unlock()
		return ChannelInfo{}, fmt.Errorf("stop recording before changing category")
	}
	st.category = clean
	info := m.buildInfo(id, st)
	st.mu.Unlock()

	m.mu.Lock()
	_ = m.saveChannelCategoriesLocked()
	m.mu.Unlock()
	return info, nil
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

	category := st.category
	if category == "" {
		category = DefaultCategory
	}
	outDir := filepath.Join(m.RecordingDir(), category)
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
	// Remux master UDP feed (already encoded by capture). No second NVENC pass.
	// aac_adtstoasc is required when copying AAC from MPEG-TS into MP4.
	args := []string{
		"-y",
		"-fflags", "+genpts+discardcorrupt",
		"-analyzeduration", "5M",
		"-probesize", "5M",
		"-f", "mpegts",
		"-i", feedURL,
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-c", "copy",
		"-bsf:a", "aac_adtstoasc",
		"-movflags", "frag_keyframe+empty_moov+default_base_moof",
		// Machine-readable progress on stdout (newline-delimited key=value).
		"-progress", "pipe:1",
		"-nostats",
		mp4Path,
	}
	cmd := exec.Command(m.ffmpegBin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return ChannelInfo{}, fmt.Errorf("stdout pipe: %w", err)
	}
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
	st.encoding = false

	go m.watchProgress(id, st, stdout)
	go m.watchStderr(id, stderr)
	go func(chID int, state *recState, c *exec.Cmd) {
		err := c.Wait()
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.cmd == c {
			state.cmd = nil
			state.status = StatusIdle
			state.elapsedSec = 0
			state.bitrateKbps = 0
			state.encoding = false
		}
		if err != nil {
			log.Printf("[recording %d] FFmpeg exited with error: %v", chID, err)
		}
	}(id, st, cmd)

	log.Printf("[recording %d] Started MP4 remux (copy from feed): %s", id, mp4Path)
	return m.buildInfo(id, st), nil
}

func (m *Manager) watchStderr(id int, stderr io.Reader) {
	scanner := newProgressScanner(stderr)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "Error") || strings.Contains(line, "error:") {
			log.Printf("[recording %d] %s", id, line)
		}
	}
}

func (m *Manager) watchProgress(id int, st *recState, r io.Reader) {
	scanner := newProgressScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		elapsed, bitrate, ok := parseProgress(line)
		if !ok {
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
			// Consider encoding active once FFmpeg reports real media progress.
			if !st.encoding && (elapsed > 0.2 || bitrate > 0) {
				st.encoding = true
				log.Printf("[recording %d] Remux active (ffmpeg progress)", id)
			}
		}
		st.mu.Unlock()
	}
}

// newProgressScanner splits on both \n and \r because FFmpeg status lines often use CR.
func newProgressScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	scanner.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		if i := bytesIndexAny(data, "\r\n"); i >= 0 {
			adv := i + 1
			if data[i] == '\r' && i+1 < len(data) && data[i+1] == '\n' {
				adv = i + 2
			}
			return adv, data[0:i], nil
		}
		if atEOF {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	return scanner
}

func bytesIndexAny(data []byte, chars string) int {
	for i, b := range data {
		for j := 0; j < len(chars); j++ {
			if b == chars[j] {
				return i
			}
		}
	}
	return -1
}

func parseProgress(line string) (elapsedSec, bitrateKbps float64, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return 0, 0, false
	}

	if mm := reFFOutMS.FindStringSubmatch(line); mm != nil {
		ms, _ := strconv.ParseFloat(mm[1], 64)
		elapsedSec = ms / 1000
		ok = true
	}
	if tm := reFFTime.FindStringSubmatch(line); tm != nil {
		h, _ := strconv.ParseFloat(tm[1], 64)
		m, _ := strconv.ParseFloat(tm[2], 64)
		s, _ := strconv.ParseFloat(tm[3], 64)
		elapsedSec = h*3600 + m*60 + s
		ok = true
	}
	if br := reFFBitrate.FindStringSubmatch(line); br != nil {
		val, _ := strconv.ParseFloat(br[1], 64)
		unit := strings.ToLower(br[2])
		switch unit {
		case "m":
			bitrateKbps = val * 1000
		case "k", "":
			bitrateKbps = val
		default:
			bitrateKbps = val
		}
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
	if st.status != StatusRecording {
		st.mu.Unlock()
		return ChannelInfo{}, fmt.Errorf("channel %d is not recording", id)
	}
	cmd := st.cmd
	if cmd != nil && cmd.Process != nil {
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			_ = cmd.Process.Kill()
		}
	}
	st.mu.Unlock()
	log.Printf("[recording %d] Stop requested – waiting for remux to exit", id)

	// Wait for the existing Wait-goroutine to clear state (do not Wait twice).
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		st.mu.Lock()
		done := st.status == StatusIdle && st.cmd == nil
		info := m.buildInfo(id, st)
		st.mu.Unlock()
		if done {
			log.Printf("[recording %d] Stopped", id)
			return info, nil
		}
		time.Sleep(40 * time.Millisecond)
	}

	// Last resort: hard-kill and force idle so Start can proceed.
	st.mu.Lock()
	if st.cmd != nil && st.cmd.Process != nil {
		_ = st.cmd.Process.Kill()
	}
	st.cmd = nil
	st.status = StatusIdle
	st.elapsedSec = 0
	st.bitrateKbps = 0
	st.encoding = false
	info := m.buildInfo(id, st)
	st.mu.Unlock()
	log.Printf("[recording %d] Stop forced after timeout", id)
	return info, nil
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
		status, ok := m.captureMgr.StatusByID(id)
		if !ok || status != capture.StatusRunning {
			errs = append(errs, fmt.Errorf("ch%d: channel must be running before recording can start", id))
			continue
		}
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
