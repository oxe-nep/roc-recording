package playout

import (
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Device is a DeckLink *output* (sink) discovered via FFmpeg.
// Name is the unique handle from -sinks (BMDDeckLinkDeviceHandle).
// Label is the human display name (may match an input with a different ID).
// OpenName is whichever of Name/Label actually accepted -list_formats (use for playout).
type Device struct {
	Name       string   `json:"name"`  // unique id, e.g. "25fb7120:00000000"
	Label      string   `json:"label"` // e.g. "DeckLink IP 100G (1)"
	OpenName   string   `json:"open_name,omitempty"`
	Formats    []Format `json:"formats"`
	ProbeLog   string   `json:"probe_log,omitempty"`
	Busy       bool     `json:"busy,omitempty"`
	BusyReason string   `json:"busy_reason,omitempty"`
}

// Format is a DeckLink mode (format_code or mode index + geometry).
type Format struct {
	Code       string  `json:"code"`
	Label      string  `json:"label"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	FPS        float64 `json:"fps"`
	Interlaced bool    `json:"interlaced"`
}

type sinkDevice struct {
	ID    string // unique handle for FFmpeg
	Label string // display name
}

var (
	reDeviceNum = regexp.MustCompile(`(?i)^\s*(\d+)\s*[:=\-]\s*['"]?(.+?)['"]?\s*$`)
	reFormatHeader = regexp.MustCompile(`(?i)format_code\s+description|supported formats`)
	reFormatFourCC = regexp.MustCompile(
		`(?i)^\s*(?:format_code\s+)?([A-Za-z][A-Za-z0-9 ]{2,7})\s+(\d+)x(\d+)\s+at\s+([\d.]+)(?:/([\d.]+))?\s*fps(.*)$`,
	)
	reFormatIndex = regexp.MustCompile(
		`(?i)^\s*(\d+)\s+(\d+)x(\d+)\s+at\s+([\d.]+)(?:/([\d.]+))?\s*fps(.*)$`,
	)
	// ffmpeg -sinks: "25fb7120:00000000 [DeckLink IP 100G (1)] (none)"
	// also older "55:00000000:00000000 [DeckLink Duo (1)]"
	reSinkUIDLabel = regexp.MustCompile(`(?i)^\s*([0-9a-f]{2,}(?::[0-9a-f]+){1,})\s+\[([^\]]+)\]`)
	reSinkUIDAlone = regexp.MustCompile(`(?i)^\s*([0-9a-f]{8}:[0-9a-f]{8})\s*$`)
	reUniqueID     = regexp.MustCompile(`(?i)^[0-9a-f]{2,}(?::[0-9a-f]+){1,}$`)
)

// ParseSinkLine extracts unique id + display label from an ffmpeg -sinks line.
func ParseSinkLine(line string) (sinkDevice, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(strings.ToLower(line), "auto-detected") {
		return sinkDevice{}, false
	}
	if mm := reSinkUIDLabel.FindStringSubmatch(line); mm != nil {
		return sinkDevice{ID: mm[1], Label: strings.TrimSpace(mm[2])}, true
	}
	if mm := reSinkUIDAlone.FindStringSubmatch(line); mm != nil {
		return sinkDevice{ID: mm[1], Label: mm[1]}, true
	}
	if strings.Contains(line, "'") {
		start := strings.Index(line, "'")
		end := strings.LastIndex(line, "'")
		if start >= 0 && end > start {
			label := line[start+1 : end]
			return sinkDevice{ID: label, Label: label}, true
		}
	}
	if mm := reDeviceNum.FindStringSubmatch(line); mm != nil {
		rest := strings.Trim(strings.TrimSpace(mm[2]), `'"`)
		if s, ok := ParseSinkLine(rest); ok {
			return s, true
		}
		return sinkDevice{ID: rest, Label: rest}, true
	}
	return sinkDevice{}, false
}

// NormalizeOpenDevice returns the FFmpeg open string for a stored device value.
// Prefer unique sink IDs; strip junk from pasted sinks lines.
func NormalizeOpenDevice(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, `'"`)
	if raw == "" {
		return ""
	}
	if s, ok := ParseSinkLine(raw); ok {
		return s.ID
	}
	if i := strings.Index(raw, " ["); i > 0 && reUniqueID.MatchString(strings.TrimSpace(raw[:i])) {
		return strings.TrimSpace(raw[:i])
	}
	return raw
}

// ListDevices probes FFmpeg for DeckLink output sinks and their formats.
func ListDevices(ffmpegBin string) ([]Device, error) {
	if ffmpegBin == "" {
		ffmpegBin = "ffmpeg"
	}
	sinks := listOutputSinks(ffmpegBin)
	if len(sinks) == 0 {
		// Legacy fallback — display names only (ambiguous for IP 8in/8out).
		names, err := listDeviceNamesLegacy(ffmpegBin)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			sinks = append(sinks, sinkDevice{ID: n, Label: n})
		}
	}
	out := make([]Device, 0, len(sinks))
	for _, s := range sinks {
		formats, raw := probeFormats(ffmpegBin, s.ID)
		openName := s.ID
		// Unique handle often lists in -sinks but fails -list_formats / write_header
		// on DeckLink IP; display label is what actually opens.
		if len(formats) == 0 && s.Label != "" && s.Label != s.ID {
			if f2, raw2 := probeFormats(ffmpegBin, s.Label); len(f2) > 0 {
				formats, raw = f2, raw2
				openName = s.Label
			} else {
				raw = raw + "\n" + raw2
			}
		}
		d := Device{Name: s.ID, Label: s.Label, OpenName: openName, Formats: formats}
		if len(formats) == 0 {
			d.ProbeLog = trimProbeLog(raw)
			log.Printf("[playout] format probe failed for sink %q (%q) (%d bytes log)", s.ID, s.Label, len(raw))
		} else {
			log.Printf("[playout] probed %d formats for sink %q label=%q open=%q", len(formats), s.ID, s.Label, openName)
		}
		out = append(out, d)
	}
	return out, nil
}

