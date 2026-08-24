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

	"github.com/roc-recording/backend/internal/audiox"
	hlsout "github.com/roc-recording/backend/internal/hls"
)

type Status string

const (
	StatusStopped Status = "stopped"
	StatusWaiting Status = "waiting" // capture loop on, no valid input yet
	StatusRunning Status = "running" // DeckLink format detected / signal present
	StatusError   Status = "error"
)

func isActiveStatus(st Status) bool {
	return st == StatusRunning || st == StatusWaiting
}

// reSignalFormat matches lines like:
// "Found Decklink mode 1920 x 1080 with rate 25.00(i)" or "50.00"
var reSignalFormat = regexp.MustCompile(`Found Decklink mode (\d+) x (\d+) with rate ([\d.]+)(\(i\))?`)

// reAstatsPeak matches per-channel sample peak only, e.g.:
// "lavfi.astats.1.Peak_level=-18.32" (channel 1 = left, channel 2 = right)
// Do NOT also match RMS_level — ametadata can emit both and last-write would
// make meters read ~3 dB low on sine tones (and worse on program).
var reAstatsPeak = regexp.MustCompile(`lavfi\.astats\.(\d+)\.Peak_level=([-\d.]+|-?inf)`)

const audioSilence = audiox.Silence // treat -inf as this value

const streamLogCap = 200

type Stream struct {
	ID           int
	Name         string
	Status       Status
	Error        string
	Format       string // e.g. "1080i50" or "1080p50", empty when unknown
	Audio        [audiox.Channels]float64
	EncodePreset string // preset id applied to master UDP encode
	ffmpegInput  string
	feedURL      string
	cmd          *exec.Cmd
	stopCh       chan struct{}
	logLines     []string
	mu           sync.Mutex
}

// Snapshot returns mutable fields under the stream lock for safe API responses.
// Error is intentionally omitted from the operator UI path — use Logs() for detail.
func (s *Stream) Snapshot() (status Status, errStr, format, preset string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Status, "", s.Format, s.EncodePreset
}

func (s *Stream) appendLog(msg string) {
	line := time.Now().Format("15:04:05") + " " + msg
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logLines = append(s.logLines, line)
	if len(s.logLines) > streamLogCap {
		s.logLines = append([]string(nil), s.logLines[len(s.logLines)-streamLogCap:]...)
	}
}

// Logs returns a copy of recent channel log lines (newest last).
func (m *Manager) Logs(id int) ([]string, bool) {
	m.mu.RLock()
	s, ok := m.streams[id]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.logLines))
	copy(out, s.logLines)
	return out, true
}

// EncodeProfile is the always-on master encode written to the local UDP feed.
// Recording remuxes that feed with -c copy (no second encode).
type EncodeProfile struct {
	VideoCodec    string
	VideoBitrate  string
	VideoMaxrate  string
	VideoBufsize  string
	VideoPreset   string
	VideoGOP      int
	AudioBitrate  string
	AudioChannels int // 2 = AAC stereo (default), 8 = four AAC stereo pairs in MPEG-TS
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
	codecCache      codecCache
	startGuard      func(id int) error
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
		startGuard:      nil,
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
		// Multicast master feed so REC and SRT can both subscribe at once.
		// Unicast 127.0.0.1 only delivers to one reader — the second remux starves.
		feedURL:  masterFeedURL(id),
		logLines: make([]string, 0, 32),
	}
}

// masterFeedURL is the MPEG-TS UDP endpoint for the always-on capture encode.
// Same URL is used as capture output and as REC/SRT input (reuse=1 for multi-reader).
func masterFeedURL(id int) string {
	return fmt.Sprintf(
		"udp://239.255.28.%d:%d?pkt_size=1316&fifo_size=5000000&overrun_nonfatal=1&reuse=1&ttl=1",
		id,
		21000+id,
	)
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
	m.mu.RLock()
	defer m.mu.RUnlock()
	if p, ok := m.presets[presetID]; ok {
		return p.Profile
	}
	if p, ok := m.presets[m.defaultPreset]; ok {
		return p.Profile
	}
	return EncodeProfile{
		VideoCodec:    "h264_nvenc",
		VideoBitrate:  "12M",
		VideoMaxrate:  "14M",
		VideoBufsize:  "20M",
		VideoPreset:   "p4",
		VideoGOP:      50,
		AudioBitrate:  "192k",
		AudioChannels: 2,
	}
}

