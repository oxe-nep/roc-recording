package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
)

// Config toggles which dashboard rows are shown per channel pair.
// TC burn-in is runtime-only (tcloop) and locks encode + decode while active.
type Config struct {
	Encode bool `json:"encode"`
	Decode bool `json:"decode"`
}

func DefaultConfig() Config {
	return Config{Encode: true, Decode: true}
}

func NormalizeConfig(c Config) Config {
	// At least one side must stay visible.
	if !c.Encode && !c.Decode {
		return DefaultConfig()
	}
	return c
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

	// New format: { "1": { "encode": true, "decode": true } }
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

	// Legacy format: { "1": "record" | "playout" | "tc" }
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
	if err := json.Unmarshal(raw, &cfg); err == nil {
		return NormalizeConfig(cfg)
	}
	var mode string
	if err := json.Unmarshal(raw, &mode); err == nil {
		return migrateLegacyMode(mode)
	}
	return DefaultConfig()
}

func migrateLegacyMode(mode string) Config {
	switch mode {
	case "playout":
		return Config{Encode: false, Decode: true}
	case "record":
		// Previously encode-only; default pair now shows both rows.
		return DefaultConfig()
	default:
		return DefaultConfig()
	}
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
	Encode *bool `json:"encode"`
	Decode *bool `json:"decode"`
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
	if in.Encode != nil {
		cfg.Encode = *in.Encode
	}
	if in.Decode != nil {
		cfg.Decode = *in.Decode
	}
	cfg = NormalizeConfig(cfg)
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
