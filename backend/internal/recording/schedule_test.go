package recording

import (
	"testing"
	"time"
)

func TestSchedulePhaseAndWindow(t *testing.T) {
	start := time.Date(2026, 8, 24, 22, 0, 0, 0, time.UTC)
	stop := time.Date(2026, 8, 25, 2, 0, 0, 0, time.UTC)

	if inScheduleWindow(start.Add(-time.Minute), start, stop) {
		t.Fatal("before start should be outside window")
	}
	if !inScheduleWindow(start, start, stop) {
		t.Fatal("at start should be in window")
	}
	if !inScheduleWindow(start.Add(2*time.Hour), start, stop) {
		t.Fatal("after midnight should still be in window")
	}
	if inScheduleWindow(stop, start, stop) {
		t.Fatal("at stop should be outside window")
	}

	if p := schedulePhase(start.Add(-time.Minute), start, stop, false); p != SchedulePending {
		t.Fatalf("pending: %q", p)
	}
	if p := schedulePhase(start.Add(time.Minute), start, stop, false); p != ScheduleWaiting {
		t.Fatalf("waiting: %q", p)
	}
	if p := schedulePhase(start.Add(time.Minute), start, stop, true); p != ScheduleActive {
		t.Fatalf("active: %q", p)
	}
	if p := schedulePhase(stop, start, stop, true); p != "" {
		t.Fatalf("expired: %q", p)
	}
}

func TestValidateSchedule(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	start := now.Add(time.Hour)
	stop := start.Add(2 * time.Hour)
	if err := validateSchedule(start, stop, now); err != nil {
		t.Fatal(err)
	}
	if err := validateSchedule(stop, start, now); err == nil {
		t.Fatal("expected stop-after-start error")
	}
	if err := validateSchedule(now.Add(-2*time.Hour), now.Add(-time.Hour), now); err == nil {
		t.Fatal("expected past stop error")
	}
	// Catch-up: start already passed, stop still ahead.
	if err := validateSchedule(now.Add(-time.Minute), now.Add(time.Hour), now); err != nil {
		t.Fatal(err)
	}
}
