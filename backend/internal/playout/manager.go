package playout

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	hlsout "github.com/roc-recording/backend/internal/hls"
)

type Status string

const (
	StatusStopped Status = "stopped"
	StatusWaiting Status = "waiting"
	StatusRunning Status = "running"
	StatusPaused  Status = "paused"
)

type Mode string

const (
	ModeListener Mode = "listener"
	ModeCaller   Mode = "caller"
)

type Source string

const (
	SourceSRT  Source = "srt"
	SourceFile Source = "file"
)

const (
	audioSilence = -90.0
	logCap       = 200
)

var (
	reAstatsPeak = regexp.MustCompile(`lavfi\.astats\.(\d+)\.Peak_level=([-\d.]+|-?inf)`)
	reBitrate    = regexp.MustCompile(`(?i)^bitrate=\s*([0-9.]+)([kKmM])?bits/s`)
)

type Client struct {
	ID          int
	Name        string
	Status      Status
	Device      string
	DeviceLabel string // persisted display name; used when cache miss / open fallback
	FormatCode  string
	DeckLinkOut bool // always true for fixed channels
	Fixed       bool // default sink-mapped channel (cannot delete)
	Source      Source
	FileID      string
	Loop        bool
	Mode        Mode
	Port        int
	Target      string
	Passphrase  string
	LatencyMS   int

	AudioL      float64
	AudioR      float64
	BitrateKbps float64
	DurationSec float64 // file length; 0 if unknown / SRT
	ElapsedSec  float64 // display position within current loop
	// File clock: DeckLink consumes at realtime, so wall time since first frame
	// (minus pauses) matches media position within a pass.
	playOrigin  time.Time
	pauseBegan  time.Time
	pausedTotal time.Duration
	fileArmed   bool // true once this pass has delivered media (for one-shot EOF)
	Sending     bool
	Reconnects  int
	LastError   string

	// deviceTryAlt: next DeckLink open uses the alternate of unique-id vs open_name.
	deviceTryAlt bool

	cmd        *exec.Cmd
	previewCmd *exec.Cmd
	stopCh     chan struct{}
	logLines   []string
	mu         sync.Mutex
}

type ClientInfo struct {
	ID          int     `json:"id"`
	Name        string  `json:"name"`
	Status      Status  `json:"status"`
	Device      string  `json:"device"`
	DeviceLabel string  `json:"device_label,omitempty"`
	FormatCode  string  `json:"format_code"`
	DeckLinkOut bool    `json:"decklink_out"`
	Fixed       bool    `json:"fixed"`
	Source      Source  `json:"source"`
	FileID      string  `json:"file_id,omitempty"`
	FileName    string  `json:"file_name,omitempty"`
	Loop        bool    `json:"loop"`
	Mode        Mode    `json:"mode"`
	Port        int     `json:"port"`
	Target      string  `json:"target"`
	HasPass     bool    `json:"has_passphrase"`
	LatencyMS   int     `json:"latency_ms"`
	BitrateKbps float64 `json:"bitrate_kbps,omitempty"`
	DurationSec float64 `json:"duration_sec,omitempty"`
	ElapsedSec  float64 `json:"elapsed_sec,omitempty"`
	RemainSec   float64 `json:"remain_sec,omitempty"`
	Sending     bool    `json:"sending"`
	Reconnects  int     `json:"reconnects"`
	Error       string  `json:"error,omitempty"`
	ListenURL   string  `json:"listen_url,omitempty"`
}

type Manager struct {
	mu             sync.RWMutex
	clients        map[int]*Client
	nextID         int
	ffmpegBin      string
	hlsDir         string
	settingsPath   string
	publicHost     string
	devCache       deviceCache
	media          *MediaStore
	libraryResolve LibraryResolver
	startGuard     func(id int) error
}

type persistedFile struct {
	NextID  int                     `json:"next_id"`
	Clients map[string]persistedCli `json:"clients"`
}

type persistedCli struct {
	Name        string `json:"name"`
	Device      string `json:"device"`
	DeviceLabel string `json:"device_label,omitempty"`
	FormatCode  string `json:"format_code"`
	DeckLinkOut bool   `json:"decklink_out"`
	Fixed       bool   `json:"fixed"`
	Source      Source `json:"source"`
	FileID      string `json:"file_id,omitempty"`
	Loop        bool   `json:"loop"`
	Mode        Mode   `json:"mode"`
	Port        int    `json:"port"`
	Target      string `json:"target"`
	Passphrase  string `json:"passphrase"`
	LatencyMS   int    `json:"latency_ms"`
}

func NewManager(ffmpegBin, hlsDir, settingsPath, publicHost string) *Manager {
	if publicHost == "" {
		publicHost = "127.0.0.1"
	}
	cfgDir := filepath.Dir(settingsPath)
	return &Manager{
		clients:      make(map[int]*Client),
		nextID:       1,
		ffmpegBin:    ffmpegBin,
		hlsDir:       hlsDir,
		settingsPath: settingsPath,
		publicHost:   publicHost,
		media: NewMediaStore(
			filepath.Join(cfgDir, "playout-media"),
			filepath.Join(cfgDir, "playout-media.json"),
		),
	}
}

func (m *Manager) Media() *MediaStore {
	return m.media
}

func (m *Manager) Load() {
	data, err := os.ReadFile(m.settingsPath)
	if err != nil {
		return
	}
	var f persistedFile
	if json.Unmarshal(data, &f) != nil || f.Clients == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if f.NextID > 0 {
		m.nextID = f.NextID
	}
	for idStr, cfg := range f.Clients {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		mode := cfg.Mode
		if mode != ModeCaller && mode != ModeListener {
			mode = ModeListener
		}
		src := cfg.Source
		if src != SourceFile {
			src = SourceSRT
		}
		port := cfg.Port
		if port <= 0 {
			port = 9200 + id
		}
		lat := cfg.LatencyMS
		if lat <= 0 {
			lat = 120
		}
		name := strings.TrimSpace(cfg.Name)
		if name == "" {
			name = fmt.Sprintf("Decode %d", id)
		}
		m.clients[id] = &Client{
			ID:          id,
			Name:        name,
			Status:      StatusStopped,
			Device:      NormalizeOpenDevice(cfg.Device),
			DeviceLabel: strings.TrimSpace(cfg.DeviceLabel),
			FormatCode:  cfg.FormatCode,
			DeckLinkOut: cfg.DeckLinkOut || cfg.Fixed,
			Fixed:       cfg.Fixed,
			Source:      src,
			FileID:      strings.TrimSpace(cfg.FileID),
			Loop:        cfg.Loop,
			Mode:        mode,
			Port:        port,
			Target:      strings.TrimSpace(cfg.Target),
			Passphrase:  cfg.Passphrase,
			LatencyMS:   lat,
			AudioL:      audioSilence,
			AudioR:      audioSilence,
			logLines:    make([]string, 0, 32),
		}
		if id >= m.nextID {
			m.nextID = id + 1
		}
	}
}

