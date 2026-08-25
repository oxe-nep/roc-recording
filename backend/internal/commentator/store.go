package commentator

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

const intercomSlots = 6

// IntercomSlot is one mono intercom channel (DeckLink IN/OUT track 3–8).
type IntercomSlot struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// ChannelSettings is persisted per channel id.
type ChannelSettings struct {
	Intercom     [intercomSlots]IntercomSlot `json:"intercom"`
	OutputFormat string                      `json:"output_format,omitempty"`
	DisplayName  string                      `json:"display_name,omitempty"`
}

func defaultSlot(id int) IntercomSlot {
	return IntercomSlot{
		ID:      id,
		Name:    fmt.Sprintf("Intercom %d", id),
		Enabled: id <= 2,
	}
}

func DefaultChannelSettings() ChannelSettings {
	var s ChannelSettings
	for i := 0; i < intercomSlots; i++ {
		s.Intercom[i] = defaultSlot(i + 1)
	}
	return s
}

func normalizeSettings(s ChannelSettings) ChannelSettings {
	out := DefaultChannelSettings()
	for i := 0; i < intercomSlots; i++ {
		slot := s.Intercom[i]
		if slot.ID < 1 || slot.ID > intercomSlots {
			slot.ID = i + 1
		}
		if slot.Name == "" {
			slot.Name = defaultSlot(slot.ID).Name
		}
		out.Intercom[i] = slot
	}
	out.OutputFormat = strings.TrimSpace(s.OutputFormat)
	out.DisplayName = strings.TrimSpace(s.DisplayName)
	return out
}

type Store struct {
	mu   sync.Mutex
	path string
	byID map[int]ChannelSettings
}

func NewStore(path string) *Store {
	return &Store{
		path: path,
		byID: make(map[int]ChannelSettings),
	}
}

func (s *Store) Load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var raw map[string]ChannelSettings
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}
	next := make(map[int]ChannelSettings, len(raw))
	for k, v := range raw {
		id, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		next[id] = normalizeSettings(v)
	}
	s.byID = next
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	out := make(map[string]ChannelSettings, len(s.byID))
	for id, cfg := range s.byID {
		out[strconv.Itoa(id)] = cfg
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

func (s *Store) Get(id int) ChannelSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.byID[id]; ok {
		return normalizeSettings(c)
	}
	return DefaultChannelSettings()
}

func (s *Store) All() map[int]ChannelSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[int]ChannelSettings, len(s.byID))
	for id, c := range s.byID {
		out[id] = normalizeSettings(c)
	}
	return out
}

type SettingsUpdateInput struct {
	Intercom     *[intercomSlots]IntercomSlot `json:"intercom"`
	OutputFormat *string                      `json:"output_format"`
	DisplayName  *string                      `json:"display_name"`
}

func (s *Store) Set(id int, in SettingsUpdateInput) (ChannelSettings, error) {
	if id < 1 {
		return DefaultChannelSettings(), fmt.Errorf("invalid channel id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := DefaultChannelSettings()
	if c, ok := s.byID[id]; ok {
		cfg = c
	}
	if in.Intercom != nil {
		cfg.Intercom = normalizeSettings(ChannelSettings{Intercom: *in.Intercom}).Intercom
	}
	if in.OutputFormat != nil {
		cfg.OutputFormat = strings.TrimSpace(*in.OutputFormat)
	}
	if in.DisplayName != nil {
		cfg.DisplayName = strings.TrimSpace(*in.DisplayName)
	}
	s.byID[id] = cfg
	if err := s.saveLocked(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (s *Store) Ensure(ids []int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for _, id := range ids {
		if _, ok := s.byID[id]; !ok {
			s.byID[id] = DefaultChannelSettings()
			changed = true
		}
	}
	if changed {
		_ = s.saveLocked()
	}
}
