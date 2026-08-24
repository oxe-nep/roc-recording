package recording

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"
)

const (
	SchedulePending = "pending"
	ScheduleWaiting = "waiting"
	ScheduleActive  = "active"
)

// Schedule is a one-shot record window for a channel.
type Schedule struct {
	StartAt time.Time `json:"start_at"`
	StopAt  time.Time `json:"stop_at"`
	Phase   string    `json:"phase,omitempty"`
}

func schedulePhase(now, start, stop time.Time, recording bool) string {
	if !now.Before(stop) {
		return ""
	}
	if now.Before(start) {
		return SchedulePending
	}
	if recording {
		return ScheduleActive
	}
	return ScheduleWaiting
}

func inScheduleWindow(now, start, stop time.Time) bool {
	return !now.Before(start) && now.Before(stop)
}

func validateSchedule(start, stop, now time.Time) error {
	if start.IsZero() || stop.IsZero() {
		return fmt.Errorf("start and stop are required")
	}
	if !stop.After(start) {
		return fmt.Errorf("stop must be after start")
	}
	if !stop.After(now) {
		return fmt.Errorf("stop must be in the future")
	}
	return nil
}

func (m *Manager) scheduleSnapshot(st *recState, now time.Time) *Schedule {
	if st == nil || !st.hasSched {
		return nil
	}
	phase := schedulePhase(now, st.schedStart, st.schedStop, st.status == StatusRecording)
	if phase == "" {
		return nil
	}
	return &Schedule{
		StartAt: st.schedStart.UTC(),
		StopAt:  st.schedStop.UTC(),
		Phase:   phase,
	}
}

func (m *Manager) SetSchedule(id int, start, stop time.Time) (ChannelInfo, error) {
	now := time.Now()
	if err := validateSchedule(start, stop, now); err != nil {
		return ChannelInfo{}, err
	}
	m.mu.RLock()
	st, ok := m.states[id]
	m.mu.RUnlock()
	if !ok {
		return ChannelInfo{}, fmt.Errorf("channel %d not found", id)
	}
	st.mu.Lock()
	st.hasSched = true
	st.schedStart = start
	st.schedStop = stop
	info := m.buildInfoAt(id, st, now)
	st.mu.Unlock()

	m.mu.Lock()
	_ = m.saveSchedulesLocked()
	m.mu.Unlock()
	log.Printf("[recording %d] schedule %s → %s", id, start.Format(time.RFC3339), stop.Format(time.RFC3339))
	return info, nil
}

func (m *Manager) ClearSchedule(id int) (ChannelInfo, error) {
	m.mu.RLock()
	st, ok := m.states[id]
	m.mu.RUnlock()
	if !ok {
		return ChannelInfo{}, fmt.Errorf("channel %d not found", id)
	}
	st.mu.Lock()
	st.hasSched = false
	st.schedStart = time.Time{}
	st.schedStop = time.Time{}
	info := m.buildInfo(id, st)
	st.mu.Unlock()

	m.mu.Lock()
	_ = m.saveSchedulesLocked()
	m.mu.Unlock()
	return info, nil
}

func (m *Manager) clearScheduleState(st *recState) {
	st.hasSched = false
	st.schedStart = time.Time{}
	st.schedStop = time.Time{}
}

// StartScheduler ticks once a second: wait for signal after start, stop at end, then drop the schedule.
func (m *Manager) StartScheduler(hasSignal func(int) bool, setRecording func(id int, on bool)) {
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		m.Tick(time.Now(), hasSignal, setRecording)
		for now := range t.C {
			m.Tick(now, hasSignal, setRecording)
		}
	}()
}

func (m *Manager) Tick(now time.Time, hasSignal func(int) bool, setRecording func(id int, on bool)) {
	type job struct {
		id          int
		start, stop time.Time
		recording   bool
	}
	m.mu.RLock()
	jobs := make([]job, 0, len(m.states))
	for id, st := range m.states {
		st.mu.Lock()
		if st.hasSched {
			jobs = append(jobs, job{id: id, start: st.schedStart, stop: st.schedStop, recording: st.status == StatusRecording})
		}
		st.mu.Unlock()
	}
	m.mu.RUnlock()

	for _, j := range jobs {
		if !now.Before(j.stop) {
			if j.recording {
				id := j.id
				go func() {
					if _, err := m.stopRecording(id); err != nil {
						log.Printf("[recording %d] schedule stop: %v", id, err)
						return
					}
					if setRecording != nil {
						setRecording(id, false)
					}
				}()
			}
			if _, err := m.ClearSchedule(j.id); err != nil {
				log.Printf("[recording %d] schedule clear: %v", j.id, err)
			} else {
				log.Printf("[recording %d] schedule finished", j.id)
			}
			continue
		}
		if !inScheduleWindow(now, j.start, j.stop) || j.recording {
			continue
		}
		if hasSignal == nil || !hasSignal(j.id) {
			continue
		}
		if _, err := m.Start(j.id); err != nil {
			log.Printf("[recording %d] schedule wait/start: %v", j.id, err)
			continue
		}
		if setRecording != nil {
			setRecording(j.id, true)
		}
		log.Printf("[recording %d] schedule started recording", j.id)
	}
}

func (m *Manager) loadSchedules() {
	if m.schedulePath == "" {
		return
	}
	data, err := os.ReadFile(m.schedulePath)
	if err != nil {
		return
	}
	var asg map[string]Schedule
	if err := json.Unmarshal(data, &asg); err != nil {
		log.Printf("[recording] bad schedules file %s: %v", m.schedulePath, err)
		return
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for idStr, sch := range asg {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		st, ok := m.states[id]
		if !ok {
			continue
		}
		if !sch.StopAt.After(sch.StartAt) || !now.Before(sch.StopAt) {
			continue
		}
		st.mu.Lock()
		st.hasSched = true
		st.schedStart = sch.StartAt
		st.schedStop = sch.StopAt
		st.mu.Unlock()
	}
}

func (m *Manager) saveSchedulesLocked() error {
	if m.schedulePath == "" {
		return nil
	}
	asg := make(map[string]Schedule, len(m.states))
	for id, st := range m.states {
		st.mu.Lock()
		if st.hasSched {
			asg[strconv.Itoa(id)] = Schedule{StartAt: st.schedStart.UTC(), StopAt: st.schedStop.UTC()}
		}
		st.mu.Unlock()
	}
	data, err := json.MarshalIndent(asg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.schedulePath, data, 0o644)
}
