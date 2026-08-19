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

	mgr := capture.NewManager(cfg.HLSDir, cfg.FFmpegBin, cfg.VideoCodec)
	for _, ch := range cfg.Channels {
		mgr.Register(ch.ID, ch.Name, ch.FFmpegInput)
	}

	hlsBase := fmt.Sprintf("http://localhost:%s", cfg.Port)
	if v := os.Getenv("PUBLIC_URL"); v != "" {
		hlsBase = v
	}

	hlsH := hlshandler.NewHandler(cfg.HLSDir, cfg.AllowedOrigins)
	router := api.NewRouter(mgr, hlsH, cfg.APIKey, cfg.AllowedOrigins, hlsBase)

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
