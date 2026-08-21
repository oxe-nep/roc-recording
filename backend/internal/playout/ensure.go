package playout

import (
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var reSinkIndex = regexp.MustCompile(`\((\d+)\)\s*$`)

// EnsureDefaultChannels creates one fixed decode client per DeckLink sink (ids 1..N).
// Existing clients keep format/SRT/file prefs; missing slots are filled and empty devices repaired.
func (m *Manager) EnsureDefaultChannels() {
	devs, err := m.Devices(false)
	if err != nil || len(devs) == 0 {
		log.Printf("[playout] EnsureDefaultChannels: no sinks (%v)", err)
		return
	}
	sorted := append([]Device(nil), devs...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sinkSortKey(sorted[i]) < sinkSortKey(sorted[j])
	})

	m.mu.Lock()
	defer m.mu.Unlock()

	changed := false
	for i, d := range sorted {
		id := i + 1
		label := strings.TrimSpace(d.Label)
		if label == "" {
			label = d.Name
		}
		format := pickDefaultFormat(d)
		openName := d.Name
		defaultName := fmt.Sprintf("Decode %d", id)

		existing, ok := m.clients[id]
		if !ok {
			m.clients[id] = &Client{
				ID:          id,
				Name:        defaultName,
				Status:      StatusStopped,
				Device:      openName,
				DeviceLabel: label,
				FormatCode:  format,
				DeckLinkOut: true,
				Fixed:       true,
				Source:      SourceSRT,
				Mode:        ModeCaller,
				Port:        9200 + id,
				Target:      fmt.Sprintf("127.0.0.1:%d", 9100+id),
				LatencyMS:   120,
				AudioL:      audioSilence,
				AudioR:      audioSilence,
				logLines:    make([]string, 0, 32),
			}
			changed = true
			log.Printf("[playout] created default decode %d → %q (%q)", id, openName, label)
			continue
		}

		existing.mu.Lock()
		existing.Fixed = true
		existing.DeckLinkOut = true
		if strings.TrimSpace(existing.Device) == "" || !deviceRefMatch(existing.Device, d.Name, d.Label) {
			if existing.Status == StatusStopped {
				existing.Device = openName
				existing.DeviceLabel = label
				changed = true
			}
		} else if existing.Status == StatusStopped {
			resolved := d.Name
			if existing.Device != resolved {
				existing.Device = resolved
				changed = true
			}
			if existing.DeviceLabel == "" || existing.DeviceLabel != label {
				existing.DeviceLabel = label
				changed = true
			}
		}
		if existing.FormatCode == "" && format != "" && existing.Status == StatusStopped {
			existing.FormatCode = format
			changed = true
		}
		if existing.Source == "" {
			existing.Source = SourceSRT
			changed = true
		}
		// Prefer friendly names — migrate away from raw DeckLink labels.
		if existing.Name == "" || looksLikeDeckLinkName(existing.Name, label) {
			existing.Name = defaultName
			changed = true
		}
		existing.mu.Unlock()
	}
	if m.nextID <= len(sorted) {
		m.nextID = len(sorted) + 1
		changed = true
	}
	if changed {
		if err := m.saveLocked(); err != nil {
			log.Printf("[playout] EnsureDefaultChannels persist: %v", err)
		}
	}
	log.Printf("[playout] EnsureDefaultChannels: %d sinks / %d clients", len(sorted), len(m.clients))
}

func looksLikeDeckLinkName(name, sinkLabel string) bool {
	n := strings.TrimSpace(name)
	if n == "" {
		return true
	}
	if sinkLabel != "" && strings.EqualFold(n, strings.TrimSpace(sinkLabel)) {
		return true
	}
	lower := strings.ToLower(n)
	return strings.Contains(lower, "decklink") || strings.Contains(lower, "blackmagic")
}

func sinkSortKey(d Device) int {
	label := d.Label
	if label == "" {
		label = d.Name
	}
	if mm := reSinkIndex.FindStringSubmatch(label); mm != nil {
		n, _ := strconv.Atoi(mm[1])
		return n
	}
	return 9999
}

func pickDefaultFormat(d Device) string {
	prefer := []string{"Hp50", "Hi50", "Hp25", "Hi25"}
	codes := map[string]bool{}
	for _, f := range d.Formats {
		codes[f.Code] = true
	}
	for _, p := range prefer {
		if codes[p] {
			return p
		}
	}
	if len(d.Formats) > 0 {
		return d.Formats[0].Code
	}
	return "Hp50"
}