// SetEncodePreset stores the encode preset and restarts capture if the channel
// is already running, so the live UDP feed matches the assignment immediately.
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
	s.EncodePreset = presetID
	active := isActiveStatus(s.Status)
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
	if !active {
		log.Printf("[channel %d] encode preset %s → %s (applies on next start)", id, prev, presetID)
		return nil
	}
	log.Printf("[channel %d] encode preset %s → %s – restarting capture", id, prev, presetID)
	s.appendLog(fmt.Sprintf("encode preset %s → %s – restarting capture", prev, presetID))
	if err := m.restart(id); err != nil {
		return fmt.Errorf("preset saved but capture restart failed: %w", err)
	}
	return nil
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
	guard := m.startGuard
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("channel %d not found", id)
	}
	if guard != nil {
		if err := guard(id); err != nil {
			return err
		}
	}

	s.mu.Lock()
	if isActiveStatus(s.Status) {
		s.mu.Unlock()
		return fmt.Errorf("channel %d is already running", id)
	}

	s.stopCh = make(chan struct{})
	// Stay in waiting until DeckLink reports a format — then flip to running.
	s.Status = StatusWaiting
	s.Error = ""
	s.Format = ""
	s.Audio = audiox.SilencePeaks()
	s.mu.Unlock()

	s.appendLog("channel start requested – waiting for signal")
	go m.runLoop(s)
	return nil
}

// SetStartGuard blocks Start when fn returns an error (e.g. TC Burn-in exclusive).
func (m *Manager) SetStartGuard(fn func(id int) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startGuard = fn
}

func (m *Manager) Stop(id int) error {
	m.mu.RLock()
	s, ok := m.streams[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("channel %d not found", id)
	}

	s.mu.Lock()
	if !isActiveStatus(s.Status) {
		s.mu.Unlock()
		return fmt.Errorf("channel %d is not running", id)
	}

	if s.stopCh == nil {
		s.mu.Unlock()
		return fmt.Errorf("channel %d stop channel is not initialized", id)
	}
	// Idempotent stop: if another request already closed stopCh, do nothing.
	select {
	case <-s.stopCh:
		s.mu.Unlock()
		return nil
	default:
		close(s.stopCh)
	}
	s.mu.Unlock()
	s.appendLog("channel stop requested")
	return nil
}