func trimProbeLog(raw string) string {
	lines := strings.Split(raw, "\n")
	if len(lines) > 40 {
		lines = lines[len(lines)-40:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func listOutputSinks(ffmpegBin string) []sinkDevice {
	cmd := exec.Command(ffmpegBin, "-hide_banner", "-sinks", "decklink")
	out, _ := cmd.CombinedOutput()
	var sinks []sinkDevice
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		s, ok := ParseSinkLine(line)
		if !ok || s.ID == "" || seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		sinks = append(sinks, s)
	}
	return sinks
}

func listDeviceNamesLegacy(ffmpegBin string) ([]string, error) {
	cmd := exec.Command(ffmpegBin, "-hide_banner", "-f", "decklink", "-list_devices", "1", "-i", "dummy")
	out, _ := cmd.CombinedOutput()
	var names []string
	seen := map[string]bool{}
	add := func(name string) {
		name = strings.TrimSpace(strings.Trim(name, `'"`))
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		names = append(names, name)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if s, ok := ParseSinkLine(strings.TrimSpace(line)); ok {
			add(s.Label)
			continue
		}
		if mm := reDeviceNum.FindStringSubmatch(line); mm != nil {
			add(mm[2])
			continue
		}
		if strings.Contains(strings.ToLower(line), "decklink") && strings.Contains(line, "'") {
			start := strings.Index(line, "'")
			end := strings.LastIndex(line, "'")
			if start >= 0 && end > start {
				add(line[start+1 : end])
			}
		}
	}
	return names, nil
}

func probeFormats(ffmpegBin, device string) ([]Format, string) {
	var combined strings.Builder
	try := func(label string, args []string) []Format {
		cmd := exec.Command(ffmpegBin, args...)
		out, _ := cmd.CombinedOutput()
		text := string(out)
		fmt.Fprintf(&combined, "--- %s ---\n%s\n", label, text)
		return parseFormatLines(text)
	}

	if f := try("outdev-lavfi", []string{
		"-hide_banner", "-loglevel", "info",
		"-f", "lavfi", "-i", "color=c=black:s=1920x1080:r=25",
		"-f", "decklink", "-list_formats", "1", device,
	}); len(f) > 0 {
		return f, combined.String()
	}
	if f := try("indev", []string{
		"-hide_banner", "-loglevel", "info",
		"-f", "decklink", "-list_formats", "1", "-i", device,
	}); len(f) > 0 {
		return f, combined.String()
	}
	if f := try("indev-duplex-full", []string{
		"-hide_banner", "-loglevel", "info",
		"-f", "decklink", "-duplex_mode", "full", "-list_formats", "1", "-i", device,
	}); len(f) > 0 {
		return f, combined.String()
	}
	return nil, combined.String()
}

func parseFormatLines(raw string) []Format {
	var formats []Format
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if i := strings.Index(line, "] "); i >= 0 && strings.HasPrefix(line, "[") {
			line = strings.TrimSpace(line[i+2:])
		}
		if reFormatHeader.MatchString(line) && !strings.Contains(strings.ToLower(line), "x") {
			continue
		}

		var code string
		var w, h int
		var num, den float64
		var tail string

		if mm := reFormatFourCC.FindStringSubmatch(line); mm != nil {
			code = strings.TrimSpace(mm[1])
			w, _ = strconv.Atoi(mm[2])
			h, _ = strconv.Atoi(mm[3])
			num, _ = strconv.ParseFloat(mm[4], 64)
			den = 1
			if mm[5] != "" {
				den, _ = strconv.ParseFloat(mm[5], 64)
				if den == 0 {
					den = 1
				}
			}
			tail = mm[6]
		} else if mm := reFormatIndex.FindStringSubmatch(line); mm != nil {
			code = mm[1]
			w, _ = strconv.Atoi(mm[2])
			h, _ = strconv.Atoi(mm[3])
			num, _ = strconv.ParseFloat(mm[4], 64)
			den = 1
			if mm[5] != "" {
				den, _ = strconv.ParseFloat(mm[5], 64)
				if den == 0 {
					den = 1
				}
			}
			tail = mm[6]
		} else {
			continue
		}

		code = strings.TrimSpace(code)
		if code == "" || strings.EqualFold(code, "format_code") || strings.EqualFold(code, "description") {
			continue
		}
		if seen[code] || w <= 0 || h <= 0 {
			continue
		}
		fps := num / den
		interlaced := strings.Contains(strings.ToLower(tail), "interlac")
		rate := int(fps + 0.5)
		if interlaced {
			rate = int(fps*2 + 0.5)
		}
		scan := "p"
		if interlaced {
			scan = "i"
		}
		seen[code] = true
		formats = append(formats, Format{
			Code:       code,
			Label:      fmt.Sprintf("%d%s%d", h, scan, rate),
			Width:      w,
			Height:     h,
			FPS:        fps,
			Interlaced: interlaced,
		})
	}
	return formats
}

func LookupFormat(code string, devices []Device) (Format, bool) {
	code = strings.TrimSpace(code)
	if code == "" {
		return Format{}, false
	}
	for _, d := range devices {
		for _, f := range d.Formats {
			if strings.EqualFold(f.Code, code) {
				return f, true
			}
		}
	}
	return Format{}, false
}

// ExtractDeckLinkName pulls a device name from an ffmpeg input arg string.
func ExtractDeckLinkName(ffmpegInput string) string {
	s := ffmpegInput
	if i := strings.LastIndex(strings.ToLower(s), "-i"); i >= 0 {
		s = strings.TrimSpace(s[i+2:])
	}
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "'") {
		if end := strings.Index(s[1:], "'"); end >= 0 {
			return strings.TrimSpace(s[1 : 1+end])
		}
	}
	if strings.HasPrefix(s, `"`) {
		if end := strings.Index(s[1:], `"`); end >= 0 {
			return strings.TrimSpace(s[1 : 1+end])
		}
	}
	fields := strings.Fields(s)
	if len(fields) > 0 {
		return NormalizeOpenDevice(fields[0])
	}
	return NormalizeOpenDevice(s)
}

