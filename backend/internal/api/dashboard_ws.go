package api

import (
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/roc-recording/backend/internal/capture"
	"github.com/roc-recording/backend/internal/playout"
	"github.com/roc-recording/backend/internal/recording"
	"github.com/roc-recording/backend/internal/srt"
	"github.com/roc-recording/backend/internal/tcloop"
	"github.com/roc-recording/backend/internal/tsl"
	"github.com/roc-recording/backend/internal/ws"
)

type meterLevels struct {
	L float64 `json:"l"`
	R float64 `json:"r"`
}

type dashboardSnapshot struct {
	Type           string                        `json:"type"`
	Streams        []streamResponse              `json:"streams"`
	Playout        []playout.ClientInfo          `json:"playout"`
	TC             []tcloop.Info                 `json:"tc"`
	Recordings     []recording.ChannelInfo       `json:"recordings"`
	SRT            []srt.ChannelInfo             `json:"srt"`
	MetersEncode   map[string]meterLevels        `json:"meters_encode"`
	MetersPlayout  map[string]meterLevels        `json:"meters_playout"`
}

func startDashboardWS(
	hub *ws.Hub,
	mgr *capture.Manager,
	recMgr *recording.Manager,
	srtMgr *srt.Manager,
	playMgr *playout.Manager,
	tcMgr *tcloop.Manager,
	tslMgr *tsl.Manager,
	hlsBaseURL string,
) {
	go hub.Run()
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for range t.C {
			if hub.ClientCount() == 0 {
				continue
			}
			hub.BroadcastJSON(buildDashboardSnapshot(mgr, recMgr, srtMgr, playMgr, tcMgr, tslMgr, hlsBaseURL))
		}
	}()
	log.Printf("[ws] dashboard hub started (500ms snapshots while clients connected)")
}

func buildDashboardSnapshot(
	mgr *capture.Manager,
	recMgr *recording.Manager,
	srtMgr *srt.Manager,
	playMgr *playout.Manager,
	tcMgr *tcloop.Manager,
	tslMgr *tsl.Manager,
	hlsBaseURL string,
) dashboardSnapshot {
	streams := mgr.List()
	streamResp := make([]streamResponse, 0, len(streams))
	ids := make([]int, 0, len(streams))
	metersEnc := make(map[string]meterLevels, len(streams))
	metersPlay := make(map[string]meterLevels, len(streams))

	for _, s := range streams {
		ids = append(ids, s.ID)
		streamResp = append(streamResp, toResponse(s, hlsBaseURL, tslMgr, tcMgr))
		key := strconv.Itoa(s.ID)
		if tcMgr != nil {
			if l, r, ok := tcMgr.AudioLevels(s.ID); ok {
				metersEnc[key] = meterLevels{L: l, R: r}
				metersPlay[key] = meterLevels{L: l, R: r}
				continue
			}
		}
		if l, r, ok := mgr.AudioLevels(s.ID); ok {
			metersEnc[key] = meterLevels{L: l, R: r}
		} else {
			metersEnc[key] = meterLevels{L: -90, R: -90}
		}
		if playMgr != nil {
			if l, r, ok := playMgr.AudioLevels(s.ID); ok {
				metersPlay[key] = meterLevels{L: l, R: r}
			} else {
				metersPlay[key] = meterLevels{L: -90, R: -90}
			}
		}
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
		MetersEncode:  metersEnc,
		MetersPlayout: metersPlay,
	}
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
	hlsBaseURL string,
) {
	r.Get("/api/dashboard", func(w http.ResponseWriter, req *http.Request) {
		jsonOK(w, buildDashboardSnapshot(mgr, recMgr, srtMgr, playMgr, tcMgr, tslMgr, hlsBaseURL))
	})
}

// chiRouter is the subset of chi.Router we need (avoids import cycle noise in helpers).
type chiRouter interface {
	Get(pattern string, handlerFn http.HandlerFunc)
}
