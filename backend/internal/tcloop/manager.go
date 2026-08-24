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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/roc-recording/backend/internal/audiox"
	hlsout "github.com/roc-recording/backend/internal/hls"
)

type Status string

const (
	StatusOff        Status = "off"
	StatusRunning    Status = "running"
	StatusRestarting Status = "restarting"
	StatusError      Status = "error"
)

type Position string

const (
	PosBottomRight Position = "bottom_right"
	PosBottomLeft  Position = "bottom_left"
	PosTopRight    Position = "top_right"
	PosTopLeft     Position = "top_left"
	PosCenter      Position = "center"
)

// Source selects the timecode overlay content.
type Source string

const (
	SourceTOD      Source = "tod"      // host wall clock
	SourceExternal Source = "external" // UDP timecode (see udp_port)
)

const audioSilence = audiox.Silence

// Settings are persisted per channel id (decode N ↔ encode/input N).
type Settings struct {
	Enabled  bool     `json:"enabled"`
	Source   Source   `json:"source"`   // tod | external, default tod
	UDPPort  int      `json:"udp_port"` // external TC listen port; default 9300+N
	FontSize int      `json:"fontsize"` // px, default 120
	Opacity  float64  `json:"opacity"`  // 0..1 text opacity, default 0.9
	Position Position `json:"position"` // default top_left
}

// Info is the API view for one channel.
type Info struct {
	ID       int      `json:"id"`
	Enabled  bool     `json:"enabled"`
	Status   Status   `json:"status"`
	Source   Source   `json:"source"`
	UDPPort  int      `json:"udp_port"`
	FontSize int      `json:"fontsize"`
	Opacity  float64  `json:"opacity"`
	Position Position `json:"position"`
	Error    string   `json:"error,omitempty"`
	Timecode string   `json:"timecode,omitempty"`
}

type UpdateInput struct {
	Enabled  *bool     `json:"enabled"`
	Source   *Source   `json:"source"`
	UDPPort  *int      `json:"udp_port"`
	FontSize *int      `json:"fontsize"`
	Opacity  *float64  `json:"opacity"`
	Position *Position `json:"position"`
}

const releaseWait = 10 * time.Second
const decklinkSettle = 3 * time.Second

// CaptureBridge provides encode-side input and activity checks.
type CaptureBridge interface {
	ChannelExists(id int) bool
	IsActive(id int) bool
	InputArgs(id int) ([]string, error)
	InputArgsForTC(id int) ([]string, error)
	Stop(id int) error
}

// PlayoutBridge provides decode sink config and activity checks.
type PlayoutBridge interface {
	ChannelExists(id int) bool
	IsActive(id int) bool
	Stop(id int) error
	Sink(id int) (device, formatCode string, err error)
	ResolveOpenDevice(device string) string
	LookupDeviceOpen(device string) string
	LookupDeviceLabel(device string) string
	OutputTiming(formatCode string) (w, h int, fps float64, interlaced bool, err error)
}

type channelState struct {
	opMu      sync.Mutex
	mu        sync.Mutex
	settings  Settings
	status    Status
	lastErr   string
	Audio     [audiox.Channels]float64
	cmd       *exec.Cmd
	stopCh    chan struct{}
	runGen    uint64 // ownership token for runLoop vs stop/start races
	deviceAlt bool   // next open uses label ↔ unique-id alternate
}

type Manager struct {
	mu           sync.RWMutex
	channels     map[int]*channelState
	ffmpegBin    string
	hlsDir       string
	settingsPath string
	capture      CaptureBridge
	playout      PlayoutBridge
}

var reAstatsPeak = regexp.MustCompile(`lavfi\.astats\.(\d+)\.Peak_level=([-\d.]+|-?inf)`)

func NewManager(ffmpegBin, hlsDir, settingsPath string, capture CaptureBridge, play PlayoutBridge) *Manager {
	return &Manager{
		channels:     make(map[int]*channelState),
		ffmpegBin:    ffmpegBin,
		hlsDir:       hlsDir,
		settingsPath: settingsPath,
		capture:      capture,
		playout:      play,
	}
}