func (m *Manager) saveLocked() error {
	if m.settingsPath == "" {
		return nil
	}
	f := persistedFile{
		NextID:  m.nextID,
		Clients: make(map[string]persistedCli, len(m.clients)),
	}
	for id, c := range m.clients {
		c.mu.Lock()
		f.Clients[strconv.Itoa(id)] = persistedCli{
			Name:        c.Name,
			Device:      c.Device,
			DeviceLabel: c.DeviceLabel,
			FormatCode:  c.FormatCode,
			DeckLinkOut: c.DeckLinkOut,
			Fixed:       c.Fixed,
			Source:      c.Source,
			FileID:      c.FileID,
			Loop:        c.Loop,
			Mode:        c.Mode,
			Port:        c.Port,
			Target:      c.Target,
			Passphrase:  c.Passphrase,
			LatencyMS:   c.LatencyMS,
		}
		c.mu.Unlock()
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.settingsPath, data, 0o644)
}

type CreateInput struct {
	Name        string `json:"name"`
	Device      string `json:"device"`
	DeviceLabel string `json:"device_label"`
	FormatCode  string `json:"format_code"`
	DeckLinkOut bool   `json:"decklink_out"`
	Source      string `json:"source"`
	FileID      string `json:"file_id"`
	Loop        bool   `json:"loop"`
	Mode        string `json:"mode"`
	Port        int    `json:"port"`
	Target      string `json:"target"`
	Passphrase  string `json:"passphrase"`
	LatencyMS   int    `json:"latency_ms"`
}

type UpdateInput struct {
	Name        *string `json:"name"`
	Device      *string `json:"device"`
	DeviceLabel *string `json:"device_label"`
	FormatCode  *string `json:"format_code"`
	DeckLinkOut *bool   `json:"decklink_out"`
	Source      *string `json:"source"`
	FileID      *string `json:"file_id"`
	Loop        *bool   `json:"loop"`
	Mode        *string `json:"mode"`
	Port        *int    `json:"port"`
	Target      *string `json:"target"`
	Passphrase  *string `json:"passphrase"`
	LatencyMS   *int    `json:"latency_ms"`
}

func (m *Manager) Create(in CreateInput) (ClientInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := m.nextID
	m.nextID++

	mode := Mode(strings.TrimSpace(in.Mode))
	if mode != ModeCaller {
		mode = ModeListener
	}
	port := in.Port
	if port <= 0 {
		port = 9200 + id
	}
	lat := in.LatencyMS
	if lat <= 0 {
		lat = 120
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = fmt.Sprintf("Decode %d", id)
	}

	c := &Client{
		ID:          id,
		Name:        name,
		Status:      StatusStopped,
		Device:      NormalizeOpenDevice(in.Device),
		DeviceLabel: strings.TrimSpace(in.DeviceLabel),
		FormatCode:  strings.TrimSpace(in.FormatCode),
		DeckLinkOut: in.DeckLinkOut,
		Source:      SourceSRT,
		FileID:      strings.TrimSpace(in.FileID),
		Loop:        in.Loop,
		Mode:        mode,
		Port:        port,
		Target:      strings.TrimSpace(in.Target),
		Passphrase:  in.Passphrase,
		LatencyMS:   lat,
		AudioL:      audioSilence,
		AudioR:      audioSilence,
		logLines:    make([]string, 0, 32),
	}
	if strings.EqualFold(strings.TrimSpace(in.Source), string(SourceFile)) {
		c.Source = SourceFile
	}
	if c.Device != "" && c.DeviceLabel == "" {
		c.DeviceLabel = m.LookupDeviceLabel(c.Device)
	}
	m.clients[id] = c
	if err := m.saveLocked(); err != nil {
		log.Printf("[playout] persist: %v", err)
	}
	return m.infoLocked(c), nil
}

func (m *Manager) Update(id int, in UpdateInput) (ClientInfo, error) {
	c, err := m.get(id)
	if err != nil {
		return ClientInfo{}, err
	}
	c.mu.Lock()
	active := c.Status != StatusStopped
	// Loop may be toggled while a file is playing; everything else requires stop.
	loopOnly := in.Loop != nil &&
		in.Name == nil && in.Device == nil && in.DeviceLabel == nil &&
		in.FormatCode == nil && in.DeckLinkOut == nil && in.Source == nil &&
		in.FileID == nil && in.Mode == nil && in.Port == nil &&
		in.Target == nil && in.Passphrase == nil && in.LatencyMS == nil
	if active && !loopOnly {
		c.mu.Unlock()
		return ClientInfo{}, fmt.Errorf("stop decode client before changing settings")
	}
	if active && loopOnly {
		c.Loop = *in.Loop
		info := m.infoLocked(c)
		c.mu.Unlock()
		m.mu.Lock()
		_ = m.saveLocked()
		m.mu.Unlock()
		c.appendLog(fmt.Sprintf("loop %v", *in.Loop))
		return info, nil
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			c.mu.Unlock()
			return ClientInfo{}, fmt.Errorf("name is required")
		}
		c.Name = name
	}
	if in.Device != nil {
		if c.Fixed {
			c.mu.Unlock()
			return ClientInfo{}, fmt.Errorf("device is locked for default decode channels")
		}
		c.Device = NormalizeOpenDevice(*in.Device)
		if in.DeviceLabel != nil {
			c.DeviceLabel = strings.TrimSpace(*in.DeviceLabel)
		} else if c.Device != "" {
			c.DeviceLabel = m.LookupDeviceLabel(c.Device)
		} else {
			c.DeviceLabel = ""
		}
	} else if in.DeviceLabel != nil {
		c.DeviceLabel = strings.TrimSpace(*in.DeviceLabel)
	}
	if in.FormatCode != nil {
		c.FormatCode = strings.TrimSpace(*in.FormatCode)
	}
	if in.DeckLinkOut != nil {
		if c.Fixed {
			c.DeckLinkOut = true
		} else {
			c.DeckLinkOut = *in.DeckLinkOut
		}
	}
	if in.Source != nil {
		src := Source(strings.TrimSpace(*in.Source))
		if src != SourceSRT && src != SourceFile {
			c.mu.Unlock()
			return ClientInfo{}, fmt.Errorf("source must be srt or file")
		}
		c.Source = src
	}
	if in.FileID != nil {
		c.FileID = strings.TrimSpace(*in.FileID)
	}
	if in.Loop != nil {
		c.Loop = *in.Loop
	}
	if in.Mode != nil {
		mode := Mode(strings.TrimSpace(*in.Mode))
		if mode != ModeListener && mode != ModeCaller {
			c.mu.Unlock()
			return ClientInfo{}, fmt.Errorf("mode must be listener or caller")
		}
		c.Mode = mode
	}
	if in.Port != nil {
		if *in.Port < 1 || *in.Port > 65535 {
			c.mu.Unlock()
			return ClientInfo{}, fmt.Errorf("invalid port")
		}
		c.Port = *in.Port
	}
	if in.Target != nil {
		c.Target = strings.TrimSpace(*in.Target)
	}
	if in.Passphrase != nil {
		c.Passphrase = *in.Passphrase
	}
	if in.LatencyMS != nil {
		if *in.LatencyMS < 20 || *in.LatencyMS > 8000 {
			c.mu.Unlock()
			return ClientInfo{}, fmt.Errorf("latency_ms must be between 20 and 8000")
		}
		c.LatencyMS = *in.LatencyMS
	}
	info := m.infoLocked(c)
	c.mu.Unlock()

	m.mu.Lock()
	err = m.saveLocked()
	m.mu.Unlock()
	if err != nil {
		log.Printf("[playout] persist: %v", err)
	}
	return info, nil
}

