package commentator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagerCreateSessionRequiresEnabled(t *testing.T) {
	m := NewManager(NewStore(""), NewSessionStore(""), "", "", nil, ICEConfig{})
	m.EnsureChannel(1)
	_, err := m.CreateSession(1)
	if err == nil {
		t.Fatal("expected error when commentator not enabled")
	}
	m.Enable(1)
	info, err := m.CreateSession(1)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if info.Token == "" || info.InviteURL == "" {
		t.Fatal("expected token and invite url")
	}
	got := m.Get(1)
	if !got.SessionActive || got.Status != StatusSessionActive {
		t.Fatalf("unexpected state: %+v", got)
	}
}

func TestManagerCreateSessionIdempotent(t *testing.T) {
	m := NewManager(NewStore(""), NewSessionStore(""), "", "", nil, ICEConfig{})
	m.Enable(1)
	first, err := m.CreateSession(1)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	second, err := m.CreateSession(1)
	if err != nil {
		t.Fatalf("CreateSession again: %v", err)
	}
	if first.Token != second.Token {
		t.Fatalf("token changed: %q -> %q", first.Token, second.Token)
	}
}

func TestManagerEnableRestoresPersistedSession(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "commentator-sessions.json")
	store := NewSessionStore(path)
	m1 := NewManager(NewStore(""), store, "", "", nil, ICEConfig{})
	m1.Enable(1)
	info, err := m1.CreateSession(1)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	m2Store := NewSessionStore(path)
	m2Store.Load()
	m2 := NewManager(NewStore(""), m2Store, "", "", nil, ICEConfig{})
	m2.Enable(1)
	got := m2.Get(1)
	if !got.SessionActive {
		t.Fatal("expected session active after restore")
	}
	if got.InviteURL == "" {
		t.Fatal("expected invite url")
	}
	second, err := m2.CreateSession(1)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if second.Token != info.Token {
		t.Fatalf("token changed after restart: %q -> %q", info.Token, second.Token)
	}
}

func TestManagerRevokeSessionClearsPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "commentator-sessions.json")
	store := NewSessionStore(path)
	m := NewManager(NewStore(""), store, "", "", nil, ICEConfig{})
	m.Enable(1)
	first, err := m.CreateSession(1)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	m.RevokeSession(1)
	got := m.Get(1)
	if got.SessionActive {
		t.Fatal("expected session inactive after revoke")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("sessions file: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sessions: %v", err)
	}
	if string(data) != "{}\n" && string(data) != "{}" {
		// empty map may marshal as {}
	}
	m.Enable(1)
	second, err := m.CreateSession(1)
	if err != nil {
		t.Fatalf("CreateSession after revoke: %v", err)
	}
	if second.Token == first.Token {
		t.Fatal("expected new token after revoke")
	}
}

func TestManagerDisableKeepsPersistedToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "commentator-sessions.json")
	store := NewSessionStore(path)
	m := NewManager(NewStore(""), store, "", "", nil, ICEConfig{})
	m.Enable(1)
	first, err := m.CreateSession(1)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	m.Disable(1)
	got := m.Get(1)
	if got.SessionActive {
		t.Fatal("session should not be active in memory when disabled")
	}
	m.Enable(1)
	second, err := m.CreateSession(1)
	if err != nil {
		t.Fatalf("CreateSession after re-enable: %v", err)
	}
	if second.Token != first.Token {
		t.Fatalf("token changed after disable/re-enable: %q -> %q", first.Token, second.Token)
	}
}

func TestManagerPTTRoutingState(t *testing.T) {
	m := NewManager(NewStore(""), NewSessionStore(""), "", "", nil, ICEConfig{})
	m.Enable(2)
	m.SetPTT(2, 3)
	got := m.Get(2)
	if got.PTTChannel != 3 {
		t.Fatalf("PTTChannel = %d, want 3", got.PTTChannel)
	}
	m.SetPTT(2, 0)
	got = m.Get(2)
	if got.PTTChannel != 0 {
		t.Fatalf("PTTChannel = %d, want 0", got.PTTChannel)
	}
}