func (m *Manager) StopAll() {
	m.mu.RLock()
	ids := make([]int, 0, len(m.streams))
	for id, s := range m.streams {
		s.mu.Lock()
		active := isActiveStatus(s.Status)
		s.mu.Unlock()
		if active {
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
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
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

func (m *Manager) AudioLevels(id int) (ch []float64, ok bool) {
	m.mu.RLock()
	s, found := m.streams[id]
	m.mu.RUnlock()
	if !found {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return audiox.Slice(s.Audio), isActiveStatus(s.Status)
}

// MasterAudioIsPCM reports whether the current encode preset writes PCM (8ch) on the UDP feed.
func (m *Manager) MasterAudioIsPCM(id int) bool {
	m.mu.RLock()
	s, ok := m.streams[id]
	m.mu.RUnlock()
	if !ok {
		return false
	}
	s.mu.Lock()
	presetID := s.EncodePreset
	s.mu.Unlock()
	return audiox.NormalizeCount(m.profileFor(presetID).AudioChannels) == audiox.Channels
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

// ChannelExists reports whether an encode channel id is registered.
func (m *Manager) ChannelExists(id int) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.streams[id]
	return ok
}

// IsActive reports whether capture is waiting or running.
func (m *Manager) IsActive(id int) bool {
	st, ok := m.StatusByID(id)
	return ok && isActiveStatus(st)
}

// InputArgs returns sanitized DeckLink capture args for channel id (for TC Burn-in).
func (m *Manager) InputArgs(id int) ([]string, error) {
	args, err := m.rawInputArgs(id)
	if err != nil {
		return nil, err
	}
	return sanitizeInputArgs(args), nil
}

// InputArgsForTC returns DeckLink input args for TC.
// Same as encode: no signal_loss_action repeat, so loss exits FFmpeg and the
// TC loop can restart when the router brings signal back (no freeze-frame).
func (m *Manager) InputArgsForTC(id int) ([]string, error) {
	args, err := m.rawInputArgs(id)
	if err != nil {
		return nil, err
	}
	return sanitizeInputArgs(args), nil
}

func (m *Manager) rawInputArgs(id int) ([]string, error) {
	m.mu.RLock()
	s, ok := m.streams[id]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("channel %d not found", id)
	}
	s.mu.Lock()
	raw := s.ffmpegInput
	s.mu.Unlock()
	args := shellSplit(raw)
	if len(args) == 0 {
		return nil, fmt.Errorf("channel %d has no ffmpeg_input", id)
	}
	return args, nil
}

func ensureSignalLossRepeat(args []string) []string {
	for i := 0; i < len(args); i++ {
		if args[i] == "-signal_loss_action" {
			return args
		}
	}
	for i := 0; i < len(args); i++ {
		if args[i] == "-i" {
			out := append([]string{}, args[:i]...)
			out = append(out, "-signal_loss_action", "repeat")
			out = append(out, args[i:]...)
			return out
		}
	}
	return append(append([]string{}, args...), "-signal_loss_action", "repeat")
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
			m.removePreview(s.ID)
			s.mu.Lock()
			s.Status = StatusStopped
			s.Format = ""
			s.Error = ""
			s.Audio = audiox.SilencePeaks()
			s.mu.Unlock()
			s.appendLog("channel stopped")
			return
		default:
		}

		s.appendLog("starting FFmpeg")
		start := time.Now()
		err := m.runFFmpeg(s)

		select {
		case <-s.stopCh:
			s.mu.Lock()
			s.Status = StatusStopped
			s.Format = ""
			s.Error = ""
			s.Audio = audiox.SilencePeaks()
			s.mu.Unlock()
			m.removePreview(s.ID)
			s.appendLog("channel stopped")
			return
		default:
		}

		// No signal / FFmpeg exited — stay in waiting until DeckLink reports a format again.
		s.mu.Lock()
		s.Format = ""
		s.Error = ""
		s.Audio = audiox.SilencePeaks()
		s.Status = StatusWaiting
		s.mu.Unlock()
		m.removePreview(s.ID)

		if err == nil {
			consecutiveFails = 0
			s.appendLog("FFmpeg exited cleanly – restarting")
			select {
			case <-s.stopCh:
				s.mu.Lock()
				s.Status = StatusStopped
				s.mu.Unlock()
				m.removePreview(s.ID)
				s.appendLog("channel stopped")
				return
			case <-time.After(500 * time.Millisecond):
			}
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

		msg := fmt.Sprintf("FFmpeg exited after %s: %v (fail #%d) – retry in %s",
			uptime.Round(time.Millisecond), err, consecutiveFails, delay)
		log.Printf("[channel %d] %s", s.ID, msg)
		s.appendLog(msg)

		select {
		case <-s.stopCh:
			s.mu.Lock()
			s.Status = StatusStopped
			s.mu.Unlock()
			m.removePreview(s.ID)
			s.appendLog("channel stopped")
			return
		case <-time.After(delay):
		}
	}
}

// runFFmpeg starts one FFmpeg per DeckLink channel with multiple outputs (fan-out):
//  1. Low-latency HLS A/V preview for card UI
//  2. Master H.264 + AAC MPEG-TS UDP feed — recording remuxes this with -c copy
//     Stereo preset: 1 AAC. 8-track preset: 4 AAC stereo pairs (MPEG-TS cannot carry 8ch PCM).
//
// Audio levels are parsed from astats metadata on a shared stereo branch in filter_complex.
func (m *Manager) runFFmpeg(s *Stream) error {
	outDir := filepath.Join(m.hlsDir, fmt.Sprintf("%d", s.ID))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	m.removePreview(s.ID)

	previewPlaylist, previewSeg := hlsout.PreviewPaths(outDir)
	inputArgs := sanitizeInputArgs(shellSplit(s.ffmpegInput))
	s.mu.Lock()
	presetID := s.EncodePreset
	s.mu.Unlock()
	enc := m.profileFor(presetID)
	gop := strconv.Itoa(enc.VideoGOP)

	audioCh := audiox.NormalizeCount(enc.AudioChannels)
	encodeTap := "[aencsrc]pan=stereo|c0=c0|c1=c1[arec];"
	audioEncode := []string{"-c:a", "aac", "-b:a", enc.AudioBitrate, "-ar", "48000", "-ac", "2"}
	masterAudio := []string{"[arec]"}
	if audioCh == audiox.Channels {
		// MPEG-TS cannot carry 8ch PCM; four AAC stereo pairs copy cleanly into MP4.
		encodeTap = audiox.PairSplitGraph("[aencsrc]", "en", "arec")
		masterAudio = []string{"[arec0]", "[arec1]", "[arec2]", "[arec3]"}
	}
	filterGraph := "[0:v]yadif=mode=0:deint=interlaced,split=2[vrec][vprevsrc];" +
		"[vprevsrc]scale=640:360,fps=10,format=yuv420p[vprev];" +
		"[vrec]fps=50,format=yuv420p[vrecout];" +
		"[0:a]" + audiox.Discrete8Pan + ",asplit=3[aencsrc][ameter][aprevsrc];" +
		audiox.PreviewPairGraph("[aprevsrc]") +
		encodeTap +
		"[ameter]astats=metadata=1:reset=1:measure_perchannel=Peak_level:measure_overall=none," +
		"ametadata=print,anullsink"

	args := []string{"-y"}
	args = append(args, inputArgs...)
	args = append(args,
		"-filter_complex", filterGraph,
	)
	args = hlsout.AppendAVPreviewOutputs(args, "[vprev]", previewPlaylist, previewSeg)
	args = append(args,
		// Master encode feed (REC remuxes with -c copy)
		"-map", "[vrecout]",
	)
	for _, pad := range masterAudio {
		args = append(args, "-map", pad)
	}
	args = append(args,
		"-c:v", enc.VideoCodec,
		"-b:v", enc.VideoBitrate,
		"-maxrate", enc.VideoMaxrate,
		"-bufsize", enc.VideoBufsize,
		"-preset", enc.VideoPreset,
		"-g", gop,
		"-r", "50",
		"-fps_mode", "cfr",
		"-bf", "0",
	)
	if strings.Contains(enc.VideoCodec, "nvenc") {
		args = append(args, "-forced-idr", "1")
	}
	args = append(args, audioEncode...)
	if audioCh == audiox.Channels {
		for i := 0; i < 4; i++ {
			args = append(args, "-metadata:s:a:"+strconv.Itoa(i), "title="+audiox.PreviewPairTitle(i))
		}
	}
	args = append(args,
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
			notable := strings.Contains(line, "Error") ||
				strings.Contains(line, "error:") ||
				strings.Contains(line, "No input signal") ||
				reSignalFormat.MatchString(line)
			if notable {
				log.Printf("[channel %d] %s", s.ID, line)
				s.appendLog(line)
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
				wasWaiting := s.Status == StatusWaiting
				s.Format = format
				s.Status = StatusRunning
				s.mu.Unlock()
				if wasWaiting {
					s.appendLog(fmt.Sprintf("signal acquired: %s", format))
				}
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
				audiox.SetPeak(&s.Audio, ch, val)
				s.mu.Unlock()
			}
			if strings.Contains(line, "No input signal detected") {
				s.mu.Lock()
				s.Status = StatusWaiting
				s.Format = ""
				s.Audio = audiox.SilencePeaks()
				s.mu.Unlock()
				m.removePreview(s.ID)
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
	return ensureDeckLinkChannels(clean, 8)
}

func decklinkInput(args []string) bool {
	for i := 0; i < len(args); i++ {
		if args[i] == "-f" && i+1 < len(args) && strings.EqualFold(args[i+1], "decklink") {
			return true
		}
	}
	return false
}

// ensureDeckLinkChannels opens 8 audio channels on DeckLink (FFmpeg default is 2).
func ensureDeckLinkChannels(args []string, n int) []string {
	if n != 2 && n != 8 && n != 16 {
		n = 8
	}
	if !decklinkInput(args) {
		return args
	}
	for i := 0; i < len(args); i++ {
		if args[i] == "-channels" || args[i] == "-audio_channels" {
			return args
		}
	}
	val := strconv.Itoa(n)
	for i := 0; i < len(args); i++ {
		if args[i] == "-i" {
			out := append([]string{}, args[:i]...)
			out = append(out, "-channels", val)
			out = append(out, args[i:]...)
			return out
		}
	}
	return append(append([]string{}, args...), "-channels", val)
}

func (m *Manager) killStream(s *Stream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

func (m *Manager) removePreview(id int) {
	dir := filepath.Join(m.hlsDir, fmt.Sprintf("%d", id))
	hlsout.RemovePreviewArtifacts(dir)
}