func (m *Manager) Delete(id int) error {
	c, err := m.get(id)
	if err != nil {
		return err
	}
	c.mu.Lock()
	fixed := c.Fixed
	active := c.Status != StatusStopped
	c.mu.Unlock()
	if fixed {
		return fmt.Errorf("default decode channels cannot be deleted")
	}
	if active {
		if _, err := m.Stop(id); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.clients, id)
	_ = os.RemoveAll(m.outDir(id))
	return m.saveLocked()
}

func (m *Manager) List() []ClientInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ClientInfo, 0, len(m.clients))
	for _, c := range m.clients {
		c.mu.Lock()
		out = append(out, m.infoLocked(c))
		c.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (m *Manager) Get(id int) (ClientInfo, error) {
	c, err := m.get(id)
	if err != nil {
		return ClientInfo{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return m.infoLocked(c), nil
}

func (m *Manager) Logs(id int) ([]string, error) {
	c, err := m.get(id)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.logLines))
	copy(out, c.logLines)
	return out, nil
}

func (m *Manager) AudioLevels(id int) (l, r float64, ok bool) {
	c, err := m.get(id)
	if err != nil {
		return 0, 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.AudioL, c.AudioR, c.Status == StatusRunning || c.Status == StatusWaiting || c.Status == StatusPaused
}

func (m *Manager) StatusByID(id int) (Status, bool) {
	c, err := m.get(id)
	if err != nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Status, true
}

// ChannelExists reports whether a decode client id is registered.
func (m *Manager) ChannelExists(id int) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.clients[id]
	return ok
}

// IsActive reports whether decode playout is not stopped.
func (m *Manager) IsActive(id int) bool {
	st, ok := m.StatusByID(id)
	return ok && st != StatusStopped
}

// Sink returns the configured DeckLink device and format for decode id.
func (m *Manager) Sink(id int) (device, formatCode string, err error) {
	c, err := m.get(id)
	if err != nil {
		return "", "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.TrimSpace(c.Device), strings.TrimSpace(c.FormatCode), nil
}

// OutputTiming resolves width/height/fps/interlace for a format_code.
func (m *Manager) OutputTiming(formatCode string) (w, h int, fps float64, interlaced bool, err error) {
	devs, _ := m.Devices(false)
	probed, ok := LookupFormat(formatCode, devs)
	w, h, fps, interlaced = resolveOutputTiming(formatCode, probed, ok)
	if w <= 0 || h <= 0 || fps <= 0 {
		return 0, 0, 0, false, fmt.Errorf("invalid output timing for format %q", formatCode)
	}
	return w, h, fps, interlaced, nil
}

// SetStartGuard blocks Start when fn returns an error (e.g. TC Burn-in exclusive).
func (m *Manager) SetStartGuard(fn func(id int) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startGuard = fn
}

func (m *Manager) ThumbPath(id int) string {
	return filepath.Join(m.outDir(id), "thumb.jpg")
}

func (m *Manager) outDir(id int) string {
	return filepath.Join(m.hlsDir, "playout", strconv.Itoa(id))
}

func (m *Manager) get(id int) (*Client, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.clients[id]
	if !ok {
		return nil, fmt.Errorf("decode client %d not found", id)
	}
	return c, nil
}

func (m *Manager) infoLocked(c *Client) ClientInfo {
	label := strings.TrimSpace(c.DeviceLabel)
	if label == "" {
		label = m.LookupDeviceLabel(c.Device)
	}
	src := c.Source
	if src == "" {
		src = SourceSRT
	}
	info := ClientInfo{
		ID:          c.ID,
		Name:        c.Name,
		Status:      c.Status,
		Device:      c.Device,
		DeviceLabel: label,
		FormatCode:  c.FormatCode,
		DeckLinkOut: c.DeckLinkOut || c.Fixed,
		Fixed:       c.Fixed,
		Source:      src,
		FileID:      c.FileID,
		Loop:        c.Loop,
		Mode:        c.Mode,
		Port:        c.Port,
		Target:      c.Target,
		HasPass:     c.Passphrase != "",
		LatencyMS:   c.LatencyMS,
		BitrateKbps: c.BitrateKbps,
		DurationSec: c.DurationSec,
		Sending:     c.Sending && (c.Status == StatusRunning || c.Status == StatusWaiting || c.Status == StatusPaused),
		Reconnects:  c.Reconnects,
		Error:       c.LastError,
	}
	if src == SourceFile && (c.Status == StatusRunning || c.Status == StatusWaiting || c.Status == StatusPaused) {
		elapsed, remain := filePositionLocked(c)
		c.ElapsedSec = elapsed
		info.ElapsedSec = elapsed
		info.RemainSec = remain
	}
	if c.FileID != "" {
		if cat, name, ok := ParseLibraryRef(c.FileID); ok {
			info.FileName = cat + "/" + name
		} else if m.media != nil {
			if it, ok := m.media.Get(c.FileID); ok {
				info.FileName = it.Name
			}
		}
	}
	if c.Mode == ModeListener && src == SourceSRT {
		info.ListenURL = m.listenURL(c)
	}
	return info
}

func (m *Manager) listenURL(c *Client) string {
	lat := c.LatencyMS
	if lat <= 0 {
		lat = 120
	}
	q := url.Values{}
	// Shareable for a remote peer → caller mode, latency in milliseconds.
	q.Set("mode", "caller")
	q.Set("latency", strconv.Itoa(lat))
	return fmt.Sprintf("srt://%s:%d?%s", m.publicHost, c.Port, q.Encode())
}

func (c *Client) appendLog(msg string) {
	line := time.Now().Format("15:04:05") + " " + msg
	c.mu.Lock()
	defer c.mu.Unlock()
	c.logLines = append(c.logLines, line)
	if len(c.logLines) > logCap {
		c.logLines = append([]string(nil), c.logLines[len(c.logLines)-logCap:]...)
	}
}

func (m *Manager) Start(id int) (ClientInfo, error) {
	c, err := m.get(id)
	if err != nil {
		return ClientInfo{}, err
	}
	c.mu.Lock()
	if c.Status != StatusStopped {
		c.mu.Unlock()
		return ClientInfo{}, fmt.Errorf("decode client %d already active", id)
	}
	source := c.Source
	if source == "" {
		source = SourceSRT
	}
	// Fixed channels always drive DeckLink out.
	wantDeckLink := c.DeckLinkOut || c.Fixed
	deviceRaw := strings.TrimSpace(c.Device)
	formatCode := strings.TrimSpace(c.FormatCode)
	fileID := strings.TrimSpace(c.FileID)
	if wantDeckLink {
		if deviceRaw == "" {
			c.mu.Unlock()
			return ClientInfo{}, fmt.Errorf("DeckLink output device is required")
		}
		if formatCode == "" {
			c.mu.Unlock()
			return ClientInfo{}, fmt.Errorf("output format is required")
		}
	}
	if source == SourceFile {
		if fileID == "" {
			c.mu.Unlock()
			return ClientInfo{}, fmt.Errorf("select a media file before starting file playout")
		}
	} else if c.Mode == ModeCaller && strings.TrimSpace(c.Target) == "" {
		c.mu.Unlock()
		return ClientInfo{}, fmt.Errorf("caller mode requires a target")
	}
	mode := c.Mode
	port := c.Port
	c.mu.Unlock()

	if source == SourceFile {
		if _, _, err := m.ResolveFilePath(fileID); err != nil {
			return ClientInfo{}, err
		}
	}

	device := ""
	if wantDeckLink {
		device = m.ResolveOpenDevice(deviceRaw)
	}

	if err := m.assertNoConflicts(id, device, mode, port, source); err != nil {
		return ClientInfo{}, err
	}

	c.mu.Lock()
	if wantDeckLink {
		c.Device = device
		c.DeckLinkOut = true
	}
	if c.Status != StatusStopped {
		c.mu.Unlock()
		return ClientInfo{}, fmt.Errorf("decode client %d already active", id)
	}
	c.mu.Unlock()

	m.mu.RLock()
	guard := m.startGuard
	m.mu.RUnlock()
	if guard != nil {
		if err := guard(id); err != nil {
			return ClientInfo{}, err
		}
	}

	c.mu.Lock()
	if c.Status != StatusStopped {
		c.mu.Unlock()
		return ClientInfo{}, fmt.Errorf("decode client %d already active", id)
	}
	c.stopCh = make(chan struct{})
	c.Status = StatusWaiting
	c.LastError = ""
	c.BitrateKbps = 0
	c.Sending = false
	c.Reconnects = 0
	c.deviceTryAlt = false
	c.DurationSec = 0
	c.ElapsedSec = 0
	c.playOrigin = time.Time{}
	c.pauseBegan = time.Time{}
	c.pausedTotal = 0
	c.fileArmed = false
	c.AudioL = audioSilence
	c.AudioR = audioSilence
	info := m.infoLocked(c)
	c.mu.Unlock()

	if source == SourceFile {
		c.appendLog("start requested – file → DeckLink")
	} else if wantDeckLink {
		c.appendLog("start requested – waiting for SRT (+ DeckLink out)")
	} else {
		c.appendLog("start requested – SRT preview only (DeckLink out disabled)")
	}
	go m.runLoop(c)
	return info, nil
}

func (m *Manager) assertNoConflicts(selfID int, device string, mode Mode, port int, source Source) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for id, other := range m.clients {
		if id == selfID {
			continue
		}
		other.mu.Lock()
		active := other.Status != StatusStopped
		dev := other.Device
		omode := other.Mode
		oport := other.Port
		osrc := other.Source
		other.mu.Unlock()
		if !active {
			continue
		}
		if device != "" && dev == device {
			return fmt.Errorf("DeckLink output %q is already in use by decode %d", device, id)
		}
		if source == SourceSRT && osrc != SourceFile && mode == ModeListener && omode == ModeListener && oport == port {
			return fmt.Errorf("SRT listen port %d is already in use by decode %d", port, id)
		}
	}
	return nil
}

func (m *Manager) Stop(id int) (ClientInfo, error) {
	c, err := m.get(id)
	if err != nil {
		return ClientInfo{}, err
	}
	c.mu.Lock()
	if c.Status == StatusStopped {
		info := m.infoLocked(c)
		c.mu.Unlock()
		return info, nil
	}
	if c.stopCh != nil {
		select {
		case <-c.stopCh:
		default:
			close(c.stopCh)
		}
	}
	killProc(c.cmd)
	killProc(c.previewCmd)
	c.mu.Unlock()
	c.appendLog("stop requested")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		done := c.Status == StatusStopped && c.cmd == nil && c.previewCmd == nil
		if done {
			c.AudioL = audioSilence
			c.AudioR = audioSilence
			c.Sending = false
			c.BitrateKbps = 0
		}
		info := m.infoLocked(c)
		c.mu.Unlock()
		if done {
			return info, nil
		}
		time.Sleep(40 * time.Millisecond)
	}
	c.mu.Lock()
	c.cmd = nil
	c.previewCmd = nil
	c.Status = StatusStopped
	c.stopCh = nil
	c.Sending = false
	c.BitrateKbps = 0
	c.AudioL = audioSilence
	c.AudioR = audioSilence
	info := m.infoLocked(c)
	c.mu.Unlock()
	return info, nil
}

