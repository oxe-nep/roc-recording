package commentator

import (
	"encoding/json"
	"os"
	"strconv"
	"sync"
	"time"
)

// PersistedSession is a durable invite token for one channel.
type PersistedSession struct {
	Token     string    `json:"token"`
	Pin       string    `json:"pin,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

type SessionStore struct {
	mu   sync.Mutex
	path string
	byID map[int]PersistedSession
}

func NewSessionStore(path string) *SessionStore {
	return &SessionStore{
		path: path,
		byID: make(map[int]PersistedSession),
	}
}

func (s *SessionStore) Load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var raw map[string]PersistedSession
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}
	next := make(map[int]PersistedSession, len(raw))
	for k, v := range raw {
		id, err := strconv.Atoi(k)
		if err != nil || v.Token == "" {
			continue
		}
		next[id] = v
	}
	s.byID = next
}

func (s *SessionStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	out := make(map[string]PersistedSession, len(s.byID))
	for id, ps := range s.byID {
		out[strconv.Itoa(id)] = ps
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

func (s *SessionStore) Get(id int) (PersistedSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps, ok := s.byID[id]
	return ps, ok && ps.Token != ""
}

func (s *SessionStore) Set(id int, ps PersistedSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ps.CreatedAt.IsZero() {
		ps.CreatedAt = time.Now().UTC()
	}
	s.byID[id] = ps
	return s.saveLocked()
}

func (s *SessionStore) Delete(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byID, id)
	return s.saveLocked()
}
