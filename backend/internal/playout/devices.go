package playout

import (
	"bufio"
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
	Name    string   `json:"name"`
	Formats []Format `json:"formats"`
}

// Format is a DeckLink mode (format_code + human label).
type Format struct {
	Code       string  `json:"code"`  // e.g. "Hi50"
	Label      string  `json:"label"` // e.g. "1080i50"
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	FPS        float64 `json:"fps"`
	Interlaced bool    `json:"interlaced"`
}

var (
	// "0: DeckLink IP 100G (1)" / "0: 'DeckLink …'"
	reDeviceNum = regexp.MustCompile(`(?i)^\s*(\d+)\s*[:=\-]\s*['"]?(.+?)['"]?\s*$`)
	// Header line in older builds: "format_code description"
	reFormatHeader = regexp.MustCompile(`(?i)^\s*format_code\s+description\s*$`)
	// Per-line: "Hi50 1920x1080 at 25000/1000 fps (interlaced, upper field first)"
	// Also tolerate "format_code Hi50: 1920x1080 at …"
	reFormatLine = regexp.MustCompile(
		`(?i)^\s*(?:format_code\s+)?([A-Za-z0-9]{3,8})\s*:?\s+(\d+)x(\d+)\s+at\s+([\d.]+)(?:/([\d.]+))?\s*fps(.*)$`,
	)
	reSinkLine = regexp.MustCompile(`(?i)^\s*(?:\[[^\]]+\]\s*)?['"]?([^'"\n]+DeckLink[^'"\n]*)['"]?\s*$`)
)

// CommonModes is a fallback list when FFmpeg probe returns no formats.
func CommonModes() []Format {
	return []Format{
		{Code: "Hi50", Label: "1080i50", Width: 1920, Height: 1080, FPS: 25, Interlaced: true},
		{Code: "Hp50", Label: "1080p50", Width: 1920, Height: 1080, FPS: 50, Interlaced: false},
		{Code: "Hp25", Label: "1080p25", Width: 1920, Height: 1080, FPS: 25, Interlaced: false},
		{Code: "Hi59", Label: "1080i59.94", Width: 1920, Height: 1080, FPS: 30000.0 / 1001.0, Interlaced: true},
		{Code: "Hp29", Label: "1080p29.97", Width: 1920, Height: 1080, FPS: 30000.0 / 1001.0, Interlaced: false},
		{Code: "Hp30", Label: "1080p30", Width: 1920, Height: 1080, FPS: 30, Interlaced: false},
		{Code: "hp50", Label: "720p50", Width: 1280, Height: 720, FPS: 50, Interlaced: false},
		{Code: "pal", Label: "576i50", Width: 720, Height: 576, FPS: 25, Interlaced: true},
		{Code: "ntsc", Label: "486i59.94", Width: 720, Height: 486, FPS: 30000.0 / 1001.0, Interlaced: true},
	}
}

// ListDevices probes FFmpeg for DeckLink output devices and their formats.
func ListDevices(ffmpegBin string) ([]Device, error) {
	if ffmpegBin == "" {
		ffmpegBin = "ffmpeg"
	}
	names := listOutputDeviceNames(ffmpegBin)
	if len(names) == 0 {
		// Fall back to legacy list_devices (may mix inputs/outputs).
		var err error
		names, err = listDeviceNamesLegacy(ffmpegBin)
		if err != nil {
			return nil, err
		}
	}
	out := make([]Device, 0, len(names))
	for _, name := range names {
		formats := listOutputFormats(ffmpegBin, name)
		if len(formats) == 0 {
			// Input-style list_formats sometimes still works on IP cards.
			formats, _ = listInputFormats(ffmpegBin, name)
		}
		if len(formats) == 0 {
			log.Printf("[playout] no formats probed for %q – offering common modes", name)
			formats = CommonModes()
		}
		out = append(out, Device{Name: name, Formats: formats})
	}
	return out, nil
}

func listOutputDeviceNames(ffmpegBin string) []string {
	// Modern FFmpeg: ffmpeg -sinks decklink
	cmd := exec.Command(ffmpegBin, "-hide_banner", "-sinks", "decklink")
	out, _ := cmd.CombinedOutput()
	var names []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(strings.ToLower(line), "auto-") {
			continue
		}
		if mm := reDeviceNum.FindStringSubmatch(line); mm != nil {
			name := strings.Trim(strings.TrimSpace(mm[2]), `'"`)
			if name != "" && !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
			continue
		}
		if strings.Contains(strings.ToLower(line), "decklink") {
			if mm := reSinkLine.FindStringSubmatch(line); mm != nil {
				name := strings.TrimSpace(mm[1])
				name = strings.Trim(name, `'"`)
				// Strip trailing " [something]"
				if i := strings.Index(name, " ["); i > 0 {
					name = name[:i]
				}
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
	stderr, _ := cmd.StderrPipe()
	_ = cmd.Start()
	var lines []string
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	_ = cmd.Wait()

	var names []string
	seen := map[string]bool{}
	for _, line := range lines {
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

// listOutputFormats uses the outdev probe:
//
//	ffmpeg -f lavfi -i nullsrc=s=64x64:d=0.1 -f decklink -list_formats 1 'Device'
func listOutputFormats(ffmpegBin, device string) []Format {
	cmd := exec.Command(
		ffmpegBin, "-hide_banner", "-loglevel", "info",
		"-f", "lavfi", "-i", "nullsrc=s=64x64:d=0.1",
		"-f", "decklink", "-list_formats", "1", device,
	)
	out, _ := cmd.CombinedOutput()
	return parseFormatLines(string(out))
}

func listInputFormats(ffmpegBin, device string) ([]Format, error) {
	cmd := exec.Command(ffmpegBin, "-hide_banner", "-f", "decklink", "-list_formats", "1", "-i", device)
	out, _ := cmd.CombinedOutput()
	return parseFormatLines(string(out)), nil
}

func parseFormatLines(raw string) []Format {
	var formats []Format
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || reFormatHeader.MatchString(line) {
			continue
		}
		// Drop ffmpeg log prefixes like "[decklink @ 0x…] "
		if i := strings.Index(line, "] "); i >= 0 && strings.HasPrefix(line, "[") {
			line = strings.TrimSpace(line[i+2:])
		}
		mm := reFormatLine.FindStringSubmatch(line)
		if mm == nil {
			continue
		}
		code := mm[1]
		if strings.EqualFold(code, "format_code") || strings.EqualFold(code, "description") {
			continue
		}
		if seen[code] {
			continue
		}
		w, _ := strconv.Atoi(mm[2])
		h, _ := strconv.Atoi(mm[3])
		num, _ := strconv.ParseFloat(mm[4], 64)
		den := 1.0
		if mm[5] != "" {
			den, _ = strconv.ParseFloat(mm[5], 64)
			if den == 0 {
				den = 1
			}
		}
		fps := num / den
		tail := strings.ToLower(mm[6])
		interlaced := strings.Contains(tail, "interlac")
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

// LookupFormat returns geometry for a format code from probe/common lists.
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
	for _, f := range CommonModes() {
		if strings.EqualFold(f.Code, code) {
			return f, true
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
