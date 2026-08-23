package tcloop

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Status string

const (
	StatusOff     Status = "off"
	StatusRunning Status = "running"
	StatusError   Status = "error"
)

type Position string

const (
	PosBottomRight Position = "bottom_right"
	PosBottomLeft  Position = "bottom_left"
	PosTopRight    Position = "top_right"
	PosTopLeft     Position = "top_left"
	PosCenter      Position = "center"
)

// Settings are persisted per channel id (decode N ↔ encode/input N).
type Settings struct {
	Enabled  bool     `json:"enabled"`
	FontSize int      `json:"fontsize"`  // px, default 48
	Opacity  float64  `json:"opacity"`  // 0..1 text opacity, default 0.9
	Position Position `json:"position"` // default bottom_right
}

// Info is the API view for one channel.
type Info struct {
	ID       int      `json:"id"`
	Enabled  bool     `json:"enabled"`
	Status   Status   `json:"status"`
	FontSize int      `json:"fontsize"`
	Opacity  float64  `json:"opacity"`
	Position Position `json:"position"`
	Error    string   `json:"error,omitempty"`
}

type UpdateInput struct {
	Enabled  *bool     `json:"enabled"`
	FontSize *int      `json:"fontsize"`
	Opacity  *float64  `json:"opacity"`
	Position *Position `json:"position"`
}

// CaptureBridge provides encode-side input and activity checks.
type CaptureBridge interface {
	ChannelExists(id int) bool
	IsActive(id int) bool
	InputArgs(id int) ([]string, error)
}

// PlayoutBridge provides decode sink config and activity checks.
type PlayoutBridge interface {
	ChannelExists(id int) bool
	IsActive(id int) bool
	Sink(id int) (device, formatCode string, err error)
	ResolveOpenDevice(device string) string
	OutputTiming(formatCode string) (w, h int, fps float64, interlaced bool, err error)
}

type channelState struct {
	mu       sync.Mutex
	settings Settings
	status   Status
	lastErr  string
	cmd      *exec.Cmd
	stopCh   chan struct{}
}

type Manager struct {
	mu           sync.RWMutex
	channels     map[int]*channelState
	ffmpegBin    string
	settingsPath string
	capture      CaptureBridge
	playout      PlayoutBridge
}

func NewManager(ffmpegBin, settingsPath string, capture CaptureBridge, play PlayoutBridge) *Manager {
	return &Manager{
		channels:     make(map[int]*channelState),
		ffmpegBin:    ffmpegBin,
		settingsPath: settingsPath,
		capture:      capture,
		playout:      play,
	}
}

func defaultSettings() Settings {
	return Settings{
		Enabled:  false,
		FontSize: 48,
		Opacity:  0.9,
		Position: PosBottomRight,
	}
}

func (m *Manager) EnsureChannel(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.channels[id]; ok {
		return
	}
	m.channels[id] = &channelState{
		settings: defaultSettings(),
		status:   StatusOff,
	}
}

