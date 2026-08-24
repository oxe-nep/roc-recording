package api

import (
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/roc-recording/backend/internal/audiox"
	"github.com/roc-recording/backend/internal/capture"
	"github.com/roc-recording/backend/internal/playout"
	"github.com/roc-recording/backend/internal/recording"
	"github.com/roc-recording/backend/internal/srt"
	"github.com/roc-recording/backend/internal/tcloop"
	"github.com/roc-recording/backend/internal/tsl"
	"github.com/roc-recording/backend/internal/workflow"
	"github.com/roc-recording/backend/internal/ws"
)

type meterLevels struct {
	L        float64   `json:"l"`
	R        float64   `json:"r"`
	Channels []float64 `json:"channels"`
}

type dashboardSnapshot struct {
	Type          string                     `json:"type"`
	Streams       []streamResponse           `json:"streams"`
	Playout       []playout.ClientInfo       `json:"playout"`
	TC            []tcloop.Info              `json:"tc"`
	Recordings    []recording.ChannelInfo    `json:"recordings"`
	SRT           []srt.ChannelInfo          `json:"srt"`
	Workflows     map[string]workflow.Config `json:"workflows"`
	MetersEncode  map[string]meterLevels     `json:"meters_encode"`
	MetersPlayout map[string]meterLevels     `json:"meters_playout"`
}

type metersFrame struct {
	Type          string                 `json:"type"`
	MetersEncode  map[string]meterLevels `json:"meters_encode"`
	MetersPlayout map[string]meterLevels `json:"meters_playout"`
}

func fromPeaks(ch []float64, ok bool) meterLevels {
	peaks := audiox.SilencePeaks()
	if ok {
		for i := 0; i < audiox.Channels && i < len(ch); i++ {
			peaks[i] = ch[i]
		}
	}
	return meterLevels{L: peaks[0], R: peaks[1], Channels: audiox.Slice(peaks)}
}

func buildWorkflowsMap(wfStore *workflow.Store, mgr *capture.Manager) map[string]workflow.Config {
	out := make(map[string]workflow.Config)
	if wfStore != nil {
		for id, cfg := range wfStore.All() {
			out[strconv.Itoa(id)] = cfg
		}
	}
	for _, s := range mgr.List() {
		key := strconv.Itoa(s.ID)
		if _, ok := out[key]; !ok {
			out[key] = workflow.DefaultConfig()
		}
	}
	return out
}

func startDashboardWS(
	hub *ws.Hub,
	mgr *capture.Manager,
	recMgr *recording.Manager,
	srtMgr *srt.Manager,
	playMgr *playout.Manager,
	tcMgr *tcloop.Manager,
	tslMgr *tsl.Manager,
	wfStore *workflow.Store,
	hlsBaseURL string,
) {
	snapshot := func() dashboardSnapshot {
		snap := buildDashboardSnapshot(mgr, recMgr, srtMgr, playMgr, tcMgr, tslMgr, wfStore, hlsBaseURL)
		sortDashboardSnapshot(&snap)
		return snap
	}
	hub.SetConnectHook(func(c *ws.Client) {
		data, err := json.Marshal(snapshot())
		if err != nil {
			return
		}
		if !c.TrySend(data) {
			log.Printf("[ws] initial snapshot dropped (client slow)")
		}
	})
	go hub.Run()
	go func() {
		snapTick := time.NewTicker(500 * time.Millisecond)
		meterTick := time.NewTicker(80 * time.Millisecond)
		defer snapTick.Stop()
		defer meterTick.Stop()
		for {
			select {
			case <-snapTick.C:
				if hub.ClientCount() == 0 {
					continue
				}
				hub.BroadcastJSON(snapshot())
			case <-meterTick.C:
				if hub.ClientCount() == 0 {
					continue
				}
				enc, play := collectMeterMaps(mgr, playMgr, tcMgr)
				hub.BroadcastJSON(metersFrame{
					Type:          "meters",
					MetersEncode:  enc,
					MetersPlayout: play,
				})
			}
		}
	}()
	log.Printf("[ws] dashboard hub started (immediate snapshot on connect, 80ms meters, 500ms snapshot)")
}

