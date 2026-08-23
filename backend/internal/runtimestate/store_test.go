package runtimestate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWantCaptureDefaultsTrue(t *testing.T) {
	s := NewStore("")
	if !s.WantCapture(1) {
		t.Fatal("expected default capture on")
	}
}

func TestPersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime-state.json")
	s := NewStore(path)
	s.SetPlayout(2, true)
	s.SetCapture(1, false)

	s2 := NewStore(path)
	s2.Load()
	if s2.WantCapture(1) {
		t.Fatal("expected capture off")
	}
	if !s2.WantPlayout(2) {
		t.Fatal("expected playout on")
	}
	if s2.WantPlayout(3) {
		t.Fatal("expected playout off for unset id")
	}
}

func TestPlayoutIDs(t *testing.T) {
	s := NewStore("")
	s.SetPlayout(1, true)
	s.SetPlayout(2, false)
	s.SetPlayout(3, true)
	ids := s.PlayoutIDs()
	if len(ids) != 2 {
		t.Fatalf("expected 2 ids, got %v", ids)
	}
}

func TestSaveCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime-state.json")
	s := NewStore(path)
	s.SetSRT(4, true)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}
