package capture

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
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

// reAstatsPeak matches per-channel sample peak only, e.g.:
// "lavfi.astats.1.Peak_level=-18.32" (channel 1 = left, channel 2 = right)
// Do NOT also match RMS_level — ametadata can emit both and last-write would
// make meters read ~3 dB low on sine tones (and worse on program).
var reAstatsPeak = regexp.MustCompile(`lavfi\.astats\.(\d+)\.Peak_level=([-\d.]+|-?inf)`)

const audioSilence = -90.0 // treat -inf as this value

type Stream struct {
	ID           int
	Name         string
	Status       Status
	Error        string
	Format       string  // e.g. "1080i50" or "1080p50", empty when unknown
	AudioL       float64 // dBFS sample-peak left
	AudioR       float64 // dBFS sample-peak right
	EncodePreset string  // preset id applied to master UDP encode
	ffmpegInput  string
	feedURL      string
	cmd          *exec.Cmd
	stopCh       chan struct{}
	mu           sync.Mutex
}

// EncodeProfile is the always-on master encode written to the local UDP feed.
// Recording remuxes that feed with -c copy (no second encode).
type EncodeProfile struct {
	VideoCodec   string
	VideoBitrate string
	VideoMaxrate string
	VideoBufsize string
	VideoPreset  string
	VideoGOP     int
	AudioBitrate string
}

// NamedPreset is a selectable encode profile (id + label + settings).
type NamedPreset struct {
	ID      string
	Label   string
	Profile EncodeProfile
}

type Manager struct {
	streams         map[int]*Stream
	mu              sync.RWMutex
	hlsDir          string
	ffmpegBin       string
	presets         map[string]NamedPreset
	defaultPreset   string
	assignmentsPath string
	presetsPath     string
}

func NewManager(hlsDir, ffmpegBin string, presets map[string]NamedPreset, defaultPreset, assignmentsPath, presetsPath string) *Manager {
	if presets == nil {
		presets = map[string]NamedPreset{}
	}
	if defaultPreset == "" || presets[defaultPreset].ID == "" {
		for id := range presets {
			defaultPreset = id
			break
		}
	}
	return &Manager{
		streams:         make(map[int]*Stream),
		hlsDir:          hlsDir,
		ffmpegBin:       ffmpegBin,
		presets:         presets,
		defaultPreset:   defaultPreset,
		assignmentsPath: assignmentsPath,
		presetsPath:     presetsPath,
	}
}

func (m *Manager) Register(id int, name, ffmpegInput, encodePreset string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if encodePreset == "" || m.presets[encodePreset].ID == "" {
		encodePreset = m.defaultPreset
	}
	m.streams[id] = &Stream{
		ID:           id,
		Name:         name,
		Status:       StatusStopped,
		EncodePreset: encodePreset,
		ffmpegInput:  ffmpegInput,
		// Larger FIFO + overrun_nonfatal so REC can join mid-stream without dropping the writer.
		feedURL: fmt.Sprintf("udp://127.0.0.1:%d?pkt_size=1316&fifo_size=5000000&overrun_nonfatal=1", 21000+id),
	}
}

// LoadAssignments overlays persisted UI preset choices onto registered channels.
func (m *Manager) LoadAssignments() {
	if m.assignmentsPath == "" {
		return
	}
	data, err := os.ReadFile(m.assignmentsPath)
	if err != nil {
		return
	}
	var asg map[string]string
	if err := json.Unmarshal(data, &asg); err != nil {
		log.Printf("[encode] bad assignments file %s: %v", m.assignmentsPath, err)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for idStr, preset := range asg {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		s, ok := m.streams[id]
		if !ok {
			continue
		}
		if m.presets[preset].ID == "" {
			continue
		}
		s.EncodePreset = preset
	}
}

func (m *Manager) saveAssignmentsLocked() error {
	if m.assignmentsPath == "" {
		return nil
	}
	asg := make(map[string]string, len(m.streams))
	for id, s := range m.streams {
		asg[strconv.Itoa(id)] = s.EncodePreset
	}
	data, err := json.MarshalIndent(asg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.assignmentsPath, data, 0o644)
}

func (m *Manager) ListPresets() []NamedPreset {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]NamedPreset, 0, len(m.presets))
	for _, p := range m.presets {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Profile.VideoBitrate == out[j].Profile.VideoBitrate {
			return out[i].ID < out[j].ID
		}
		return bitrateSortKey(out[i].Profile.VideoBitrate) < bitrateSortKey(out[j].Profile.VideoBitrate)
	})
	return out
}

