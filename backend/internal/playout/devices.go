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

// Device is a DeckLink output discovered via FFmpeg.
type Device struct {
	Name      string   `json:"name"`
	Formats   []Format `json:"formats"`
	ProbeLog  string   `json:"probe_log,omitempty"` // set when formats could not be parsed
}

// Format is a DeckLink mode (format_code or mode index + geometry).
type Format struct {
	Code       string  `json:"code"`  // FourCC (Hi50) or numeric mode id ("11")
	Label      string  `json:"label"` // e.g. "1080i50"
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	FPS        float64 `json:"fps"`
	Interlaced bool    `json:"interlaced"`
}

var (
	reDeviceNum = regexp.MustCompile(`(?i)^\s*(\d+)\s*[:=\-]\s*['"]?(.+?)['"]?\s*$`)
	reFormatHeader = regexp.MustCompile(`(?i)format_code\s+description|supported formats`)
	// FourCC style: "Hi50 1920x1080 at 25000/1000 fps (interlaced, …)"
	reFormatFourCC = regexp.MustCompile(
		`(?i)^\s*(?:format_code\s+)?([A-Za-z][A-Za-z0-9 ]{2,7})\s+(\d+)x(\d+)\s+at\s+([\d.]+)(?:/([\d.]+))?\s*fps(.*)$`,
	)
	// Older style: "11 1920x1080 at 25000/1000 fps (interlaced, …)"
	reFormatIndex = regexp.MustCompile(
		`(?i)^\s*(\d+)\s+(\d+)x(\d+)\s+at\s+([\d.]+)(?:/([\d.]+))?\s*fps(.*)$`,
	)
	reSinkLine = regexp.MustCompile(`(?i)DeckLink`)
)

// ListDevices probes FFmpeg for DeckLink output devices and their formats.
// Does not invent fake modes — formats come only from FFmpeg probe output.
func ListDevices(ffmpegBin string) ([]Device, error) {
	if ffmpegBin == "" {
		ffmpegBin = "ffmpeg"
	}
	names := listOutputDeviceNames(ffmpegBin)
	if len(names) == 0 {
		var err error
		names, err = listDeviceNamesLegacy(ffmpegBin)
		if err != nil {
			return nil, err
		}
	}
	out := make([]Device, 0, len(names))
	for _, name := range names {
		formats, raw := probeFormats(ffmpegBin, name)
		d := Device{Name: name, Formats: formats}
		if len(formats) == 0 {
			d.ProbeLog = trimProbeLog(raw)
			log.Printf("[playout] format probe failed for %q (%d bytes log)", name, len(raw))
		} else {
			log.Printf("[playout] probed %d formats for %q", len(formats), name)
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

func listOutputDeviceNames(ffmpegBin string) []string {
	cmd := exec.Command(ffmpegBin, "-hide_banner", "-sinks", "decklink")
	out, _ := cmd.CombinedOutput()
	var names []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if mm := reDeviceNum.FindStringSubmatch(line); mm != nil {
			name := strings.Trim(strings.TrimSpace(mm[2]), `'"`)
			if i := strings.Index(name, " ["); i > 0 {
				name = name[:i]
			}
			if name != "" && !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
			continue
		}
		if reSinkLine.MatchString(line) && strings.Contains(line, "'") {
			start := strings.Index(line, "'")
			end := strings.LastIndex(line, "'")
			if start >= 0 && end > start {
				name := line[start+1 : end]
				if name != "" && !seen[name] {
					seen[name] = true
					names = append(names, name)
				}
			}
		}
	}
	return names
}

func listDeviceNamesLegacy(ffmpegBin string) ([]string, error) {
	cmd := exec.Command(ffmpegBin, "-hide_banner", "-f", "decklink", "-list_devices", "1", "-i", "dummy")
	out, _ := cmd.CombinedOutput()
	var names []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		if mm := reDeviceNum.FindStringSubmatch(line); mm != nil {
			name := strings.Trim(strings.TrimSpace(mm[2]), `'"`)
			if name != "" && !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
			continue
		}
		if strings.Contains(strings.ToLower(line), "decklink") && strings.Contains(line, "'") {
			start := strings.Index(line, "'")
			end := strings.LastIndex(line, "'")
			if start >= 0 && end > start {
				name := line[start+1 : end]
				if name != "" && !seen[name] {
					seen[name] = true
					names = append(names, name)
				}
			}
		}
	}
	return names, nil
}

// probeFormats tries several FFmpeg invocations used across DeckLink SDK / FFmpeg versions.
func probeFormats(ffmpegBin, device string) ([]Format, string) {
	var combined strings.Builder
	try := func(label string, args []string) []Format {
		cmd := exec.Command(ffmpegBin, args...)
		out, _ := cmd.CombinedOutput()
		text := string(out)
		fmt.Fprintf(&combined, "--- %s ---\n%s\n", label, text)
		return parseFormatLines(text)
	}

	// 1) Official outdev example shape (needs a dummy input).
	if f := try("outdev-lavfi", []string{
		"-hide_banner", "-loglevel", "info",
		"-f", "lavfi", "-i", "color=c=black:s=1920x1080:r=25",
		"-f", "decklink", "-list_formats", "1", device,
	}); len(f) > 0 {
		return f, combined.String()
	}

	// 2) Input-style list_formats (often works on IP cards for both directions).
	if f := try("indev", []string{
		"-hide_banner", "-loglevel", "info",
		"-f", "decklink", "-list_formats", "1", "-i", device,
	}); len(f) > 0 {
		return f, combined.String()
	}

	// 3) Duplex full — some multi-subdevice cards need this to enumerate.
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
			code = mm[1] // numeric mode index
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
		if seen[code] {
			continue
		}
		if w <= 0 || h <= 0 {
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
		label := fmt.Sprintf("%d%s%d", h, scan, rate)
		seen[code] = true
		formats = append(formats, Format{
			Code:       code,
			Label:      label,
			Width:      w,
			Height:     h,
			FPS:        fps,
			Interlaced: interlaced,
		})
	}
	return formats
}

// LookupFormat finds geometry for a format code from probed devices only.
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
		return c.devices, c.err
	}
	devs, err := ListDevices(ffmpegBin)
	c.devices = devs
	c.err = err
	c.at = time.Now()
	return devs, err
}

func (c *deviceCache) refresh(ffmpegBin string) ([]Device, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	devs, err := ListDevices(ffmpegBin)
	c.devices = devs
	c.err = err
	c.at = time.Now()
	return devs, err
}

// WarmProbe fills the device/format cache. Call before capture opens DeckLink
// devices so list_formats is more likely to succeed.
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
	log.Printf("[playout] warm probe: %d devices, %d formats total", len(devs), nFmt)
}

// Devices returns cached probe results. Pass refresh=true to re-run FFmpeg.
func (m *Manager) Devices(refresh bool) ([]Device, error) {
	if refresh {
		return m.devCache.refresh(m.ffmpegBin)
	}
	return m.devCache.get(m.ffmpegBin, 60*time.Second)
}
