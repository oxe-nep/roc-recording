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
	Code      string `json:"code"`       // e.g. "Hp50"
	Label     string `json:"label"`      // e.g. "1080i50"
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	FPS       float64 `json:"fps"`
	Interlaced bool  `json:"interlaced"`
}

var (
	reDeviceNum  = regexp.MustCompile(`(?i)^\s*(\d+)\s*[:=\-]\s*['"]?(.+?)['"]?\s*$`)
	reFormatCode = regexp.MustCompile(`(?i)format_code\s+(\S+)\s*:\s*(\d+)x(\d+)\s+at\s+([\d.]+)(?:/([\d.]+))?\s*fps(.*?)$`)
	reListDevHdr = regexp.MustCompile(`(?i)decklink.*devices|input/output devices`)
)

// ListDevices probes FFmpeg for DeckLink devices and their formats.
// Best-effort: if probing fails, returns an empty list (UI can still type a name later).
func ListDevices(ffmpegBin string) ([]Device, error) {
	if ffmpegBin == "" {
		ffmpegBin = "ffmpeg"
	}
	names, err := listDeviceNames(ffmpegBin)
	if err != nil {
		return nil, err
	}
	out := make([]Device, 0, len(names))
	for _, name := range names {
		formats, ferr := listFormats(ffmpegBin, name)
		if ferr != nil {
			log.Printf("[playout] list_formats for %q: %v", name, ferr)
			formats = nil
		}
		out = append(out, Device{Name: name, Formats: formats})
	}
	return out, nil
}

func listDeviceNames(ffmpegBin string) ([]string, error) {
	// FFmpeg prints device list to stderr and exits with an error code.
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
	inList := false
	for _, line := range lines {
		if reListDevHdr.MatchString(line) {
			inList = true
			continue
		}
		if !inList && !strings.Contains(strings.ToLower(line), "decklink") {
			continue
		}
		inList = true
		// Common shapes:
		//   [decklink @ ...] 'DeckLink IP 100G (1)'
		//   1: DeckLink IP 100G (1)
		if mm := reDeviceNum.FindStringSubmatch(line); mm != nil {
			name := strings.TrimSpace(mm[2])
			name = strings.Trim(name, `'"`)
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

func listFormats(ffmpegBin, device string) ([]Format, error) {
	cmd := exec.Command(ffmpegBin, "-hide_banner", "-f", "decklink", "-list_formats", "1", "-i", device)
	stderr, _ := cmd.StderrPipe()
	_ = cmd.Start()
	var formats []Format
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		mm := reFormatCode.FindStringSubmatch(line)
		if mm == nil {
			continue
		}
		code := mm[1]
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
		interlaced := strings.Contains(strings.ToLower(mm[6]), "interlac")
		rate := int(fps + 0.5)
		if interlaced {
			rate = int(fps*2 + 0.5)
		}
		scan := "p"
		if interlaced {
			scan = "i"
		}
		label := fmt.Sprintf("%d%s%d", h, scan, rate)
		formats = append(formats, Format{
			Code:       code,
			Label:      label,
			Width:      w,
			Height:     h,
			FPS:        fps,
			Interlaced: interlaced,
		})
	}
	_ = cmd.Wait()
	return formats, nil
}

// Cache probed devices briefly so the UI can poll without hammering FFmpeg.
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