func bitrateSortKey(b string) float64 {
	b = strings.TrimSpace(strings.ToUpper(b))
	mult := 1.0
	switch {
	case strings.HasSuffix(b, "M"):
		mult = 1000
		b = strings.TrimSuffix(b, "M")
	case strings.HasSuffix(b, "K"):
		b = strings.TrimSuffix(b, "K")
	}
	v, _ := strconv.ParseFloat(b, 64)
	return v * mult
}

func (m *Manager) profileFor(presetID string) EncodeProfile {
	if p, ok := m.presets[presetID]; ok {
		return p.Profile
	}
	if p, ok := m.presets[m.defaultPreset]; ok {
		return p.Profile
	}
	return EncodeProfile{
		VideoCodec:   "h264_nvenc",
		VideoBitrate: "12M",
		VideoMaxrate: "14M",
		VideoBufsize: "20M",
		VideoPreset:  "p4",
		VideoGOP:     50,
		AudioBitrate: "192k",
	}
}

// SetEncodePreset changes the master encode preset for a channel.
// If the channel is running, capture FFmpeg is restarted to apply settings.
func (m *Manager) SetEncodePreset(id int, presetID string) error {
	m.mu.RLock()
	if m.presets[presetID].ID == "" {
		m.mu.RUnlock()
		return fmt.Errorf("unknown encode preset %q", presetID)
	}
	s, ok := m.streams[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("channel %d not found", id)
	}

	s.mu.Lock()
	prev := s.EncodePreset
	wasRunning := s.Status == StatusRunning
	s.EncodePreset = presetID
	s.mu.Unlock()

	m.mu.Lock()
	err := m.saveAssignmentsLocked()
	m.mu.Unlock()
	if err != nil {
		log.Printf("[encode] failed to persist assignments: %v", err)
	}

	if prev == presetID {
		return nil
	}
	log.Printf("[channel %d] encode preset %s → %s", id, prev, presetID)
	if !wasRunning {
		return nil
	}
	return m.restart(id)
}

