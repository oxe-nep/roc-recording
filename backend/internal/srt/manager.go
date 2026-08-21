package srt

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/roc-recording/backend/internal/capture"
)

type Status string

const (
	StatusIdle      Status = "idle"
	StatusStreaming Status = "streaming"
)

type Mode string

const (
	ModeListener Mode = "listener"
	ModeCaller   Mode = "caller"
)

type channelState struct {
	mu          sync.Mutex
	status      Status
	mode        Mode
	port        int    // listener bind port
	target      string // caller destination host:port or full srt:// URL
	passphrase  string
	latencyMS   int
	cmd         *exec.Cmd
	stopCh      chan struct{}
	lastError   string
	bitrateKbps float64
	sending     bool // true once FFmpeg reports real output bitrate (client connected)
}

type Manager struct {
	mu           sync.RWMutex
	states       map[int]*channelState
	captureMgr   *capture.Manager
	ffmpegBin    string
	settingsPath string
	publicHost   string // hostname clients use to connect (listener publish URL)
}

type ChannelInfo struct {
	ID          int     `json:"id"`
	Status      Status  `json:"status"`
	Mode        Mode    `json:"mode"`
	Port        int     `json:"port"`
	Target      string  `json:"target"`
	Passphrase  string  `json:"passphrase,omitempty"` // only returned if set (masked in API)
	HasPass     bool    `json:"has_passphrase"`
	LatencyMS   int     `json:"latency_ms"`
	PublishURL  string  `json:"publish_url"`
	Error       string  `json:"error,omitempty"`
	BitrateKbps float64 `json:"bitrate_kbps,omitempty"`
	Sending     bool    `json:"sending"`
}

type settingsFile struct {
	Channels map[string]channelSettings `json:"channels"`
}

type channelSettings struct {
	Mode       Mode   `json:"mode"`
	Port       int    `json:"port"`
	Target     string `json:"target"`
	Passphrase string `json:"passphrase"`
	LatencyMS  int    `json:"latency_ms"`
}

func NewManager(ffmpegBin string, captureMgr *capture.Manager, settingsPath, publicHost string) *Manager {
	if publicHost == "" {
		publicHost = "127.0.0.1"
	}
	return &Manager{
		states:       make(map[int]*channelState),
		captureMgr:   captureMgr,
		ffmpegBin:    ffmpegBin,
		settingsPath: settingsPath,
		publicHost:   publicHost,
	}
}

func (m *Manager) Register(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[id] = &channelState{
		status:    StatusIdle,
		mode:      ModeListener,
		port:      9100 + id,
		latencyMS: 120,
	}
}

func (m *Manager) LoadSettings() {
	data, err := os.ReadFile(m.settingsPath)
	if err != nil {
		return
	}
	var f settingsFile
	if json.Unmarshal(data, &f) != nil || f.Channels == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for idStr, cfg := range f.Channels {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		st, ok := m.states[id]
		if !ok {
			continue
		}
		st.mu.Lock()
		if cfg.Mode == ModeCaller || cfg.Mode == ModeListener {
			st.mode = cfg.Mode
		}
		if cfg.Port > 0 {
			st.port = cfg.Port
		}
		st.target = strings.TrimSpace(cfg.Target)
		st.passphrase = cfg.Passphrase
		if cfg.LatencyMS > 0 {
			st.latencyMS = cfg.LatencyMS
		}
		st.mu.Unlock()
	}
}

func (m *Manager) saveSettingsLocked() error {
	out := settingsFile{Channels: make(map[string]channelSettings)}
	for id, st := range m.states {
		st.mu.Lock()
		out.Channels[strconv.Itoa(id)] = channelSettings{
			Mode:       st.mode,
			Port:       st.port,
			Target:     st.target,
			Passphrase: st.passphrase,
			LatencyMS:  st.latencyMS,
		}
		st.mu.Unlock()
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.settingsPath, data, 0o644)
}

type UpdateInput struct {
	Mode       *string `json:"mode"`
	Port       *int    `json:"port"`
	Target     *string `json:"target"`
	Passphrase *string `json:"passphrase"`
	LatencyMS  *int    `json:"latency_ms"`
}

