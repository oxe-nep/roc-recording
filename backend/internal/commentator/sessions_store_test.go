package commentator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "commentator-sessions.json")
	store := NewSessionStore(path)
	if err := store.Set(3, "abc123"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	reloaded := NewSessionStore(path)
	reloaded.Load()
	ps, ok := reloaded.Get(3)
	if !ok || ps.Token != "abc123" {
		t.Fatalf("Get = %+v ok=%v", ps, ok)
	}
	if err := reloaded.Delete(3); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := reloaded.Get(3); ok {
		t.Fatal("expected session deleted")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "{}\n" && string(data) != "{}" {
		t.Fatalf("unexpected file after delete: %q", string(data))
	}
}
