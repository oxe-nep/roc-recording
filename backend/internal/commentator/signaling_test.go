package commentator

import "testing"

func TestIntercomTracksStale(t *testing.T) {
	prev := []IntercomSlot{{ID: 1, Enabled: true}, {ID: 2, Enabled: true}}
	same := []IntercomSlot{{ID: 1, Enabled: true, Name: "Renamed"}, {ID: 2, Enabled: true}}
	if intercomTracksStale(prev, same) {
		t.Fatal("name-only change should not require reconnect")
	}
	added := []IntercomSlot{{ID: 1, Enabled: true}, {ID: 2, Enabled: true}, {ID: 3, Enabled: true}}
	if !intercomTracksStale(prev, added) {
		t.Fatal("added slot should require reconnect")
	}
	removed := []IntercomSlot{{ID: 1, Enabled: true}}
	if !intercomTracksStale(prev, removed) {
		t.Fatal("removed slot should require reconnect")
	}
}
