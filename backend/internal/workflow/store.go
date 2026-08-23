package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
)

// Mode selects which dashboard workflow a channel pair uses.
type Mode string

const (
	ModePair Mode = "pair" // encode + decode rows
	ModeTC   Mode = "tc"   // TC burn-in (decode row)
)

// Config is persisted per channel.
type Config struct {
	Mode Mode `json:"mode"`
}

func DefaultConfig() Config {
	return Config{Mode: ModePair}
}

func NormalizeConfig(c Config) Config {
	switch c.Mode {
	case ModeTC:
		return Config{Mode: ModeTC}
	default:
		return Config{Mode: ModePair}
	}
}

type Store struct {
	mu   sync.Mutex
	path string
	byID map[int]Config
}

func NewStore(path string) *Store {
	return &Store{
		path: path,
		byID: make(map[int]Config),
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

	// Current format: { "1": { "mode": "pair" | "tc" } }
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err == nil {
		next := make(map[int]Config, len(obj))
		for k, raw := range obj {
			id, err := strconv.Atoi(k)
			if err != nil {
				continue
			}
			next[id] = parseConfigRaw(raw)
		}
		s.byID = next
		return
	}

	// Legacy string: { "1": "record" | "playout" | "tc" }
	var legacy map[string]string
	if err := json.Unmarshal(data, &legacy); err != nil {
		return
	}
	next := make(map[int]Config, len(legacy))
	for k, v := range legacy {
		id, err := strconv.Atoi(k)
		if err != nil {
			continue
		}
		next[id] = migrateLegacyMode(v)
	}
	s.byID = next
}

func parseConfigRaw(raw json.RawMessage) Config {
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err == nil && cfg.Mode != "" {
		return NormalizeConfig(cfg)
	}
	// Transitional bool format: { "encode": true, "decode": true }
	var toggles struct {
		Encode bool `json:"encode"`
		Decode bool `json:"decode"`
	}
	if err := json.Unmarshal(raw, &toggles); err == nil {
		return migrateBoolConfig(toggles.Encode, toggles.Decode)
	}
	var mode string
	if err := json.Unmarshal(raw, &mode); err == nil {
		return migrateLegacyMode(mode)
	}
	return DefaultConfig()
}

func migrateBoolConfig(encode, decode bool) Config {
	_ = encode
	_ = decode
	return DefaultConfig()
}

func migrateLegacyMode(mode string) Config {
	if mode == "tc" {
		return Config{Mode: ModeTC}
	}
	return DefaultConfig()
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	out := make(map[string]Config, len(s.byID))
	for id, cfg := range s.byID {
		out[strconv.Itoa(id)] = cfg
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

func (s *Store) Get(id int) Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.byID[id]; ok {
		return NormalizeConfig(c)
	}
	return DefaultConfig()
}

func (s *Store) All() map[int]Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[int]Config, len(s.byID))
	for id, c := range s.byID {
		out[id] = NormalizeConfig(c)
	}
	return out
}

type UpdateInput struct {
	Mode *string `json:"mode"`
}

func (s *Store) Set(id int, in UpdateInput) (Config, error) {
	if id < 1 {
		return DefaultConfig(), fmt.Errorf("invalid channel id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := DefaultConfig()
	if c, ok := s.byID[id]; ok {
		cfg = c
	}
	if in.Mode != nil {
		cfg = NormalizeConfig(Config{Mode: Mode(*in.Mode)})
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
			s.byID[id] = DefaultConfig()
			changed = true
		}
	}
	if changed {
		_ = s.saveLocked()
	}
}