func (m *Manager) Update(id int, in UpdateInput) (ChannelInfo, error) {
	m.mu.RLock()
	st, ok := m.states[id]
	m.mu.RUnlock()
	if !ok {
		return ChannelInfo{}, fmt.Errorf("channel %d not found", id)
	}

	st.mu.Lock()
	if st.status == StatusStreaming {
		st.mu.Unlock()
		return ChannelInfo{}, fmt.Errorf("stop SRT before changing settings")
	}
	if in.Mode != nil {
		mode := Mode(strings.TrimSpace(*in.Mode))
		if mode != ModeListener && mode != ModeCaller {
			st.mu.Unlock()
			return ChannelInfo{}, fmt.Errorf("mode must be listener or caller")
		}
		st.mode = mode
	}
	if in.Port != nil {
		if *in.Port < 1 || *in.Port > 65535 {
			st.mu.Unlock()
			return ChannelInfo{}, fmt.Errorf("invalid port")
		}
		st.port = *in.Port
	}
	if in.Target != nil {
		st.target = strings.TrimSpace(*in.Target)
	}
	if in.Passphrase != nil {
		st.passphrase = *in.Passphrase
	}
	if in.LatencyMS != nil {
		if *in.LatencyMS < 20 || *in.LatencyMS > 8000 {
			st.mu.Unlock()
			return ChannelInfo{}, fmt.Errorf("latency_ms must be between 20 and 8000")
		}
		st.latencyMS = *in.LatencyMS
	}
	info := m.buildInfo(id, st)
	st.mu.Unlock()

	m.mu.Lock()
	err := m.saveSettingsLocked()
	m.mu.Unlock()
	if err != nil {
		log.Printf("[srt] failed to persist settings: %v", err)
	}
	return info, nil
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

func (m *Manager) Get(id int) (ChannelInfo, error) {
	m.mu.RLock()
	st, ok := m.states[id]
	m.mu.RUnlock()
	if !ok {
		return ChannelInfo{}, fmt.Errorf("channel %d not found", id)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return m.buildInfo(id, st), nil
}

func (m *Manager) buildInfo(id int, st *channelState) ChannelInfo {
	info := ChannelInfo{
		ID:          id,
		Status:      st.status,
		Mode:        st.mode,
		Port:        st.port,
		Target:      st.target,
		HasPass:     st.passphrase != "",
		LatencyMS:   st.latencyMS,
		Error:       st.lastError,
		BitrateKbps: st.bitrateKbps,
		Sending:     st.sending && st.status == StatusStreaming,
	}
	info.PublishURL = m.publishURL(st)
	// Don't include passphrase in publish URL for API responses shown in UI logs —
	// clients that need auth already have the passphrase field when configuring.
	if st.passphrase != "" && st.mode == ModeListener {
		u, err := url.Parse(info.PublishURL)
		if err == nil {
			q := u.Query()
			q.Del("passphrase")
			u.RawQuery = q.Encode()
			info.PublishURL = u.String()
		}
	}
	return info
}

func (m *Manager) publishURL(st *channelState) string {
	latency := st.latencyMS
	if latency <= 0 {
		latency = 120
	}
	q := url.Values{}
	// Shareable URL is for the remote peer. If we listen, they must call.
	q.Set("mode", string(ModeCaller))
	q.Set("latency", strconv.Itoa(srtLatencyUs(latency)))
	if st.passphrase != "" {
		q.Set("passphrase", st.passphrase)
	}
	switch st.mode {
	case ModeListener:
		return fmt.Sprintf("srt://%s:%d?%s", m.publicHost, st.port, q.Encode())
	case ModeCaller:
		target := strings.TrimSpace(st.target)
		if target == "" {
			return ""
		}
		if strings.HasPrefix(target, "srt://") {
			return target
		}
		// host:port
		return fmt.Sprintf("srt://%s?%s", target, q.Encode())
	default:
		return ""
	}
}

func (m *Manager) outputURL(st *channelState) (string, error) {
	latency := st.latencyMS
	if latency <= 0 {
		latency = 120
	}
	q := url.Values{}
	q.Set("mode", string(st.mode))
	q.Set("latency", strconv.Itoa(srtLatencyUs(latency)))
	if st.passphrase != "" {
		q.Set("passphrase", st.passphrase)
	}
	switch st.mode {
	case ModeListener:
		return fmt.Sprintf("srt://0.0.0.0:%d?%s", st.port, q.Encode()), nil
	case ModeCaller:
		target := strings.TrimSpace(st.target)
		if target == "" {
			return "", fmt.Errorf("caller mode requires a target host:port")
		}
		if strings.HasPrefix(target, "srt://") {
			u, err := url.Parse(target)
			if err != nil {
				return "", fmt.Errorf("invalid target URL")
			}
			existing := u.Query()
			for k, vals := range q {
				for _, v := range vals {
					existing.Set(k, v)
				}
			}
			u.RawQuery = existing.Encode()
			return u.String(), nil
		}
		return fmt.Sprintf("srt://%s?%s", target, q.Encode()), nil
	default:
		return "", fmt.Errorf("unknown mode")
	}
}

// srtLatencyUs converts UI milliseconds to FFmpeg SRT latency (microseconds).
// Values already > 8000 are treated as microseconds for backward compatibility.
func srtLatencyUs(ms int) int {
	if ms <= 0 {
		ms = 120
	}
	if ms > 8000 {
		return ms
	}
	return ms * 1000
}

func (m *Manager) Start(id int) (ChannelInfo, error) {
	m.mu.RLock()
	st, ok := m.states[id]
	m.mu.RUnlock()
	if !ok {
		return ChannelInfo{}, fmt.Errorf("channel %d not found", id)
	}

	status, ok := m.captureMgr.StatusByID(id)
	if !ok || status != capture.StatusRunning {
		return ChannelInfo{}, fmt.Errorf("channel must be running before SRT can start")
	}
	if _, ok := m.captureMgr.FeedURL(id); !ok {
		return ChannelInfo{}, fmt.Errorf("channel %d has no feed url", id)
	}

	st.mu.Lock()
	if st.status == StatusStreaming {
		st.mu.Unlock()
		return ChannelInfo{}, fmt.Errorf("channel %d SRT already streaming", id)
	}
	if _, err := m.outputURL(st); err != nil {
		st.mu.Unlock()
		return ChannelInfo{}, err
	}

	st.stopCh = make(chan struct{})
	st.status = StatusStreaming
	st.lastError = ""
	info := m.buildInfo(id, st)
	st.mu.Unlock()

	log.Printf("[srt %d] started %s → %s", id, info.Mode, info.PublishURL)
	go m.runLoop(id, st)
	return info, nil
}

// runLoop keeps FFmpeg remuxing to SRT while the operator has STREAM on.
// Listener mode exits when a client disconnects — we restart so the endpoint
// stays published until Stop is called.
func (m *Manager) runLoop(id int, st *channelState) {
	const restartDelay = 800 * time.Millisecond

	defer func() {
		st.mu.Lock()
		if st.cmd != nil && st.cmd.Process != nil {
			_ = st.cmd.Process.Kill()
			st.cmd = nil
		}
		st.status = StatusIdle
		st.stopCh = nil
		st.bitrateKbps = 0
		st.sending = false
		st.mu.Unlock()
		log.Printf("[srt %d] stopped", id)
	}()

	for {
		st.mu.Lock()
		stopCh := st.stopCh
		st.mu.Unlock()
		if stopCh == nil {
			return
		}

		select {
		case <-stopCh:
			return
		default:
		}

		status, ok := m.captureMgr.StatusByID(id)
		if !ok || status != capture.StatusRunning {
			st.mu.Lock()
			st.lastError = "waiting for channel signal"
			st.mu.Unlock()
			select {
			case <-stopCh:
				return
			case <-time.After(1 * time.Second):
			}
			continue
		}

		feedURL, ok := m.captureMgr.FeedURL(id)
		if !ok {
			select {
			case <-stopCh:
				return
			case <-time.After(1 * time.Second):
			}
			continue
		}

		st.mu.Lock()
		outURL, err := m.outputURL(st)
		mode := st.mode
		if err != nil {
			st.lastError = err.Error()
			st.mu.Unlock()
			select {
			case <-stopCh:
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}

		args := []string{
			"-hide_banner",
			"-loglevel", "warning",
			"-fflags", "+genpts+discardcorrupt",
			"-f", "mpegts",
			"-i", feedURL,
			"-map", "0:v:0",
			"-map", "0:a:0?",
			"-c", "copy",
			"-f", "mpegts",
			"-progress", "pipe:1",
			"-nostats",
			outURL,
		}
		cmd := exec.Command(m.ffmpegBin, args...)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			st.lastError = err.Error()
			st.mu.Unlock()
			select {
			case <-stopCh:
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		if err := cmd.Start(); err != nil {
			st.lastError = err.Error()
			st.cmd = nil
			st.bitrateKbps = 0
			st.sending = false
			st.mu.Unlock()
			log.Printf("[srt %d] failed to start ffmpeg: %v", id, err)
			select {
			case <-stopCh:
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}

		st.cmd = cmd
		st.lastError = ""
		st.bitrateKbps = 0
		st.sending = false
		st.mu.Unlock()
		log.Printf("[srt %d] ffmpeg remux running (%s)", id, mode)

		go m.watchProgress(id, st, stdout)

		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()

		select {
		case <-stopCh:
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-done
			st.mu.Lock()
			st.cmd = nil
			st.lastError = ""
			st.bitrateKbps = 0
			st.sending = false
			st.mu.Unlock()
			return
		case err := <-done:
			st.mu.Lock()
			if st.cmd == cmd {
				st.cmd = nil
			}
			// Client disconnect / feed glitch — keep STREAM on and restart quietly.
			st.lastError = ""
			st.bitrateKbps = 0
			st.sending = false
			if err != nil {
				log.Printf("[srt %d] ffmpeg exited: %v – restarting (client disconnect or feed loss)", id, err)
			} else {
				log.Printf("[srt %d] ffmpeg exited cleanly – restarting", id)
			}
			st.mu.Unlock()

			select {
			case <-stopCh:
				return
			case <-time.After(restartDelay):
			}
		}
	}
}

func (m *Manager) Stop(id int) (ChannelInfo, error) {
	m.mu.RLock()
	st, ok := m.states[id]
	m.mu.RUnlock()
	if !ok {
		return ChannelInfo{}, fmt.Errorf("channel %d not found", id)
	}

	st.mu.Lock()
	if st.status != StatusStreaming {
		st.mu.Unlock()
		return ChannelInfo{}, fmt.Errorf("channel %d SRT is not streaming", id)
	}
	if st.stopCh != nil {
		select {
		case <-st.stopCh:
		default:
			close(st.stopCh)
		}
	}
	cmd := st.cmd
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	st.mu.Unlock()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st.mu.Lock()
		done := st.status == StatusIdle && st.cmd == nil
		info := m.buildInfo(id, st)
		st.mu.Unlock()
		if done {
			return info, nil
		}
		time.Sleep(40 * time.Millisecond)
	}

	st.mu.Lock()
	st.cmd = nil
	st.status = StatusIdle
	st.stopCh = nil
	st.bitrateKbps = 0
	st.sending = false
	info := m.buildInfo(id, st)
	st.mu.Unlock()
	return info, nil
}

func (m *Manager) StopAll() {
	m.mu.RLock()
	ids := make([]int, 0, len(m.states))
	for id := range m.states {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	for _, id := range ids {
		_, _ = m.Stop(id)
	}
}

var reSrtBitrate = regexp.MustCompile(`(?i)^bitrate=\s*([0-9.]+)([kKmM])?bits/s`)

func (m *Manager) watchProgress(id int, st *channelState, r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	scanner.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		for i, b := range data {
			if b == '\n' || b == '\r' {
				adv := i + 1
				if b == '\r' && i+1 < len(data) && data[i+1] == '\n' {
					adv = i + 2
				}
				return adv, data[0:i], nil
			}
		}
		if atEOF {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		br, ok := parseSrtBitrate(line)
		if !ok || br <= 0 {
			continue
		}
		st.mu.Lock()
		if st.status == StatusStreaming {
			st.bitrateKbps = br
			if !st.sending {
				st.sending = true
				log.Printf("[srt %d] remux sending (client connected / bitrate reported)", id)
			}
		}
		st.mu.Unlock()
	}
}

func parseSrtBitrate(line string) (kbps float64, ok bool) {
	mm := reSrtBitrate.FindStringSubmatch(line)
	if mm == nil {
		return 0, false
	}
	val, err := strconv.ParseFloat(mm[1], 64)
	if err != nil {
		return 0, false
	}
	switch strings.ToLower(mm[2]) {
	case "m":
		return val * 1000, true
	default:
		return val, true
	}
}
