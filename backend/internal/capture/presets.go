package capture

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"unicode"
)

type presetFile struct {
	Default string                 `json:"default"`
	Presets map[string]presetEntry `json:"presets"`
}

type presetEntry struct {
	Label        string `json:"label"`
	VideoCodec   string `json:"video_codec"`
	VideoBitrate string `json:"video_bitrate"`
	VideoMaxrate string `json:"video_maxrate"`
	VideoBufsize string `json:"video_bufsize"`
	VideoPreset  string `json:"video_preset"`
	VideoGOP     int    `json:"video_gop"`
	AudioBitrate string `json:"audio_bitrate"`
}

// PresetInput is the API payload for create/update.
type PresetInput struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	VideoCodec   string `json:"video_codec"`
	VideoBitrate string `json:"video_bitrate"`
	VideoMaxrate string `json:"video_maxrate"`
	VideoBufsize string `json:"video_bufsize"`
	VideoPreset  string `json:"video_preset"`
	VideoGOP     int    `json:"video_gop"`
	AudioBitrate string `json:"audio_bitrate"`
}

func (m *Manager) LoadPresetsFile() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.presetsPath == "" {
		return
	}
	data, err := os.ReadFile(m.presetsPath)
	if err != nil {
		if os.IsNotExist(err) {
			_ = m.savePresetsLocked()
		}
		return
	}
	var file presetFile
	if err := json.Unmarshal(data, &file); err != nil {
		log.Printf("[encode] bad presets file %s: %v", m.presetsPath, err)
		return
	}
	if len(file.Presets) == 0 {
		return
	}
	next := make(map[string]NamedPreset, len(file.Presets))
	for id, e := range file.Presets {
		clean := sanitizePresetID(id)
		if clean == "" {
			continue
		}
		next[clean] = namedFromEntry(clean, e)
	}
	if len(next) == 0 {
		return
	}
	m.presets = next
	if file.Default != "" && m.presets[file.Default].ID != "" {
		m.defaultPreset = file.Default
	} else {
		for id := range m.presets {
			m.defaultPreset = id
			break
		}
	}
}

func (m *Manager) savePresetsLocked() error {
	if m.presetsPath == "" {
		return nil
	}
	file := presetFile{
		Default: m.defaultPreset,
		Presets: make(map[string]presetEntry, len(m.presets)),
	}
	for id, p := range m.presets {
		file.Presets[id] = presetEntry{
			Label:        p.Label,
			VideoCodec:   p.Profile.VideoCodec,
			VideoBitrate: p.Profile.VideoBitrate,
			VideoMaxrate: p.Profile.VideoMaxrate,
			VideoBufsize: p.Profile.VideoBufsize,
			VideoPreset:  p.Profile.VideoPreset,
			VideoGOP:     p.Profile.VideoGOP,
			AudioBitrate: p.Profile.AudioBitrate,
		}
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.presetsPath, data, 0o644)
}

func namedFromEntry(id string, e presetEntry) NamedPreset {
	if e.Label == "" {
		e.Label = id
	}
	if e.VideoCodec == "" {
		e.VideoCodec = "h264_nvenc"
	}
	if e.VideoBitrate == "" {
		e.VideoBitrate = "12M"
	}
	if e.VideoMaxrate == "" {
		e.VideoMaxrate = e.VideoBitrate
	}
	if e.VideoBufsize == "" {
		e.VideoBufsize = "20M"
	}
	if e.VideoPreset == "" {
		e.VideoPreset = "p4"
	}
	if e.VideoGOP <= 0 {
		e.VideoGOP = 50
	}
	if e.AudioBitrate == "" {
		e.AudioBitrate = "192k"
	}
	return NamedPreset{
		ID:    id,
		Label: e.Label,
		Profile: EncodeProfile{
			VideoCodec:   e.VideoCodec,
			VideoBitrate: e.VideoBitrate,
			VideoMaxrate: e.VideoMaxrate,
			VideoBufsize: e.VideoBufsize,
			VideoPreset:  e.VideoPreset,
			VideoGOP:     e.VideoGOP,
			AudioBitrate: e.AudioBitrate,
		},
	}
}

func (m *Manager) UpsertPreset(in PresetInput, create bool) (NamedPreset, error) {
	id := sanitizePresetID(in.ID)
	if id == "" {
		return NamedPreset{}, fmt.Errorf("invalid preset id")
	}
	entry := presetEntry{
		Label:        strings.TrimSpace(in.Label),
		VideoCodec:   strings.TrimSpace(in.VideoCodec),
		VideoBitrate: strings.TrimSpace(in.VideoBitrate),
		VideoMaxrate: strings.TrimSpace(in.VideoMaxrate),
		VideoBufsize: strings.TrimSpace(in.VideoBufsize),
		VideoPreset:  strings.TrimSpace(in.VideoPreset),
		VideoGOP:     in.VideoGOP,
		AudioBitrate: strings.TrimSpace(in.AudioBitrate),
	}
	if entry.Label == "" {
		entry.Label = id
	}
	if entry.VideoBitrate == "" {
		return NamedPreset{}, fmt.Errorf("video_bitrate is required")
	}

	m.mu.Lock()
	_, exists := m.presets[id]
	if create && exists {
		m.mu.Unlock()
		return NamedPreset{}, fmt.Errorf("preset already exists")
	}
	if !create && !exists {
		m.mu.Unlock()
		return NamedPreset{}, fmt.Errorf("preset not found")
	}
	p := namedFromEntry(id, entry)
	m.presets[id] = p
	err := m.savePresetsLocked()
	m.mu.Unlock()
	if err != nil {
		return NamedPreset{}, err
	}

	// Restart running channels already on this preset so new encode settings apply.
	ids := m.channelIDsUsingPreset(id)
	for _, chID := range ids {
		st, ok := m.StatusByID(chID)
		if ok && st == StatusRunning {
			if err := m.restart(chID); err != nil {
				log.Printf("[encode] restart channel %d after preset upsert: %v", chID, err)
			}
		}
	}
	return p, nil
}

func (m *Manager) DeletePreset(id string) error {
	id = sanitizePresetID(id)
	if id == "" {
		return fmt.Errorf("invalid preset id")
	}
	m.mu.Lock()
	if _, ok := m.presets[id]; !ok {
		m.mu.Unlock()
		return fmt.Errorf("preset not found")
	}
	if len(m.presets) <= 1 {
		m.mu.Unlock()
		return fmt.Errorf("cannot delete the last preset")
	}
	affected := make([]int, 0)
	for chID, s := range m.streams {
		if s.EncodePreset == id {
			affected = append(affected, chID)
		}
	}
	delete(m.presets, id)
	if m.defaultPreset == id {
		for other := range m.presets {
			m.defaultPreset = other
			break
		}
	}
	fallback := m.defaultPreset
	for _, chID := range affected {
		m.streams[chID].EncodePreset = fallback
	}
	err := m.savePresetsLocked()
	_ = m.saveAssignmentsLocked()
	m.mu.Unlock()
	if err != nil {
		return err
	}

	for _, chID := range affected {
		st, ok := m.StatusByID(chID)
		if ok && st == StatusRunning {
			if err := m.restart(chID); err != nil {
				log.Printf("[encode] restart channel %d after preset delete: %v", chID, err)
			}
		}
	}
	return nil
}

func (m *Manager) channelIDsUsingPreset(presetID string) []int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]int, 0)
	for id, s := range m.streams {
		if s.EncodePreset == presetID {
			out = append(out, id)
		}
	}
	return out
}

func sanitizePresetID(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_-")
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}
