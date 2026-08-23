package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
)

// Mode selects which operator UI/pipeline focus a channel pair uses.
type Mode string

const (
	ModeRecord  Mode = "record"
	ModeTC      Mode = "tc"
	ModePlayout Mode = "playout"
)

func Normalize(m Mode) Mode {
	switch m {
	case ModeTC, ModePlayout, ModeRecord:
		return m
	default:
		return ModeRecord
	}
}

type Store struct {
	mu   sync.Mutex
	path string
	byID map[int]Mode
}

func NewStore(path string) *Store {
	return &Store{
		path: path,
		byID: make(map[int]Mode),
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
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}
	next := make(map[int]Mode, len(raw))
	for k, v := range raw {
		id, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		next[id] = Normalize(Mode(v))
	}
	s.byID = next
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	out := make(map[string]string, len(s.byID))
	for id, mode := range s.byID {
		out[strconv.Itoa(id)] = string(mode)
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

func (s *Store) Get(id int) Mode {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.byID[id]; ok {
		return Normalize(m)
	}
	return ModeRecord
}

func (s *Store) All() map[int]Mode {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[int]Mode, len(s.byID))
	for id, m := range s.byID {
		out[id] = Normalize(m)
	}
	return out
}

func (s *Store) Set(id int, mode Mode) (Mode, error) {
	mode = Normalize(mode)
	s.mu.Lock()
	defer s.mu.Unlock()
	if id < 1 {
		return ModeRecord, fmt.Errorf("invalid channel id")
	}
	s.byID[id] = mode
	if err := s.saveLocked(); err != nil {
		return mode, err
	}
	return mode, nil
}

func (s *Store) Ensure(ids []int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for _, id := range ids {
		if _, ok := s.byID[id]; !ok {
			s.byID[id] = ModeRecord
			changed = true
		}
	}
	if changed {
		_ = s.saveLocked()
	}
}