func collectMeterMaps(
	mgr *capture.Manager,
	playMgr *playout.Manager,
	tcMgr *tcloop.Manager,
) (metersEnc, metersPlay map[string]meterLevels) {
	streams := mgr.List()
	metersEnc = make(map[string]meterLevels, len(streams))
	metersPlay = make(map[string]meterLevels, len(streams))
	for _, s := range streams {
		key := strconv.Itoa(s.ID)
		if tcMgr != nil {
			if ch, ok := tcMgr.AudioLevels(s.ID); ok {
				m := fromPeaks(ch, true)
				metersEnc[key] = m
				metersPlay[key] = m
				continue
			}
		}
		ch, ok := mgr.AudioLevels(s.ID)
		metersEnc[key] = fromPeaks(ch, ok)
		if playMgr != nil {
			pch, pok := playMgr.AudioLevels(s.ID)
			metersPlay[key] = fromPeaks(pch, pok)
		}
	}
	return metersEnc, metersPlay
}

func buildDashboardSnapshot(
	mgr *capture.Manager,
	recMgr *recording.Manager,
	srtMgr *srt.Manager,
	playMgr *playout.Manager,
	tcMgr *tcloop.Manager,
	tslMgr *tsl.Manager,
	wfStore *workflow.Store,
	hlsBaseURL string,
) dashboardSnapshot {
	streams := mgr.List()
	streamResp := make([]streamResponse, 0, len(streams))
	ids := make([]int, 0, len(streams))
	metersEnc, metersPlay := collectMeterMaps(mgr, playMgr, tcMgr)

	for _, s := range streams {
		ids = append(ids, s.ID)
		streamResp = append(streamResp, toResponse(s, hlsBaseURL, tslMgr, tcMgr))
	}

	var playList []playout.ClientInfo
	if playMgr != nil {
		playList = playMgr.List()
	}
	var tcList []tcloop.Info
	if tcMgr != nil {
		tcList = tcMgr.List(ids)
	}
	var recList []recording.ChannelInfo
	if recMgr != nil {
		recList = recMgr.ListAll()
	}
	var srtList []srt.ChannelInfo
	if srtMgr != nil {
		srtList = srtMgr.ListAll()
	}

	return dashboardSnapshot{
		Type:          "snapshot",
		Streams:       streamResp,
		Playout:       playList,
		TC:            tcList,
		Recordings:    recList,
		SRT:           srtList,
		Workflows:     buildWorkflowsMap(wfStore, mgr),
		MetersEncode:  metersEnc,
		MetersPlayout: metersPlay,
	}
}

func sortDashboardSnapshot(s *dashboardSnapshot) {
	sort.Slice(s.Streams, func(i, j int) bool { return s.Streams[i].ID < s.Streams[j].ID })
	sort.Slice(s.Playout, func(i, j int) bool { return s.Playout[i].ID < s.Playout[j].ID })
	sort.Slice(s.TC, func(i, j int) bool { return s.TC[i].ID < s.TC[j].ID })
	sort.Slice(s.Recordings, func(i, j int) bool { return s.Recordings[i].ID < s.Recordings[j].ID })
	sort.Slice(s.SRT, func(i, j int) bool { return s.SRT[i].ID < s.SRT[j].ID })
}

func registerDashboardWS(r chiRouter, hub *ws.Hub, apiKey string) {
	r.Get("/ws", func(w http.ResponseWriter, req *http.Request) {
		if apiKey != "" {
			got := req.Header.Get("X-API-Key")
			if got == "" {
				got = req.URL.Query().Get("api_key")
			}
			if got != apiKey {
				jsonError(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		hub.ServeHTTP(w, req)
	})
}

func registerDashboardHTTP(
	r chiRouter,
	mgr *capture.Manager,
	recMgr *recording.Manager,
	srtMgr *srt.Manager,
	playMgr *playout.Manager,
	tcMgr *tcloop.Manager,
	tslMgr *tsl.Manager,
	wfStore *workflow.Store,
	hlsBaseURL string,
) {
	r.Get("/api/dashboard", func(w http.ResponseWriter, req *http.Request) {
		snap := buildDashboardSnapshot(mgr, recMgr, srtMgr, playMgr, tcMgr, tslMgr, wfStore, hlsBaseURL)
		sortDashboardSnapshot(&snap)
		jsonOK(w, snap)
	})
}

// chiRouter is the subset of chi.Router we need (avoids import cycle noise in helpers).
type chiRouter interface {
	Get(pattern string, handlerFn http.HandlerFunc)
}