func defaultSettings() Settings {
	return Settings{
		Enabled:  false,
		Source:   SourceTOD,
		UDPPort:  0,
		FontSize: 120,
		Opacity:  0.9,
		Position: PosTopLeft,
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
	if s.Source != SourceExternal {
		s.Source = SourceTOD
	}
	if s.UDPPort < 0 || s.UDPPort > 65535 {
		s.UDPPort = 0
	}
	return s
}

func (m *Manager) AudioLevels(id int) (ch []float64, ok bool) {
	st := m.get(id)
	if st == nil {
		return nil, false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.status != StatusRunning && st.status != StatusRestarting {
		return nil, false
	}
	if st.cmd == nil {
		return nil, false
	}
	return audiox.Slice(st.Audio), true
}

// EncodeThumbPath is kept for API compatibility; TC preview lives under playout/.
func (m *Manager) EncodeThumbPath(id int) string {
	return m.PlayoutThumbPath(id)
}

func (m *Manager) PlayoutThumbPath(id int) string {
	return filepath.Join(m.hlsDir, "playout", strconv.Itoa(id), "thumb.jpg")
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
	return st.status == StatusRunning || st.status == StatusRestarting
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
		Source:   st.settings.Source,
		UDPPort:  effectiveUDPPort(id, st.settings),
		FontSize: st.settings.FontSize,
		Opacity:  st.settings.Opacity,
		Position: st.settings.Position,
		Error:    st.lastErr,
		Timecode: currentTimecode(id, st.status),
	}, nil
}

// List returns TC state for the given channel ids (missing channels are ensured as off).
func (m *Manager) List(ids []int) []Info {
	out := make([]Info, 0, len(ids))
	for _, id := range ids {
		info, err := m.Get(id)
		if err != nil {
			continue
		}
		out = append(out, info)
	}
	return out
}

func effectiveUDPPort(id int, s Settings) int {
	if s.UDPPort > 0 {
		return s.UDPPort
	}
	return defaultUDPPort(id)
}

func currentTimecode(id int, status Status) string {
	if status != StatusRunning {
		return ""
	}
	path := filepath.Join(os.TempDir(), fmt.Sprintf("roc-tcloop-%d-tc.txt", id))
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
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
	if st == nil {
		return Info{}, fmt.Errorf("channel %d not found", id)
	}
	st.opMu.Lock()
	defer st.opMu.Unlock()

	st.mu.Lock()
	cfg := st.settings
	if in.Enabled != nil {
		cfg.Enabled = *in.Enabled
	}
	if in.Source != nil {
		cfg.Source = *in.Source
	}
	if in.UDPPort != nil {
		cfg.UDPPort = *in.UDPPort
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
		m.stopLocked(id, st)
		if !m.waitStopped(id, releaseWait) {
			log.Printf("[tcloop] channel %d: previous TC process did not exit in time", id)
		}
		time.Sleep(decklinkSettle)
		if err := m.startLocked(id, st); err != nil {
			st.mu.Lock()
			st.settings.Enabled = false
			st.mu.Unlock()
			m.mu.Lock()
			_ = m.saveLocked()
			m.mu.Unlock()
			return Info{}, err
		}
	} else {
		m.stopLocked(id, st)
		if !m.waitStopped(id, releaseWait) {
			log.Printf("[tcloop] channel %d: TC process did not exit in time", id)
		}
		time.Sleep(decklinkSettle)
		// Leave DeckLink free. Pair-mode workflow starts encode/decode itself;
		// auto-restarting playout here races TC Start and steals the preview path.
	}
	return m.Get(id)
}

func (m *Manager) Start(id int) error {
	m.EnsureChannel(id)
	st := m.get(id)
	if st == nil {
		return fmt.Errorf("channel %d not found", id)
	}
	st.opMu.Lock()
	defer st.opMu.Unlock()
	return m.startLocked(id, st)
}

func (m *Manager) startLocked(id int, st *channelState) error {
	if err := m.releaseInput(id); err != nil {
		return err
	}

	st.mu.Lock()
	if st.status == StatusRunning || st.status == StatusRestarting {
		st.mu.Unlock()
		return fmt.Errorf("TC Burn-in on channel %d is already starting or running", id)
	}
	st.settings.Enabled = true
	st.settings = normalizeSettings(st.settings)
	cfg := st.settings
	st.runGen++
	gen := st.runGen
	st.stopCh = make(chan struct{})
	st.status = StatusRestarting
	st.lastErr = ""
	stopCh := st.stopCh
	st.mu.Unlock()

	m.mu.Lock()
	_ = m.saveLocked()
	m.mu.Unlock()

	go m.runLoop(id, st, stopCh, cfg, gen)
	return nil
}

func waitUntil(ok func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return ok()
}

func (m *Manager) waitStopped(id int, timeout time.Duration) bool {
	return waitUntil(func() bool {
		st := m.get(id)
		if st == nil {
			return true
		}
		st.mu.Lock()
		defer st.mu.Unlock()
		return st.status == StatusOff && st.cmd == nil
	}, timeout)
}

// releaseInput stops encode/decode on the channel so DeckLink can be opened for TC Burn-in.
func (m *Manager) releaseInput(id int) error {
	released := false
	if m.capture != nil && m.capture.IsActive(id) {
		log.Printf("[tcloop] stopping encode on channel %d for TC Burn-in", id)
		if err := m.capture.Stop(id); err != nil && !strings.Contains(err.Error(), "not running") {
			return fmt.Errorf("stop encode on channel %d: %w", id, err)
		}
		if !waitUntil(func() bool { return !m.capture.IsActive(id) }, releaseWait) {
			return fmt.Errorf("encode on channel %d did not stop in time", id)
		}
		released = true
	}
	if m.playout != nil && m.playout.IsActive(id) {
		log.Printf("[tcloop] stopping decode playout on channel %d for TC Burn-in", id)
		if err := m.playout.Stop(id); err != nil {
			return fmt.Errorf("stop decode playout on channel %d: %w", id, err)
		}
		if !waitUntil(func() bool { return !m.playout.IsActive(id) }, releaseWait) {
			return fmt.Errorf("decode playout on channel %d did not stop in time", id)
		}
		released = true
	}
	if released {
		time.Sleep(decklinkSettle)
	}
	return nil
}

func (m *Manager) Stop(id int) error {
	st := m.get(id)
	if st == nil {
		return nil
	}
	st.opMu.Lock()
	defer st.opMu.Unlock()
	m.stopLocked(id, st)
	return nil
}

func (m *Manager) stopLocked(id int, st *channelState) {
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

func (m *Manager) runLoop(id int, st *channelState, stopCh <-chan struct{}, cfg Settings, gen uint64) {
	const (
		restartDelay = 2 * time.Second
		stableAfter  = 10 * time.Second
		maxBackoff   = 30 * time.Second
	)
	consecutiveFails := 0

	defer func() {
		st.mu.Lock()
		defer st.mu.Unlock()
		// Only the active generation may clear shared slots — a superseded
		// loop must not Kill/nil the replacement process (stop→start race).
		if st.runGen != gen {
			return
		}
		if st.cmd != nil && st.cmd.Process != nil {
			_ = st.cmd.Process.Kill()
		}
		st.cmd = nil
		st.stopCh = nil
	}()

	for {
		select {
		case <-stopCh:
			st.mu.Lock()
			if st.runGen == gen {
				st.status = StatusOff
			}
			st.mu.Unlock()
			return
		default:
		}

		st.mu.Lock()
		cfg = normalizeSettings(st.settings)
		enabled := cfg.Enabled
		mine := st.runGen == gen
		st.mu.Unlock()
		if !mine {
			return
		}
		if !enabled {
			st.mu.Lock()
			if st.runGen == gen {
				st.status = StatusOff
			}
			st.mu.Unlock()
			return
		}

		if consecutiveFails > 0 {
			if err := m.releaseInput(id); err != nil {
				log.Printf("[tcloop %d] release before retry: %v", id, err)
			}
			select {
			case <-stopCh:
				st.mu.Lock()
				if st.runGen == gen {
					st.status = StatusOff
				}
				st.mu.Unlock()
				return
			case <-time.After(decklinkSettle):
			}
		}

		st.mu.Lock()
		if st.runGen != gen {
			st.mu.Unlock()
			return
		}
		st.status = StatusRestarting
		st.lastErr = ""
		st.mu.Unlock()

		start := time.Now()
		err := m.runOnce(id, st, stopCh, cfg, gen)
		select {
		case <-stopCh:
			st.mu.Lock()
			if st.runGen == gen {
				st.status = StatusOff
			}
			st.mu.Unlock()
			return
		default:
		}

		st.mu.Lock()
		if st.runGen != gen {
			st.mu.Unlock()
			return
		}
		if !st.settings.Enabled {
			st.status = StatusOff
			st.mu.Unlock()
			return
		}

		uptime := time.Since(start)
		if err == nil || uptime >= stableAfter {
			consecutiveFails = 0
		} else {
			consecutiveFails++
		}

		delay := restartDelay
		if err != nil {
			st.lastErr = err.Error()
			if isFilterConfigError(err.Error()) {
				delay = 30 * time.Second
				st.status = StatusError
			} else if consecutiveFails >= 12 {
				st.status = StatusError
				delay = maxBackoff
			} else {
				st.status = StatusRestarting
				exp := restartDelay * time.Duration(1<<min(consecutiveFails-1, 4))
				if exp > delay {
					delay = exp
				}
				if delay > maxBackoff {
					delay = maxBackoff
				}
			}
			log.Printf("[tcloop %d] ffmpeg exited after %s: %v (fail #%d) – retry in %s",
				id, uptime.Round(time.Millisecond), err, consecutiveFails, delay)
		} else {
			st.lastErr = ""
			log.Printf("[tcloop %d] ffmpeg exited after %s – retry in %s", id, uptime.Round(time.Millisecond), delay)
		}
		st.mu.Unlock()

		select {
		case <-stopCh:
			st.mu.Lock()
			if st.runGen == gen {
				st.status = StatusOff
			}
			st.mu.Unlock()
			return
		case <-time.After(delay):
		}
	}
}

func (m *Manager) runOnce(id int, st *channelState, stopCh <-chan struct{}, cfg Settings, gen uint64) error {
	inputArgs, err := m.capture.InputArgsForTC(id)
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
	openPrimary, openAlt := m.resolveDeckLinkOpen(device)
	st.mu.Lock()
	tryAlt := st.deviceAlt
	st.mu.Unlock()
	openDevice := openPrimary
	if tryAlt && openAlt != "" && openAlt != openPrimary {
		openDevice = openAlt
	}
	w, h, fps, interlaced, err := m.playout.OutputTiming(formatCode)
	if err != nil {
		return err
	}
	samplesPerFrame := int(math.Round(48000 / fps))
	if samplesPerFrame < 1 {
		samplesPerFrame = 1920
	}

	// Preview is a single low-latency HLS (A/V) for the TC card — no JPEG / audio-only split.
	playoutOutDir := filepath.Join(m.hlsDir, "playout", strconv.Itoa(id))
	_ = os.RemoveAll(playoutOutDir)
	_ = os.MkdirAll(playoutOutDir, 0o755)
	previewPlaylist, previewSeg := hlsout.PreviewPaths(playoutOutDir)

	textPath := filepath.Join(os.TempDir(), fmt.Sprintf("roc-tcloop-%d-tc.txt", id))
	metaPath := filepath.Join(os.TempDir(), fmt.Sprintf("roc-tcloop-%d-astats.meta", id))
	_ = os.Remove(metaPath)
	clockStop := make(chan struct{})
	if err := startClockFile(textPath, cfg.Source, id, cfg.UDPPort, clockStop); err != nil {
		return err
	}
	defer func() {
		close(clockStop)
		_ = os.Remove(textPath)
		_ = os.Remove(metaPath)
	}()

	draw := buildDrawtext(cfg, textPath)
	metaEsc := escapeFilterPath(metaPath)
	var vbase string
	if interlaced {
		vbase = fmt.Sprintf(
			"[0:v]yadif=mode=0:deint=interlaced,scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%g,tinterlace=interleave_top,format=yuv422p10le,%s",
			w, h, w, h, fps*2, draw,
		)
	} else {
		vbase = fmt.Sprintf(
			"[0:v]yadif=mode=0:deint=interlaced,scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%g,format=yuv422p10le,%s",
			w, h, w, h, fps, draw,
		)
	}
	filter := vbase + ",split=2[vdl][vprevsrc];" +
		"[vprevsrc]scale=640:360,fps=10,format=yuv420p[vprev];" +
		fmt.Sprintf(
			"[0:a]"+audiox.Discrete8Pan+",asplit=3[adeck][ameter][aprevsrc];"+
				audiox.PreviewPairGraph("[aprevsrc]")+
				"[adeck]asetnsamples=n=%d:p=0[aout];"+
				"[ameter]astats=metadata=1:reset=1:measure_perchannel=Peak_level:measure_overall=none,"+
				"ametadata=print:file=%s,anullsink",
			samplesPerFrame, metaEsc,
		)

	args := []string{"-hide_banner", "-loglevel", "warning", "-fflags", "+genpts+discardcorrupt", "-y"}
	args = append(args, inputArgs...)
	args = append(args,
		"-filter_complex", filter,
		// Output #1: DeckLink with burned-in TC
		"-map", "[vdl]",
		"-map", "[aout]",
		"-c:v", "v210",
		"-c:a", "pcm_s16le",
		"-ar", "48000",
		"-ac", "8",
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
	args = append(args, "-preroll", "0.5", "-f", "decklink", openDevice)
	args = hlsout.AppendAVPreviewOutputs(args, "[vprev]", previewPlaylist, previewSeg)

	cmd := exec.Command(m.ffmpegBin, args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	st.mu.Lock()
	if st.runGen != gen {
		st.mu.Unlock()
		return fmt.Errorf("superseded")
	}
	st.cmd = cmd
	st.Audio = audiox.SilencePeaks()
	st.mu.Unlock()

	srcLabel := "time of day"
	if cfg.Source == SourceExternal {
		srcLabel = fmt.Sprintf("UDP :%d", effectiveUDPPort(id, cfg))
	}
	log.Printf("[tcloop %d] starting TC burn-in (%s) → decklink %q format=%s (primary=%q alt=%q tryAlt=%v)",
		id, srcLabel, openDevice, formatCode, openPrimary, openAlt, tryAlt)
	astatsStop := make(chan struct{})
	go tailAstatsMeta(metaPath, st, astatsStop)
	defer close(astatsStop)
	if err := cmd.Start(); err != nil {
		st.mu.Lock()
		if st.runGen == gen {
			st.cmd = nil
		}
		st.mu.Unlock()
		return err
	}
	st.mu.Lock()
	if st.runGen == gen {
		st.status = StatusRunning
	}
	st.mu.Unlock()
	errLines := make(chan []string, 1)
	go func() {
		errLines <- collectStderr(stderr, st, cmd, func() {
			_ = os.RemoveAll(playoutOutDir)
		})
	}()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-stopCh:
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
		<-errLines
		st.mu.Lock()
		if st.runGen == gen {
			st.cmd = nil
		}
		st.mu.Unlock()
		return nil
	case err := <-done:
		lines := <-errLines
		st.mu.Lock()
		if st.runGen == gen {
			st.cmd = nil
			if err != nil && openAlt != "" && openAlt != openPrimary && isDeckLinkOpenFailure(lines) {
				next := openAlt
				if tryAlt {
					next = openPrimary
				}
				st.deviceAlt = !tryAlt
				log.Printf("[tcloop %d] DeckLink open with %q failed – next retry will use %q", id, openDevice, next)
			}
		}
		st.mu.Unlock()
		if err != nil {
			if msg := summarizeFFmpegErr(lines); msg != "" {
				return fmt.Errorf("%v: %s", err, msg)
			}
		}
		return err
	}
}

// resolveDeckLinkOpen prefers the display label for DeckLink IP write_header;
// unique BMD handles often fail even when -sinks lists them.
func (m *Manager) resolveDeckLinkOpen(device string) (primary, alt string) {
	resolved := strings.TrimSpace(m.playout.ResolveOpenDevice(device))
	openName := strings.TrimSpace(m.playout.LookupDeviceOpen(device))
	label := strings.TrimSpace(m.playout.LookupDeviceLabel(device))
	primary = openName
	if primary == "" {
		primary = resolved
	}
	if primary == "" {
		primary = strings.TrimSpace(device)
	}
	if label != "" && !strings.EqualFold(label, primary) {
		return label, primary
	}
	if resolved != "" && !strings.EqualFold(resolved, primary) {
		return primary, resolved
	}
	return primary, ""
}

func buildDrawtext(cfg Settings, textFile string) string {
	x, y := positionXY(cfg.Position)
	boxA := cfg.Opacity * 0.55
	if boxA < 0.2 {
		boxA = 0.2
	}
	// textfile+reload avoids putting ':' inside filtergraph option values
	// (this FFmpeg build still splits quoted text='%H:%M:%S').
	// expansion=none: never interpret % / %{...} from the clock file (external TC).
	return fmt.Sprintf(
		"drawtext=font=Sans:fontsize=%d:fontcolor=white@%.2f:box=1:boxcolor=black@%.2f:boxborderw=10:x=%s:y=%s:reload=1:expansion=none:textfile=%s",
		cfg.FontSize, cfg.Opacity, boxA, x, y, escapeFilterPath(textFile),
	)
}

func escapeFilterPath(p string) string {
	p = strings.ReplaceAll(p, `\`, `\\`)
	p = strings.ReplaceAll(p, `:`, `\:`)
	p = strings.ReplaceAll(p, `'`, `\'`)
	return p
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

func isFilterConfigError(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "error parsing") ||
		strings.Contains(lower, "no option name") ||
		strings.Contains(lower, "filter graph") ||
		strings.Contains(lower, "does not exist in any defined filter") ||
		(strings.Contains(lower, "invalid argument") && strings.Contains(lower, "filter"))
}

func isDeckLinkOpenFailure(lines []string) bool {
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "error parsing") ||
			strings.Contains(lower, "no option name") ||
			strings.Contains(lower, "filter graph") ||
			strings.Contains(lower, "does not exist in any defined filter") {
			return false
		}
	}
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "error opening output file") ||
			strings.Contains(lower, "error opening output files") {
			continue
		}
		if strings.Contains(lower, "decklink") ||
			strings.Contains(lower, "no such device") ||
			strings.Contains(lower, "device or resource busy") ||
			strings.Contains(lower, "could not write header") ||
			strings.Contains(lower, "error opening input") {
			return true
		}
	}
	return false
}