func killProc(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func (m *Manager) Pause(id int) (ClientInfo, error) {
	c, err := m.get(id)
	if err != nil {
		return ClientInfo{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Status != StatusRunning && c.Status != StatusWaiting {
		return ClientInfo{}, fmt.Errorf("decode client %d is not playing", id)
	}
	if err := signalProc(c.cmd, syscall.SIGSTOP); err != nil {
		return ClientInfo{}, fmt.Errorf("pause failed: %w", err)
	}
	_ = signalProc(c.previewCmd, syscall.SIGSTOP)
	if c.pauseBegan.IsZero() {
		c.pauseBegan = time.Now()
	}
	c.ElapsedSec, _ = filePositionLocked(c)
	c.Status = StatusPaused
	c.Sending = false
	c.appendLogUnlocked("paused")
	return m.infoLocked(c), nil
}

func (m *Manager) Resume(id int) (ClientInfo, error) {
	c, err := m.get(id)
	if err != nil {
		return ClientInfo{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Status != StatusPaused {
		return ClientInfo{}, fmt.Errorf("decode client %d is not paused", id)
	}
	if err := signalProc(c.cmd, syscall.SIGCONT); err != nil {
		return ClientInfo{}, fmt.Errorf("resume failed: %w", err)
	}
	_ = signalProc(c.previewCmd, syscall.SIGCONT)
	if !c.pauseBegan.IsZero() {
		c.pausedTotal += time.Since(c.pauseBegan)
		c.pauseBegan = time.Time{}
	}
	c.Status = StatusRunning
	c.Sending = true
	c.appendLogUnlocked("resumed")
	return m.infoLocked(c), nil
}

func signalProc(cmd *exec.Cmd, sig os.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Signal(sig)
}

func (c *Client) appendLogUnlocked(msg string) {
	line := time.Now().Format("15:04:05") + " " + msg
	c.logLines = append(c.logLines, line)
	if len(c.logLines) > logCap {
		c.logLines = append([]string(nil), c.logLines[len(c.logLines)-logCap:]...)
	}
}

func (m *Manager) StopAll() {
	m.mu.RLock()
	ids := make([]int, 0, len(m.clients))
	for id := range m.clients {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	for _, id := range ids {
		_, _ = m.Stop(id)
	}
}

func (m *Manager) runLoop(c *Client) {
	const restartDelay = 1500 * time.Millisecond
	defer func() {
		c.mu.Lock()
		killProc(c.cmd)
		killProc(c.previewCmd)
		c.cmd = nil
		c.previewCmd = nil
		c.Status = StatusStopped
		c.stopCh = nil
		c.BitrateKbps = 0
		c.Sending = false
		c.AudioL = audioSilence
		c.AudioR = audioSilence
		c.mu.Unlock()
		_ = os.Remove(m.ThumbPath(c.ID))
		c.appendLog("stopped")
	}()

	for {
		c.mu.Lock()
		stopCh := c.stopCh
		c.mu.Unlock()
		if stopCh == nil {
			return
		}
		select {
		case <-stopCh:
			return
		default:
		}

		c.mu.Lock()
		c.Status = StatusWaiting
		c.Sending = false
		c.BitrateKbps = 0
		c.AudioL = audioSilence
		c.AudioR = audioSilence
		c.mu.Unlock()
		_ = os.Remove(m.ThumbPath(c.ID))

		err := m.runOnce(c, stopCh)
		select {
		case <-stopCh:
			return
		default:
		}
		c.mu.Lock()
		src := c.Source
		loop := c.Loop
		armed := c.fileArmed
		c.fileArmed = false
		c.mu.Unlock()
		// One-shot file: stop only after we actually played media and FFmpeg exited cleanly.
		// An early clean exit (before frames) is treated as failure and retried — otherwise
		// "loop off" looks broken when startup fails once.
		if src == SourceFile && !loop && err == nil && armed {
			c.appendLog("file playback finished")
			return
		}
		if err != nil {
			c.mu.Lock()
			c.Reconnects++
			c.LastError = ""
			c.mu.Unlock()
			c.appendLog(fmt.Sprintf("ffmpeg exited: %v – retry in %s", err, restartDelay))
		} else if src == SourceFile && !loop && !armed {
			c.appendLog("ffmpeg exited before media – retry in " + restartDelay.String())
		} else {
			c.appendLog("ffmpeg exited – retry in " + restartDelay.String())
		}
		select {
		case <-stopCh:
			return
		case <-time.After(restartDelay):
		}
	}
}

func (m *Manager) runOnce(c *Client, stopCh <-chan struct{}) error {
	outDir := m.outDir(c.ID)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	hlsout.RemovePreviewArtifacts(outDir)
	previewPlaylist, previewSeg := hlsout.PreviewPaths(outDir)

	c.mu.Lock()
	wantDeckLink := c.DeckLinkOut || c.Fixed
	deviceRaw := strings.TrimSpace(c.Device)
	formatCode := strings.TrimSpace(c.FormatCode)
	source := c.Source
	if source == "" {
		source = SourceSRT
	}
	fileID := strings.TrimSpace(c.FileID)
	loop := c.Loop
	mode := c.Mode
	var srtURL string
	var srtErr error
	if source == SourceSRT {
		srtURL, srtErr = m.srtInputURL(c)
	}
	c.mu.Unlock()
	if srtErr != nil {
		return srtErr
	}

	filePath := ""
	if source == SourceFile {
		p, _, err := m.ResolveFilePath(fileID)
		if err != nil {
			return err
		}
		filePath = p
		dur := probeFileDurationSec(m.ffmpegBin, filePath)
		c.mu.Lock()
		c.DurationSec = dur
		c.ElapsedSec = 0
		// Start the file clock on first media frame (markReceiving), not here —
		// otherwise startup/preroll burns the duration and one-shot play stops early.
		c.playOrigin = time.Time{}
		c.pauseBegan = time.Time{}
		c.pausedTotal = 0
		c.fileArmed = false
		c.mu.Unlock()
		if dur > 0 {
			c.appendLog(fmt.Sprintf("file duration %.1fs", dur))
		}
	}

	useDeckLink := wantDeckLink && deviceRaw != "" && formatCode != ""
	device := ""
	deviceLabel := ""
	openPrimary := ""
	openAlt := ""
	if useDeckLink {
		if _, err := m.devCache.refresh(m.ffmpegBin); err != nil {
			c.appendLog(fmt.Sprintf("decklink device refresh: %v", err))
		}
		device = m.ResolveOpenDevice(deviceRaw)
		if d, ok := m.FindDevice(device); ok {
			deviceLabel = d.Label
			// Prefer full unique sink id (works on DeckLink IP); label as fallback.
			openPrimary = d.Name
			if d.Label != "" && d.Label != d.Name {
				openAlt = d.Label
			}
		} else {
			deviceLabel = strings.TrimSpace(c.DeviceLabel)
			if deviceLabel == "" {
				deviceLabel = m.LookupDeviceLabel(device)
			}
			openPrimary = device
			if deviceLabel != "" && deviceLabel != device {
				openAlt = deviceLabel
			}
		}
		if deviceLabel != "" {
			c.mu.Lock()
			c.DeviceLabel = deviceLabel
			if device != deviceRaw {
				c.Device = device
			}
			c.mu.Unlock()
		} else if device != deviceRaw {
			c.mu.Lock()
			c.Device = device
			c.mu.Unlock()
		}
	}

	w, h, fps := 1920, 1080, 25.0
	var fmtInfo Format
	if useDeckLink || formatCode != "" {
		devs, _ := m.Devices(false)
		probed, ok := LookupFormat(formatCode, devs)
		w, h, fps, fmtInfo.Interlaced = resolveOutputTiming(formatCode, probed, ok)
		if ok {
			fmtInfo.Code = probed.Code
			fmtInfo.Label = probed.Label
			fmtInfo.Width, fmtInfo.Height, fmtInfo.FPS = w, h, fps
		}
	}

	c.mu.Lock()
	tryAlt := c.deviceTryAlt
	c.mu.Unlock()
	openDevice := openPrimary
	if openDevice == "" {
		openDevice = device
	}
	if useDeckLink && tryAlt && openAlt != "" && openAlt != openDevice {
		openDevice = openAlt
	}

	samplesPerFrame := int(math.Round(48000 / fps))
	if samplesPerFrame < 1 {
		samplesPerFrame = 1920
	}

	var vchain string
	if fmtInfo.Interlaced {
		vchain = fmt.Sprintf(
			"[0:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%g,tinterlace=interleave_top,format=yuv422p10le,split=2[vdl][vt]",
			w, h, w, h, fps*2,
		)
	} else {
		vchain = fmt.Sprintf(
			"[0:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%g,format=yuv422p10le,split=2[vdl][vt]",
			w, h, w, h, fps,
		)
	}

	args := []string{
		"-hide_banner",
		"-loglevel", "info",
		"-fflags", "+genpts+discardcorrupt",
		"-progress", "pipe:1",
		"-nostats",
		"-analyzeduration", "2M",
		"-probesize", "2M",
	}
	audioSrc := "[0:a]"
	silenceInput := false
	if source == SourceFile {
		args = append(args, "-hwaccel", "cuda")
		// Always loop at demuxer level; when Loop is false we stop at end-of-pass
		// ourselves so LOOP can be toggled without restarting playback.
		args = append(args, "-stream_loop", "-1")
		args = append(args, "-i", filePath)
		if !fileHasAudioStream(m.ffmpegBin, filePath) {
			args = append(args,
				"-f", "lavfi",
				"-i", "anullsrc=channel_layout=stereo:sample_rate=48000",
			)
			audioSrc = "[1:a]"
			silenceInput = true
			c.appendLog("file has no audio – using silent track")
		}
	} else {
		args = append(args, "-hwaccel", "cuda", "-f", "mpegts", "-i", srtURL)
	}

	// File → DeckLink: keep a single DeckLink output (proven path). Preview runs separately
	// so image2/HLS cannot stall the DeckLink consumer after preroll.
	if useDeckLink && source == SourceFile {
		var vdl string
		if fmtInfo.Interlaced {
			vdl = fmt.Sprintf(
				"[0:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%g,tinterlace=interleave_top,format=yuv422p10le[v]",
				w, h, w, h, fps*2,
			)
		} else {
			vdl = fmt.Sprintf(
				"[0:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%g,format=yuv422p10le[v]",
				w, h, w, h, fps,
			)
		}
		filter := vdl + ";" + fmt.Sprintf(
			"%saresample=48000:async=1:first_pts=0,aformat=channel_layouts=stereo,asetnsamples=n=%d:p=0[a]",
			audioSrc, samplesPerFrame,
		)
		dlArgs := append([]string{}, args...)
		dlArgs = append(dlArgs,
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
		if fmtInfo.Interlaced {
			dlArgs = append(dlArgs, "-flags", "+ilme+ildct", "-field_order", "tt")
		}
		if formatCode != "" && !isAllDigits(formatCode) {
			dlArgs = append(dlArgs, "-format_code", strings.TrimSpace(formatCode))
		}
		dlArgs = append(dlArgs, "-preroll", "0.5", "-f", "decklink", openDevice)

		previewArgs := []string{
			"-hide_banner", "-loglevel", "info",
			"-fflags", "+genpts+discardcorrupt",
			"-nostats",
			"-re",
			"-stream_loop", "-1",
		}
		// Soft decode for UI preview — NVDEC reserved for the DeckLink process.
		previewArgs = append(previewArgs, "-i", filePath)
		prevAudio := "[0:a]"
		if silenceInput {
			previewArgs = append(previewArgs, "-f", "lavfi", "-i", "anullsrc=channel_layout=stereo:sample_rate=48000")
			prevAudio = "[1:a]"
		}
		prevFilter :=
			"[0:v]scale=640:360:force_original_aspect_ratio=decrease,pad=640:360:(ow-iw)/2:(oh-ih)/2,fps=10,format=yuv420p[vprev];" +
				fmt.Sprintf("%sasplit=2[aprev][ameter];", prevAudio) +
				"[ameter]astats=metadata=1:reset=1:measure_perchannel=Peak_level:measure_overall=none," +
				"ametadata=print,anullsink"
		previewArgs = append(previewArgs,
			"-filter_complex", prevFilter,
		)
		previewArgs = hlsout.AppendAVPreviewOutputs(previewArgs, "[vprev]", "[aprev]", previewPlaylist, previewSeg)

		return m.runFileDeckLinkAndPreview(c, stopCh, openDevice, formatCode, w, h, fps, fmtInfo.Interlaced, loop, dlArgs, previewArgs)
	}

	var filter string
	if useDeckLink {
		filter = vchain + ";" +
			"[vt]scale=640:360,fps=10,format=yuv420p[vprev];" +
			fmt.Sprintf(
				"%saresample=48000:async=1:first_pts=0,aformat=channel_layouts=stereo,asetnsamples=n=%d:p=0,asplit=3[aout][aprev][ameter];",
				audioSrc, samplesPerFrame,
			) +
			"[ameter]astats=metadata=1:reset=1:measure_perchannel=Peak_level:measure_overall=none," +
			"ametadata=print,anullsink"
		args = append(args,
			"-filter_complex", filter,
			"-map", "[vdl]",
			"-map", "[aout]",
			"-c:v", "v210",
			"-c:a", "pcm_s16le",
			"-ar", "48000",
			"-ac", "2",
			"-fps_mode", "cfr",
			"-r", fmt.Sprintf("%g", fps),
			"-s", fmt.Sprintf("%dx%d", w, h),
		)
		if fmtInfo.Interlaced {
			args = append(args, "-flags", "+ilme+ildct", "-field_order", "tt")
		}
		if formatCode != "" && !isAllDigits(formatCode) {
			args = append(args, "-format_code", strings.TrimSpace(formatCode))
		}
		args = append(args,
			"-preroll", "0.5",
			"-f", "decklink", openDevice,
		)
		args = hlsout.AppendAVPreviewOutputs(args, "[vprev]", "[aprev]", previewPlaylist, previewSeg)
	} else {
		filter =
			"[0:v]scale=640:360:force_original_aspect_ratio=decrease,pad=640:360:(ow-iw)/2:(oh-ih)/2,fps=10,format=yuv420p[vprev];" +
				fmt.Sprintf("%sasplit=2[aprev][ameter];", audioSrc) +
				"[ameter]astats=metadata=1:reset=1:measure_perchannel=Peak_level:measure_overall=none," +
				"ametadata=print,anullsink"
		args = append(args,
			"-filter_complex", filter,
		)
		args = hlsout.AppendAVPreviewOutputs(args, "[vprev]", "[aprev]", previewPlaylist, previewSeg)
	}

	cmd := exec.Command(m.ffmpegBin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.cmd = cmd
	c.LastError = ""
	c.mu.Unlock()
	if useDeckLink {
		c.appendLog(fmt.Sprintf(
			"starting FFmpeg (%s/%s → decklink %q format=%s %dx%d@%g interlaced=%v loop=%v)",
			source, mode, openDevice, formatCode, w, h, fps, fmtInfo.Interlaced, loop && source == SourceFile,
		))
		c.appendLog("decklink args: " + strings.Join(decklinkArgSummary(args, openDevice), " "))
	} else {
		c.appendLog(fmt.Sprintf("starting FFmpeg (%s → preview only)", source))
	}

	if err := cmd.Start(); err != nil {
		c.mu.Lock()
		c.cmd = nil
		c.mu.Unlock()
		return err
	}

	go m.watchStderr(c, stderr)
	go m.watchProgress(c, stdout)
	if source == SourceFile {
		go m.watchFileEnd(c, stopCh)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-stopCh:
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
		c.mu.Lock()
		c.cmd = nil
		c.mu.Unlock()
		return nil
	case err := <-done:
		c.mu.Lock()
		if c.cmd == cmd {
			c.cmd = nil
		}
		c.Sending = false
		c.BitrateKbps = 0
		c.Status = StatusWaiting
		if useDeckLink && err != nil && openAlt != "" && openAlt != openPrimary {
			if !tryAlt {
				c.deviceTryAlt = true
				c.mu.Unlock()
				c.appendLog(fmt.Sprintf("DeckLink open with %q failed – next retry will use %q", openDevice, openAlt))
			} else {
				c.deviceTryAlt = false
				c.mu.Unlock()
				c.appendLog(fmt.Sprintf("DeckLink open with %q also failed – reverting to %q", openDevice, openPrimary))
			}
		} else {
			c.deviceTryAlt = false
			c.mu.Unlock()
		}
		return err
	}
}

func (m *Manager) runFileDeckLinkAndPreview(
	c *Client,
	stopCh <-chan struct{},
	openDevice, formatCode string,
	w, h int, fps float64, interlaced, loop bool,
	dlArgs, previewArgs []string,
) error {
	dlCmd := exec.Command(m.ffmpegBin, dlArgs...)
	dlStdout, err := dlCmd.StdoutPipe()
	if err != nil {
		return err
	}
	dlStderr, err := dlCmd.StderrPipe()
	if err != nil {
		return err
	}

	prevCmd := exec.Command(m.ffmpegBin, previewArgs...)
	prevStderr, err := prevCmd.StderrPipe()
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.cmd = dlCmd
	c.previewCmd = prevCmd
	c.LastError = ""
	c.mu.Unlock()

	c.appendLog(fmt.Sprintf(
		"starting file DeckLink (%q format=%s %dx%d@%g interlaced=%v loop=%v) + preview",
		openDevice, formatCode, w, h, fps, interlaced, loop,
	))
	c.appendLog("decklink args: " + strings.Join(decklinkArgSummary(dlArgs, openDevice), " "))

	if err := dlCmd.Start(); err != nil {
		c.mu.Lock()
		c.cmd = nil
		c.previewCmd = nil
		c.mu.Unlock()
		return err
	}
	if err := prevCmd.Start(); err != nil {
		killProc(dlCmd)
		c.mu.Lock()
		c.cmd = nil
		c.previewCmd = nil
		c.mu.Unlock()
		return fmt.Errorf("preview start: %w", err)
	}

	go m.watchStderr(c, dlStderr)
	go m.watchStderr(c, prevStderr)
	go m.watchProgress(c, dlStdout)
	go m.watchFileEnd(c, stopCh)

	dlDone := make(chan error, 1)
	prevDone := make(chan error, 1)
	go func() { dlDone <- dlCmd.Wait() }()
	go func() { prevDone <- prevCmd.Wait() }()

	select {
	case <-stopCh:
		killProc(dlCmd)
		killProc(prevCmd)
		<-dlDone
		<-prevDone
		c.mu.Lock()
		c.cmd = nil
		c.previewCmd = nil
		c.mu.Unlock()
		return nil
	case err := <-dlDone:
		killProc(prevCmd)
		select {
		case <-prevDone:
		case <-time.After(2 * time.Second):
			killProc(prevCmd)
		}
		c.mu.Lock()
		c.cmd = nil
		c.previewCmd = nil
		c.Sending = false
		c.BitrateKbps = 0
		if c.Status != StatusPaused {
			c.Status = StatusWaiting
		}
		c.mu.Unlock()
		return err
	case <-prevDone:
		// Preview exit is non-fatal while DeckLink still runs.
		c.mu.Lock()
		c.previewCmd = nil
		c.mu.Unlock()
		c.appendLog("preview ffmpeg exited – DeckLink continues")
		select {
		case <-stopCh:
			killProc(dlCmd)
			<-dlDone
			c.mu.Lock()
			c.cmd = nil
			c.mu.Unlock()
			return nil
		case err := <-dlDone:
			c.mu.Lock()
			c.cmd = nil
			c.Sending = false
			c.BitrateKbps = 0
			if c.Status != StatusPaused {
				c.Status = StatusWaiting
			}
			c.mu.Unlock()
			return err
		}
	}
}

// MediaInUse reports whether any active file-playout channel references mediaID.
func (m *Manager) MediaInUse(mediaID string) bool {
	mediaID = strings.TrimSpace(mediaID)
	if mediaID == "" {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, c := range m.clients {
		c.mu.Lock()
		inUse := c.Status != StatusStopped && c.Source == SourceFile && c.FileID == mediaID
		c.mu.Unlock()
		if inUse {
			return true
		}
	}
	return false
}

func (m *Manager) srtInputURL(c *Client) (string, error) {
	lat := c.LatencyMS
	if lat <= 0 {
		lat = 120
	}
	q := url.Values{}
	q.Set("mode", string(c.Mode))
	q.Set("latency", strconv.Itoa(srtLatencyUs(lat)))
	q.Set("transtype", "live")
	if c.Passphrase != "" {
		q.Set("passphrase", c.Passphrase)
	}
	switch c.Mode {
	case ModeListener:
		return fmt.Sprintf("srt://0.0.0.0:%d?%s", c.Port, q.Encode()), nil
	case ModeCaller:
		target := strings.TrimSpace(c.Target)
		if target == "" {
			return "", fmt.Errorf("caller target required")
		}
		if strings.HasPrefix(target, "srt://") {
			u, err := url.Parse(target)
			if err != nil {
				return "", fmt.Errorf("invalid target URL")
			}
			preferLocalSRTHost(u, m.publicHost)
			existing := u.Query()
			// Force caller + our FFmpeg latency (µs). Pasted share URLs use ms.
			for k, vals := range q {
				for _, v := range vals {
					existing.Set(k, v)
				}
			}
			u.RawQuery = existing.Encode()
			return u.String(), nil
		}
		hostport := preferLocalHostPort(target, m.publicHost)
		return fmt.Sprintf("srt://%s?%s", hostport, q.Encode()), nil
	default:
		return "", fmt.Errorf("unknown mode")
	}
}

// preferLocalSRTHost rewrites same-machine hosts to 127.0.0.1 (IPv4).
// "localhost" often resolves to ::1 while SRT listeners bind 0.0.0.0 → I/O error.
func preferLocalSRTHost(u *url.URL, publicHost string) {
	host := u.Hostname()
	if host == "" {
		return
	}
	rewrite := false
	if host == "127.0.0.1" {
		return
	}
	if strings.EqualFold(host, "localhost") || host == "::1" {
		rewrite = true
	}
	pub := strings.TrimSpace(publicHost)
	if pub != "" && (strings.EqualFold(host, pub) || host == pub) {
		rewrite = true
	}
	if !rewrite {
		return
	}
	port := u.Port()
	if port != "" {
		u.Host = "127.0.0.1:" + port
	} else {
		u.Host = "127.0.0.1"
	}
}

func preferLocalHostPort(target, publicHost string) string {
	target = strings.TrimSpace(target)
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		if strings.EqualFold(target, "localhost") {
			return "127.0.0.1"
		}
		return target
	}
	if host == "127.0.0.1" {
		return target
	}
	if strings.EqualFold(host, "localhost") || host == "::1" {
		return net.JoinHostPort("127.0.0.1", port)
	}
	pub := strings.TrimSpace(publicHost)
	if pub != "" && (strings.EqualFold(host, pub) || host == pub) {
		return net.JoinHostPort("127.0.0.1", port)
	}
	return target
}

// srtLatencyUs converts UI milliseconds to FFmpeg SRT latency (microseconds).
func srtLatencyUs(ms int) int {
	if ms <= 0 {
		ms = 120
	}
	if ms > 8000 {
		return ms
	}
	return ms * 1000
}

func formatGeometry(code string) (w, h int, fps float64) {
	w, h, fps = 1920, 1080, 25
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "hi50", "hi25":
		return 1920, 1080, 25
	case "hp50":
		return 1920, 1080, 50
	case "hp25":
		return 1920, 1080, 25
	case "hp60", "hp59.94", "hp5994":
		return 1920, 1080, 60
	case "hp30", "hp29.97", "hp29":
		return 1920, 1080, 30
	case "hp24", "hp23.98":
		return 1920, 1080, 24
	case "hp720p50", "hp50p":
		return 1280, 720, 50
	}
	return w, h, fps
}

// resolveOutputTiming picks width/height/fps/interlace for a DeckLink format_code.
// Probe often reports field-rate as fps for interlaced modes (Hi50 → 50); normalize to frame rate.
func resolveOutputTiming(formatCode string, probed Format, haveProbed bool) (w, h int, fps float64, interlaced bool) {
	w, h, fps = 1920, 1080, 25
	interlaced = formatCodeLooksInterlaced(formatCode)
	if haveProbed {
		w, h, fps = probed.Width, probed.Height, probed.FPS
		if probed.Interlaced {
			interlaced = true
		}
	}
	gw, gh, gfps := formatGeometry(formatCode)
	code := strings.ToLower(strings.TrimSpace(formatCode))
	switch {
	case code == "hi50" || code == "hi25":
		w, h, fps, interlaced = gw, gh, gfps, true
	case code == "hp50":
		w, h, fps, interlaced = gw, gh, gfps, false
	case code == "hp25":
		w, h, fps, interlaced = gw, gh, gfps, false
	default:
		if w <= 0 {
			w = gw
		}
		if h <= 0 {
			h = gh
		}
		if fps <= 0 {
			fps = gfps
		}
		// Interlaced modes listed at field rate (e.g. 50 for i50) → frame rate.
		if interlaced && fps > 30 {
			fps = fps / 2
		}
	}
	if fps <= 0 {
		fps = 25
	}
	return w, h, fps, interlaced
}

func formatCodeLooksInterlaced(code string) bool {
	c := strings.ToLower(strings.TrimSpace(code))
	if c == "" {
		return false
	}
	if strings.HasPrefix(c, "hi") {
		return true
	}
	if strings.Contains(c, "i50") || strings.Contains(c, "i60") || strings.Contains(c, "i59") {
		return true
	}
	return false
}

// decklinkArgSummary returns args from the DeckLink video map onward (for logs).
func decklinkArgSummary(args []string, device string) []string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-map" && args[i+1] == "[vout]" {
			return args[i:]
		}
	}
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-f" && args[i+1] == "decklink" {
			start := i
			if i >= 8 {
				start = i - 8
			} else {
				start = 0
			}
			return args[start:]
		}
	}
	if device != "" {
		return []string{"-f", "decklink", device}
	}
	return nil
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (m *Manager) watchStderr(c *Client, r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		notable := strings.Contains(line, "Error") ||
			strings.Contains(line, "error:") ||
			strings.Contains(line, "SRT") ||
			strings.Contains(line, "decklink")
		if notable {
			log.Printf("[playout %d] %s", c.ID, line)
			c.appendLog(line)
		}
		// ametadata=print may prefix the line; match Peak_level anywhere.
		if mm := reAstatsPeak.FindStringSubmatch(line); mm != nil {
			ch, _ := strconv.Atoi(mm[1])
			val := audioSilence
			raw := strings.TrimSpace(mm[2])
			if raw != "inf" && raw != "-inf" {
				if parsed, err := strconv.ParseFloat(raw, 64); err == nil {
					val = parsed
				}
			}
			if val > 0 {
				val = 0
			}
			c.mu.Lock()
			switch ch {
			case 1:
				c.AudioL = val
			case 2:
				c.AudioR = val
			}
			if c.Status == StatusWaiting {
				c.Status = StatusRunning
			}
			if !c.Sending {
				c.Sending = true
				c.mu.Unlock()
				c.appendLog("receiving media (audio peaks)")
				continue
			}
			c.mu.Unlock()
		}
	}
}

func (m *Manager) markReceiving(c *Client, why string) {
	c.mu.Lock()
	wasWaiting := c.Status == StatusWaiting
	first := !c.Sending
	c.Sending = true
	c.Reconnects = 0
	if wasWaiting {
		c.Status = StatusRunning
	}
	if c.Source == SourceFile {
		c.fileArmed = true
		if c.playOrigin.IsZero() {
			c.playOrigin = time.Now()
			c.pauseBegan = time.Time{}
			c.pausedTotal = 0
		}
	}
	c.mu.Unlock()
	if first {
		c.appendLog("receiving media (" + why + ")")
	}
}

func filePlayedSecLocked(c *Client) float64 {
	if c.playOrigin.IsZero() {
		return 0
	}
	end := time.Now()
	if c.Status == StatusPaused && !c.pauseBegan.IsZero() {
		end = c.pauseBegan
	}
	total := end.Sub(c.playOrigin).Seconds() - c.pausedTotal.Seconds()
	if total < 0 {
		return 0
	}
	return total
}

func filePositionLocked(c *Client) (elapsed, remain float64) {
	pos := filePlayedSecLocked(c)
	dur := c.DurationSec
	if dur <= 0 {
		return pos, 0
	}
	// Always show position within the current pass. Never snap to EOF just because
	// Loop was toggled off while absolute play time already exceeds duration.
	display := math.Mod(pos, dur)
	if display < 0 {
		display += dur
	}
	// At a pass boundary Mod→0; show the true end only when a pass has finished and loop is off.
	if !c.Loop && pos >= dur-0.02 && display < 0.12 {
		return dur, 0
	}
	return display, dur - display
}

// watchFileEnd stops after the current pass when Loop is false.
// FFmpeg always uses -stream_loop -1 so LOOP can be toggled without restart.
func (m *Manager) watchFileEnd(c *Client, stopCh <-chan struct{}) {
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-t.C:
			c.mu.Lock()
			id := c.ID
			loop := c.Loop
			dur := c.DurationSec
			armed := c.fileArmed
			pos := filePlayedSecLocked(c)
			st := c.Status
			c.mu.Unlock()
			if st == StatusStopped || st == StatusPaused {
				continue
			}
			if loop || !armed || dur <= 0 {
				continue
			}
			// Finish the current pass, then stop (works if loop was turned off mid-play).
			if pos < dur-0.05 {
				continue
			}
			rem := math.Mod(pos, dur)
			if rem < 0 {
				rem += dur
			}
			if rem < 0.15 || rem >= dur-0.15 {
				c.appendLog("end of file – stopping (loop off)")
				_, _ = m.Stop(id)
				return
			}
		}
	}
}