func (m *Manager) Load() {
	if m.settingsPath == "" {
		return
	}
	data, err := os.ReadFile(m.settingsPath)
	if err != nil {
		return
	}
	var file map[string]Settings
	if err := json.Unmarshal(data, &file); err != nil {
		log.Printf("[tcloop] bad settings file: %v", err)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for idStr, cfg := range file {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		st := &channelState{settings: normalizeSettings(cfg), status: StatusOff}
		m.channels[id] = st
	}
}

func (m *Manager) saveLocked() error {
	if m.settingsPath == "" {
		return nil
	}
	out := make(map[string]Settings, len(m.channels))
	for id, st := range m.channels {
		st.mu.Lock()
		out[strconv.Itoa(id)] = st.settings
		st.mu.Unlock()
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.settingsPath, data, 0o644)
}

func normalizeSettings(s Settings) Settings {
	d := defaultSettings()
	if s.FontSize < 12 {
		s.FontSize = d.FontSize
	}
	if s.FontSize > 200 {
		s.FontSize = 200
	}
	if s.Opacity <= 0 || s.Opacity > 1 {
		s.Opacity = d.Opacity
	}
	switch s.Position {
	case PosBottomRight, PosBottomLeft, PosTopRight, PosTopLeft, PosCenter:
	default:
		s.Position = d.Position
	}
	return s
}

func (m *Manager) IsEnabled(id int) bool {
	st := m.get(id)
	if st == nil {
		return false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.settings.Enabled
}

func (m *Manager) IsRunning(id int) bool {
	st := m.get(id)
	if st == nil {
		return false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.status == StatusRunning
}

func (m *Manager) get(id int) *channelState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.channels[id]
}

func (m *Manager) Get(id int) (Info, error) {
	m.EnsureChannel(id)
	st := m.get(id)
	if st == nil {
		return Info{}, fmt.Errorf("channel %d not found", id)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return Info{
		ID:       id,
		Enabled:  st.settings.Enabled,
		Status:   st.status,
		FontSize: st.settings.FontSize,
		Opacity:  st.settings.Opacity,
		Position: st.settings.Position,
		Error:    st.lastErr,
	}, nil
}

func (m *Manager) Update(id int, in UpdateInput) (Info, error) {
	if m.capture == nil || !m.capture.ChannelExists(id) {
		return Info{}, fmt.Errorf("encode channel %d not found (TC Burn-in needs matching input)", id)
	}
	if m.playout == nil || !m.playout.ChannelExists(id) {
		return Info{}, fmt.Errorf("decode channel %d not found", id)
	}
	m.EnsureChannel(id)
	st := m.get(id)
	st.mu.Lock()
	cfg := st.settings
	if in.Enabled != nil {
		cfg.Enabled = *in.Enabled
	}
	if in.FontSize != nil {
		cfg.FontSize = *in.FontSize
	}
	if in.Opacity != nil {
		cfg.Opacity = *in.Opacity
	}
	if in.Position != nil {
		cfg.Position = *in.Position
	}
	cfg = normalizeSettings(cfg)
	st.settings = cfg
	st.mu.Unlock()

	m.mu.Lock()
	_ = m.saveLocked()
	m.mu.Unlock()

	if cfg.Enabled {
		_ = m.Stop(id)
		if err := m.Start(id); err != nil {
			st.mu.Lock()
			st.settings.Enabled = false
			st.mu.Unlock()
			m.mu.Lock()
			_ = m.saveLocked()
			m.mu.Unlock()
			return Info{}, err
		}
	} else {
		_ = m.Stop(id)
	}
	return m.Get(id)
}

func (m *Manager) Start(id int) error {
	m.EnsureChannel(id)
	st := m.get(id)
	if st == nil {
		return fmt.Errorf("channel %d not found", id)
	}
	if m.capture.IsActive(id) {
		return fmt.Errorf("stop encode on channel %d before enabling TC Burn-in", id)
	}
	if m.playout.IsActive(id) {
		return fmt.Errorf("stop decode playout on channel %d before enabling TC Burn-in", id)
	}

	st.mu.Lock()
	if st.status == StatusRunning {
		st.mu.Unlock()
		return fmt.Errorf("TC Burn-in on channel %d is already running", id)
	}
	st.settings.Enabled = true
	st.settings = normalizeSettings(st.settings)
	cfg := st.settings
	st.stopCh = make(chan struct{})
	st.status = StatusRunning
	st.lastErr = ""
	stopCh := st.stopCh
	st.mu.Unlock()

	m.mu.Lock()
	_ = m.saveLocked()
	m.mu.Unlock()

	go m.runLoop(id, st, stopCh, cfg)
	return nil
}

func (m *Manager) Stop(id int) error {
	st := m.get(id)
	if st == nil {
		return nil
	}
	st.mu.Lock()
	if st.stopCh != nil {
		select {
		case <-st.stopCh:
		default:
			close(st.stopCh)
		}
	}
	if st.cmd != nil && st.cmd.Process != nil {
		_ = st.cmd.Process.Kill()
	}
	st.status = StatusOff
	st.mu.Unlock()
	return nil
}

func (m *Manager) StopAll() {
	m.mu.RLock()
	ids := make([]int, 0, len(m.channels))
	for id := range m.channels {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	for _, id := range ids {
		_ = m.Stop(id)
	}
}

// StartEnabled boots any channels persisted with enabled=true (after decode sinks exist).
func (m *Manager) StartEnabled() {
	m.mu.RLock()
	ids := make([]int, 0, len(m.channels))
	for id, st := range m.channels {
		st.mu.Lock()
		on := st.settings.Enabled
		st.mu.Unlock()
		if on {
			ids = append(ids, id)
		}
	}
	m.mu.RUnlock()
	for _, id := range ids {
		if err := m.Start(id); err != nil {
			log.Printf("[tcloop] channel %d auto-start failed: %v", id, err)
		}
	}
}

func (m *Manager) runLoop(id int, st *channelState, stopCh <-chan struct{}, cfg Settings) {
	const restartDelay = 2 * time.Second
	defer func() {
		st.mu.Lock()
		if st.cmd != nil && st.cmd.Process != nil {
			_ = st.cmd.Process.Kill()
		}
		st.cmd = nil
		st.stopCh = nil
		st.mu.Unlock()
	}()

	for {
		select {
		case <-stopCh:
			st.mu.Lock()
			st.status = StatusOff
			st.mu.Unlock()
			return
		default:
		}

		st.mu.Lock()
		cfg = normalizeSettings(st.settings)
		enabled := cfg.Enabled
		st.mu.Unlock()
		if !enabled {
			st.mu.Lock()
			st.status = StatusOff
			st.mu.Unlock()
			return
		}

		st.mu.Lock()
		st.status = StatusRunning
		st.mu.Unlock()

		err := m.runOnce(id, st, stopCh, cfg)
		select {
		case <-stopCh:
			st.mu.Lock()
			st.status = StatusOff
			st.mu.Unlock()
			return
		default:
		}
		st.mu.Lock()
		if !st.settings.Enabled {
			st.status = StatusOff
			st.mu.Unlock()
			return
		}
		if err != nil {
			st.lastErr = err.Error()
			st.status = StatusError
			log.Printf("[tcloop %d] ffmpeg exited: %v – retry in %s", id, err, restartDelay)
		} else {
			st.lastErr = ""
			log.Printf("[tcloop %d] ffmpeg exited – retry in %s", id, restartDelay)
		}
		st.mu.Unlock()
		select {
		case <-stopCh:
			st.mu.Lock()
			st.status = StatusOff
			st.mu.Unlock()
			return
		case <-time.After(restartDelay):
		}
	}
}

func (m *Manager) runOnce(id int, st *channelState, stopCh <-chan struct{}, cfg Settings) error {
	inputArgs, err := m.capture.InputArgs(id)
	if err != nil {
		return err
	}
	device, formatCode, err := m.playout.Sink(id)
	if err != nil {
		return err
	}
	if strings.TrimSpace(device) == "" || strings.TrimSpace(formatCode) == "" {
		return fmt.Errorf("decode %d needs a DeckLink device and output format", id)
	}
	openDevice := m.playout.ResolveOpenDevice(device)
	w, h, fps, interlaced, err := m.playout.OutputTiming(formatCode)
	if err != nil {
		return err
	}
	samplesPerFrame := int(math.Round(48000 / fps))
	if samplesPerFrame < 1 {
		samplesPerFrame = 1920
	}

	draw := buildDrawtext(cfg)
	var vchain string
	if interlaced {
		vchain = fmt.Sprintf(
			"[0:v]yadif=mode=0:deint=interlaced,%s,scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%g,tinterlace=interleave_top,format=yuv422p10le[v]",
			draw, w, h, w, h, fps*2,
		)
	} else {
		vchain = fmt.Sprintf(
			"[0:v]yadif=mode=0:deint=interlaced,%s,scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%g,format=yuv422p10le[v]",
			draw, w, h, w, h, fps,
		)
	}
	filter := vchain + ";" + fmt.Sprintf(
		"[0:a]pan=stereo|c0=c0|c1=c1,aresample=48000:async=1:first_pts=0,aformat=channel_layouts=stereo,asetnsamples=n=%d:p=0[a]",
		samplesPerFrame,
	)

	args := []string{"-hide_banner", "-loglevel", "info", "-fflags", "+genpts+discardcorrupt"}
	args = append(args, inputArgs...)
	args = append(args,
		"-filter_complex", filter,
		"-map", "[v]",
		"-map", "[a]",
		"-c:v", "v210",
		"-c:a", "pcm_s16le",
		"-ar", "48000",
		"-ac", "2",
		"-fps_mode", "cfr",
		"-r", fmt.Sprintf("%g", fps),
		"-s", fmt.Sprintf("%dx%d", w, h),
	)
	if interlaced {
		args = append(args, "-flags", "+ilme+ildct", "-field_order", "tt")
	}
	if formatCode != "" && !isAllDigits(formatCode) {
		args = append(args, "-format_code", strings.TrimSpace(formatCode))
	}
	args = append(args, "-preroll", "0.1", "-f", "decklink", openDevice)

	cmd := exec.Command(m.ffmpegBin, args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	st.mu.Lock()
	st.cmd = cmd
	st.mu.Unlock()

	log.Printf("[tcloop %d] starting TOD burn-in → decklink %q format=%s", id, openDevice, formatCode)
	if err := cmd.Start(); err != nil {
		st.mu.Lock()
		st.cmd = nil
		st.mu.Unlock()
		return err
	}
	go drain(stderr)

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
		st.mu.Unlock()
		return nil
	case err := <-done:
		st.mu.Lock()
		st.cmd = nil
		st.mu.Unlock()
		return err
	}
}

func buildDrawtext(cfg Settings) string {
	x, y := positionXY(cfg.Position)
	boxA := cfg.Opacity * 0.55
	if boxA < 0.2 {
		boxA = 0.2
	}
	// Time of day from host clock (NTP). Escaping for filtergraph argv (no shell).
	return fmt.Sprintf(
		"drawtext=fontsize=%d:fontcolor=white@%.2f:box=1:boxcolor=black@%.2f:boxborderw=10:x=%s:y=%s:text='%%{localtime\\:%%T}'",
		cfg.FontSize, cfg.Opacity, boxA, x, y,
	)
}

func positionXY(pos Position) (x, y string) {
	const margin = 40
	switch pos {
	case PosBottomLeft:
		return fmt.Sprintf("%d", margin), fmt.Sprintf("h-th-%d", margin)
	case PosTopRight:
		return fmt.Sprintf("w-tw-%d", margin), fmt.Sprintf("%d", margin)
	case PosTopLeft:
		return fmt.Sprintf("%d", margin), fmt.Sprintf("%d", margin)
	case PosCenter:
		return "(w-tw)/2", "(h-th)/2"
	default: // bottom_right
		return fmt.Sprintf("w-tw-%d", margin), fmt.Sprintf("h-th-%d", margin)
	}
}

func drain(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
	}
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// SettingsPath helper for callers.
func SettingsPath(cfgDir string) string {
	return filepath.Join(cfgDir, "tc-loop.json")
}
