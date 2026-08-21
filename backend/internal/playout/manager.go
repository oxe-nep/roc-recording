package playout

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
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
	StatusWaiting Status = "waiting"
	StatusRunning Status = "running"
)

type Mode string

const (
	ModeListener Mode = "listener"
	ModeCaller   Mode = "caller"
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
	DeckLinkOut bool // when false, SRT preview only (thumb/meters) — ignore Device
	Mode        Mode
	Port        int
	Target      string
	Passphrase  string
	LatencyMS   int

	AudioL      float64
	AudioR      float64
	BitrateKbps float64
	Sending     bool
	Reconnects  int
	LastError   string

	// deviceTryAlt: next DeckLink open uses the alternate of unique-id vs open_name.
	deviceTryAlt bool

	cmd      *exec.Cmd
	stopCh   chan struct{}
	logLines []string
	mu       sync.Mutex
}

type ClientInfo struct {
	ID          int     `json:"id"`
	Name        string  `json:"name"`
	Status      Status  `json:"status"`
	Device      string  `json:"device"`
	DeviceLabel string  `json:"device_label,omitempty"`
	FormatCode  string  `json:"format_code"`
	DeckLinkOut bool    `json:"decklink_out"`
	Mode        Mode    `json:"mode"`
	Port        int     `json:"port"`
	Target      string  `json:"target"`
	HasPass     bool    `json:"has_passphrase"`
	LatencyMS   int     `json:"latency_ms"`
	BitrateKbps float64 `json:"bitrate_kbps,omitempty"`
	Sending     bool    `json:"sending"`
	Reconnects  int     `json:"reconnects"`
	Error       string  `json:"error,omitempty"`
	ListenURL   string  `json:"listen_url,omitempty"`
}

