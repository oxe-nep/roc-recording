package runtimestate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// State tracks which subsystems were intentionally running before restart/crash.
type State struct {
	Capture   map[string]bool `json:"capture,omitempty"`
	Playout   map[string]bool `json:"playout,omitempty"`
	SRT       map[string]bool `json:"srt,omitempty"`
	Recording map[string]bool `json:"recording,omitempty"`
}

// Store persists operational desired-on flags beside config.yaml.
type Store struct {
	mu    sync.Mutex
	path  string
	state State
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

func (s *Store) Load() {
	if s.path == "" {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return
	}
	s.mu.Lock()
	s.state = st
	s.mu.Unlock()
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func key(id int) string {
	return strconv.Itoa(id)
}

func (s *Store) setField(field *map[string]bool, id int, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if *field == nil {
		*field = make(map[string]bool)
	}
	(*field)[key(id)] = on
	_ = s.saveLocked()
}

func (s *Store) wantField(field map[string]bool, id int, defaultOn bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if field == nil {
		return defaultOn
	}
	v, ok := field[key(id)]
	if !ok {
		return defaultOn
	}
	return v
}

func (s *Store) SetCapture(id int, on bool)   { s.setField(&s.state.Capture, id, on) }
func (s *Store) SetPlayout(id int, on bool)   { s.setField(&s.state.Playout, id, on) }
func (s *Store) SetSRT(id int, on bool)       { s.setField(&s.state.SRT, id, on) }
func (s *Store) SetRecording(id int, on bool) { s.setField(&s.state.Recording, id, on) }

// WantCapture defaults to true when unset (matches legacy always-on encode boot).
func (s *Store) WantCapture(id int) bool { return s.wantField(s.state.Capture, id, true) }

func (s *Store) WantPlayout(id int) bool   { return s.wantField(s.state.Playout, id, false) }
func (s *Store) WantSRT(id int) bool       { return s.wantField(s.state.SRT, id, false) }
func (s *Store) WantRecording(id int) bool { return s.wantField(s.state.Recording, id, false) }

// IDs returns channel ids marked on for a subsystem (for boot restore).
func (s *Store) IDs(field map[string]bool) []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(field) == 0 {
		return nil
	}
	out := make([]int, 0, len(field))
	for k, on := range field {
		if !on {
			continue
		}
		id, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		out = append(out, id)
	}
	return out
}

func (s *Store) PlayoutIDs() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idsLocked(s.state.Playout)
}

func (s *Store) SRTIDs() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idsLocked(s.state.SRT)
}

func (s *Store) RecordingIDs() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idsLocked(s.state.Recording)
}

func (s *Store) idsLocked(field map[string]bool) []int {
	if len(field) == 0 {
		return nil
	}
	out := make([]int, 0, len(field))
	for k, on := range field {
		if !on {
			continue
		}
		id, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		out = append(out, id)
	}
	return out
}