func (m *Manager) watchProgress(c *Client, r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	sc.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
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
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "frame=") {
			n, err := strconv.ParseInt(strings.TrimPrefix(line, "frame="), 10, 64)
			if err == nil && n > 0 {
				m.markReceiving(c, "frames")
			}
			continue
		}
		if strings.HasPrefix(line, "out_time_ms=") {
			n, err := strconv.ParseInt(strings.TrimPrefix(line, "out_time_ms="), 10, 64)
			if err == nil && n > 0 {
				m.markReceiving(c, "out_time")
			}
			continue
		}
		if strings.HasPrefix(line, "out_time_us=") {
			n, err := strconv.ParseInt(strings.TrimPrefix(line, "out_time_us="), 10, 64)
			if err == nil && n > 0 {
				m.markReceiving(c, "out_time")
			}
			continue
		}
		mm := reBitrate.FindStringSubmatch(line)
		if mm == nil {
			continue
		}
		val, err := strconv.ParseFloat(mm[1], 64)
		if err != nil {
			continue
		}
		kbps := val
		if strings.ToLower(mm[2]) == "m" {
			kbps = val * 1000
		}
		if kbps <= 0 {
			continue
		}
		c.mu.Lock()
		c.BitrateKbps = kbps
		c.mu.Unlock()
		m.markReceiving(c, "bitrate")
	}
}
