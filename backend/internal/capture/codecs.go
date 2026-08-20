package capture

import (
	"bufio"
	"bytes"
	"log"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// CodecOption is a selectable video encoder exposed to the UI.
type CodecOption struct {
	ID      string         `json:"id"`
	Label   string         `json:"label"`
	Presets []PresetOption `json:"presets"`
}

// PresetOption is an encoder speed/quality preset (e.g. NVENC p1–p7).
type PresetOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Codecs we care about, in UI order. Only included if ffmpeg lists them.
var preferredCodecs = []struct {
	ID    string
	Label string
}{
	{"h264_nvenc", "H.264 (NVENC)"},
	{"hevc_nvenc", "H.265 / HEVC (NVENC)"},
	{"av1_nvenc", "AV1 (NVENC)"},
	{"h264_qsv", "H.264 (Intel QSV)"},
	{"hevc_qsv", "H.265 / HEVC (Intel QSV)"},
	{"libx264", "H.264 (software CPU)"},
	{"libx265", "H.265 (software CPU)"},
}

var nvencPresetLabels = map[string]string{
	"p1": "p1 — Fastest (lower quality)",
	"p2": "p2 — Faster",
	"p3": "p3 — Fast",
	"p4": "p4 — Balanced (recommended)",
	"p5": "p5 — Slow",
	"p6": "p6 — Slower",
	"p7": "p7 — Slowest (best quality)",
}

var rePresetValues = regexp.MustCompile(`(?i)Possible values:\s*(.+)`)

type codecCache struct {
	mu      sync.Mutex
	at      time.Time
	codecs  []CodecOption
	encoder map[string]bool
}

func (m *Manager) ListEncodeOptions() []CodecOption {
	m.codecCache.mu.Lock()
	defer m.codecCache.mu.Unlock()
	if len(m.codecCache.codecs) > 0 && time.Since(m.codecCache.at) < 5*time.Minute {
		out := make([]CodecOption, len(m.codecCache.codecs))
		copy(out, m.codecCache.codecs)
		return out
	}

	available := m.probeEncodersLocked()
	out := make([]CodecOption, 0, len(preferredCodecs))
	for _, c := range preferredCodecs {
		if !available[c.ID] {
			continue
		}
		presets := m.probeEncoderPresetsLocked(c.ID)
		out = append(out, CodecOption{
			ID:      c.ID,
			Label:   c.Label,
			Presets: presets,
		})
	}
	// Always offer at least H.264 NVENC so the UI stays usable if probe fails.
	if len(out) == 0 {
		log.Printf("[encode] ffmpeg encoder probe returned nothing; falling back to h264_nvenc")
		out = []CodecOption{{
			ID:      "h264_nvenc",
			Label:   "H.264 (NVENC)",
			Presets: defaultNVENCPresets(),
		}}
	}

	m.codecCache.codecs = out
	m.codecCache.at = time.Now()
	copied := make([]CodecOption, len(out))
	copy(copied, out)
	return copied
}

func (m *Manager) probeEncodersLocked() map[string]bool {
	if m.codecCache.encoder != nil && !m.codecCache.at.IsZero() && time.Since(m.codecCache.at) < 5*time.Minute {
		return m.codecCache.encoder
	}
	out := make(map[string]bool)
	cmd := exec.Command(m.ffmpegBin, "-hide_banner", "-encoders")
	raw, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[encode] ffmpeg -encoders failed: %v", err)
		m.codecCache.encoder = out
		return out
	}
	text := string(raw)
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		fields := strings.Fields(strings.TrimSpace(sc.Text()))
		if len(fields) < 2 {
			continue
		}
		flags, name := fields[0], fields[1]
		if !strings.HasPrefix(flags, "V") || len(flags) < 5 {
			continue
		}
		out[name] = true
	}
	for _, c := range preferredCodecs {
		if strings.Contains(text, c.ID) {
			out[c.ID] = true
		}
	}
	m.codecCache.encoder = out
	return out
}

func (m *Manager) probeEncoderPresetsLocked(codecID string) []PresetOption {
	if strings.Contains(codecID, "nvenc") {
		cmd := exec.Command(m.ffmpegBin, "-hide_banner", "-h", "encoder="+codecID)
		raw, err := cmd.CombinedOutput()
		if err == nil {
			text := string(raw)
			found := make([]PresetOption, 0, 7)
			for _, id := range []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7"} {
				if strings.Contains(text, id) {
					found = append(found, PresetOption{ID: id, Label: nvencPresetLabels[id]})
				}
			}
			if len(found) > 0 {
				return found
			}
		}
		return defaultNVENCPresets()
	}

	cmd := exec.Command(m.ffmpegBin, "-hide_banner", "-h", "encoder="+codecID)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		return defaultSoftwarePresets(codecID)
	}
	text := string(raw)
	if idx := strings.Index(strings.ToLower(text), "-preset"); idx >= 0 {
		chunk := text[idx:]
		if mm := rePresetValues.FindStringSubmatch(chunk); mm != nil {
			parts := strings.Fields(strings.ReplaceAll(mm[1], ",", " "))
			out := make([]PresetOption, 0, len(parts))
			for _, p := range parts {
				p = strings.Trim(p, " .)")
				if p == "" || p == ".." {
					continue
				}
				out = append(out, PresetOption{ID: p, Label: p})
				if len(out) >= 16 {
					break
				}
			}
			if len(out) > 0 {
				return out
			}
		}
	}
	return defaultSoftwarePresets(codecID)
}

func defaultNVENCPresets() []PresetOption {
	ids := []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7"}
	out := make([]PresetOption, 0, len(ids))
	for _, id := range ids {
		out = append(out, PresetOption{ID: id, Label: nvencPresetLabels[id]})
	}
	return out
}

func defaultSoftwarePresets(codecID string) []PresetOption {
	if strings.HasPrefix(codecID, "libx") {
		return []PresetOption{
			{ID: "ultrafast", Label: "ultrafast"},
			{ID: "superfast", Label: "superfast"},
			{ID: "veryfast", Label: "veryfast"},
			{ID: "faster", Label: "faster"},
			{ID: "fast", Label: "fast"},
			{ID: "medium", Label: "medium (recommended)"},
			{ID: "slow", Label: "slow"},
			{ID: "slower", Label: "slower"},
			{ID: "veryslow", Label: "veryslow"},
		}
	}
	return []PresetOption{{ID: "medium", Label: "medium"}}
}
