package playout

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
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
	ID         int
	Name       string
	Status     Status
	Device     string
	FormatCode string
	Mode       Mode
	Port       int
	Target     string
	Passphrase string
	LatencyMS  int

	AudioL      float64
	AudioR      float64
	BitrateKbps float64
	Sending     bool
	Reconnects  int
	LastError   string

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
	FormatCode  string  `json:"format_code"`
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
	Name       string `json:"name"`
	Device     string `json:"device"`
	FormatCode string `json:"format_code"`
	Mode       Mode   `json:"mode"`
	Port       int    `json:"port"`
	Target     string `json:"target"`
	Passphrase string `json:"passphrase"`
	LatencyMS  int    `json:"latency_ms"`
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

func (m *Manager) Devices() ([]Device, error) {
	return m.devCache.get(m.ffmpegBin, 15*time.Second)
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
			ID:         id,
			Name:       name,
			Status:     StatusStopped,
			Device:     cfg.Device,
			FormatCode: cfg.FormatCode,
			Mode:       mode,
			Port:       port,
			Target:     strings.TrimSpace(cfg.Target),
			Passphrase: cfg.Passphrase,
			LatencyMS:  lat,
			AudioL:     audioSilence,
			AudioR:     audioSilence,
			logLines:   make([]string, 0, 32),
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
			Name:       c.Name,
			Device:     c.Device,
			FormatCode: c.FormatCode,
			Mode:       c.Mode,
			Port:       c.Port,
			Target:     c.Target,
			Passphrase: c.Passphrase,
			LatencyMS:  c.LatencyMS,
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
	Name       string `json:"name"`
	Device     string `json:"device"`
	FormatCode string `json:"format_code"`
	Mode       string `json:"mode"`
	Port       int    `json:"port"`
	Target     string `json:"target"`
	Passphrase string `json:"passphrase"`
	LatencyMS  int    `json:"latency_ms"`
}

type UpdateInput struct {
	Name       *string `json:"name"`
	Device     *string `json:"device"`
	FormatCode *string `json:"format_code"`
	Mode       *string `json:"mode"`
	Port       *int    `json:"port"`
	Target     *string `json:"target"`
	Passphrase *string `json:"passphrase"`
	LatencyMS  *int    `json:"latency_ms"`
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
		ID:         id,
		Name:       name,
		Status:     StatusStopped,
		Device:     strings.TrimSpace(in.Device),
		FormatCode: strings.TrimSpace(in.FormatCode),
		Mode:       mode,
		Port:       port,
		Target:     strings.TrimSpace(in.Target),
		Passphrase: in.Passphrase,
		LatencyMS:  lat,
		AudioL:     audioSilence,
		AudioR:     audioSilence,
		logLines:   make([]string, 0, 32),
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
		c.Device = strings.TrimSpace(*in.Device)
	}
	if in.FormatCode != nil {
		c.FormatCode = strings.TrimSpace(*in.FormatCode)
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
	info := ClientInfo{
		ID:          c.ID,
		Name:        c.Name,
		Status:      c.Status,
		Device:      c.Device,
		FormatCode:  c.FormatCode,
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
	q.Set("mode", "listener")
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
	if strings.TrimSpace(c.Device) == "" {
		c.mu.Unlock()
		return ClientInfo{}, fmt.Errorf("DeckLink output device is required")
	}
	if strings.TrimSpace(c.FormatCode) == "" {
		c.mu.Unlock()
		return ClientInfo{}, fmt.Errorf("output format is required")
	}
	if c.Mode == ModeCaller && strings.TrimSpace(c.Target) == "" {
		c.mu.Unlock()
		return ClientInfo{}, fmt.Errorf("caller mode requires a target")
	}
	device := c.Device
	mode := c.Mode
	port := c.Port
	c.mu.Unlock()

	if err := m.assertNoConflicts(id, device, mode, port); err != nil {
		return ClientInfo{}, err
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
	c.AudioL = audioSilence
	c.AudioR = audioSilence
	info := m.infoLocked(c)
	c.mu.Unlock()

	c.appendLog("start requested – waiting for SRT")
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
	device := c.Device
	formatCode := c.FormatCode
	mode := c.Mode
	srtURL, err := m.srtInputURL(c)
	w, h, fps := formatGeometry(formatCode)
	c.mu.Unlock()
	if err != nil {
		return err
	}

	// Decode SRT → scale to selected DeckLink mode → DeckLink + thumb + HLS audio + meters.
	filter := fmt.Sprintf(
		"[0:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,fps=%g,format=uyvy422,split=2[vout][vt];"+
			"[vt]scale=640:360,format=yuv420p[vthumb];"+
			"[0:a]pan=stereo|c0=c0|c1=c1,asplit=3[aout][ahls][ameter];"+
			"[ameter]astats=metadata=1:reset=1:measure_perchannel=Peak_level:measure_overall=none,"+
			"ametadata=print,anullsink",
		w, h, w, h, fps,
	)

	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-fflags", "+genpts+discardcorrupt",
		"-progress", "pipe:1",
		"-nostats",
		"-i", srtURL,
		"-filter_complex", filter,
		// Thumbnail
		"-map", "[vthumb]",
		"-r", "1",
		"-q:v", "4",
		"-update", "1",
		"-f", "image2",
		thumbPath,
		// Browser audio monitor
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
		// DeckLink output
		"-map", "[vout]",
		"-map", "[aout]",
		"-pix_fmt", "uyvy422",
		"-f", "decklink",
		"-format_code", formatCode,
		device,
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
	c.appendLog(fmt.Sprintf("starting FFmpeg (%s → %s %s)", mode, device, formatCode))

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
		c.mu.Unlock()
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
	q.Set("latency", strconv.Itoa(lat))
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

func formatGeometry(code string) (w, h int, fps float64) {
	// Sensible defaults; refine from known Blackmagic codes when possible.
	w, h, fps = 1920, 1080, 25
	switch strings.ToLower(code) {
	case "hp50", "hi50":
		return 1920, 1080, 25
	case "hp25", "hi25":
		return 1920, 1080, 25
	case "hp60", "hp59.94", "hp5994":
		return 1920, 1080, 30
	case "hp30", "hp29.97":
		return 1920, 1080, 30
	case "hp24", "hp23.98":
		return 1920, 1080, 24
	case "hp720p50", "hp50p":
		return 1280, 720, 50
	}
	// Try parse trailing digits as field/frame rate hint for scale/fps filter only.
	return w, h, fps
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
			c.mu.Unlock()
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
		if !c.Sending {
			c.Sending = true
			c.Status = StatusRunning
			c.mu.Unlock()
			c.appendLog("receiving media")
			continue
		}
		if c.Status == StatusWaiting {
			c.Status = StatusRunning
		}
		c.mu.Unlock()
	}
}
