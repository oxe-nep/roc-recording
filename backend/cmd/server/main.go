package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/roc-recording/backend/internal/api"
	"github.com/roc-recording/backend/internal/capture"
	"github.com/roc-recording/backend/internal/config"
	hlshandler "github.com/roc-recording/backend/internal/hls"
	"github.com/roc-recording/backend/internal/playout"
	"github.com/roc-recording/backend/internal/recording"
	"github.com/roc-recording/backend/internal/srt"
	"github.com/roc-recording/backend/internal/sysmetrics"
	"github.com/roc-recording/backend/internal/tcloop"
)

// tcPlayoutBridge adapts playout.Manager for tcloop.PlayoutBridge.
type tcPlayoutBridge struct {
	*playout.Manager
}

func (b tcPlayoutBridge) Stop(id int) error {
	_, err := b.Manager.Stop(id)
	return err
}

func main() {
	cfgPath := "config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	presets := make(map[string]capture.NamedPreset, len(cfg.EncodePresets))
	for id, p := range cfg.EncodePresets {
		presets[id] = capture.NamedPreset{
			ID:    id,
			Label: p.Label,
			Profile: capture.EncodeProfile{
				VideoCodec:   p.VideoCodec,
				VideoBitrate: p.VideoBitrate,
				VideoMaxrate: p.VideoMaxrate,
				VideoBufsize: p.VideoBufsize,
				VideoPreset:  p.VideoPreset,
				VideoGOP:     p.VideoGOP,
				AudioBitrate: p.AudioBitrate,
			},
		}
	}

	assignmentsPath := filepath.Join(filepath.Dir(cfgPath), "encode-assignments.json")
	presetsPath := filepath.Join(filepath.Dir(cfgPath), "encode-presets.json")
	mgr := capture.NewManager(cfg.HLSDir, cfg.FFmpegBin, presets, cfg.DefaultEncodePreset, assignmentsPath, presetsPath)
	mgr.LoadPresetsFile()
	for _, ch := range cfg.Channels {
		mgr.Register(ch.ID, ch.Name, ch.FFmpegInput, ch.EncodePreset)
	}
	mgr.LoadAssignments()

	hlsBase := fmt.Sprintf("http://localhost:%s", cfg.Port)
	if v := os.Getenv("PUBLIC_URL"); v != "" {
		hlsBase = v
	}

	publicSRTHost := strings.TrimSpace(os.Getenv("PUBLIC_SRT_HOST"))
	if publicSRTHost == "" {
		if u, err := url.Parse(hlsBase); err == nil && u.Hostname() != "" {
			publicSRTHost = u.Hostname()
		}
	} else if strings.Contains(publicSRTHost, "://") {
		if u, err := url.Parse(publicSRTHost); err == nil && u.Hostname() != "" {
			publicSRTHost = u.Hostname()
		}
	} else if host, _, err := net.SplitHostPort(publicSRTHost); err == nil {
		publicSRTHost = host
	}
	if publicSRTHost == "" {
		publicSRTHost = "127.0.0.1"
	}

	// Probe DeckLink outputs/formats BEFORE capture opens devices (exclusive lock).
	playMgr := playout.NewManager(
		cfg.FFmpegBin,
		cfg.HLSDir,
		filepath.Join(filepath.Dir(cfgPath), "playout-clients.json"),
		publicSRTHost,
	)
	playMgr.Load()
	playMgr.WarmProbe()
	playMgr.EnsureDefaultChannels()

	tcMgr := tcloop.NewManager(
		cfg.FFmpegBin,
		tcloop.SettingsPath(filepath.Dir(cfgPath)),
		mgr,
		tcPlayoutBridge{playMgr},
	)
	tcMgr.Load()
	for _, ch := range cfg.Channels {
		tcMgr.EnsureChannel(ch.ID)
	}
	guard := func(id int) error {
		if tcMgr.IsEnabled(id) || tcMgr.IsRunning(id) {
			return fmt.Errorf("TC Burn-in is exclusive on channel %d — disable it first", id)
		}
		return nil
	}
	mgr.SetStartGuard(guard)
	playMgr.SetStartGuard(guard)

	for _, ch := range cfg.Channels {
		if tcMgr.IsEnabled(ch.ID) {
			log.Printf("Skipping encode auto-start for channel %d (TC Burn-in enabled)", ch.ID)
			continue
		}
		if err := mgr.Start(ch.ID); err != nil {
			log.Printf("Failed to auto-start channel %d: %v", ch.ID, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
	tcMgr.StartEnabled()

	recMgr := recording.NewManager(
		cfg.RecordingsDir,
		cfg.FFmpegBin,
		mgr,
		filepath.Join(filepath.Dir(cfgPath), "channel-categories.json"),
		filepath.Join(filepath.Dir(cfgPath), "recordings-path.json"),
	)
	for _, ch := range cfg.Channels {
		recMgr.Register(ch.ID, ch.Name)
	}
	recMgr.LoadCategoryAssignments()
	playMgr.SetLibraryResolver(func(category, name string) (string, error) {
		return recMgr.LibraryFilePath(category, name)
	})

	srtMgr := srt.NewManager(
		cfg.FFmpegBin,
		mgr,
		filepath.Join(filepath.Dir(cfgPath), "srt-settings.json"),
		publicSRTHost,
	)
	for _, ch := range cfg.Channels {
		srtMgr.Register(ch.ID)
	}
	srtMgr.LoadSettings()

	hlsH := hlshandler.NewHandler(cfg.HLSDir, cfg.AllowedOrigins)
	metrics := sysmetrics.NewCollector(recMgr.RecordingDir())
	router := api.NewRouter(mgr, recMgr, srtMgr, playMgr, tcMgr, hlsH, cfg.APIKey, cfg.AllowedOrigins, hlsBase, metrics)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router,
	}

	go func() {
		log.Printf("roc-recording backend starting on :%s (%d encode presets, default=%s, srt_host=%s)",
			cfg.Port, len(presets), cfg.DefaultEncodePreset, publicSRTHost)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down...")
	tcMgr.StopAll()
	playMgr.StopAll()
	srtMgr.StopAll()
	_ = recMgr.StopAll()
	mgr.StopAll()
	time.Sleep(1500 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Println("Shutdown complete")
}