func (m *Manager) restart(id int) error {
	if err := m.Stop(id); err != nil {
		// Already stopped is fine for apply.
		if !strings.Contains(err.Error(), "is not running") {
			return err
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st, ok := m.StatusByID(id)
		if ok && st == StatusStopped {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return m.Start(id)
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
	s.AudioL = audioSilence
	s.AudioR = audioSilence

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

	if s.stopCh == nil {
		return fmt.Errorf("channel %d stop channel is not initialized", id)
	}
	// Idempotent stop: if another request already closed stopCh, do nothing.
	select {
	case <-s.stopCh:
		return nil
	default:
		close(s.stopCh)
	}
	return nil
}

func (m *Manager) StopAll() {
	m.mu.RLock()
	ids := make([]int, 0, len(m.streams))
	for id, s := range m.streams {
		s.mu.Lock()
		running := s.Status == StatusRunning
		s.mu.Unlock()
		if running {
			ids = append(ids, id)
		}
	}
	m.mu.RUnlock()
	for _, id := range ids {
		_ = m.Stop(id)
	}
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

func (m *Manager) FeedURL(id int) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.streams[id]
	if !ok {
		return "", false
	}
	return s.feedURL, true
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

	for {
		select {
		case <-s.stopCh:
			m.killStream(s)
			m.removeThumb(s.ID)
			s.mu.Lock()
			s.Status = StatusStopped
			s.Format = ""
			s.AudioL = audioSilence
			s.AudioR = audioSilence
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

// runFFmpeg starts one FFmpeg per DeckLink channel with multiple outputs (fan-out):
// 1) JPEG thumbnail for grid preview
// 2) Master H.264/AAC MPEG-TS UDP feed — recording remuxes this with -c copy
// 3) HLS audio-only stream for browser monitoring
// Audio levels are parsed from astats metadata on a shared stereo branch in filter_complex.
func (m *Manager) runFFmpeg(s *Stream) error {
	outDir := filepath.Join(m.hlsDir, fmt.Sprintf("%d", s.ID))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	m.removeAudioHLS(s.ID)

	thumbPath := filepath.Join(outDir, "thumb.jpg")
	audioPlaylist := filepath.Join(outDir, "audio.m3u8")
	audioSegmentPattern := filepath.Join(outDir, "audio_%03d.ts")
	inputArgs := sanitizeInputArgs(shellSplit(s.ffmpegInput))
	s.mu.Lock()
	presetID := s.EncodePreset
	s.mu.Unlock()
	enc := m.profileFor(presetID)
	gop := strconv.Itoa(enc.VideoGOP)

	// One decode for all audio consumers: recording, meters and browser monitor.
	// astats on the shared stereo stream avoids inconsistent per-output audio decodes.
	filterGraph := "[0:v]yadif=mode=0:deint=interlaced,split=2[vrec][vthumb];" +
		"[vthumb]scale=640:360,format=yuv420p[vthumbout];" +
		"[vrec]format=yuv420p[vrecout];" +
		"[0:a]pan=stereo|c0=c0|c1=c1,asplit=3[arec][ameter][ahls];" +
		// Only measure Peak_level so ametadata=print cannot emit RMS that overwrites meters.
		// Do not set key= on ametadata — only one key is accepted and would drop L or R.
		"[ameter]astats=metadata=1:reset=0.25:measure_perchannel=Peak_level:measure_overall=none," +
		"ametadata=print,anullsink"

	args := []string{"-y"}
	args = append(args, inputArgs...)
	args = append(args,
		"-filter_complex", filterGraph,
		// Output #1: thumbnail JPEG (updated every second)
		"-map", "[vthumbout]",
		"-r", "1",
		"-q:v", "4",
		"-update", "1",
		"-f", "image2",
		thumbPath,
		// Output #2: HLS audio-only for browser monitoring
		"-map", "[ahls]",
		"-c:a", "aac",
		"-b:a", enc.AudioBitrate,
		"-ar", "48000",
		"-ac", "2",
		"-f", "hls",
		"-hls_time", "1",
		"-hls_list_size", "4",
		"-hls_flags", "delete_segments+independent_segments+omit_endlist",
		"-hls_segment_filename", audioSegmentPattern,
		audioPlaylist,
		// Output #3: master encode feed (REC remuxes with -c copy)
		"-map", "[vrecout]",
		"-map", "[arec]",
		"-c:v", enc.VideoCodec,
		"-b:v", enc.VideoBitrate,
		"-maxrate", enc.VideoMaxrate,
		"-bufsize", enc.VideoBufsize,
		"-preset", enc.VideoPreset,
		"-g", gop,
		// Avoid libx264-only / encoder-private flags here — this FFmpeg build
		// rejects unknown options with "Error splitting the argument list".
		"-c:a", "aac",
		"-b:a", enc.AudioBitrate,
		"-ar", "48000",
		"-ac", "2",
		"-f", "mpegts",
		"-mpegts_flags", "+resend_headers",
		s.feedURL,
	)

	cmd := exec.Command(m.ffmpegBin, args...)

	s.mu.Lock()
	s.cmd = cmd
	s.mu.Unlock()

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	// Stderr parser: signal format + audio levels + signal loss.
	go func() {
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "Error") || strings.Contains(line, "error:") {
				log.Printf("[channel %d] %s", s.ID, line)
			}
			if mm := reSignalFormat.FindStringSubmatch(line); mm != nil {
				height := mm[2]
				rateStr := mm[3]
				interlaced := mm[4] != ""
				rateFloat, _ := strconv.ParseFloat(rateStr, 64)
				rateInt := int(math.Round(rateFloat))
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
			if mm := reAstatsPeak.FindStringSubmatch(line); mm != nil {
				ch, _ := strconv.Atoi(mm[1])
				val := audioSilence
				raw := strings.TrimSpace(mm[2])
				if raw != "inf" && raw != "-inf" {
					parsed, err := strconv.ParseFloat(raw, 64)
					if err == nil {
						val = parsed
					}
				}
				if val > 0 {
					val = 0
				}
				s.mu.Lock()
				switch ch {
				case 1:
					s.AudioL = val
				case 2:
					s.AudioR = val
				}
				s.mu.Unlock()
			}
			if strings.Contains(line, "No input signal detected") {
				s.mu.Lock()
				s.Format = ""
				s.AudioL = audioSilence
				s.AudioR = audioSilence
				s.mu.Unlock()
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

func (m *Manager) removeAudioHLS(id int) {
	dir := filepath.Join(m.hlsDir, fmt.Sprintf("%d", id))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "audio") {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}
