package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/roc-recording/backend/internal/api"
	"github.com/roc-recording/backend/internal/capture"
	"github.com/roc-recording/backend/internal/config"
	hlshandler "github.com/roc-recording/backend/internal/hls"
	"github.com/roc-recording/backend/internal/recording"
)

func main() {
	cfgPath := "config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	mgr := capture.NewManager(cfg.HLSDir, cfg.FFmpegBin)
	for _, ch := range cfg.Channels {
		mgr.Register(ch.ID, ch.Name, ch.FFmpegInput)
	}
	for _, ch := range cfg.Channels {
		if err := mgr.Start(ch.ID); err != nil {
			log.Printf("Failed to auto-start channel %d: %v", ch.ID, err)
		}
		time.Sleep(500 * time.Millisecond)
	}

	recMgr := recording.NewManager(cfg.RecordingsDir, cfg.FFmpegBin, mgr)
	for _, ch := range cfg.Channels {
		recMgr.Register(ch.ID)
	}

	hlsBase := fmt.Sprintf("http://localhost:%s", cfg.Port)
	if v := os.Getenv("PUBLIC_URL"); v != "" {
		hlsBase = v
	}

	hlsH := hlshandler.NewHandler(cfg.HLSDir, cfg.AllowedOrigins)
	router := api.NewRouter(mgr, recMgr, hlsH, cfg.RecordingsDir, cfg.APIKey, cfg.AllowedOrigins, hlsBase)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router,
	}

	go func() {
		log.Printf("roc-recording backend starting on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