func isTCLoopNoise(line string) bool {
	if strings.Contains(line, "Parsed_ametadata") ||
		strings.Contains(line, "lavfi.astats") ||
		strings.Contains(line, "[hls @") {
		return true
	}
	if strings.Contains(line, "Opening '") &&
		(strings.Contains(line, ".ts") || strings.Contains(line, ".m3u8")) {
		return true
	}
	return false
}

func tailAstatsMeta(path string, st *channelState, stop <-chan struct{}) {
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			b, err := os.ReadFile(path)
			if err != nil || len(b) == 0 {
				continue
			}
			start := 0
			if len(b) > 8192 {
				start = len(b) - 8192
			}
			for _, line := range strings.Split(string(b[start:]), "\n") {
				line = strings.TrimSpace(line)
				if mm := reAstatsPeak.FindStringSubmatch(line); len(mm) == 3 {
					ch, _ := strconv.Atoi(mm[1])
					val := parsePeakDB(mm[2])
					st.mu.Lock()
					audiox.SetPeak(&st.Audio, ch, val)
					st.mu.Unlock()
				}
			}
		}
	}
}

func collectStderr(r io.Reader, st *channelState, cmd *exec.Cmd, onSignalLoss func()) []string {
	var lines []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !isTCLoopNoise(line) {
			log.Printf("[tcloop] %s", line)
		}
		lines = append(lines, line)
		if len(lines) > 40 {
			lines = lines[len(lines)-40:]
		}
		if mm := reAstatsPeak.FindStringSubmatch(line); len(mm) == 3 {
			ch, _ := strconv.Atoi(mm[1])
			val := parsePeakDB(mm[2])
			st.mu.Lock()
			audiox.SetPeak(&st.Audio, ch, val)
			st.mu.Unlock()
		}
		// Match encode: drop freeze-frame behavior by killing on signal loss so
		// runLoop restarts and re-acquires when the router brings the source back.
		if strings.Contains(line, "No input signal detected") {
			st.mu.Lock()
			st.status = StatusRestarting
			st.Audio = audiox.SilencePeaks()
			st.mu.Unlock()
			if onSignalLoss != nil {
				onSignalLoss()
			}
			if cmd != nil && cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		}
	}
	return lines
}

func parsePeakDB(s string) float64 {
	if s == "-inf" {
		return audioSilence
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return audioSilence
	}
	return v
}

func summarizeFFmpegErr(lines []string) string {
	var picks []string
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "error") ||
			strings.Contains(lower, "failed") ||
			strings.Contains(lower, "no such") ||
			strings.Contains(lower, "invalid") ||
			strings.Contains(lower, "unable") ||
			strings.Contains(lower, "cannot") {
			picks = append(picks, strings.TrimSpace(line))
		}
	}
	if len(picks) == 0 && len(lines) > 0 {
		picks = tailStrings(lines, 3)
	}
	if len(picks) > 3 {
		picks = tailStrings(picks, 3)
	}
	return strings.Join(picks, " | ")
}

func tailStrings(lines []string, n int) []string {
	if n <= 0 || len(lines) == 0 {
		return nil
	}
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
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