type Manager struct {
	mu           sync.RWMutex
	clients      map[int]*Client
	nextID       int
	ffmpegBin    string
	hlsDir       string
	settingsPath string
	publicHost   string
	devCache     deviceCache
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
	return &Manager{
		clients:      make(map[int]*Client),
		nextID:       1,
		ffmpegBin:    ffmpegBin,
		hlsDir:       hlsDir,
		settingsPath: settingsPath,
		publicHost:   publicHost,
	}
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
			DeckLinkOut: cfg.DeckLinkOut, // default false — SRT preview until explicitly enabled
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
		Mode:        mode,
		Port:        port,
		Target:      strings.TrimSpace(in.Target),
		Passphrase:  in.Passphrase,
		LatencyMS:   lat,
		AudioL:      audioSilence,
		AudioR:      audioSilence,
		logLines:    make([]string, 0, 32),
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
	if c.Status != StatusStopped {
		c.mu.Unlock()
		return ClientInfo{}, fmt.Errorf("stop decode client before changing settings")
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
		c.DeckLinkOut = *in.DeckLinkOut
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
	active := c.Status != StatusStopped
	c.mu.Unlock()
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
	return c.AudioL, c.AudioR, c.Status == StatusRunning || c.Status == StatusWaiting
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
	info := ClientInfo{
		ID:          c.ID,
		Name:        c.Name,
		Status:      c.Status,
		Device:      c.Device,
		DeviceLabel: label,
		FormatCode:  c.FormatCode,
		DeckLinkOut: c.DeckLinkOut,
		Mode:        c.Mode,
		Port:        c.Port,
		Target:      c.Target,
		HasPass:     c.Passphrase != "",
		LatencyMS:   c.LatencyMS,
		BitrateKbps: c.BitrateKbps,
		Sending:     c.Sending && (c.Status == StatusRunning || c.Status == StatusWaiting),
		Reconnects:  c.Reconnects,
		Error:       c.LastError,
	}
	if c.Mode == ModeListener {
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
	// DeckLink is opt-in (decklink_out). Existing clients keep a saved device but
	// default decklink_out=false so SRT preview works without opening sinks.
	wantDeckLink := c.DeckLinkOut
	deviceRaw := strings.TrimSpace(c.Device)
	formatCode := strings.TrimSpace(c.FormatCode)
	if wantDeckLink {
		if deviceRaw == "" {
			c.mu.Unlock()
			return ClientInfo{}, fmt.Errorf("DeckLink output device is required when decklink_out is enabled")
		}
		if formatCode == "" {
			c.mu.Unlock()
			return ClientInfo{}, fmt.Errorf("output format is required when decklink_out is enabled")
		}
	}
	if c.Mode == ModeCaller && strings.TrimSpace(c.Target) == "" {
		c.mu.Unlock()
		return ClientInfo{}, fmt.Errorf("caller mode requires a target")
	}
	mode := c.Mode
	port := c.Port
	c.mu.Unlock()

	device := ""
	if wantDeckLink {
		device = m.ResolveOpenDevice(deviceRaw)
	}

	if err := m.assertNoConflicts(id, device, mode, port); err != nil {
		return ClientInfo{}, err
	}

	c.mu.Lock()
	if wantDeckLink {
		c.Device = device
	}
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
	c.AudioL = audioSilence
	c.AudioR = audioSilence
	info := m.infoLocked(c)
	c.mu.Unlock()

	if wantDeckLink {
		c.appendLog("start requested – waiting for SRT (+ DeckLink out)")
	} else {
		c.appendLog("start requested – SRT preview only (DeckLink out disabled)")
	}
	go m.runLoop(c)
	return info, nil
}

func (m *Manager) assertNoConflicts(selfID int, device string, mode Mode, port int) error {
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
		other.mu.Unlock()
		if !active {
			continue
		}
		if device != "" && dev == device {
			return fmt.Errorf("DeckLink output %q is already in use by decode %d", device, id)
		}
		if mode == ModeListener && omode == ModeListener && oport == port {
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
	cmd := c.cmd
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	c.mu.Unlock()
	c.appendLog("stop requested")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		done := c.Status == StatusStopped && c.cmd == nil
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
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
			c.cmd = nil
		}
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
		if err != nil {
			c.mu.Lock()
			c.Reconnects++
			c.LastError = ""
			c.mu.Unlock()
			c.appendLog(fmt.Sprintf("ffmpeg exited: %v – retry in %s", err, restartDelay))
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
	thumbPath := filepath.Join(outDir, "thumb.jpg")
	audioPlaylist := filepath.Join(outDir, "audio.m3u8")
	audioSeg := filepath.Join(outDir, "audio_%03d.ts")

	c.mu.Lock()
	wantDeckLink := c.DeckLinkOut
	deviceRaw := strings.TrimSpace(c.Device)
	formatCode := strings.TrimSpace(c.FormatCode)
	mode := c.Mode
	srtURL, err := m.srtInputURL(c)
	c.mu.Unlock()
	if err != nil {
		return err
	}

	useDeckLink := wantDeckLink && deviceRaw != "" && formatCode != ""
	device := ""
	deviceLabel := ""
	openPrimary := ""
	openAlt := ""
	if useDeckLink {
		// Refresh sinks so open_name / labels match current FFmpeg + driver state.
		if _, err := m.devCache.refresh(m.ffmpegBin); err != nil {
			c.appendLog(fmt.Sprintf("decklink device refresh: %v", err))
		}
		device = m.ResolveOpenDevice(deviceRaw)
		if d, ok := m.FindDevice(device); ok {
			deviceLabel = d.Label
			openPrimary = d.OpenName
			if openPrimary == "" {
				openPrimary = d.Label
			}
			if openPrimary == "" {
				openPrimary = d.Name
			}
			openAlt = d.Name
			if openPrimary == openAlt {
				openAlt = d.Label
			}
		} else {
			deviceLabel = strings.TrimSpace(c.DeviceLabel)
			if deviceLabel == "" {
				deviceLabel = m.LookupDeviceLabel(device)
			}
			// Prefer display label: unique IDs from -sinks often fail write_header on DeckLink IP.
			if deviceLabel != "" && deviceLabel != device {
				openPrimary = deviceLabel
				openAlt = device
			} else {
				openPrimary = device
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
	if useDeckLink {
		devs, _ := m.Devices(false)
		if f, ok := LookupFormat(formatCode, devs); ok {
			fmtInfo = f
			w, h, fps = f.Width, f.Height, f.FPS
			if fps <= 0 {
				fps = 25
			}
		} else {
			w, h, fps = formatGeometry(formatCode)
			fmtInfo.Interlaced = formatCodeLooksInterlaced(formatCode)
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

	var filter string
	var args []string
	base := []string{
		"-hide_banner",
		// info (not warning): ametadata=print peak lines are AV_LOG_INFO and drive UI meters.
		"-loglevel", "info",
		"-fflags", "+genpts+discardcorrupt",
		"-progress", "pipe:1",
		"-nostats",
		"-analyzeduration", "2M",
		"-probesize", "2M",
		// Encode STREAM remuxes MPEG-TS over SRT — force demuxer so probe does not stall.
		"-f", "mpegts",
		"-i", srtURL,
	}

	if useDeckLink {
		// Progressive: scale → fps → uyvy422.
		// Interlaced (e.g. Hi50): produce 2× frames then tinterlace into fields.
		var vchain string
		if fmtInfo.Interlaced {
			vchain = fmt.Sprintf(
				"[0:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%g,tinterlace=interleave_top,format=uyvy422,split=2[vout][vt]",
				w, h, w, h, fps*2,
			)
		} else {
			vchain = fmt.Sprintf(
				"[0:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%g,format=uyvy422,split=2[vout][vt]",
				w, h, w, h, fps,
			)
		}
		filter = vchain + ";" +
			"[vt]scale=640:360,format=yuv420p[vthumb];" +
			"[0:a]pan=stereo|c0=c0|c1=c1,asplit=3[aout][ahls][ameter];" +
			"[ameter]astats=metadata=1:reset=1:measure_perchannel=Peak_level:measure_overall=none," +
			"ametadata=print,anullsink"
		args = append(base,
			"-filter_complex", filter,
			"-map", "[vthumb]",
			"-r", "1",
			"-q:v", "4",
			"-update", "1",
			"-f", "image2",
			thumbPath,
			"-map", "[ahls]",
			"-c:a", "aac",
			"-b:a", "128k",
			"-ar", "48000",
			"-ac", "2",
			"-f", "hls",
			"-hls_time", "1",
			"-hls_list_size", "4",
			"-hls_flags", "delete_segments+independent_segments+omit_endlist",
			"-hls_segment_filename", audioSeg,
			audioPlaylist,
			"-map", "[vout]",
			"-map", "[aout]",
			"-c:v", "rawvideo",
			"-pix_fmt", "uyvy422",
			"-c:a", "pcm_s16le",
			"-ar", "48000",
			"-ac", "2",
			"-r", fmt.Sprintf("%g", fps),
			"-s", fmt.Sprintf("%dx%d", w, h),
		)
		if fmtInfo.Interlaced {
			args = append(args, "-flags", "+ilme+ildct", "-field_order", "tt")
		}
		if formatCode != "" && !isAllDigits(formatCode) {
			args = append(args, "-format_code", strings.TrimSpace(formatCode))
		}
		args = append(args, "-preroll", "0.5", "-f", "decklink", openDevice)
	} else {
		// Preview only. Keep meters in filter_complex (anullsink) — never write
		// a null muxer to stdout, that steals -progress pipe:1 and freezes UI state.
		filter =
			"[0:v]fps=1,scale=640:360:force_original_aspect_ratio=decrease,pad=640:360:(ow-iw)/2:(oh-ih)/2,format=yuv420p[vthumb];" +
				"[0:a]asplit=2[ahls][ameter];" +
				"[ameter]astats=metadata=1:reset=1:measure_perchannel=Peak_level:measure_overall=none," +
				"ametadata=print,anullsink"
		args = append(base,
			"-filter_complex", filter,
			"-map", "[vthumb]",
			"-q:v", "4",
			"-update", "1",
			"-f", "image2",
			thumbPath,
			"-map", "[ahls]",
			"-c:a", "aac",
			"-b:a", "128k",
			"-ar", "48000",
			"-ac", "2",
			"-f", "hls",
			"-hls_time", "1",
			"-hls_list_size", "4",
			"-hls_flags", "delete_segments+independent_segments+omit_endlist",
			"-hls_segment_filename", audioSeg,
			audioPlaylist,
		)
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
			"starting FFmpeg (%s → decklink %q label=%q primary=%q alt=%q format=%s interlaced=%v)",
			mode, openDevice, deviceLabel, openPrimary, openAlt, formatCode, fmtInfo.Interlaced,
		))
		c.appendLog("decklink args: " + strings.Join(decklinkArgSummary(args, openDevice), " "))
	} else {
		c.appendLog(fmt.Sprintf("starting FFmpeg (%s → preview only)", mode))
	}

	if err := cmd.Start(); err != nil {
		c.mu.Lock()
		c.cmd = nil
		c.mu.Unlock()
		return err
	}

	go m.watchStderr(c, stderr)
	go m.watchProgress(c, stdout)

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
		// Alternate unique-id vs probe-proven open_name/label on the next loop.
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
	case "hp50", "hi50":
		return 1920, 1080, 25
	case "hp25", "hi25":
		return 1920, 1080, 25
	case "hp60", "hp59.94", "hp5994":
		return 1920, 1080, 30
	case "hp30", "hp29.97", "hp29":
		return 1920, 1080, 30
	case "hp24", "hp23.98":
		return 1920, 1080, 24
	case "hp720p50", "hp50p":
		return 1280, 720, 50
	}
	return w, h, fps
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
	c.mu.Unlock()
	if first {
		c.appendLog("receiving media (" + why + ")")
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
