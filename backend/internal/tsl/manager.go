package tsl

import (
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

const defaultPort = 30947

// staleAfter clears a display label if no UMD update arrives within this window.
// Many routers stop refreshing when a source is routed away instead of sending blank text.
const staleAfter = 3 * time.Second

// ChannelMap binds encode channel IDs to TSL display indices.
type ChannelMap map[int]int

// Info is the API view of TSL state for one encode channel.
type Info struct {
	Index int    `json:"tsl_index"`
	Text  string `json:"tsl_text,omitempty"`
}

type displayState struct {
	text    string
	updated time.Time
}

// Manager listens for TSL v5 UMD and stores per-display labels.
type Manager struct {
	mu       sync.RWMutex
	port     int
	byIndex  map[int]displayState
	byChan   ChannelMap
	indexMap map[int]int // tsl index -> channel id
	conn     *net.UDPConn
	stopCh   chan struct{}
}

func NewManager(port int, byChannel ChannelMap) *Manager {
	indexMap := make(map[int]int, len(byChannel))
	for ch, idx := range byChannel {
		if idx <= 0 {
			idx = ch
		}
		indexMap[idx] = ch
		byChannel[ch] = idx
	}
	return &Manager{
		port:     port,
		byIndex:  make(map[int]displayState),
		byChan:   byChannel,
		indexMap: indexMap,
	}
}

func (m *Manager) Enabled() bool {
	return m != nil && m.port > 0
}

func (m *Manager) Start() error {
	if m == nil || m.port <= 0 {
		return nil
	}
	if m.conn != nil {
		return nil
	}
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf(":%d", m.port))
	if err != nil {
		return fmt.Errorf("tsl addr: %w", err)
	}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return fmt.Errorf("tsl listen :%d: %w", m.port, err)
	}
	m.conn = conn
	m.stopCh = make(chan struct{})
	go m.readLoop()
	log.Printf("[tsl] listening for UMD v5 on UDP :%d (%d mapped channels)", m.port, len(m.byChan))
	return nil
}

func (m *Manager) Stop() {
	if m == nil {
		return
	}
	if m.stopCh != nil {
		select {
		case <-m.stopCh:
		default:
			close(m.stopCh)
		}
	}
	if m.conn != nil {
		_ = m.conn.Close()
		m.conn = nil
	}
}

func (m *Manager) readLoop() {
	buf := make([]byte, maximumPacketSize)
	for {
		select {
		case <-m.stopCh:
			return
		default:
		}
		_ = m.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, _, err := m.conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				m.expireStale()
				continue
			}
			select {
			case <-m.stopCh:
				return
			default:
				log.Printf("[tsl] read: %v", err)
				continue
			}
		}
		msgs, err := ParsePacket(buf[:n])
		if err != nil {
			log.Printf("[tsl] parse: %v", err)
			continue
		}
		for _, msg := range msgs {
			m.apply(msg)
		}
		m.expireStale()
	}
}

func (m *Manager) apply(msg Message) {
	idx := int(msg.Index)
	text := strings.TrimSpace(msg.Text)
	m.mu.Lock()
	prev := m.byIndex[idx].text
	if text == "" {
		delete(m.byIndex, idx)
	} else {
		m.byIndex[idx] = displayState{text: text, updated: time.Now()}
	}
	m.mu.Unlock()

	if ch, ok := m.indexMap[idx]; ok && text != prev {
		if text == "" {
			log.Printf("[tsl] ch %d index %d cleared", ch, idx)
		} else {
			log.Printf("[tsl] ch %d index %d text=%q", ch, idx, text)
		}
	}
}

func (m *Manager) expireStale() {
	cutoff := time.Now().Add(-staleAfter)
	m.mu.Lock()
	defer m.mu.Unlock()
	for idx, st := range m.byIndex {
		if st.text == "" || st.updated.Before(cutoff) {
			if ch, ok := m.indexMap[idx]; ok && st.text != "" {
				log.Printf("[tsl] ch %d index %d stale — cleared", ch, idx)
			}
			delete(m.byIndex, idx)
		}
	}
}

// InfoForChannel returns TSL state for an encode channel id.
func (m *Manager) InfoForChannel(channelID int) Info {
	if m == nil || !m.Enabled() {
		return Info{}
	}
	idx := channelID
	if mapped, ok := m.byChan[channelID]; ok && mapped > 0 {
		idx = mapped
	}
	m.mu.RLock()
	st, ok := m.byIndex[idx]
	m.mu.RUnlock()
	if !ok {
		return Info{Index: idx}
	}
	if st.text == "" || time.Since(st.updated) > staleAfter {
		return Info{Index: idx}
	}
	return Info{
		Index: idx,
		Text:  strings.TrimSpace(st.text),
	}
}

// BuildChannelMap builds tsl index mapping; 0 tsl_index means use channel id.
func BuildChannelMap(channelIDs []int, tslIndexByID map[int]int) ChannelMap {
	out := make(ChannelMap, len(channelIDs))
	for _, id := range channelIDs {
		idx := tslIndexByID[id]
		if idx <= 0 {
			idx = id
		}
		out[id] = idx
	}
	return out
}