// ResolveOpenDevice maps a stored value (display name, sinks line, or id) to a sink unique id.
func (m *Manager) ResolveOpenDevice(raw string) string {
	raw = NormalizeOpenDevice(raw)
	if raw == "" {
		return ""
	}
	devs, err := m.devCache.get(m.ffmpegBin, 5*time.Minute)
	if err != nil || len(devs) == 0 {
		return raw
	}
	for _, d := range devs {
		if strings.EqualFold(d.Name, raw) {
			return d.Name
		}
	}
	for _, d := range devs {
		if strings.EqualFold(d.Label, raw) {
			return d.Name
		}
	}
	return raw
}

// LookupDeviceLabel returns the human sink label for a unique id (or the id itself).
func (m *Manager) LookupDeviceLabel(idOrName string) string {
	idOrName = strings.TrimSpace(idOrName)
	if idOrName == "" {
		return ""
	}
	devs, err := m.devCache.get(m.ffmpegBin, 5*time.Minute)
	if err != nil {
		return ""
	}
	for _, d := range devs {
		if strings.EqualFold(d.Name, idOrName) {
			if d.Label != "" {
				return d.Label
			}
			return d.Name
		}
		if strings.EqualFold(d.Label, idOrName) {
			return d.Label
		}
	}
	return ""
}

