package commentator

import "testing"

func TestDefaultChannelSettings(t *testing.T) {
	s := DefaultChannelSettings()
	if len(s.Intercom) != intercomSlots {
		t.Fatalf("expected %d slots, got %d", intercomSlots, len(s.Intercom))
	}
	if !s.Intercom[0].Enabled || !s.Intercom[1].Enabled {
		t.Fatal("expected first two intercom slots enabled by default")
	}
	if s.Intercom[2].Enabled {
		t.Fatal("expected third intercom slot disabled by default")
	}
}

func TestManagerCreateSessionRequiresEnabled(t *testing.T) {
	m := NewManager(NewStore(""), "", "", nil, ICEConfig{})
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

func TestManagerPTTRoutingState(t *testing.T) {
	m := NewManager(NewStore(""), "", "", nil, ICEConfig{})
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
