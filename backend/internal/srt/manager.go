package srt

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
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
	mu         sync.Mutex
	status     Status
	mode       Mode
	port       int    // listener bind port
	target     string // caller destination host:port or full srt:// URL
	passphrase string
	latencyMS  int
	cmd        *exec.Cmd
	lastError  string
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
	ID         int    `json:"id"`
	Status     Status `json:"status"`
	Mode       Mode   `json:"mode"`
	Port       int    `json:"port"`
	Target     string `json:"target"`
	Passphrase string `json:"passphrase,omitempty"` // only returned if set (masked in API)
	HasPass    bool   `json:"has_passphrase"`
	LatencyMS  int    `json:"latency_ms"`
	PublishURL string `json:"publish_url"`
	Error      string `json:"error,omitempty"`
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
		ID:        id,
		Status:    st.status,
		Mode:      st.mode,
		Port:      st.port,
		Target:    st.target,
		HasPass:   st.passphrase != "",
		LatencyMS: st.latencyMS,
		Error:     st.lastError,
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
	q.Set("mode", string(st.mode))
	q.Set("latency", strconv.Itoa(latency))
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
	q.Set("latency", strconv.Itoa(latency))
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
				if existing.Get(k) == "" {
					for _, v := range vals {
						existing.Set(k, v)
					}
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
	feedURL, ok := m.captureMgr.FeedURL(id)
	if !ok {
		return ChannelInfo{}, fmt.Errorf("channel %d has no feed url", id)
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	if st.status == StatusStreaming {
		return ChannelInfo{}, fmt.Errorf("channel %d SRT already streaming", id)
	}

	outURL, err := m.outputURL(st)
	if err != nil {
		return ChannelInfo{}, err
	}

	// Remux master UDP TS → SRT (no second encode).
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
		outURL,
	}
	cmd := exec.Command(m.ffmpegBin, args...)
	if err := cmd.Start(); err != nil {
		return ChannelInfo{}, fmt.Errorf("start srt ffmpeg: %w", err)
	}

	st.cmd = cmd
	st.status = StatusStreaming
	st.lastError = ""
	log.Printf("[srt %d] started %s → %s", id, st.mode, m.publishURL(st))

	go func(chID int, state *channelState, c *exec.Cmd) {
		err := c.Wait()
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.cmd == c {
			state.cmd = nil
			state.status = StatusIdle
			if err != nil {
				state.lastError = err.Error()
				log.Printf("[srt %d] ffmpeg exited: %v", chID, err)
			} else {
				log.Printf("[srt %d] stopped", chID)
			}
		}
	}(id, st, cmd)

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
	if st.status != StatusStreaming {
		st.mu.Unlock()
		return ChannelInfo{}, fmt.Errorf("channel %d SRT is not streaming", id)
	}
	cmd := st.cmd
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	st.mu.Unlock()

	deadline := time.Now().Add(3 * time.Second)
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