// LookupDeviceOpen returns the FFmpeg open string that accepted format probing.
func (m *Manager) LookupDeviceOpen(idOrName string) string {
	idOrName = strings.TrimSpace(idOrName)
	if idOrName == "" {
		return ""
	}
	devs, err := m.devCache.get(m.ffmpegBin, 5*time.Minute)
	if err != nil {
		return ""
	}
	for _, d := range devs {
		if strings.EqualFold(d.Name, idOrName) || strings.EqualFold(d.Label, idOrName) {
			if d.OpenName != "" {
				return d.OpenName
			}
			if d.Label != "" {
				return d.Label
			}
			return d.Name
		}
	}
	return ""
}

// FindDevice returns a cached sink matching unique id or label.
func (m *Manager) FindDevice(idOrName string) (Device, bool) {
	idOrName = strings.TrimSpace(idOrName)
	if idOrName == "" {
		return Device{}, false
	}
	devs, err := m.devCache.get(m.ffmpegBin, 5*time.Minute)
	if err != nil {
		return Device{}, false
	}
	for _, d := range devs {
		if strings.EqualFold(d.Name, idOrName) || strings.EqualFold(d.Label, idOrName) {
			return d, true
		}
	}
	return Device{}, false
}

type deviceCache struct {
	mu      sync.Mutex
	at      time.Time
	devices []Device
	err     error
}

func (c *deviceCache) get(ffmpegBin string, maxAge time.Duration) ([]Device, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.at) < maxAge && c.devices != nil {
		return cloneDevices(c.devices), c.err
	}
	devs, err := ListDevices(ffmpegBin)
	c.devices = mergeFormatCache(c.devices, devs)
	c.err = err
	c.at = time.Now()
	return cloneDevices(c.devices), err
}

func (c *deviceCache) refresh(ffmpegBin string) ([]Device, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	devs, err := ListDevices(ffmpegBin)
	c.devices = mergeFormatCache(c.devices, devs)
	c.err = err
	c.at = time.Now()
	return cloneDevices(c.devices), err
}

func cloneDevices(in []Device) []Device {
	out := make([]Device, len(in))
	copy(out, in)
	for i := range out {
		if in[i].Formats != nil {
			out[i].Formats = append([]Format(nil), in[i].Formats...)
		}
	}
	return out
}

// mergeFormatCache keeps previously probed formats when a re-probe returns empty
// (e.g. sink briefly busy). Device list/ids still update from the new probe.
func mergeFormatCache(prev, next []Device) []Device {
	if len(prev) == 0 {
		return next
	}
	byID := map[string]Device{}
	for _, d := range prev {
		byID[d.Name] = d
	}
	out := make([]Device, 0, len(next))
	for _, d := range next {
		if old, ok := byID[d.Name]; ok {
			if len(d.Formats) == 0 && len(old.Formats) > 0 {
				d.Formats = old.Formats
				d.ProbeLog = ""
			}
			if d.OpenName == "" && old.OpenName != "" {
				d.OpenName = old.OpenName
			}
			if d.Label == "" && old.Label != "" {
				d.Label = old.Label
			}
		}
		if d.OpenName == "" {
			if d.Label != "" {
				d.OpenName = d.Label
			} else {
				d.OpenName = d.Name
			}
		}
		out = append(out, d)
	}
	return out
}

func (m *Manager) WarmProbe() {
	devs, err := m.devCache.refresh(m.ffmpegBin)
	if err != nil {
		log.Printf("[playout] warm probe error: %v", err)
		return
	}
	nFmt := 0
	for _, d := range devs {
		nFmt += len(d.Formats)
	}
	log.Printf("[playout] warm probe: %d output sinks, %d formats total", len(devs), nFmt)
}

// Devices returns probed DeckLink outputs. Capture inputs are separate instances —
// they are not marked busy here (8in/8out is supported).
func (m *Manager) Devices(refresh bool) ([]Device, error) {
	if refresh {
		return m.devCache.refresh(m.ffmpegBin)
	}
	return m.devCache.get(m.ffmpegBin, 5*time.Minute)
}
