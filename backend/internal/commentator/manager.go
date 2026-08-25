package commentator

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

type Status string

const (
	StatusOff           Status = "off"
	StatusWaiting       Status = "waiting"
	StatusSessionActive Status = "session_active"
	StatusConnected     Status = "connected"
)

const defaultSessionTTL = 8 * time.Hour

type session struct {
	token     string
	expiresAt time.Time
}

type channel struct {
	id         int
	enabled    bool
	status     Status
	connected  bool
	pttChannel int
	session    *session
	errorText  string
}

// Info is the API/dashboard view for one channel.
type Info struct {
	ID               int            `json:"id"`
	Enabled          bool           `json:"enabled"`
	Status           Status         `json:"status"`
	SessionActive    bool           `json:"session_active"`
	Connected        bool           `json:"connected"`
	PTTChannel       int            `json:"ptt_channel"`
	InviteURL        string         `json:"invite_url,omitempty"`
	SessionExpiresAt *time.Time     `json:"session_expires_at,omitempty"`
	Intercom         []IntercomSlot `json:"intercom"`
	Error            string         `json:"error,omitempty"`
	OutputFormat     string         `json:"output_format,omitempty"`
	OutputDevice     string         `json:"output_device,omitempty"`
}

type SessionInfo struct {
	Token     string    `json:"token"`
	InviteURL string    `json:"invite_url"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Manager struct {
	mu            sync.Mutex
	settings      *Store
	publicBaseURL string
	ffmpegBin     string
	channelInputs map[int]string
	ice           ICEConfig
	playout       PlayoutBridge
	joinLimit     *joinLimiter
	byID          map[int]*channel
	rtcByChannel  map[int]*rtcSession
}

func NewManager(settings *Store, publicBaseURL string, ffmpegBin string, channelInputs map[int]string, ice ICEConfig, playout PlayoutBridge) *Manager {
	publicBaseURL = strings.TrimRight(strings.TrimSpace(publicBaseURL), "/")
	if channelInputs == nil {
		channelInputs = make(map[int]string)
	}
	return &Manager{
		settings:      settings,
		publicBaseURL: publicBaseURL,
		ffmpegBin:     ffmpegBin,
		channelInputs: channelInputs,
		ice:           ice,
		playout:       playout,
		joinLimit:     newJoinLimiter(),
		byID:          make(map[int]*channel),
		rtcByChannel:  make(map[int]*rtcSession),
	}
}

func (m *Manager) SetPublicBaseURL(u string) {
	m.mu.Lock()
	m.publicBaseURL = strings.TrimRight(strings.TrimSpace(u), "/")
	m.mu.Unlock()
}

func (m *Manager) EnsureChannel(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byID[id]; !ok {
		m.byID[id] = &channel{id: id, status: StatusOff}
	}
}

func (m *Manager) IsActive(id int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.byID[id]
	return ok && ch.enabled
}

func (m *Manager) IsRunning(id int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.byID[id]
	if !ok || !ch.enabled {
		return false
	}
	return ch.status == StatusWaiting || ch.status == StatusSessionActive || ch.status == StatusConnected
}

func (m *Manager) Enable(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := m.channelLocked(id)
	ch.enabled = true
	if ch.status == StatusOff {
		ch.status = StatusWaiting
	}
	ch.errorText = ""
}

func (m *Manager) Disable(id int) {
	m.mu.Lock()
	ch, ok := m.byID[id]
	if ok {
		ch.enabled = false
		ch.status = StatusOff
		ch.connected = false
		ch.pttChannel = 0
		ch.session = nil
		ch.errorText = ""
	}
	if sess, ok := m.rtcByChannel[id]; ok {
		delete(m.rtcByChannel, id)
		m.mu.Unlock()
		sess.stop()
		return
	}
	m.mu.Unlock()
}

func (m *Manager) Stop(id int) {
	m.Disable(id)
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	sessions := make([]*rtcSession, 0, len(m.rtcByChannel))
	for _, sess := range m.rtcByChannel {
		sessions = append(sessions, sess)
	}
	m.rtcByChannel = make(map[int]*rtcSession)
	for id := range m.byID {
		if ch := m.byID[id]; ch != nil {
			ch.enabled = false
			ch.status = StatusOff
			ch.connected = false
			ch.pttChannel = 0
			ch.session = nil
			ch.errorText = ""
		}
	}
	m.mu.Unlock()
	for _, sess := range sessions {
		sess.stop()
	}
}

func (m *Manager) GetSettings(id int) ChannelSettings {
	if m.settings != nil {
		return m.settings.Get(id)
	}
	return DefaultChannelSettings()
}

// OutputSink resolves DeckLink device + format for commentator return video.
// Device comes from the playout mapping; format from commentator settings or playout default.
func (m *Manager) OutputSink(id int) (device, formatCode string, err error) {
	if m.playout == nil {
		return "", "", fmt.Errorf("playout not configured")
	}
	device, defaultFormat, err := m.playout.Sink(id)
	if err != nil {
		return "", "", err
	}
	device = strings.TrimSpace(device)
	formatCode = strings.TrimSpace(m.GetSettings(id).OutputFormat)
	if formatCode == "" {
		formatCode = strings.TrimSpace(defaultFormat)
	}
	if device == "" || formatCode == "" {
		return "", "", fmt.Errorf("decklink output device/format not configured for channel %d", id)
	}
	return device, formatCode, nil
}

func (m *Manager) UpdateSettings(id int, in SettingsUpdateInput) (ChannelSettings, error) {
	if m.settings == nil {
		return DefaultChannelSettings(), fmt.Errorf("settings store not configured")
	}
	cfg, err := m.settings.Set(id, in)
	if err != nil {
		return cfg, err
	}
	m.notifyCommentatorConfig(id)
	return cfg, nil
}

func (m *Manager) notifyCommentatorConfig(id int) {
	settings := m.GetSettings(id)
	intercom := enabledIntercom(settings)
	m.mu.Lock()
	sess := m.rtcByChannel[id]
	m.mu.Unlock()
	if sess != nil {
		sess.notifyConfig(intercom)
	}
}

func (m *Manager) CreateSession(id int) (SessionInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := m.channelLocked(id)
	if !ch.enabled {
		return SessionInfo{}, fmt.Errorf("remote commentator is not enabled on channel %d", id)
	}
	token, err := newToken()
	if err != nil {
		return SessionInfo{}, err
	}
	exp := time.Now().Add(defaultSessionTTL)
	ch.session = &session{token: token, expiresAt: exp}
	ch.status = StatusSessionActive
	ch.connected = false
	ch.pttChannel = 0
	return SessionInfo{
		Token:     token,
		InviteURL: m.inviteURLLocked(token),
		ExpiresAt: exp,
	}, nil
}

func (m *Manager) RevokeSession(id int) {
	m.mu.Lock()
	ch, ok := m.byID[id]
	if ok && ch.enabled {
		ch.session = nil
		ch.connected = false
		ch.pttChannel = 0
		ch.status = StatusWaiting
	}
	if sess, ok := m.rtcByChannel[id]; ok {
		delete(m.rtcByChannel, id)
		m.mu.Unlock()
		sess.stop()
		return
	}
	m.mu.Unlock()
}

func (m *Manager) SetPTT(id int, channel int) {
	m.mu.Lock()
	ch, ok := m.byID[id]
	if !ok {
		m.mu.Unlock()
		return
	}
	if channel < 0 || channel > intercomSlots {
		channel = 0
	}
	ch.pttChannel = channel
	sess := m.rtcByChannel[id]
	m.mu.Unlock()
	if sess != nil && sess.router != nil {
		sess.router.SetPTT(channel)
	}
}

func (m *Manager) SetConnected(id int, connected bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch, ok := m.byID[id]
	if !ok || !ch.enabled {
		return
	}
	ch.connected = connected
	if connected {
		ch.status = StatusConnected
		return
	}
	if ch.session != nil {
		ch.status = StatusSessionActive
		return
	}
	ch.status = StatusWaiting
}

func (m *Manager) List(ids []int) []Info {
	out := make([]Info, 0, len(ids))
	for _, id := range ids {
		out = append(out, m.Get(id))
	}
	return out
}

func (m *Manager) Get(id int) Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.infoLocked(m.channelLocked(id))
}

func (m *Manager) channelLocked(id int) *channel {
	if ch, ok := m.byID[id]; ok {
		return ch
	}
	ch := &channel{id: id, status: StatusOff}
	m.byID[id] = ch
	return ch
}

func (m *Manager) infoLocked(ch *channel) Info {
	settings := DefaultChannelSettings()
	if m.settings != nil {
		settings = m.settings.Get(ch.id)
	}
	info := Info{
		ID:             ch.id,
		Enabled:        ch.enabled,
		Status:         ch.status,
		Connected:      ch.connected,
		PTTChannel:     ch.pttChannel,
		Intercom:       settings.Intercom[:],
		Error:          ch.errorText,
		OutputFormat:   strings.TrimSpace(settings.OutputFormat),
	}
	if !ch.enabled {
		info.Status = StatusOff
	}
	if ch.session != nil {
		info.SessionActive = true
		exp := ch.session.expiresAt
		info.SessionExpiresAt = &exp
		info.InviteURL = m.inviteURLLocked(ch.session.token)
	}
	if device, format, err := m.OutputSink(ch.id); err == nil {
		if info.OutputFormat == "" {
			info.OutputFormat = format
		}
		info.OutputDevice = device
	}
	return info
}

func (m *Manager) inviteURLLocked(token string) string {
	if m.publicBaseURL == "" {
		return "/commentator/" + token
	}
	return m.publicBaseURL + "/commentator/" + token
}

func newToken() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
